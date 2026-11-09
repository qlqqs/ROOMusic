# Backend Directory Structure

## Status And Intent

后端已按能力拆到 `backend/internal/`（B0，2026-09-30 完成，行为保持的纯重构）。`backend/cmd/roomusic`
只剩进程入口 `main.go`、子命令 `reset_password.go` 与前端产物 `web/`；迁移位于 `backend/migrations/`。下方“Current Layout”是实际布局，
“Deferred”列出尚未创建的层和能力。实现尚未满足拆分触发条件时不要创建空目录或伪接口。

## Current Layout

```text
backend/
├── cmd/roomusic/
│   ├── main.go                     # 进程入口：加载配置、连接数据库、装配 app、HTTP 生命周期；带参数时分派子命令
│   ├── reset_password.go           # 子命令 reset-password：读密码、只连库、映射退出码（见身份与成员合同）
│   └── web/                        # Vite 生产产物（go:embed），不手工编辑
├── internal/
│   ├── app/                        # 组合根：装配服务与 handler、挂载全部路由、降级 handler
│   ├── platform/
│   │   ├── config/                 # 环境变量解码与校验
│   │   ├── httpserver/             # 请求 ID 与完成日志中间件、JSON 与错误信封、/api 兜底、
│   │   │                           # 前端静态资源、健康检查
│   │   ├── postgres/               # 连接与迁移执行器：Connect 只连接与 ping，Open = Connect + 迁移
│   │   │   └── postgrestest/       # 仅测试使用：隔离 schema 的测试库
│   │   ├── workpool/               # 固定并发、有界有序流水线与按输入顺序返回的 Map（只依赖标准库）
│   │   └── uuid/                   # UUID 生成与格式校验
│   ├── identity/                   # 会话、密码、Guard（同源、登录、管理员校验）
│   │   └── httpapi/                # setup、登录登出、成员管理
│   ├── library/                    # 路径校验、扫描引擎（遍历、解析、把观察交给 catalog/ingest 暂存）、诊断、扫描执行锁与中断恢复、Service
│   │   ├── parser/                 # 音频与 CUE 解析、与标签无关的音频内容指纹（叶子包）
│   │   │   └── parsertest/         # 仅测试使用：合成音频夹具
│   │   └── httpapi/                # 目录、目录操作、扫描
│   ├── playback/                   # 来源核对、安全打开、可渲染格式表与渲染来源入口（OpenForRender）、旧直出与停机中止
│   │   ├── render/                 # 渲染器：实现 zones 的 Renderer 端口（解码、处理链、换轨与淡化、抖动、编码、HLS 切片、未来事件队列、信号路径）
│   │   │   └── ffmpeg/             # ffmpeg 定位、版本与编解码清单、内嵌素材自检（标准库叶子包）
│   │   ├── analysis/               # 后台音频分析：ffmpeg 探测与自检、一首的响度/真峰值/波形/首尾静音（叶子包，不读数据库，不依赖任何内部包）
│   │   └── httpapi/                # 格式表、旧曲目预检与音频（下一版删除）、音频分析进度
│   ├── dsp/                        # 纯算法包，只依赖标准库：leveling（电平化选择与施加）、fade（淡化曲线）、dither（LFSR/TPDF 量化）
│   │   └── chain/                  # 处理链：Headroom、量化出口、适配器与信号路径步骤（只导入 dsp 算法包）
│   ├── zones/                      # 播放区：设备、队列与传输状态、命令、输出端会话与租约、hub、会话事件应用、服务端计次、协商调用
│   │   ├── queue/                  # 队列纯函数：四种模式、插入移除移动、前进后退、随机预选（标准库叶子包）
│   │   ├── zonestest/              # 仅测试使用：静音 WAV 假渲染器
│   │   └── httpapi/                # 控制接口、事件流（SSE）、输出端认领/回报/释放、长流与 HLS 写出
│   ├── listening/                  # 播放历史：每用户的真实播放记录、计次门槛校验、最近播放的发行与清除（发行归属只经 catalog 公开函数换算）
│   │   └── httpapi/                # 记录一次播放、最近播放、清除
│   ├── personal/                   # 本人收藏、歌单、排序、幂等收据与适应撤销；目录事实只经 catalog 公开读取
│   │   └── httpapi/                # 本人路由、严格解码与鉴权，不含 SQL
│   ├── operations/                 # 受治理操作的共同流程：操作记录、同一操作者同一键的串行与重放、同一事务写记录、撤销与展示的 Kind 接口（不含业务表、规则与 HTTP）
│   │   └── httpapi/                # 操作记录列表与撤销（不导入 catalog，各类型的撤销经 Kind 由 app 注入）
│   └── catalog/                    # Release 图与有效值读取投影、版本对比、修正字段词表与修正意图读取
│       ├── governance/             # 修正、人工归组、默认版本、问题核对与认领的执行器、逆执行器及治理读取 Store
│       ├── ingest/                 # 观察合同、扫描暂存表、身份规划、organizer、来源身份与 Release 图写入（不导入 catalog 核心）
│       ├── internal/releasesql/    # 仅 catalog 子树可导入的共享 SQL 片段与纯格式化函数（依赖 artistgraph 与 textnorm 的当前版本）
│       ├── metadata/               # 署名、空白规范化、有界文本、艺人名规则（叶子包）
│       ├── artwork/                # 受管封面：校验、落盘、关联、读取
│       └── httpapi/                # Release 列表/详情/证据、按组读取与版本对比、封面读取、修正、人工归组、默认版本、问题列表与核对、认领的预览与写入、待认领的读取
├── migrations/
└── testdata/                       # small repository-wide fixtures only
```

