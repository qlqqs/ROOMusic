# B0 执行计划

需求见 [prd.md](prd.md)，设计见 [design.md](design.md)。按五步推进：

- 前四步是代码，每步独立可编译，通过完整门禁后单独提交到 main；
- 第五步同步文档；
- 提交前后都核对测试函数数量。
- 第 2 步是实施中新增的：library 的数据库测试需要“隔离 schema 加迁移”的测试库，迁移执行器必须先离开 `package main`，
  所以把原计划里第 3 步的 `platform/postgres` 提前为单独一步。

## 每步门禁

```bash
cd backend
test -z "$(gofmt -l .)"
go vet ./...
go build ./...
go test ./... -count=1
go test -race ./... -count=1
go test ./... -list '.*' | grep -cE '^Test'   # 与基线 161 比较，差异逐项说明
cd .. && ./scripts/test-integration.sh      # 全部 TestPostgreSQL*，脚本自动清理容器
git diff --check
```

范围说明：本任务整体搬移全部后端代码，改动影响无法局部覆盖，所以每步都跑全量测试（符合 AGENTS.md 测试范围约束中的例外）。
第一步起，`test-integration.sh` 改为 `./...`，因此集成门禁覆盖所有包。

## 步骤

1. [x] **catalog 与共享叶子包**（阶段 I 所需）
   - 新建 `platform/uuid`（`New`、`Valid`；包名不用 `id`，避免被 handler 里的局部变量 `id` 遮蔽）和 `catalog/metadata`（Credit、`NormalizeValue`、`BoundedText`、艺人规则、`CanonicalCredits`）。
   - 新建 `catalog`：观察合同与候选类型、organizer、文件夹名解析、`NormalizedPath`、`DirectoryOf`、来源身份函数、`PersistCandidate`（不含封面）、`MarkMissingSources`、读取查询与 DTO、`SafeRelativeSourceRef`。
   - 新建 `catalog/artwork`：`Store` 负责保存、移除、读取，以及 MIME 嗅探与尺寸计算。
   - `package main` 改为调用这些包：
     - 解析器改用 `metadata`；
     - scanner 编排封面步骤并调用 `MarkMissingSources`；
     - catalog handler 暂留 main，改调 `catalog.Store`。
   - 被测代码已移走的测试随之移动；`organizer_test.go` 按归属拆分。删除 catalog 范围内的死代码。
   - `scripts/test-integration.sh` 改为 `./...`。
   - 过门禁后提交：`refactor(backend): 拆出 catalog、metadata、artwork 与 platform/uuid`。
   - 实际验证：gofmt、vet、build、`go test ./...`、`go test -race ./...`、`./scripts/test-integration.sh` 全部通过；测试名单与基线 161 逐项对账，只多两个（`TestNewReturnsValidVersion4Identifiers`、从搜索解析测试拆出的 `TestEscapeLikePatternEscapesWildcards`）；搬迁的函数用脚本对照原文，只有预期改动。
2. [x] **platform/postgres**
   - 新建 `platform/postgres`：`Open`（连接、探测、应用迁移，返回 `*sql.DB`）与迁移执行器（原 `database.go` 除 `databaseState`、扫描恢复、就绪状态外的全部）。
   - 新建 `platform/postgres/postgrestest`：隔离 schema 的测试库（`ROOMUSIC_TEST_DATABASE_URL`），不导入 `postgres`，避免测试循环依赖。
   - `openDatabase` 留在 `package main` 作为薄封装：调用 `postgres.Open`，再执行中断扫描恢复（第 3 步随 library 移走），再包成 `databaseState`。
   - 迁移测试随执行器移到 `platform/postgres`；`newIntegrationTestApplication` 改用 `postgrestest`。
   - 提交：`refactor(backend): 拆出 platform/postgres 与测试库辅助包`。
   - 实际验证：gofmt、vet、build、`go test ./...`、`-race`、`./scripts/test-integration.sh` 全部通过；测试名单与第 1 步后一致（迁移测试 12 个随执行器移动，名称不变）。
3. [ ] **library 与解析器**
   - 新建 `library/parser`（含 `parsertest`），以及 `library`：
     - 路径校验、扫描引擎、暂存、rip log 与文件夹封面发现、诊断写入；
     - 扫描执行锁、`RecoverInterruptedScans`、`Service.StartScan` 的类型化结果、扫描查询与 DTO、`WaitForScans`。
   - `openDatabase` 不再做恢复，改由 `main` 紧接着调用 `library.RecoverInterruptedScans`。
   - 扫描 handler 暂留 main，改为映射 `StartScan` 的结果；目录类 handler 暂不动。
   - 相关测试移到 `library` 和 `parser`，删除 library 范围内的死代码。
   - 提交：`refactor(backend): 拆出 library 与 parser`。
4. [ ] **httpserver、config、identity、httpapi 与 app**
   - 新建 `platform/config`、`platform/httpserver`（`SetActor`、`Frontend(fs.FS)`）、`identity`（`Guard`）。
   - 新建三个 `httpapi` 包，按 design 的 handler 处理方式搬移。
   - 新建 `app`（`New`、`Degraded`、`WaitForScans`），`cmd/roomusic` 只保留 `main.go` 与 `web/`。
   - 完整 HTTP 链路测试移入 `app`。
   - 新增两个 `app` 内部测试：
     - 路由表测试：原有的每个“方法 + 路径”仍解析到同一模式，`/api` 兜底仍为 `<unmatched>`；
     - `dependency_rules_test.go`：按 design 的依赖规则检查各包导入。
   - 提交：`refactor(backend): 拆出 platform、identity、各能力 httpapi 与 app`。
5. [ ] **文档与收尾**
   - 用 `grep -rn 'cmd/roomusic/' --include=*.md --include=*.html` 找出全部旧链接，逐一改到新位置。ADR 证据表注明“路径已随 B0 更新”。
   - 更新以下文档：
     - `directory-structure.md`：实际布局、`httpapi` 命名、`library/httpapi` 与 `identity/httpapi` 内仍有 SQL 的技术债；
     - `backend/index.md`；
     - `core0-runtime-contracts.md`：代码位置、`openDatabase` 与恢复的新分工；
     - `database-guidelines.md`、`logging-guidelines.md`：所有权路径；
     - `catalog-rest-contracts.md`：测试命令路径；
     - `architecture-decisions.md` 的代码树；
     - `core-1-roadmap.md` 与 `core-1-architecture.html` 的 B0 状态；
     - M1a、阶段 I 的 PRD 中引用的旧路径。
   - 在 PRD 末尾记录每步的提交哈希、测试数量对账与实际验证命令，然后提交，归档任务并修正归档后的相对链接。

## 风险与回退点

- **高风险点**：
  - 第一步的封面编排与 missing 对账搬移：由 `scanner_persistence_integration_test.go` 与封面相关测试覆盖；
  - 第二步的扫描启动映射：由扫描取消、恢复测试和 F1/F2 的 HTTP 测试覆盖。
- **测试函数数量少于基线**：立即停下，逐项找回，不以“已删除”为由跳过，除非对应的是 design 列出的死代码。
- **回退**：每步是独立提交，可按步 `git revert`。第一步的集成脚本改动随第一步一起回退。
- **与其他任务的冲突**：阶段 I 与 M1a 的 Go 测试样本都会改到这些文件，B0 归档前不并行启动它们。
