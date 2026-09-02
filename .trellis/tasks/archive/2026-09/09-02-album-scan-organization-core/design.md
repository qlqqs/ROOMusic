# V0 自动整理能力恢复技术设计

## 设计结论

本任务恢复的是 V0 已验证的自动整理行为，不恢复 V0 的运行时形态。当前 ROOMusic
继续保持一个 Go 模块化单体、PostgreSQL 唯一业务权威和 `/api/v1` REST；现有扫描
锁、取消、异常恢复、只读路径安全与成功扫描才执行 `missing` 的合同全部保留。

实现只增量建立三个真实边界，不借本任务进行全仓目录重构：

1. **Library parser**：只读遍历、格式解析、CUE/log 解析和来源观察；
2. **Catalog organizer**：纯函数完成候选归组、字段选择、credits 和图谱组装；
3. **Catalog persistence/query**：PostgreSQL 短事务、重扫对账和 REST 读投影。

最小 evidence 与只读 attention 投影属于自动整理的正确性合同，而不是独立治理
产品。人工确认、metadata overlay、图谱 merge/split、AI 和独立问题工作台不进入
本设计。

## 当前架构落点

当前实现仍集中在 `backend/cmd/roomusic`，因此第一版使用同 package 的窄类型和
文件边界，例如 parser、organizer、catalog store/query；不先创建空的
`internal/*` 分层。等第二个真实消费者出现后，再按已有接口机械迁移目录。

```text
HTTP / scan coordinator（保留）
              |
              v
只读 walker -> parser adapters -> scan-source observations
                                  |
                                  v
                         pure catalog organizer
                                  |
                                  v
                   candidate graph + decisions
                                  |
                                  v
                     PostgreSQL catalog store
                                  |
                                  v
                  REST query -> frontend decoder -> view
```

所有跨边界值均为类型化合同。walker 不执行 Release SQL，organizer 不访问文件系统
或数据库，repository 不决定多数决、合碟或 CUE 去重策略，HTTP handler 不包含整理
规则。

## 扫描与有界暂存

候选归组需要看到同目录文件、多碟兄弟目录和 CUE/分轨共存关系，不能继续在发现
单文件时立即建 Release；但 ROOMusic 面向大型 NAS，也不能把整个 root 的音频和
标签无界保存在内存。

采用两段式 root 流程：

1. walker 按稳定相对路径顺序发现来源，parser 产出有界的 `SourceObservation`；
2. PostgreSQL adapter 以短批次写入 scan-run 临时观察；
3. root 遍历结束后，organizer 按目录作用域流式读取观察，生成稳定排序的候选；
4. 每个候选以独立短事务写入当前 catalog；
5. scan 进入终态后清理临时观察；启动恢复也清理已失败旧 run 的暂存。

临时观察至少包含 scan/root、规范化相对来源标识、目录、来源种类、核心 tag、音频
事实和 CUE 关系。它可以使用带 schema version 的有界 JSONB payload，因为它是
可重建的 scan staging，不是查询权威；root、path、kind 等分组和约束字段保持类型化
列。不得保存完整 raw tag dump、封面字节、绝对路径或日志全文。

解析、遍历或安全错误仍写入当前 bounded scan diagnostics。有效来源可以继续参与
整理，但只要 root 不完整，整个扫描就不得进入 `succeeded`，也不得执行负向对账。

## Parser 合同

`SourceObservation` 是 parser 的唯一输出，主要包含：

- `root_id`、安全相对路径、目录和 `physical|cue_virtual` 来源种类；
- title、album、album artist、track artist、date/year、disc/track number；
- release/source/media/provider/edition、label、catalog、barcode、genre；
- composer、conductor、performer、producer 等原始 display credit；
- duration、codec、bit depth、sample rate、channels、bitrate、ISRC；
- 每个值的来源类别与原始键名等最小 parser evidence；
- CUE sheet、父音频、track/index 和 offset 关系；
- 受支持的 folder/log 结构观察。

parser 只清理编码、控制字符、长度和显然无效的数值，不投票、不合并目录、不把音频
规格解释为发行来源。缺失字段保持空。

M4A 是当前真实资产需要的必选 adapter。实施前对纯 Go 依赖做一次窄评估：必须支持
MP4/M4A 标签、AAC/ALAC 区分和有界读取，许可证可接受且无需 CGo。V0 使用过的
`dhowden/tag` 可作为 tag 行为证据，但其音频属性覆盖不足，不能未经验证就成为唯一
adapter。该依赖选择不改变本设计的 parser 合同。

