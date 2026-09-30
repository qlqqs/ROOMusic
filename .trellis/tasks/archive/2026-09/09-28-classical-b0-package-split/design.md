# B0 技术设计

需求与裁决见 [prd.md](prd.md)，执行顺序见 [implement.md](implement.md)。
本设计按 D1 落地：按能力拆包，只有真正需要时才把 SQL 从 handler 中抽出。

## 目标布局

```text
backend/
├── cmd/roomusic/
│   ├── main.go            # 入口：加载配置、打开数据库、中断扫描恢复、装配 app、HTTP 生命周期
│   │                      # 保留 //go:embed web，把 fs.FS 交给 app
│   └── web/               # Vite 产物，不动
└── internal/
    ├── app/               # 组合根：构建各能力、按原顺序挂载全部路由（含 F1 的 /api 兜底）、
    │                      # 降级 handler、等待扫描退出
    ├── platform/
    │   ├── config/        # 环境变量解析（config.go）
    │   ├── httpserver/    # 请求 ID 与完成日志中间件、响应记录器、JSON 与错误信封、请求体解码、
    │   │                  # /api 兜底、前端静态资源、健康检查
    │   ├── postgres/      # 连接、迁移执行器
    │   │   └── postgrestest/  # 仅测试使用：隔离 schema 的测试库
    │   └── uuid/          # UUID 生成与格式校验（包名不用 id：handler 里大量局部变量叫 id，会遮蔽包名）
    ├── identity/          # 会话与密码、当前用户、Guard（同源、登录、管理员校验）
    │   └── httpapi/       # setup、auth、users handler（原样搬移，内含 SQL）
    ├── catalog/           # 观察合同、organizer、文件夹名解析、Release 图写入、missing 对账、读取查询与投影
    │   ├── metadata/      # 元数据值规则：Credit、空白规范化、有界文本、艺人名拆分与别名
    │   ├── artwork/       # 受管封面：数据目录文件、release_artworks 关联、格式与尺寸校验
    │   └── httpapi/       # /releases、/artworks handler（只做解析、鉴权与响应映射）
    └── library/           # 路径校验、扫描引擎、暂存、rip log 与文件夹封面发现、诊断、
        │                  # 扫描执行锁与中断恢复、扫描启动与查询
        ├── parser/        # 音频与 CUE 解析
        │   └── parsertest/    # 仅测试使用：合成音频夹具
        └── httpapi/       # /library-roots、/library-root-operations、/scans handler
                           # 目录类原样搬移，内含 SQL 与 F2 的幂等流程
```

- 不建 `domain/`、`application/`、`ports/`、`adapters/` 这几层：每个能力目前只有一个 PostgreSQL 实现，没有第二个适配器（`directory-structure.md`：层只在需要时创建）。
- 能力的 HTTP 层放在 `<能力>/httpapi`。`transport/` 中间层只会再套一层目录，所以不建，并同步更新规范的布局图。

## 所有权与拆环