`<能力>/httpapi` 是该能力的 REST 传输层，各自提供 `Register(mux)` 注册路由；应用服务放在能力包本身
（例如 `library.Service`、`catalog.Store`、`governance.Store`、`identity.Guard`）。

C2 的 B3 已将治理实现移到 `catalog/governance`，其 `Store` 经 `NewStore(catalog.Queryer)` 构造；
`catalog/httpapi` 分别持有核心与治理 Store，治理调用不再经过核心 Store。核心 `fields.go` 保留修正字段词表、
修正意图类型与读取函数；共享 SQL 片段与纯格式化函数位于 `catalog/internal/releasesql`。
艺人引用门禁从 `artistgraph.Version` 和 `textnorm.Version` 生成版本条件，不另设版本常量。
`releasesql` 可以依赖这两个包；`searchindex` 当前未导入 `releasesql`，后续不得引入反向路径形成循环。
摘要扫描辅助类型与哨兵错误仍在核心，治理继续使用核心错误而不重复定义；
`catalog.SafeRelativeSourceRef` 保留公开入口并委托给共享实现。

## Deferred

- 没有 `domain/`、`application/`、`ports/`、`adapters/` 这几层：每个能力目前只有一个 PostgreSQL 实现，
  为它们建目录只会多一层间接。出现第二个实现或需要隔离持久化时再拆。
- 没有 `search/`：搜索仍是 catalog 的 PostgreSQL 查询。`operations/` 已在 M3 建立（见下），但没有审批队列、Change Set 与执行模式（M6 的 Agent 阶段才需要）。
- 没有 `platform/observability`：日志由 `httpserver` 的中间件和各处 `slog` 调用承担。
- **已知技术债**：`identity/httpapi` 与 `library/httpapi` 中目录、目录操作和成员管理的 handler 仍直接执行 SQL
  （含 F2 的幂等流程），与“handler 不含 SQL”的规则不符。M3 的新操作（修正、归组、撤销）已按 `operations.Run` 实现，handler 只做解析、鉴权与响应映射；
  目录操作与成员管理没有迁入共同流程（目录操作记录不并入操作记录页，成员写操作的接入见[治理合同](./governance-contracts.md)第 11 节与
  [身份与成员合同](./identity-contracts.md)），下一次改动它们时再抽出用例。扫描相关 handler 已只做解析、鉴权和响应映射。
  2026-11-05 新增的改密码与重置接口从一开始就把 SQL 放在 `identity/passwords.go`，已有的成员写入 SQL 原样不动。

## Ownership

