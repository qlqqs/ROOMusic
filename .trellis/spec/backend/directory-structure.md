# Backend Directory Structure

## Status And Intent

后端已按能力拆到 `backend/internal/`（B0，2026-09-30 完成，行为保持的纯重构）。`backend/cmd/roomusic`
只剩进程入口 `main.go` 与前端产物 `web/`；迁移位于 `backend/migrations/`。下方“Current Layout”是实际布局，
“Deferred”列出尚未创建的层和能力。实现尚未满足拆分触发条件时不要创建空目录或伪接口。

## Current Layout

```text
backend/
├── cmd/roomusic/
│   ├── main.go                     # 进程入口：加载配置、连接数据库、装配 app、HTTP 生命周期
│   └── web/                        # Vite 生产产物（go:embed），不手工编辑
├── internal/
│   ├── app/                        # 组合根：装配服务与 handler、挂载全部路由、降级 handler
│   ├── platform/
│   │   ├── config/                 # 环境变量解码与校验
│   │   ├── httpserver/             # 请求 ID 与完成日志中间件、JSON 与错误信封、/api 兜底、
│   │   │                           # 前端静态资源、健康检查
│   │   ├── postgres/               # 连接与迁移执行器
│   │   │   └── postgrestest/       # 仅测试使用：隔离 schema 的测试库
│   │   └── uuid/                   # UUID 生成与格式校验
│   ├── identity/                   # 会话、密码、Guard（同源、登录、管理员校验）
│   │   └── httpapi/                # setup、登录登出、成员管理
│   ├── library/                    # 路径校验、扫描引擎（遍历、解析、把观察交给 catalog 暂存）、诊断、扫描执行锁与中断恢复、Service
│   │   ├── parser/                 # 音频与 CUE 解析、与标签无关的音频内容指纹（叶子包）
│   │   │   └── parsertest/         # 仅测试使用：合成音频夹具
│   │   └── httpapi/                # 目录、目录操作、扫描
│   ├── playback/                   # 鉴权交付核心：来源核对、安全打开、格式表、周期复核与派生编排
│   │   ├── derivation/             # 无损派生：受管缓存、转换任务、ffmpeg 适配与输出核对（不含 HTTP，不依赖任何内部包）
│   │   └── httpapi/                # 格式表、曲目预检（含派生等待）与音频
│   ├── listening/                  # 播放历史：每用户的真实播放记录、计次门槛校验、最近播放的发行与清除（发行归属只经 catalog 公开函数换算）
│   │   └── httpapi/                # 记录一次播放、最近播放、清除
│   ├── operations/                 # 受治理操作的共同流程：操作记录、同一操作者同一键的串行与重放、同一事务写记录、撤销与展示的 Kind 接口（不含业务表、规则与 HTTP）
│   │   └── httpapi/                # 操作记录列表与撤销（不导入 catalog，各类型的撤销经 Kind 由 app 注入）
│   └── catalog/                    # 观察合同、扫描暂存表、身份规划、organizer、来源身份、Release 图写入、读取投影、修正、人工归组与默认版本的执行器、有效值视图、版本对比
│       ├── metadata/               # 署名、空白规范化、有界文本、艺人名规则（叶子包）
│       ├── artwork/                # 受管封面：校验、落盘、关联、读取
│       └── httpapi/                # Release 列表/详情/证据、按组读取与版本对比、封面读取、修正、人工归组与默认版本
├── migrations/
└── testdata/                       # small repository-wide fixtures only
```

`<能力>/httpapi` 是该能力的 REST 传输层，各自提供 `Register(mux)` 注册路由；应用服务放在能力包本身
（例如 `library.Service`、`catalog.Store`、`identity.Guard`）。

## Deferred

- 没有 `domain/`、`application/`、`ports/`、`adapters/` 这几层：每个能力目前只有一个 PostgreSQL 实现，
  为它们建目录只会多一层间接。出现第二个实现或需要隔离持久化时再拆。