| 问题 | 处理 |
| --- | --- |
| 解析器与 organizer 互相引用 | 共用的元数据值规则移入叶子包 `catalog/metadata`：`creditObservation` → `metadata.Credit`，`normalizeValue`、`boundedAudioText`、`maxAudioTextBytes` 与艺人规则都在这里。解析器和 catalog 都导入它，方向是 library → catalog |
| `fieldObservation` 定义在解析器 | 移入 `catalog`（Release 图写入用它记录字段来源）。解析器里唯一引用它的 `audioObservation.fieldObservations` 没有调用方，直接删除 |
| organizer 用 scanner 的 `directoryOf`，与 `normalizedPath` 同为相对来源路径规则 | 两者移入 `catalog` 并导出（`DirectoryOf`、`NormalizedPath`），library 调用 |
| scanner 的 `observationOrganizationScope` 依赖 organizer 内部的目录定位 | 移入 `catalog.OrganizationScope`，不再向外导出 organizer 的内部函数 |
| `canonicalCreditObservations` 是署名值规则 | 移入 `metadata.CanonicalCredits`，与 `Credit` 同包 |
| scanner 的 `cloneBoolMap` 与 organizer 同名函数语义相同（总返回非 nil） | 第 3 步随 `buildCueVirtualObservation` 留在 library，各包自带一份 7 行实现，不为它导出 catalog 的内部函数 |
| Release 图写入提交后保存封面 | `catalog.PersistCandidate` 只写 Release 图，返回 release ID。<br>提交后的封面步骤由 library 编排：先取内嵌或文件夹封面（发现在 library），再调用 `artwork.Store`（写文件与关联在 catalog）。<br>顺序、错误包装（`errArtworkBinding`、`discover folder artwork: %w`）、没有封面时移除关联的分支都保持不变 |
| `finalizeScan` 直接写 `tracks` | missing 对账 SQL 移入 `catalog.MarkMissingSources(ctx, tx, rootID, scanID)`。library 在同一个扫描事务里调用 |
| `openDatabase` 内含中断扫描恢复 | `postgres.Open` 只负责连接与迁移，`library.RecoverInterruptedScans` 由 `main` 紧接着调用。失败时同样关闭连接并进入降级模式，与现在一致 |
| 共享工具 | `createIdentifier` → `uuid.New`；`isValidIdentifier` → `uuid.Valid`；`safeRelativeSourceRef` → `catalog.SafeRelativeSourceRef`；错误信封、`writeJSON`、`decodeJSON` → `httpserver` |
| 鉴权是 `roomusicApplication` 的方法 | `identity.Guard` 公开 `RequireSameOrigin`、`RequireAdmin`、`Authenticate`、`WriteUnauthenticated`。<br>各能力的 httpapi 依赖 `identity` 这份公开合同，不依赖 `identity/httpapi`。<br>完成日志需要的 actor 通过 `httpserver.SetActor` 写入 |
| 前端 `//go:embed web` 只能留在 `cmd/roomusic` | `main` 把 `fs.Sub(frontendAssets, "web")` 传给 `app`，`httpserver.Frontend(fs.FS)` 负责提供页面。Vite 输出目录和 CI 检查都不用改 |
| `databaseState`（连接加就绪标志）只在“非 nil 即就绪”时使用 | 删除；`app.New` 直接接收 `*sql.DB`，nil 表示未就绪（路由测试利用这一点不连库建立路由表）。`openDatabase`（`postgres.Open` 加 `library.RecoverInterruptedScans`）留在 `cmd/roomusic/main.go` |
| 各 handler 按能力分散后如何保持路由模式不变 | 每个 `httpapi` 包提供 `Register(mux)`，模式字符串原样不变；`app` 的路由表测试逐条固定“方法 + 路径 → 模式”，`route_template` 日志因此不变 |
| 前端嵌入产物只能留在 `cmd/roomusic` | 验证真实嵌入产物的 `TestProductionFrontendServesRootFallbackAndAsset404` 留在 `cmd/roomusic/main_test.go`；`app` 的测试用 `fstest.MapFS` 充当前端 |
| `roomusicApplication` 汇集了全部状态 | 拆成多个服务：<br>• `catalog.Store`：db<br>• `artwork.Store`：数据目录<br>• `library.Service`：db、允许的根、logger、扫描 context 与 worker 状态、catalog 依赖<br>• `identity.Guard`：db、`SecureCookies`、`PublicURL`<br>每个服务只持有自己需要的依赖 |

## 依赖规则

| 包 | 允许导入的内部包 | 禁止 |
| --- | --- | --- |
| `platform/*` | 无（`postgres` 可导入 `backend/migrations`） | 任何能力包 |
| `catalog/metadata` | 无 | `net/http`、`database/sql` |
| `catalog`、`catalog/artwork` | `catalog/metadata`、`platform/uuid` | `net/http`、library、identity |
| `library/parser` | `catalog/metadata` | `net/http`、`database/sql` |
| `library` | `library/parser`、`catalog`、`catalog/metadata`、`catalog/artwork`、`platform/uuid` | `net/http`、identity |
| `identity` | `platform/httpserver`、`platform/uuid` | 其他能力包 |
| `*/httpapi` | 本能力包、`identity`、`platform/httpserver`、`platform/uuid`。<br>`library/httpapi` 可另外导入 `catalog`，只用 `SafeRelativeSourceRef` | 其他能力的 `httpapi` |
| `app` | 全部 | — |

这些规则由 `internal/app/dependency_rules_test.go` 用 `go/build` 读取各包的导入来检查，CI 的 `go test ./...` 会自动运行。

## handler 处理方式（D1）

- **catalog**：`listReleases`、`releaseDetail`、`releaseEvidence`、`artworkResource` 移入 `catalog/httpapi`，只保留参数解析、鉴权和响应映射。
  - 查询、投影和 `writeReleaseLoadError` 所依据的错误判断都在 `catalog`，参数解析函数随 handler 移动。
  - DTO 的 JSON 标签保持不变，作为读取投影的公开合同放在 `catalog`。
- **扫描**：`startScan` 会操作共享状态（`runningScan`、`scanWorkers`），它的编排移入 `library.Service.StartScan`，返回类型化结果：已在运行、已启动、协调不可用、数据库失败。
  - `library/httpapi` 按原样把这些结果映射成现有的状态码、错误码和文案。
  - 扫描 DTO 和诊断汇总移入 `library`（`Scan`、`DiagnosticsSummary`、`Diagnostic`）；`GetScan`、`ActiveScan` 也是 `Service` 方法。
  - 实施时把 `cancelScan`、`diagnostics` 也做成了 `Service.CancelScan`、`Service.Diagnostics`：原计划让它们带着 SQL 搬进 `library/httpapi`，
    但过渡期它们还在 `package main`，而 `library` 不能导入 `net/http`，与其导出 `scanRow`、`scanSelectColumns` 这类内部函数，
    不如直接抽成用例。结果是扫描相关 handler 不再含 SQL，只剩参数解析、鉴权与响应映射。
  - 各失败点仍映射到原来的状态码与文案，靠哨兵错误区分：`ErrScanNotFound`、`ErrCoordinationUnavailable`、`ErrStartFailed`、
    `ErrDiagnosticsUnavailable`、`ErrQuery`。
  - 目录（`listRoots`、`addRoot`）与目录操作（`root_operations.go`）的 handler 仍原样保留，路径校验 `ValidatePath` 属于 `library`。
