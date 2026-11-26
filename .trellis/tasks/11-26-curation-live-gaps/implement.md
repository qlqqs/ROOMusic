# 实施计划

工作目录由 Orca 创建：`orca worktree create --repo path:<主仓库> --branch task/live-gaps-<w> --base-branch main --setup skip`。子代理不提交 Git；主代理验收后提交，并合回本地 `main`。不推送。

## 步骤

1. **W1、W2、W3 并行起步**：
   - W1：`gpt-6.2-astra`，分支 `task/live-gaps-w1`；
   - W2：`gpt-6.2-astra`，分支 `task/live-gaps-w2`；
   - W3：`gpt-6.2-astra`，分支 `task/live-gaps-w3`。先写样本、`accept` 字段和“符合预期”统计，再写 R1/R2/R3/R16 的定向用例。实现缺失导致的失败照实记录，不改实现代码。
2. **验收 W1、W2**：
   - 审阅改动，检查有没有越出允许范围的目录；
   - 两边各自跑改动包的单元测试和受影响的既有集成用例，范围按改动收窄；
   - 提交后合入 `main`，顺序为先 W2、再 W1。
3. **W3 变基到新 `main` 跑通**：
   - 跑度量的两种模式和新增定向用例；
   - 不符合预期的样本逐个归因：属于实现缺陷的交回 W1/W2 修；样本预期与合同不符的由主代理裁定，并在 PRD 中记录。
4. **修复回合**：只重跑失败的样本或用例，以及被改动的包。
5. **W4 真实试用**（`gpt-6.2-astra`）：
   - 扩充公开曲库，加入长网页回环；
   - 跑程序轮、oracle 轮和真实模型轮。真实模型沿用本机 Codex 配置，密钥只在进程内读取；
   - 报告写入本任务 `research/`，不写端点地址与密钥。
6. **文档**：主代理修订合同第 22–27 节、ADR 第 16 节、审查合同，以及前端解码与文案（如有新原因码）。
7. **收尾**：勾选 PRD 验收项，更新父任务的相关结论，归档本任务。

## 验证命令（范围上限）

- 单元测试：`cd backend && mise exec -- go test ./internal/curation/... ./internal/library/parser/... ./internal/catalog/... -count=1`。按改动收窄。
- 集成测试：`sg docker -c 'export NO_PROXY=127.0.0.1,localhost no_proxy=127.0.0.1,localhost TMPDIR="$PWD/temp/<名>" ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-compose-test-<名> ROOMUSIC_TEST_RUN="<选择器>"; mise exec -- ./scripts/test-integration.sh'`
- 度量：`ROOMUSIC_TEST_RUN="^TestPostgreSQLCurationMetrics$"`，两种模式。
- 依赖规则：`go test ./internal/app -run TestDependencyRules`（W1、W2 改了导入时跑）。

## 回滚点

- W2、W1 各自一个合并提交，可以单独 revert。
- 迁移只新增枚举，revert 代码后保留迁移不影响旧代码读取。