EAC/XLD log parser 只产出明确的 CD/source/media 证据和安全诊断；不保存全文，也不
根据模糊文本生成质量评分。

## 候选归组与稳定锚点

### 纯规则输入输出

organizer 输入同一安全组织作用域内按来源身份排序的 observations，输出：

- `CandidateAnchor`；
- 一个初始独立 ReleaseGroup；
- Release、Medium、Track 当前 base graph；
- field decisions、grouping evidence 和 attention reasons；
- artwork 的候选来源标识，不直接读取或写入图片。

所有投票和 tie-break 在规范化值相同的前提下计数，并以稳定值、来源优先级和相对
来源标识排序；不得依赖 `WalkDir` 返回顺序、map 遍历顺序或随机 UUID。

### CandidateAnchor

持久化锚点使用版本化 opaque key，逻辑组成是：

```text
root_id + anchor_version + candidate_kind + scope_path + optional partition_key
```

各类锚点规则：

| candidate kind | scope | partition | 身份行为 |
| --- | --- | --- | --- |
| ordinary directory | 当前叶目录 | 空 | album/title 改变仍复用 Release |
| strict multidisc | 多碟父目录 | 空 | 只在严格合碟证据成立时使用 |
| box leaf | leaf 目录 | 空 | 父 collection 只作 evidence |
| same-dir split | 当前目录 | 规范化 album + album artist 摘要 | 明确多 album 时稳定拆分 |
| loose album | loose 文件所在 scope | 规范化 album + album artist 摘要 | 有 album evidence 的散落文件归组 |
| loose unknown | 单个物理来源身份 | 空 | 无 album evidence 时一来源一 Release |

`partition_key` 只保存稳定摘要，不把用户标签或路径拼入公开 ID。同一普通目录仅
metadata 改变时 anchor 不变；single/split、leaf/multidisc 等候选形状发生实质变化
时可以产生新 Release，但已有 Track 仍按来源身份复用并原子重挂。旧 Release 没有
present Track 后从普通查询隐藏，不伪装成同一语义版本。

一 Release 一初始 ReleaseGroup。标题、艺术家、年份或目录相似度不会跨目录 hard
merge；将来版本聚合由独立 topology 治理能力处理。

### 核心规则

- tag 优先，folder 只补缺；明确 tag 不被目录名覆盖；
- album/album artist 采用多数决并保留少数候选，无明显多数的明确 album 分歧才拆候选；
- 多碟必须同时满足 Disc/CD/Disk 结构、相同 album 证据、从 1 连续且不重复的碟号；
- Box/collection 保持 leaf Release，父目录只进入 `parent_collection` evidence；
- loose 文件有 album evidence 时归组，无 album 时生成“未知专辑”单来源候选；
- source/media/release type、provider、edition、label/catalog/barcode 只来自明确证据；
- credits 使用确定性分隔；保留原 display credit，歧义组合标为 `uncertain_apply`；
- 除明确“未知专辑”兜底外，不制造缺失字段。

## CUE 模型与去重

### 解码与安全

CUE 解码顺序固定：BOM 指示的 UTF-8/UTF-16、有效 UTF-8，再按确定性策略尝试
GBK、Shift-JIS、Big5；无法无损解码或存在同等歧义时产生诊断，不静默使用平台
locale。支持多 `FILE`、sheet/track TITLE/PERFORMER、REM DATE/GENRE、CATALOG、
ISRC 和 INDEX 01。

每个 `FILE` 引用均相对 CUE 所在目录解析。拒绝绝对路径、volume、`..` 越界、
symlink 逃逸和越出同一注册 root 的真实目标；诊断只含安全相对标识。

### 来源身份

- 物理 Track：`root_id + normalized relative path`，保持当前稳定身份；
- CUE 虚拟 Track：`root_id + cue relative path + parent relative path + track number +
  INDEX 01 offset` 的版本化摘要；
- Track 另存 cue sheet、父来源、index frames、start/end/duration 供详情与未来播放使用。

同一父文件的下一条 INDEX 01 决定前一 Track 的 end；最后一轨在已知音频时长时以
父时长收尾，否则 end 保持空且给出非阻断性不确定标记。

### 共存去重

- 整轨音频 + CUE：展示虚拟 Tracks，父整轨只作为物理来源，不再单独成为可见曲目；
- CUE + 明确真实分轨：真实分轨优先，CUE 保留为 evidence；
- 多文件 CUE：按每个 `FILE`/track 的映射判断，不能因为存在 CUE 就全局排除所有
  真实音频；