- 没有 `search/`：搜索仍是 catalog 的 PostgreSQL 查询。`operations/` 已在 M3 建立（见下），但没有审批队列、Change Set 与执行模式（M6 的 Agent 阶段才需要）。
- 没有 `platform/observability`：日志由 `httpserver` 的中间件和各处 `slog` 调用承担。
- **已知技术债**：`identity/httpapi` 与 `library/httpapi` 中目录、目录操作和成员管理的 handler 仍直接执行 SQL
  （含 F2 的幂等流程），与“handler 不含 SQL”的规则不符。M3 的新操作（修正、归组、撤销）已按 `operations.Run` 实现，handler 只做解析、鉴权与响应映射；
  目录操作与成员管理没有迁入共同流程（目录操作记录不并入操作记录页，成员写操作的接入见[治理合同](./governance-contracts.md)第 11 节与
  [身份与成员合同](./identity-contracts.md)），下一次改动它们时再抽出用例。扫描相关 handler 已只做解析、鉴权和响应映射。

## Ownership

- `identity` owns initialization closure, users, password verification, sessions,
  revocation, and admin/user authorization decisions.
- `library` owns allowed-root registration workflows, read-only source
  discovery, parsers, scan runs, diagnostics, and missing-source reconciliation.
- `catalog` owns ReleaseGroup, Release, Medium, Track, key field provenance, and
  conservative grouping rules. M3 起还拥有发行字段的修正表 `release_corrections`、人工修改版本号 `releases.edit_revision`、有效值视图
  `release_effective`，以及字段修正与人工归组的执行器和逆执行器（在 `operations.Run` 给的事务里执行，不碰鉴权、幂等与操作记录表）；
  M4 起还拥有专辑的默认版本与版本号（`release_groups.default_release_id`、`edit_revision`）、默认版本的执行器与逆执行器，以及版本对比的读取。扫描暂存表（`scan_observations`）与身份规划（曲目重绑定、Release 成员关系匹配）也归它：
  library 经 `catalog.StageObservation` 等函数写入暂存，规划需要读文件时通过 catalog 定义的窄端口
  `catalog.Fingerprinter` 向 library 取值（library 在注册 root 内校验路径后调用 parser），catalog 不导入 library 或
  parser，两边不读对方的私有表。
- `playback` 拥有只读直出、派生交付编排与周期复核，经 catalog、library 公开合同读取来源，经 `playback/derivation` 的窄端口取得派生文件；
  HTTP 层经 identity 鉴权。具体错误、Range 与资源关闭规则见[播放合同](./playback-contracts.md)。
  `playback/derivation` 拥有缓存目录、转换任务与 ffmpeg 适配，不知道曲目与权限，由 `playback` 提供源文件版本与“安全打开并核对”的回调，
  由 `app.OpenDerivation` 在启动时装配（含缓存目录与音乐根互不包含的检查），见[无损派生合同](./playback-derivation-contracts.md)。
- `listening` 拥有播放历史（`play_history` 表）：计次门槛的服务端校验、按用户隔离的记录、最近播放的发行与清除；曲目时长与所在发行只经
  `catalog.LoadTrackDuration`、`catalog.LoadTrackPlacements` 读取，HTTP 层经 identity 鉴权，所有 SQL 都带 `user_id`，路径与请求体里没有用户 ID。
  见[播放历史合同](./listening-contracts.md)。
- `search` owns query semantics and result projections while PostgreSQL remains
  the authority. It cannot write catalog truth through a search projection.
- `operations`（M3）拥有操作记录表 `operations`、共同写入流程 `Run`（幂等键串行与重放、同键异义冲突、同一事务写记录、错误分类）与撤销、展示的 `Kind`
  接口（M4 起目标可以是发行或专辑，列表按目标的并集筛选）；它不含业务表、规则与 HTTP，依赖只有 `platform/uuid`。业务表与版本号列留在拥有它们的能力里（catalog），各类型的撤销能力由 catalog 实现、
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
