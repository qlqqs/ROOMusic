# B0 执行计划

需求见 [prd.md](prd.md)，设计见 [design.md](design.md)。按四步推进：

- 前三步是代码，每步独立可编译，通过完整门禁后单独提交到 main；
- 第四步同步文档；
- 提交前后都核对测试函数数量。

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

1. [ ] **catalog 与共享叶子包**（阶段 I 所需）
   - 新建 `platform/id`（`New`、`Valid`）和 `catalog/metadata`（Credit、`NormalizeValue`、`BoundedText`、艺人规则）。
   - 新建 `catalog`：观察合同与候选类型、organizer、文件夹名解析、`NormalizedPath`、`DirectoryOf`、来源身份函数、`PersistCandidate`（不含封面）、`MarkMissingSources`、读取查询与 DTO、`SafeRelativeSourceRef`。
   - 新建 `catalog/artwork`：`Store` 负责保存、移除、读取，以及 MIME 嗅探与尺寸计算。
   - `package main` 改为调用这些包：
     - 解析器改用 `metadata`；
     - scanner 编排封面步骤并调用 `MarkMissingSources`；
     - catalog handler 暂留 main，改调 `catalog.Store`。
   - 被测代码已移走的测试随之移动；`organizer_test.go` 按归属拆分。删除 catalog 范围内的死代码。
   - `scripts/test-integration.sh` 改为 `./...`。
   - 过门禁后提交：`refactor(backend): 拆出 catalog、metadata 与 platform/id`。
2. [ ] **library 与解析器**
   - 新建 `library/parser`（含 `parsertest`），以及 `library`：
     - 路径校验、扫描引擎、暂存、rip log 与文件夹封面发现、诊断写入；
     - 扫描执行锁、`RecoverInterruptedScans`、`Service.StartScan` 的类型化结果、扫描查询与 DTO、`WaitForScans`。
   - `openDatabase` 不再做恢复，改由 `main` 紧接着调用 `library.RecoverInterruptedScans`。
   - 扫描 handler 暂留 main，改为映射 `StartScan` 的结果；目录类 handler 暂不动。
   - 相关测试移到 `library` 和 `parser`，删除 library 范围内的死代码。
   - 提交：`refactor(backend): 拆出 library 与 parser`。
3. [ ] **platform、identity、httpapi 与 app**
   - 新建 `platform/config`、`platform/httpserver`（`SetActor`、`Frontend(fs.FS)`）、`platform/postgres`（含 `postgrestest`）、`identity`（`Guard`）。
   - 新建三个 `httpapi` 包，按 design 的 handler 处理方式搬移。
   - 新建 `app`（`New`、`Degraded`、`WaitForScans`），`cmd/roomusic` 只保留 `main.go` 与 `web/`。
   - 完整 HTTP 链路测试移入 `app`。
   - 新增两个 `app` 内部测试：
     - 路由表测试：原有的每个“方法 + 路径”仍解析到同一模式，`/api` 兜底仍为 `<unmatched>`；
     - `dependency_rules_test.go`：按 design 的依赖规则检查各包导入。
   - 提交：`refactor(backend): 拆出 platform、identity、各能力 httpapi 与 app`。
4. [ ] **文档与收尾**
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
