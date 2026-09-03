# 技术设计：解析、归组与候选闭环

## 1. 边界与责任

保持当前 `backend/cmd/roomusic` 过渡单体，但按能力划分责任：

| 边界 | 负责 | 不负责 |
| --- | --- | --- |
| parser/Library | 只读遍历、路径 containment、格式解析、bounded diagnostics、safe observation | candidate 合并、Release SQL、HTTP |
| organizer/Catalog policy | 规范化、字段优先级、多数决、candidate anchor、Medium/Track/CUE 去重、decisions/evidence | 文件系统、数据库事务、REST |
| candidate store/PostgreSQL adapter | staging、Release Graph upsert、source identity、current evidence/credits/artwork 关系、missing finalize | 选择 album 或判断权限 |
| application/REST | session/role、参数解码、查询/投影、错误 envelope、管理员 evidence 权限 | 直接遍历文件或发 SQL 业务规则 |
| frontend | typed API、URL/filter/local UI state、只读呈现、恢复状态 | 安全、权限、metadata 写入 |

实现可先在现有文件中增量抽取窄函数；只有形成真实复用边界时才新建 package。所有
跨边界值使用 typed struct，不传 SQL row、HTTP request 或 `map[string]any` 作为内部
政策输入。

## 2. 数据流

```text
WalkDir + containment
  -> parser adapters
  -> scan_observation staging (bounded batches)
  -> ordered candidate stream
  -> pure organizer
  -> candidate transaction (Release/Medium/Track/decision/evidence)
  -> post-candidate artwork link
  -> succeeded-only finalize missing
  -> REST projections + frontend decoders
```

遍历和解析不持有数据库事务；每批 staging 写入使用短事务。候选按
`candidate_anchor` 排序，逐个短事务提交，因此一个候选失败不会回滚其它已完成候选。
扫描终态仍由现有 advisory lock、持久取消轮询和 `finalizeScan` 统一决定。

## 3. 统一 observation 合同

在 `organizer.go` 中保留纯领域类型，扩展为以下语义（字段名可按现有命名调整）：

- identity：`RelativePath`、`Directory`、`SourceKind`、`SourceIdentity`；物理来源是
  `root_id + normalized relative path`，CUE 虚拟来源是 CUE 相对路径、父来源相对路径、
  track number 与 INDEX 01 offset 的版本化摘要。
- metadata：title、artist、album、album artist、date/year、release/media/source
  type、edition、label、catalog、barcode、genre、credits；每个字段带 `source_kind`
  与 `inferred`。
- audio facts：duration、codec、bit depth、sample rate、channels、bitrate。
- cue：sheet relative path、parent relative path、referenced file、track number、
  index frames、start/end/duration、ISRC、sheet/track performer/title；引用只存安全
  相对标识，禁止绝对路径。
- artwork：候选的受管 source reference/hash/MIME/dimensions，不在 organizer 中写文件。

字段事实和派生决定分开：parser 只报告看到的值，organizer 决定 tag/folder/filename
优先级和 confidence，adapter 才将权威列和 current evidence 写入 PostgreSQL。

## 4. M4A adapter

### 读取策略

1. 通过 `os.Open` + `io.ReaderAt`/受限 `SectionReader` 读取 8/16 字节 atom header，
   校验 size、largesize、size=0、文件边界和最大 atom header/metadata budget。
2. 只递归 `moov/udta/meta/ilst` 等 metadata 容器；音频事实从 `mvhd/mdhd/stsd` 和
   必要 sample table 读取。绝不 `os.ReadFile` 整个媒体，也不以总文件大小判定 metadata
   大小；对单个 metadata atom 设置上界并返回可诊断错误。
3. 使用 raw atom key `[]byte{0xa9,'n','a','m'}` 等匹配 iTunes 标签，解析 data atom
   的类型/locale/header 后做 UTF-8 或受限文本解码。`mp4a` 映射 AAC，`alac` 映射 ALAC；
   sample rate 使用定点字段，duration 使用 timescale，缺失时保留零值。
4. 错误分类为 invalid stream、unsupported brand、metadata too large、truncated atom
   等 parser cause；scanner 只保存安全相对路径和 bounded message。

### 测试 fixture

