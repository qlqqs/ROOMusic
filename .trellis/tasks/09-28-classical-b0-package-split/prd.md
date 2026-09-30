# B0：后端拆包（catalog 先拆）

> 父任务：[Classical 重设计 / Core 1](../09-27-classical-core-1/prd.md)。2026-09-28 由[路线修订任务](../archive/2026-09/09-28-core-1-roadmap-revision/design.md)创建，2026-09-29 完成规划。
> 复杂任务：目标布局、拆环与依赖规则见 [design.md](design.md)，分步提交与门禁见 [implement.md](implement.md)。

## 目标与用户价值

把 `backend/cmd/roomusic` 的单个 `package main` 按能力拆到 `backend/internal/`，给阶段 I（身份稳定、按组接口）和 M3（受治理操作）明确的修改落点，并让依赖方向可以检查。
纯重构：路由、JSON、状态码、错误文案、SQL 语义、日志字段与迁移都不变。

## 已核实事实（2026-09-29，F1、F2 合入后）

- **规模**：非测试代码 8,154 行（13 个文件），测试 4,983 行（13 个文件）。

- **文件间依赖**（AST 分析）：
  - 解析器与 organizer 互相引用：
    - `audio_parser.go` 用了 organizer 的 `creditObservation`、`normalizeValue`（`organizer.go:57`、`:143`）；
    - `organizer.go` 用了解析器的 `fieldObservation`（`audio_parser.go:2008`）。
  - 艺人规则跨两侧：
    - `artist_normalization.go:42` 调用解析器的 `boundedAudioText`（`audio_parser.go:742`）；
    - 解析器的 `splitCreditNames`（`audio_parser.go:1973`）与 Release 图写入（`scanner.go:958` 的 `canonicalTrackArtist`）都用艺人规则。
  - organizer 还用了 `folder_metadata.go` 的 `parseFolderMetadata`，以及 scanner 的 `directoryOf`（`scanner.go:825`）。
  - Release 图写入 `persistOneCandidate`（`scanner.go:912`–`1040`）在提交后调用 `saveArtworkForRelease`（`scanner.go:1184`）。后者混合了三件事：
    - 在音乐根里发现文件夹封面；
    - 往数据目录写封面文件；
    - 写 `release_artworks` 关联。
    `artworkResource`（`application.go:312`）按同一存储布局读取封面。
  - `finalizeScan`（`scanner.go:163`）在扫描事务里直接把 `tracks` 标为 missing。
  - `openDatabase`（`database.go:55`）同时负责连接、迁移和中断扫描恢复。其中恢复函数 `recoverInterruptedScans`（`database.go:75`）依赖扫描锁 `acquireScanExecution`（`scans.go:18`）。
  - 被多处共用的函数：
    - `createIdentifier`（`security.go:23`）：身份、目录、catalog 都在用；
    - `isValidIdentifier`（`catalog_api.go:853`）：扫描和目录操作在用；
    - `safeRelativeSourceRef`：扫描诊断在用；
    - `writeAPIError`、`writeJSON`、`decodeJSON`：所有 handler 都在用。
  - 鉴权函数 `requireAdmin`、`requireSameOrigin`、`currentUser` 是 `roomusicApplication` 的方法（`security.go`），所有 handler 都会调用。

- **handler 形态**：
  - catalog 的 handler 已经与查询函数（`loadRelease*`）分开。
  - 目录、目录操作、扫描、成员与会话的 handler 共约 770 行，都在 handler 里直接执行 SQL。
  - 扫描的启动和取消还会操作共享扫描状态（`runningScan`、`scanWorkers`，`application.go:17`）。
  - 错误映射不统一：`database_error` 在不同 handler 里分别返回 500 和 503。

- **无调用方的函数**：
  - 完全没有调用：`readinessHandler`、`decisionValue`、`refineDirectoryKind`、`listRootsGoverned`、`markMissing`、`recordDiagnostic`、`saveArtwork`（连同只为它服务的 `saveArtworkWith`）、`audioObservation.fieldObservations`。
  - 只有测试在用：`buildApplicationHandler`、`parseCue`、`parseM4AAtoms`、`saveObservation`。

- **测试耦合**：
  - 共享辅助函数分散在各测试文件里：
    - `auth_root_operations_integration_test.go`：`newIntegrationTestApplication`、`request`、`createUser`、`integrationDatabaseURL`；
    - `core_acceptance_test.go`：`createFLACFixture`；
    - `audio_parser_repair_test.go`：`repairFLACFixture`；
    - `database_migration_test.go`：`findMigrationByVersion`；
    - `scanner_observation_test.go`：伪执行器。
  - `organizer_test.go` 同时测 organizer 和 scanner 的 rip log、封面发现。

- **外部路径依赖**：
  - 集成脚本 `scripts/test-integration.sh:23` 只测 `./cmd/roomusic`，CI 会调用它（`.github/workflows/ci.yml:116`）。
  - 前端构建产物：
    - `frontend/vite.config.ts:9` 输出到 `backend/cmd/roomusic/web`；
    - CI 检查这个目录（`ci.yml:86`–`90`）；
    - 前端资源通过 `main.go:27` 的 `//go:embed web` 嵌入。
  - `scripts/prod.sh`、`scripts/dev.sh`、`deploy/smoke/current.Dockerfile` 都构建 `./cmd/roomusic`。
  - 规范、ADR、`frontend/DESIGN.md`、README 里有 40 多处链接指向 `backend/cmd/roomusic/*.go`。
  - CI 运行 `gofmt`、`go test ./...`、`go test -race ./...`、`go vet`、`go build`，以及集成脚本。