- `identity` owns initialization closure, users, password verification, sessions,
  revocation, and admin/user authorization decisions.
- `library` owns allowed-root registration workflows, read-only source
  discovery, parsers, scan runs, diagnostics, and missing-source reconciliation.
- `catalog` 核心拥有 ReleaseGroup、Release、Medium、Track 的读取投影与字段来源读取、有效值视图 `release_effective` 的读取、
  组的生效默认版本与版本对比读取。`fields.go` 保存修正字段词表与修正意图读取；“需要关注”的三类表达式由
  `internal/releasesql/issues.go` 提供，核心不依赖治理执行器。
- `catalog/governance` 拥有发行字段修正表 `release_corrections`、人工修改版本号 `releases.edit_revision`、人工归组、
  专辑默认版本与版本号（`release_groups.default_release_id`、`edit_revision`）、核对表 `release_issue_reviews` 的治理流程，
  提供对应执行器与逆执行器（在 `operations.Run` 给的事务里执行，不碰鉴权、幂等与操作记录表）。
  `governance.Store` 读取修正状态、问题及待认领信息；核对时间与操作者经 `operations.Load` 读取。
  待认领判定与推荐位于 `governance/claims.go`，认领预览与执行器位于 `governance/claim.go`，
  撤销与展示位于 `governance/claim_revert.go`，待认领读取位于 `governance/claim_read.go`。
- `catalog/ingest` 拥有扫描观察合同、保守整理规则、扫描暂存表（`scan_observations`）、身份规划（曲目重绑定、Release 成员关系匹配）与 Release 图的扫描写入。
  library 经 `ingest.StageObservation` 等函数写入暂存，规划需要读文件时通过 ingest 定义的窄端口
  `ingest.Fingerprinter` 向 library 取值（library 在注册 root 内校验路径后调用 parser）。ingest 不导入 catalog 核心、library 或
  parser。library 经 ingest 公开合同完成扫描写入；ingest 的负向对账保留读取 `scan_runs.started_at` 的既有跨表查询，本次不改变。
  library 的读取侧仍通过 `catalog.SafeRelativeSourceRef` 等核心合同取值。M5 管理员报告另消费 `catalog.Store.ListScanAddedAlbums` 与 `catalog/governance.Store.LoadClaimHintsBatch` 的公开只读入口，共用library拥有的只读RR事务，不查询catalog/治理私表。
- `playback` 拥有来源核对与安全打开、可渲染格式表、渲染来源入口 `OpenForRender` 与旧直出（下一版删除），经 catalog、library 公开合同读取来源；
  HTTP 层经 identity 鉴权。规则见[播放格式表与旧直出接口合同](./playback-contracts.md)。派生缓存已退役，`app.RemoveLegacyMediaCache` 在启动时删除旧 `media-cache`。
  `zones` 是播放控制面，`playback/render` 实现它的渲染端口，`dsp/*` 是渲染用的纯算法；三者的依赖方向见下文“播放区”与“DSP”各节，对外协议见[播放区协议](./playback-zone-contracts.md)。
  `playback/analysis_worker.go` 拥有后台音频分析的单线程调度（扫描让路、低优先级、失败不阻塞）、源文件安全打开与进度提供者，经 catalog 公开函数读写分析结果、
  经 library 公开合同核对目录；`playback/analysis` 是只管 ffmpeg 与 PCM 计算的叶子包，由 `app.OpenAnalysis` 探测并装配，见[后台音频分析合同](./audio-analysis-contracts.md)。
- `catalog` 核心的 `track_analysis.go` 拥有表 `track_analyses`（2026-11-08，迁移 0033）：待分析候选、分析结果写入、按曲目读取（含按 Roon 的曲目与专辑增益）与进度计数；
  `catalog/httpapi` 的分析接口不导入 `playback/analysis`，分析器版本由 `app` 经 `WithAnalysisVersion` 注入。
- `listening` 拥有播放历史（`play_history` 表）：计次门槛的服务端校验、按用户隔离的记录、最近播放的发行与清除；曲目时长与所在发行只经
  `catalog.LoadTrackDuration`、`catalog.LoadTrackPlacements` 读取，HTTP 层经 identity 鉴权，所有 SQL 都带 `user_id`，路径与请求体里没有用户 ID。
  见[播放历史合同](./listening-contracts.md)。