- 去重只在同一候选和明确来源关系内发生，不用标题/时长近似跨目录删除曲目。

## Catalog 数据模型

下一版本迁移采用 expand/backfill/switch 方式扩充当前 schema，不改写已发布迁移。

### Release Graph

- `releases` 增加 candidate key/kind/scope、album artist display、date/year、release
  type、source/media type、provider、edition、label/catalog/barcode、parent collection；
- `media` 保证 `(release_id, position)` 唯一，保存 title/format；track count 查询时
  派生，避免可漂移计数器；
- `tracks` 增加 source kind/identity、CUE 关系、duration、codec、bit depth、sample
  rate、channels、bitrate、ISRC；Hi-Res 由事实和版本化规则派生，不作为身份；
- 物理 Track 继续唯一约束 root + 规范化相对路径；统一 source identity 另有唯一
  约束，以支持多个 CUE 虚拟 Track；
- artwork 仍使用 ROOMusic 管理的数据目录和现有 `release_artworks`，只把选择结果
  绑定到 organizer 生成的 Release。

既有 `releases_source_directory` 唯一索引不能表达同目录多候选，迁移在 backfill
candidate anchor 后替换为 `(source_root_id, candidate_anchor)` 唯一约束。旧数据先标为
`legacy_directory`，沿用原 Release/Track UUID；首次新扫描再生成完整 decisions。
该迁移不提供破坏性 down，生产回退依赖迁移前备份或 forward-fix。

### Credits、label 与 genre

建立本地 `artists` 与 release/track credit 关系，credit 至少包含 role、position、
credited/display name。artist 只按当前确定性规范化名称复用，不进行 MusicBrainz
identity、跨语言 alias 或近似合并。原始组合 display credit 始终保存在所属实体。

label/genre 使用本地规范化实体与 Release 关系；只做大小写/空白等确定性规范化。
provider 仍是 Release 字段，不提前建设外部 provider 账户或 identity 模型。

### 当前 field decisions

逻辑合同为：

```text
entity + field_key + selected_value + selected_source
+ confidence + action + rule_id
+ optional candidates/reason
+ scan_run_id + observed_at
```

存储采用 release/medium/track 三张 FK-backed current-decision 表，避免无外键的多态
`entity_id`。query 层把它们统一投影为 REST DTO。`selected_value` 和受限 candidates
可用 JSONB，因为权威可查询值已经存在实体类型化列中，JSONB 只承载版本化证据。

每次重扫替换该实体/字段的当前 decision，不追加无限历史。candidates 只保存有界的
值、来源类别和票数；不保存 raw tag dump 或绝对路径。grouping evidence 单独按
Release 保存 candidate kind、规则、受限相对 source refs、parent collection 和
reason code。

`confidence` 仅允许 `high|medium|low`，`action` 仅允许
`auto_apply|uncertain_apply`。本任务没有 confirmation writer，因此不建
confirmation status、correction path 或 issue lifecycle。

## 候选事务、重扫与可见性

全局 PostgreSQL scan coordination 已保证同一时刻只有一个 scanner writer。每个
候选在一个短事务中：

1. 按 candidate anchor 查找或创建 Release/初始 ReleaseGroup；
2. upsert Media；
3. 按 source identity 查找 Track，保持 ID 并更新归属和当前字段；
4. 替换当前 credits、label/genre、field/grouping decisions；
5. 提交后才让该候选的新形状可见。

不得在遍历或读取 tag 时持有事务。候选按 anchor 排序，事务失败写诊断并使 run
不完整，但不回滚已成功候选。

本任务延续当前“增量可见、终态安全”语义，不引入整库 generation/snapshot：扫描中
已提交候选可以被浏览；失败或取消时未访问的旧 Track 仍保持 present，绝不执行
missing。完整成功后才在 finalize 事务中把未观察来源标为 missing。

普通 Release 查询必须存在至少一个 present Track；Medium/Release 空壳和全 missing
历史只供管理员诊断。候选变化后，已移动的 Track 不会同时留在旧 Medium；因此旧
父实体一旦无 present Track 就立即从普通查询消失。扫描进行中的短暂新旧混合状态
由 scan status 明确表达，将来若产品要求整库原子切换，再单独引入 catalog generation。

## REST、权限与前端

### REST 投影