- **规范约束**：
  - transport 只解码请求、映射响应，不持有业务策略，也不执行 SQL（`.trellis/spec/backend/index.md` 的 Core 0 Boundaries；`quality-guidelines.md` 的 Forbidden Patterns）。
  - 目标布局只在真正需要时才建层，不为对称创建空目录或伪接口（`directory-structure.md`）。
  - 跨能力只能调用对方公开的应用合同，不导入对方的 transport 或 adapters。

## 已裁决

| 编号 | 事项 | 选择 | 依据 |
| --- | --- | --- | --- |
| D1 | 拆分深度 | 按能力拆包，只在需要时抽用例：<br>• catalog 完全分层；<br>• 扫描启动（涉及共享状态）抽进 library；<br>• 目录、目录操作、身份的 handler 原样搬进各自的 httpapi，里面的 SQL 登记为技术债，M3 处理 | qlqq，2026-09-29，选择推荐项 |
| D2 | 范围与顺序 | catalog → platform/postgres → library（含解析器）→ httpserver、config、identity、httpapi 与 app，最后同步文档；每步单独提交 | 路线修订 D1（2026-09-28）批准了前三步；platform、identity 与 app 是“`cmd/roomusic` 只保留入口”的前提 |
| D3 | identity 是否纳入 | 纳入 | 初稿规则“成本低则同批完成”：约 390 行；handler 离开 `package main` 后都需要可导入的鉴权 |

## 需求与验收

| 编号 | 需求 | 验收标准 |
| --- | --- | --- |
| R1 | 包布局 | 按 design 的目标布局建包；`backend/cmd/roomusic` 只剩 `main.go` 和 `web/`，`main.go` 只负责入口与装配 |
| R2 | 依赖方向 | 满足 design 的依赖规则：<br>• `catalog`、`catalog/metadata`、`catalog/artwork`、`library`、`library/parser` 不导入 `net/http`；<br>• 能力包之间只经公开合同调用，不导入其他能力的 `httpapi`；<br>• `internal/app/dependency_rules_test.go` 持续检查，CI 自动运行 |
| R3 | 对外行为不变 | 以下都不变：路由模式、JSON 形状、状态码、错误码与文案、完成日志字段、SQL 文本与事务边界、迁移文件、暂存观察的 JSON 形状。<br>证据：<br>• 现有测试只改包名、导入和标识符，断言不改，全部通过；<br>• 新增路由表测试，证明每个“方法 + 路径”仍解析到原模式，`/api` 兜底仍为 `<unmatched>` |
| R4 | 所有权修正 | • Release 图写入、missing 对账、封面关联与受管存储归 catalog；<br>• 文件夹封面发现与扫描编排归 library；<br>• 打开数据库不再夹带扫描恢复，改由 `main` 紧接着调用，失败仍进入降级模式。<br>扫描持久化、封面、取消与恢复相关测试全部通过 |
| R5 | handler 处理（D1） | • `catalog/httpapi` 只做参数解析、鉴权与响应映射，没有 SQL；<br>• 扫描相关 handler（启动、查询、取消、诊断）只做解析、鉴权与响应映射，用例在 `library.Service`，以哨兵错误区分失败点；<br>• 目录、目录操作、身份的 handler 原样搬进各自的 httpapi，技术债写进 `directory-structure.md` |
| R6 | 死代码 | design 列出的无调用函数被删除；`recordingResponseWriter.Unwrap` 保留 |
| R7 | 测试与门禁 | • 测试随被测代码归属移动；<br>• 测试函数数量不少于基线 161，死代码对应的测试除外，完成时逐项列出；<br>• `scripts/test-integration.sh` 覆盖 `./...`；<br>• 每步都通过 implement 列出的门禁：gofmt、vet、build、全量测试、race、集成脚本、`git diff --check` |
| R8 | 文档 | • 指向 `backend/cmd/roomusic/*.go` 的链接全部改到新位置；<br>• 规范写明实际布局与技术债；<br>• 路线图、架构图、父任务更新 B0 状态；<br>• M1a 与阶段 I 的 PRD 不再引用旧路径 |
| R9 | 分步提交 | 五个提交（四步代码加文档；platform/postgres 因测试库依赖从第 3 步提前为单独一步），每个都能独立 `git revert` |

## 范围外

- 新增端点、schema 或迁移、业务规则、身份规则改动（阶段 I）、性能优化。
- 目录、目录操作、身份 handler 的用例抽取（D1 的技术债，M3 处理）。
- 统一错误分类（例如 `database_error` 有 500 和 503 两种），另行立项。
- 已知缺陷的修复另行立项：
  - 成员接口路由 ID 未校验，格式错误时返回 503；
  - 幂等键长度无上限。
- `domain/`、`application/`、`ports/`、`adapters/` 细分层；`cmd/roomusic-smoke*` 冒烟工具；前端。
- F1（`/api` 兜底）与 F2（目录幂等指纹）已于 2026-09-29 合入，拆包必须保持它们的行为和回归测试。

## 最终规划决策记录

- D1 由用户（qlqq）于 2026-09-29 选定；D2、D3 按已批准的路线与初稿规则推出，随最终规划摘要一并确认。
- 状态：用户于 2026-09-29 明确批准最终规划摘要，进入实施。