- `personal` 拥有收藏及歌单的本人关系、集合/歌单修订号、成功幂等收据与撤销凭证；只经 `catalog` 批量读取安全事实，
  不进入 `operations.Run`、不扩大 `listening`，参见[本人收藏与歌单合同](./personal-contracts.md)。
- `search` owns query semantics and result projections while PostgreSQL remains
  the authority. It cannot write catalog truth through a search projection.
- `operations`（M3）拥有操作记录表 `operations`、共同写入流程 `Run`（幂等键串行与重放、同键异义冲突、同一事务写记录、错误分类）与撤销、展示的 `Kind`
  接口（M4 起目标可以是发行或专辑，列表按目标的并集筛选）；它不含业务表、规则与 HTTP，依赖只有 `platform/uuid`。业务表与版本号列留在拥有它们的能力里（catalog 子树），各类型的撤销能力由 `catalog/governance` 实现、
  经 `app` 注册给 `operations/httpapi`。Change Set、审批与执行模式尚未实现，见[治理合同](./governance-contracts.md)。
  Original sketch for later phases: Change Sets, Operation Journal events, and supported inverse actions.
- `platform` owns mechanisms shared by the process, never feature policy.
- `app` is the only composition root. It may import concrete adapters to wire
  dependencies; domain and application packages may not.

Artwork discovery belongs to the library scan boundary; catalog owns the
release-level association exposed to users. Managed thumbnail storage is a
filesystem adapter writing only to ROOMusic's data directory, never the music
root.

## Dependency Rules

Within a capability:

```text
transport -> application -> domain
                  |
                  v
                ports <- adapters
```

Go construction reverses the runtime dependency: `app` creates an adapter and
passes it through a consumer-owned port. Domain packages import only the standard
library and their own domain vocabulary where practical.

Across capabilities, call a published application contract or narrow port.
Never import another capability's `adapters`, `transport`, persistence row
types, or unexported policy package. Cross-capability orchestration belongs in a
clearly named application service, not a circular callback chain.

依赖方向由 `internal/app/dependency_rules_test.go` 强制：它读取每个 `internal/` 包的导入，要求
每个包在 `packageRules` 中声明允许依赖的内部包，以及是否可以使用 `net/http`（能力核心 `catalog`、
`library`、`parser` 不可以）。新增包必须先声明规则，测试才会通过；CI 的 `go test ./...` 会运行它。

`platform/workpool` 是只依赖标准库的机制包；`library` 与 `catalog/ingest` 可导入，不能把解析、诊断或 SQL 放入池实现。

`platform/weborigin` 是只依赖标准库的平台叶子包；`identity` 与 `platform/config` 允许导入它，共用来源解析与规范化。

C2 的 B3 拆包后，核心 `catalog` 只允许导入内部包 `catalog/internal/releasesql` 与 `platform/uuid`，
不导入 `operations`、`catalog/metadata`、`catalog/ingest` 或 `catalog/governance`。
`catalog/governance` 允许导入 `catalog`、`catalog/internal/releasesql`、`catalog/metadata`、`operations` 与 `platform/uuid`；
`catalog/httpapi` 与 `app` 增加对治理包的依赖，五种操作的撤销与展示实现由 `app` 从治理包注册。
`catalog/ingest` 的基础允许依赖为内部包 `catalog/metadata`、`platform/uuid` 与 `platform/workpool`（其余增量见下文），`library` 经它写入扫描结果。
`catalog/internal/releasesql` 仅允许导入 `catalog/internal/artistgraph` 与 `catalog/textnorm`，用于读取统一版本常量。核心、治理、ingest 与 releasesql 均不允许使用 `net/http`。

## File And Package Naming

- Use short, lowercase Go package names that describe a business concept:
  `session`, `scanrun`, `release`. Avoid `util`, `common`, `manager`, and
  `service` without a business qualifier.
- Name files by responsibility, such as `disable_root.go`,
  `reconcile_missing.go`, or `session_store.go`; do not mirror type names
  mechanically when one cohesive file is clearer.