- `GET /api/v1/releases` 保持稳定分页和搜索，新增核心摘要、Medium/Track 数、
  `attention_count`，并支持 `attention=required`；默认只查含 present Track 的 Release；
- `GET /api/v1/releases/{id}` 返回完整 Medium/Track、audio facts、credits、label/genre
  和不泄露路径的 field decision 摘要；
- 管理员专用 `GET /api/v1/releases/{id}/evidence` 返回有界冲突 candidates、reason
  code 和安全相对来源标识；普通用户无权访问；
- 复用 `GET /api/v1/scans/{id}/diagnostics` 展示硬诊断，并在 scan/status DTO 中增加
  有界聚合计数，不创建扫描历史中心；
- 所有枚举、分页、筛选和 ID 在 transport 边界校验；SQL 排序/筛选来自 allowlist。

`attention_count` 是当前 `uncertain_apply` 字段、grouping inconsistency 和可见
Release 下 missing 来源的去重计数。硬 parse/permission/catalog 错误只属于 scan
diagnostics，不伪装为某个 Release 的 field issue。

### 前端闭环

沿用 React、原生状态和当前样式，不引入 GraphQL、query/cache、状态库或第二套 UI
框架：

- Release 列表显示 attention badge，并把 `attention=required` 放入 URL；
- Release 详情展示字段、Medium/Track、credits/audio facts 和安全 evidence 摘要；
- 管理员可展开冲突 evidence，并在现有管理区读取 scan diagnostics；
- 统一提示“修正外部源文件或标签后重扫”，不出现 confirm/edit/merge 控件；
- `frontend/src/api.ts` 对每个新增 payload 做一次 runtime decode，组件不接收
  `unknown` 或 raw cast。

## 后续能力的保留缝隙

### Metadata overlay

本任务产出的实体 UUID、稳定 `field_key` 和 scanner base decision 是未来 overlay 的
输入。overlay 将作为独立表和 Operations command 保存人工值、base evidence guard、
revision、idempotency、before/after 与 revert；resolved projection 才执行
`overlay > scanner base`。scanner 永远不把人工值伪装成 tag，也不覆盖 overlay。

### AI 与外部 provider

Music Steward/provider 只消费经过角色裁剪的 evidence projection，返回 candidate 或
typed overlay proposal；不能直接更新 base tables。当前 rule ID 和 confidence 可用于
定位需要帮助的字段，但本任务不预建 ledger、memory 或模型 payload。

### Topology governance

Release/ReleaseGroup merge/split、Track 重挂和跨目录版本聚合使用 grouping evidence、
candidate anchor 与稳定 Track identity，但必须另行设计 topology revision、影响范围、
stale guard 和逆操作。本任务的“一 Release 一 Group”不会妨碍后续关系重建。

### Search 与 playback

搜索只索引当前 catalog/REST projection，PostgreSQL 继续是 authority；未来
Meilisearch 可随时重建。播放直接消费物理 source identity 或 CUE parent + start/end，
不需要重新解析整理规则。

## 兼容性、失败与回退

- 现有 REST 路径、认证、封面资源、扫描状态和取消 wire 枚举保持兼容；新增字段为
  向后兼容扩展，前端与后端在同一发布中切换 decoder；
- legacy catalog 在首次重扫前仍可浏览，evidence 缺失不被伪造为 attention；
- staging、candidate 或 evidence 写入失败会产生有界诊断并使扫描 incomplete；
- 不读取、改写或把真实 `music/` 纳入测试；所有格式与规则使用小型合成 fixture；
- schema 迁移后的二进制回退不保证兼容多候选数据，发布前必须备份；失败后优先
  forward-fix，不执行破坏性自动 down migration；
- Redis、Meilisearch、GraphQL、V0 SQLite 与真实文件写入均不成为 fallback。

## 关键取舍

1. 保留最小 evidence 和嵌入现有页面的 attention 入口，因为 coverage-first 会直接
   应用不确定值；去掉它们会让错误结果不可解释、不可验收。
2. 不保留完整 V0 evidence/confirmation/issue 平台，因为当前没有任何 writer 或
   人工生命周期消费它们。
3. 采用 scan staging + candidate 短事务，而不是全 root 内存图或扫描期长事务。
4. 候选形状变化允许 Release ID 变化，但 Track 来源身份保持稳定；这比用标题伪造
   永久 Release identity 更保守。
5. 当前不引入 catalog generation；延续已落地的增量可见语义，并把整库原子快照作为
   有明确产品需求后再增加的能力。