在测试中构造最小 ftyp/moov/stsd/ilst，并追加超过 16 MiB 的 `mdat`/padding，使 moov
位于文件前后两种位置；验证 AAC/ALAC 标签、duration、采样率、声道及损坏 atom。

## 5. CUE adapter 与去重

1. 先以字节预算读取 CUE 文本，按 BOM/UTF-8 严格解码，失败后按允许的 GBK、Shift-JIS、
   Big5 顺序尝试；记录 encoding，不使用无限制的替换字符吞错。
2. 解析 sheet-level `PERFORMER/TITLE/REM DATE/REM GENRE/CATALOG`，每个 `FILE` 开启
   一个引用上下文；track-level `TRACK/TITLE/PERFORMER/ISRC/INDEX` 产生独立 cue track。
3. 把 `FILE` 路径相对化到 CUE 所在目录，清理分隔符后执行 root containment；任何
   `..` 越界、绝对路径、EvalSymlinks 后逃逸或不存在目标只生成 diagnostic，该引用
   不进入 candidate。
4. 根据相邻 INDEX 01 和父音频 duration 推导 end/duration；不能推导时保持空并标记
   非阻断性不确定 evidence。每条虚拟轨拥有确定性 identity。
5. organizer 只在同一 candidate 内对显式 CUE 父来源去重：若存在引用关系对应的
   真实分轨，则真实 track 可见、cue track 留 evidence；其它未对应父来源的 CUE
   track 保留。标题或时长相似不构成去重关系。

## 6. Organizer 规则

### candidate 构建

1. 先按安全 directory 和显式 disc/CD/Disk 结构建立初始 scope；根目录观察按
   `album + album artist` 分区，有明确 album 的 loose candidate，没有的每个物理文件
   单独进入 loose unknown。
2. 对同目录 observations 计算规范化 album/album artist 组合。只有少数派且存在
   明显多数时在同一 candidate 采用多数决；比例接近或 album 明确分歧时按组合稳定
   拆分，并在每个 candidate 记录 inconsistency。
3. 多碟合并需要父目录结构、相同 album/album artist 证据、1..N 连续且每碟唯一；
   否则保持 leaf candidate。所有 leaf 都缺少权威 album 时，共同的非空父目录可作为
   `folder_fallback` album 证据，决定必须是 `low/uncertain_apply`；任一相互冲突的
   album/album artist tag 都会阻止合并。Box 父目录只记录 parent collection evidence。
4. anchor 使用 `root:v2:<kind>:<scope>:<partition>`，scope 为规范化相对目录，
   partition 为稳定候选分区，不包含可变 title。保留 v1 anchor 读取兼容，首次成功
   重扫后写 v2。

### field decisions

- 对每个决定字段收集 tag/folder/filename 候选；按来源优先级过滤后再计票，缺失值不
  计票。高置信单一 tag 为 `high/auto_apply`；少数冲突为 `medium/uncertain_apply`；
  仅 fallback 或无值为 `low/uncertain_apply`。
- candidates 只保存有界的规范化值/票数，reason 使用稳定 code；不保存 raw tag dump。
- Track position 先用 tag/CUE，缺失时使用稳定路径排序；相同 position 以 source
  identity 排序，确保遍历顺序无关。

## 7. Staging 与 candidate persistence

### schema

新增 forward migration（预计 `0011_album_scan_repair.sql`，最终编号以仓库当前版本
为准）：

- `scan_observations`：`scan_run_id/root_id/sequence/relative_path/source_kind/payload`
  或拆列的 bounded typed fields，唯一键保证同一次扫描不重复；payload 仅用于版本化
  变量 CUE/decision 证据，核心 identity/facts 使用类型化列。按 scan/run/root 建索引，
  终态清理已消费或过期 staging。
- `releases`：candidate anchor/kind、album artist、year/source/media 等列及
  `(source_root_id,candidate_anchor)` 唯一约束；旧直属目录索引通过 expand/backfill/
  switch 移除。
- `tracks`：source identity、source kind、CUE parent/index/start/end、audio facts、
  ISRC 等列；root + normalized physical path 和 virtual identity 各有唯一约束。
- `release_grouping_evidence`：按 release 的 current row 唯一，重扫使用 replace/upsert；
  source refs/candidates 有数据库或应用层数量上限。