- Keep tests beside their owner as `*_test.go`. Put feature fixtures in the
  nearest `testdata/`; keep large/private music libraries outside ordinary
  tests.
- REST routes use plural resources under `/api/v1`, for example
  `/api/v1/library-roots`, `/api/v1/scan-runs`, and `/api/v1/releases/{id}`.
- Export only contracts required by another package. Package-private is the
  default.

## Focused Example

A planned `DisableLibraryRoot` use case belongs in
`library/application/disable_root.go`. Its HTTP decoder belongs in
`library/transport/httpapi`; its compare-and-update SQL belongs in
`library/adapters/postgres`; its Change Set collaboration uses a narrow
published operations contract. None of these belong in a global controllers or
repositories directory.

## Anti-Patterns

- Top-level `controllers/`, `models/`, and `repositories/` buckets that mix
  every capability.
- A `platform/database` package exporting feature queries.
- A global service container looked up from handlers or domain functions.
- Parsing FLAC, applying Release grouping, opening transactions, and formatting
  REST responses in one scanner package.
- Creating `plugins/`, RPC hosts, or WASM loaders during Core 0 merely to reserve
  a future directory.

The capability map follows [Modular Design](../guides/modular-design.md) and the
current [Core 0 PRD](../../tasks/archive/2026-09/08-31-roomusic-core-0-rebuild/prd.md).

## M5 ④搜索包增量

`catalog/textnorm` 是纯叶子；`catalog/internal/writegate` 只提供事务 gate；`catalog/internal/searchindex` 的历史 allowlist 允许 textnorm 与 releasesql（当前实际依赖 textnorm/metadata，不导入 releasesql）；`catalog/maintenance` 只依赖 textnorm、searchindex、writegate。catalog 核心增加 textnorm/searchindex，ingest 与 governance 增加 searchindex/writegate；app 增加 maintenance。逐包 allowlist 由依赖测试维护，不允许通配放开 catalog 子树，core 仍不得导入 metadata/ingest/governance。完整在线协议见[搜索合同](./search-contracts.md)。

## M5 ⑤艺人包增量

`catalog/internal/artistgraph`仅依赖textnorm、metadata、searchindex、uuid，负责事实和派生，扫描读意图但不写意图。catalog核心、ingest、governance、maintenance可依赖它；searchindex新增metadata以拆多值署名。catalog核心增加writegate供personal窄公开入口，governance增加textnorm用于范围验证；app注入personal刷新函数，catalog/governance不反向导入personal。精确allowlist见依赖规则测试。

maintenance的原始名字工作量估计经`artistgraph.ArtistInputNameCost`窄端口，不能直接导入metadata或复制拆分算法。调整批处理时先跑`TestPackageDependencyDirections`，不得为纯函数复用扩张allowlist。

## ⑧古典作品叶子包

`catalog/classical` 只依赖标准库，公开作品编号解析、排序键、标题段切分与作品拆分纯函数；不读流派、数据库或容器。仅 `library/parser`（TIT1保护）和 `catalog/ingest`（候选作品分配）获准导入，精确登记于 `app/dependency_rules_test.go`，不扩大核心或其它能力的依赖范围。parser不再仅依赖metadata，作品标签读取仍在parser，扫描写入仍在ingest。

## 播放区 B1 增量

`zones/queue` 是只依赖标准库的队列叶子包；B1 在 `zones` 引入持久化，允许依赖 `catalog`、`platform/uuid`、`zones/queue`，不导入 listening 或 DSP。详见[播放区数据与队列合同](./zones-data-contracts.md)。

## 播放区 B2 增量

`zones` 增加控制 Service、渲染消费方端口、设备与命令、内部 hub；`zones/httpapi` 依赖 `identity`、`platform/httpserver`、`platform/uuid`、`zones`，负责控制接口、SSE、输出端认领/回报/释放与渲染流写出；在线租约、会话事件与计次策略仍属于 zones。
`zones/zonestest` 只依赖 zones 与标准库，拥有静音 WAV 假渲染器；生产 `app` 不允许导入此测试辅助包。
`app.New` 装配 Service 与路由，进程关停经 `WaitForZones` 等待 Service 关闭；真实 Renderer 未装配时不拿假渲染器顶替。服务端合同见[播放区服务端实现合同](./playback-zone-service-contracts.md)，对外协议见[播放区协议](./playback-zone-contracts.md)。

