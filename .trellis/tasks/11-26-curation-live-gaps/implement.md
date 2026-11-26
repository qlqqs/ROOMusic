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

## 收尾顺序（2026-11-26 进度审查后）

步骤 1–4 与 W6 模型并发已完成。剩余工作按下面的顺序做，依据见 PRD“进度审查后的裁定”：

1. 不再修改限信息 oracle 的检索策略和归因程序的判定逻辑。
2. 度量按 R18 把“等来源重试”计为符合；真实曲库新增 8 张的允许结果改成与回归库同类情形一致。只跑受影响的度量用例。
3. 《Mezzanine》的部分吻合修复，范围见 PRD；超出范围就记为已知局限。
4. 用并发版跑一次真实模型轮，写报告。程序轮、oracle 轮和串行部分快照沿用已有结果。
5. 文档（原步骤 6）与收尾（原步骤 7）。

### 在途状态（2026-11-26 09:00 UTC，供上下文压缩后续命）

停止规则和后续事项以 prd.md 的“收尾停止规则”“后续事项”为准。

- 最终真实模型轮（唯一一次，之后不再重跑）：
  - W3 子代理：Orca 终端句柄 `term_92c11118-23a8-4515-bf02-188258b30706`（Codex，gpt-6.2-astra）；
  - 工作目录：`/home/qlqq/workspace/.worktrees/ROOMusic/ROOMusic/live-gaps-w3`，分支 `task/live-gaps-w3`，基于 main `270f3691`；
  - 本轮临时目录：该工作目录下 `temp/real-final-jx5TQgEV/`（`run.log` 为进度）；
  - Compose 项目：`roomusic-compose-test-real-final-jx5tqgev`；
  - 开跑时间：08:58:40 UTC；
  - 观察窗上限：max(30 分钟, 专辑数 × 5 分钟)，28 张即 140 分钟（`backend/internal/app/curation_live_corpus_integration_test.go:312`）；
  - 设置：model_concurrency=4，每日与联网每日上限不限，主模型与审查都用 gpt-6.2-sol，网页回环 long；
  - 结果文件：`.trellis/tasks/11-26-curation-live-gaps/research/live/real.json` 与 `live-trial-2026-11-26.md` 的真实轮部分（落在 W3 工作目录，需提交后合入 main）。
- 剩余步骤：
  1. 节点三，未完成：真实模型轮出结果后，审阅报告，提交 W3 分支并合入 main（合入前跑 `TestPackageDependencyDirections`），向会话 roomusic-37（`uds:/run/user/1000/cc-socks/1837906.sock`）报进度；符合预期不到 100% 只报数字与逐张原因，不修。
  2. 文档：合同第 28 节、ADR 0006 第 16.8 节已提交（`42b31bfe`、`472bb807`）；只需按真实轮结果补 V6 数字。
  3. 勾选 prd.md 验收项（V1–V10，按“进度审查后的裁定”修订后的口径）。
  4. 归档：`task.py archive 11-26-curation-live-gaps --skip-branch-validation`，提交删除。需要清理：Orca 工作目录 `live-gaps-w3`，`orca worktree rm` 后删分支 `task/live-gaps-w3`；关闭终端 `term_92c11118-…`；确认该轮 Compose 项目的容器、网络、卷已清理。其他 W1、W2、W5–W9 的工作目录和分支都已删除。

## 验证命令（范围上限）

- 单元测试：`cd backend && mise exec -- go test ./internal/curation/... ./internal/library/parser/... ./internal/catalog/... -count=1`。按改动收窄。
- 集成测试：`sg docker -c 'export NO_PROXY=127.0.0.1,localhost no_proxy=127.0.0.1,localhost TMPDIR="$PWD/temp/<名>" ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-compose-test-<名> ROOMUSIC_TEST_RUN="<选择器>"; mise exec -- ./scripts/test-integration.sh'`
- 度量：`ROOMUSIC_TEST_RUN="^TestPostgreSQLCurationMetrics$"`，两种模式。
- 依赖规则：`go test ./internal/app -run TestDependencyRules`（W1、W2 改了导入时跑）。

## 回滚点

- W2、W1 各自一个合并提交，可以单独 revert。
- 迁移只新增枚举，revert 代码后保留迁移不影响旧代码读取。