- `release_field_decisions`：confidence/action CHECK、FK、(release,field) 唯一；重扫
  替换 current rows。

迁移前先 backfill legacy anchor/source identity；不能安全 backfill 的行保持 legacy
标记并可浏览。迁移不写 down，集成测试验证 fresh/rerun/backfill/constraint/rollback。

### persistence sequence

1. scanner 每 128/256 条（具体批大小由基准确定）将 observation 写 staging，遇到解析
   错误继续遍历并记 bounded diagnostic；取消立即停止后续批次。
2. 按 anchor 流式取 staging，调用纯 organizer，逐 candidate 开启短事务：查找/创建
   ReleaseGroup/Release，upsert Medium，按 source identity upsert Track 并更新 medium
   归属；替换该 Release 的 current decisions、credits、grouping evidence。
3. candidate 成功后处理与其关联的 artwork observation：受管文件先安全写入临时 key，
   DB link 在 Release ID 已知后提交；替换旧 link 后清理确认不再引用的受管 key。FS
   写入失败只影响 artwork diagnostic，不回滚已提交的 catalog candidate。命名封面存在
   但为 symlink、非普通文件、空文件、超限或无效格式时属于失败而非“封面不存在”，
   因而不得静默删除旧关系；复用 content-addressed 受管 key 前重新校验实际内容，旧 key
   查询或清理失败必须传播为分类错误。
4. 所有 candidate 处理完成后删除本次 staging。root/run 不完整不执行 missing；成功
   finalize 才按 scan started_at 对未观察 Track 做 missing。旧父实体只要没有 present
   Track 即从普通 query 隐藏，保留行和诊断。

## 8. REST 合同

### list/detail

- list SQL 以 `EXISTS present track` 为普通可见性谓词，稳定排序为 title、artist、id；
  `attention=required` 只接受 allowlist 值，attention count 用 bounded 子查询/聚合
  统计 current uncertain decisions、非空 grouping inconsistency 和 present Release
  下的 missing track。
- detail 查询使用有限 join/batch，返回 album artist/year/source/media、counts、
  Medium/Track facts、credits、artwork 和 role-safe evidence summary；普通用户只能
  看 source category/relative basename 等安全值。
- 新增管理员 endpoint `/api/v1/releases/{id}/evidence`，先验证 session/admin，再
  查询当前 grouping evidence 和 bounded field candidates；数据库错误映射稳定 envelope。
- scan DTO 增加 diagnostics/attention 聚合时，保留旧字段和 wire enum；取消/终态由
  现有状态机负责，不让 handler 推断成功。

### 前端

`frontend/src/api.ts` 作为唯一 decoder：使用 `requireEnum`、nullable 数值/字符串和
bounded array 校验；malformed/403 映射到现有 error contract。`main.tsx` 维护 URL
query 的 `q` 与 `attention`，列表提供 attention 筛选和 badge；详情展示 facts、
credits、summary，管理员才按需请求 evidence endpoint；扫描管理显示 diagnostics
聚合和四种终态。组件不调用 fetch、不保存 token、不构造绝对路径。

## 9. 兼容、回退与安全

- 保持 `/api/v1` 既有认证、root、scan、artwork 路径和旧必填响应字段；新增字段可选，
  前后端同版本发布时切换 decoder。
- 数据库迁移采用 expand/backfill/switch；发布前备份。候选 persistence 出错时只回滚
  当前 candidate 事务并写 diagnostic，禁止旧 scanner 在新唯一约束上继续写入；通过
  forward-fix 恢复。
- 所有相对路径在 parser、staging、REST 三层都不升级成绝对路径；普通用户不返回
  `relative_path` 原值或受管存储物理位置。任何 SQL/日志错误在 transport 边界脱敏。
- 不引入 Redis/Meilisearch/GraphQL/V0 SQLite，不修改根 `music/`，不在应用启动时推断
  schema。

## 10. 未来能力保留

稳定 entity UUID、candidate anchor、field key、source identity 和最小 evidence 可供
未来 overlay/provider/topology/playback 消费，但本任务不写 overlay、不合并跨目录 Release、
不实现人工确认或文件操作。