## DSP 第一批纯包合同

### 1. 范围

`dsp/leveling`、`dsp/fade`、`dsp/dither` 是只依赖标准库的算法叶子包；`dsp/chain` 只允许导入这些算法包，精确 allowlist 见依赖方向测试。不接数据库、播放区或前端；由 `playback/render` 的渲染会话调用。

### 2. 签名

- `chain.Build(Spec) (*Chain, error)` 装配 Headroom 与独立量化出口；`Chain.Process(*Buffer) *Buffer`、`Drain() *Buffer`、`Quantize([]byte, *Buffer) ([]byte, int)`、`Reset()`、`Steps() []SignalStep`。
- `chain.Wrap(Processor, Format, Format, SignalStep, bool) *Adapter`；适配器实现 `Node`，另提供 `Drain() *Buffer`。
- `leveling.Apply(samples [][]float64, regions *[]GainRegion, exact *bool)`；`GainRegion` 定义于 leveling，`chain.GainRegion` 是别名。调用方式为 `leveling.Apply(b.Samples, &b.Regions, &b.Exact)`，避免 leveling 反向导入 chain。

### 3. 合同

样本为 planar float64。适配器按流内绝对帧号映射区域，丢弃流头固有延迟，流尾喂零至总输出为 `floor(总输入 × 输出率 / 输入率)`；输入元数据保留至对应输出产生，不能以当前输入块的 `Exact` 冒充延迟输出的属性。`Drain` 分块返回，调用至 nil；返回缓冲只在下次调用前有效。容量增长后复用，不逐块分配。

增益由分析阶段算好，Choose 只选择、不再限峰值。Apply 清空区域，只有增益不等于 1 时清 Exact。Headroom 启用即清 Exact。量化直通条件为 `Exact && 0 < SourceBits && SourceBits <= OutputBits`，直通不推进 LFSR。Steps 只含构造时的节点，逐曲目的电平化/抖动显示由后续渲染会话添加。

### 4. 校验与错误

Build 的采样率/声道非正、输出位深非 0/16/24、启用的 Headroom 超出 −30…0 dB 或不符合 0.1 dB 步进，返回错误。共享链收到未施加区域、冲刷后未经 Reset 又 Process，以及纯算法的非法装配参数，视为程序错误并 panic；不作为用户输入边界使用。

### 5. 正常、基准与错误案例

正常：延迟处理器的输出沿用对应来源的元数据，尾部补齐真实输入的帧数。基准：16 位精确源经关闭 Headroom 的链输出 16/24 位时逐位还原。错误：在链头仍携带电平化区域，或把补零当成新输入计入长度。

### 6. 必需测试

独立测试 agent 补充：固定延迟与攒块冲刷、变率绝对边界、跨块 Exact/SourceBits、区域入口 panic、选择表与 Apply、淡化分块公式、LFSR/TPDF/量化、稳态分配。具体向量见三个任务设计的测试节；本次实现自查不代替算法单测验收。

### 7. 错误与正确调用

错误：循环复用未清空的 Regions，重复电平化；直接把 Exact 传给量化器而忽略降位深。正确：来源段先 Apply 清空区域，再进入共享链，使用 `Chain.Quantize` 判断逐位直通，流尾循环 Drain 并立即消费每块。

## DSP 渲染骨架增量（P-c）

`playback/render/ffmpeg` 是标准库叶子包；`playback/render` 只依赖 zones 端口、dsp 算法与 chain、ffmpeg 和 uuid，不反向导入 playback 或 catalog。app 通过 `OpenForRender` 与 catalog 的分析读取实现 TrackSource。生产装配使用真实 Renderer，测试假渲染器保留；关停先关闭 zones 再等待 Renderer 回收。派生已在 P-d 删除。详见[服务端渲染合同](./playback-render-contracts.md)。