- **目录与目录操作**：`listRoots`、`addRoot`、F2 的 `beginRootOperation`、`updateRoot`、`restoreRoot`、`rootOperations` 原样搬进 `library/httpapi`。只有路径校验 `validateLibraryPath` 属于策略，移入 `library`。
- **身份**：setup、登录、登出、`me`、成员 handler 原样搬进 `identity/httpapi`。
- **技术债**：`library/httpapi` 和 `identity/httpapi` 里仍有 SQL。这一点写进 `directory-structure.md`，M3 重做目录操作时抽出用例，身份接口下次变更时一并处理。

## 路由与行为不变的保证

- `app` 按现有 `buildApplication` 的顺序注册同样的模式字符串，因此完成日志里的 `route_template` 不变。
- 新增的 `app` 内部测试逐条断言：原有的每个“方法 + 路径”仍解析到同一模式；F1 的 `/api` 兜底仍解析为 `<unmatched>`。
- 以下内容逐字保留：
  - DTO、`catalog.SourceObservation` 的 JSON 标签（暂存到 `scan_observations` 的 JSON 形状不变）；
  - 错误码与文案；
  - 日志事件名与字段；
  - 事务边界与 SQL 文本。
- 现有测试只改包名、导入和标识符，不改断言。测试函数总数保持 161 个，唯一例外是随死代码删除的测试，完成时逐项列出。

## 死代码

以下函数没有调用方，搬移时直接删除：
- `readinessHandler`
- `decisionValue`
- `refineDirectoryKind`
- `listRootsGoverned`
- `markMissing`
- `recordDiagnostic`
- `saveArtwork` 与 `saveArtworkWith`
- `audioObservation.fieldObservations`

只有测试使用的包装按测试所在位置处理：
- `buildApplicationHandler` 由 `app` 的测试辅助替代；
- `parseCue`、`parseM4AAtoms`、`saveObservation` 在测试仍需要时，随测试所在的包保留；
- 保留的都不导出。

`recordingResponseWriter.Unwrap` 看起来没有调用方，实际是 `http.ResponseController` 依赖的接口方法，必须保留。

## 测试布局

| 测试 | 去向 |
| --- | --- |
| 单元测试 | 与被测代码同包（内部测试，可访问未导出标识符）。跨包测试的文件按被测函数拆开：<br>• `organizer_test.go`：organizer 部分到 `catalog`，rip log 与文件夹封面发现到 `library`，封面尺寸到 `catalog/artwork`<br>• `core_acceptance_test.go`：按用例归属拆开 |
| 完整 HTTP 链路的集成测试（权限矩阵、目录操作与 F2、catalog API、历史记录、最后管理员、F1 兜底） | `internal/app`。辅助函数 `request`、`createUser` 放在 `app` 的 `_test.go` 中 |
| 数据库层的 library 测试（扫描持久化、取消、恢复、未完成扫描不对账） | `internal/library` |
| 迁移测试 | `internal/platform/postgres`（隔离 schema 的测试库在 `postgres/postgrestest`，它不导入 `postgres`，避免测试循环依赖） |
| 共享测试夹具 | 隔离 schema 的测试库 → `postgrestest`；合成 FLAC 等 → `parsertest`；伪执行器 → 使用方所在包的测试文件 |
| `real_music_readonly_test.go` | `library/parser`，仍由环境变量守护，只读 |

## 外部路径

- `scripts/test-integration.sh` 改为在 `backend/` 下运行 `go test ./... -run 'TestPostgreSQL' -count=1`。CI 通过这个脚本运行，工作流本身不用改。
- 以下都继续构建 `./cmd/roomusic`，不用改：`scripts/prod.sh`、`scripts/dev.sh`、`deploy/smoke/current.Dockerfile`、Vite 输出目录。
- 文档里指向 `backend/cmd/roomusic/*.go` 的链接统一改到新位置，涉及：规范、ADR 0001–0004、`frontend/DESIGN.md`、README、架构文档。
  - ADR 证据表只改链接，内容照旧，并注明“路径已随 B0 更新，证据基线不变”。

## 回退

- 每一步单独提交，任何一步都可以用 `git revert` 回退，不涉及 schema 与数据。
- 集成脚本的改动与第一次移出测试放在同一次提交里，回退时一起撤销。
