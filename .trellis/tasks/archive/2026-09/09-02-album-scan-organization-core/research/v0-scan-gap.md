# V0 扫描整理能力与当前实现差距

## 结论

当前版本已经具备安全的扫描运行骨架，但“专辑整理”仍停留在最小纵向切片。用户已明确 V0 的整理能力是本任务最重要的产品能力，因此最值得继承的不只是若干归组规则，而是它已经验证过的 coverage-first 整理母线与 Release Graph 字段语义：遍历、解析证据、候选归组、字段决策、组装图谱、事务持久化和只读投影应当重新形成清晰边界。

V0 对“整理”的定义不是只输出诊断：高置信结果 `auto_apply`，中低置信结果 `uncertain_apply`，两者均形成可用默认图谱；所有决定保留原始值/候选、来源、置信度、规则 ID、冲突或未决原因、确认状态及修正路径。这个合同见 `ROOMusic-V0/.planning/quick/260528-scanner-policy-alignment/260528-scanner-policy-PLAN.md`、`ROOMusic-V0/internal/evidence/types.go` 与 `ROOMusic-V0/internal/scanner/evidence.go`。

V0 的 GraphQL、Redis/asynq、Meilisearch、AI/metadata overlay、完整 generation/fencing、拓扑合并和已经退役的 golden SQLite 工作流不应随扫描器一起迁入。

## 当前版本已经具备

- PostgreSQL 持久化扫描状态、跨进程 advisory lock、取消意图、异常恢复和仅成功终态执行 `missing` 对账（`backend/cmd/roomusic/scans.go:1-253`、`backend/cmd/roomusic/scanner.go:63-192`）。
- 允许目录、真实路径 containment、只读扫描和符号链接边界（`backend/cmd/roomusic/scanner.go:194-365`）。
- FLAC、MP3、OGG、Opus、WAV 的基础标签读取，以及 UTF-8/UTF-16、单 `FILE` CUE 子集（`backend/cmd/roomusic/audio_parser.go:28-501`）。
- 最小 `ReleaseGroup -> Release -> Medium -> Track`、字段观察和发行封面表（`backend/migrations/0002_core_slice.sql` 至 `0005_release_artwork.sql`）。
- Release 列表/详情、扫描启动/查询/取消 API 与 React 浏览骨架（`backend/cmd/roomusic/application.go:315-490`、`frontend/src/api.ts:11-220`、`frontend/src/main.tsx:89-319`）。

## 影响核心体验的差距

### 1. 归组还不是真正的专辑候选流水线

- 当前每处理一个文件就直接按“文件所在的直属相对目录”查找或创建 Release（`backend/cmd/roomusic/scanner.go:371-424`）。
- `Disc 1/Disc 2` 因直属目录不同会形成两个 Release，而不是父目录下一个 Release 的两个 Medium。现有集成测试只断言 Medium 总数为 2，没有断言它们属于同一个 Release（`backend/cmd/roomusic/auth_root_operations_integration_test.go:404-433`）。
- 根目录散落文件全部共享空目录锚点，无法按 album tag 分组；同目录存在多个明确 album tag 时也没有稳定拆分。
- Release 的标题和艺术家只由第一个创建它的文件决定；后续文件不会做多数决，也不会保存 album/album artist 冲突。

### 2. CUE 会产生不正确或不完整的曲目形状

- 当前 CUE 只支持一个 `FILE`，缺少 sheet 级标题/表演者、REM 日期/流派、CATALOG/ISRC、常见 CJK 编码与多文件 CUE。
- CUE 虚拟 Track 没有持久化父音频来源、开始/结束偏移和时长。
- 被 CUE 引用的整轨音频随后仍会按普通文件处理，可能与虚拟 Track 同时出现在图谱；V0 的已确认规则是“整轨+CUE 使用虚拟 Track；CUE 与分轨共存时优先分轨，CUE 只保留为证据”。

### 3. 元数据与音频事实不足以支撑整理视图

- 当前核心字段只有 release `title/artist`、medium `position/title`、track `title/artist/position/disc_number`。
- V0 已验证的核心 release evidence 包括 album artist、year/date、release/media/source type、provider、edition、label、catalog number、barcode、parent collection、candidate kind、grouping evidence 和 inconsistency。
- V0 已验证的核心 track evidence 包括 source kind、CUE parent/offset、duration、bit depth、sample rate、channels、codec、bitrate、Hi-Res 与核心 credits。
- 当前手写 parser 只覆盖少数字段；相邻真实资产的已记录格式包含 `.m4a`，而当前扫描会把它诊断为 unsupported。

### 4. 重扫会留下图谱残骸

- Track 的同 root+相对路径身份能够复用，这是应保留的当前合同。
- 但文件标签改变、目录候选拆分/合并或碟号改变时，旧的空 Medium/Release 不会被候选级对账清理或隐藏。
- Release 列表没有限定至少存在一个 `present` Track，可能展示只剩 `missing` 来源的空壳；前端也没有缺失来源或整理问题状态。
- 需要在不物理删除来源历史的前提下，定义候选级幂等 upsert、空父实体可见性和完整成功扫描后的负向对账。

### 5. 前端只有操作按钮，没有整理工作台

- 页面能启动、轮询、取消扫描并浏览基础 Release，但没有读取现有诊断 API。
- Scan DTO 没有目录/文件/候选/诊断计数或当前阶段，无法表达有意义的进度。
- 没有扫描历史、问题筛选、来源/推断证据、缺失状态、CUE/多碟形状或丰富音频字段展示。
- 扫描进行时 Release 列表不会按进度主动刷新，因此当前“边扫描边浏览”只在后端偶然成立。

## 建议继承的 V0 产品规则

以下规则来自 `ROOMusic-V0/.planning/phases/01-data-layer-scanning/01-CONTEXT.md` 与 `01.1-roon-style-release-graph-refactor/01.1-CONTEXT.md`，并与当前只读/Core 0 边界兼容：

- tag 优先、folder name 仅补缺；音频规格不反推来源、版本或厂牌。
- 同一专辑中的少量 tag 冲突以确定性多数决处理并保存 inconsistency；明确不同 album tag 且没有明显多数时才拆候选。
- 多碟合并同时要求结构证据、相同 album 证据和连续碟号；证据不足时保留 leaf releases。
- Box/collection 父目录只保存为 evidence，不凭目录形状制造假 Release。
- 每个本地 Release 初始拥有独立 ReleaseGroup，不做弱相似跨目录自动合并。
- 散落文件有 album tag 时按 album 归组；无 album tag 时成为独立 `未知专辑` Release。
- CUE 整轨生成虚拟 Track；CUE 与真实分轨共存时优先分轨，不重复展示。
- 缺失字段保持空，只有明确业务兜底才使用占位值。
- `folder_path`/相对目录是本地重扫锚点，不是 Release 的语义身份。
- parser 只产出原始证据与最小规范化；assembler 决定候选和 Release Graph；repository 独占 SQL 与事务。
- scanner 采用 coverage-first：确定性高置信字段 `auto_apply`，中低置信字段 `uncertain_apply`；不确定结果仍进入正常 graph/default value，同时保留 evidence、confidence、rule id、confirmation status 和 correction path。
- “不确定但已有可解释默认值”与“无法解析/无法可靠归组”是两类状态：前者进入整理结果并标记，后者才进入阻断性诊断。
- Artist credit、label/provider/catalog、source/media 和软版本/collection 线索均需保留字段级判断依据；软关联不得升级为跨目录 hard merge。

## 不建议继承的 V0 范围

- GraphQL、Redis/asynq、Meilisearch、AI worker、外部 metadata provider。
- ReleaseGroup/Artist/Track 的 merge、split、redirect、topology governance。
- metadata overlay、人工编辑、pin/revert；用户已明确本任务只恢复自动整理，这些能力另立后续任务。
- lease/generation/fencing 完整状态机；当前已落地的 PostgreSQL session advisory lock 合同继续有效。
- 已退役的 golden SQLite 运行链路。应把有代表性的 V0 行为改写为当前仓库的小型合成 fixture 与单元/集成测试，不读取真实 `music/` 作为自动化测试输入。

## 已收敛的范围决定

- M4A 是当前真实资产事实所要求的首个闭环格式，纳入本任务；adapter 必须为纯 Go、
  支持 AAC/ALAC 区分并使用小型合成 fixture 验证，自动化测试不读取真实资产。
- 不建设跨扫描人工 issue 生命周期。当前专辑 attention 从最新
  `uncertain_apply`、grouping inconsistency 与 missing 状态派生；硬解析、安全和
  catalog 错误继续使用 scan-run diagnostics。
- 最小 evidence 与嵌入 Release 列表/详情的只读入口保留，因为它们是 coverage-first
  自动应用不确定结果时的解释和验收边界；confirmation、correction path、overlay、
  review/revert 和独立大型问题工作台全部延后。
