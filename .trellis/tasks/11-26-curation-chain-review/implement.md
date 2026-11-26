# 整理链梳理：实施与合入顺序

本任务只做协调。规则见 `docs/architecture/adr/0009-curation-chain-review.md`；各子任务的技术设计和实施步骤，在各自目录的 `design.md`、`implement.md` 里。

## 批次

- 第一批同时做：1、2、3、4、5、6、7。
- 第二批：8 等 2、3、7 合入；9 等 5、7 合入；10 等 7 合入。
- 最后：11 等 2–10 合入，然后做总体验收。

## 每个子任务的流程

1. Codex 实现代理（gpt-6.2-astra，high）先写 `design.md`、`implement.md`。主代理审过后运行 `task.py start`，再让同一个代理实现。
2. 另一个 Codex 代理（gpt-6.2-sol，high）按 `implement.md` 的测试清单补测试，只改测试文件。
3. 主代理审阅改动和验证结果；前端由主代理补上。
4. 合入本地 `main`：
   - 先变基到当前的 `main`，处理冲突；
   - 跑依赖门禁 `TestPackageDependencyDirections`；
   - 子任务 1 合入以后，再加跑固定样本回归。

## 合入时要协调的

- **迁移号**：各子任务的迁移都写作 `NNNN`，合入时按当时的最大号顺延（基线是 0097）。
- **扫描版本**：2 只升 `CommonVersion`（5 → 6）；3 只升 `GroupingVersion`（7 → 8）。
- **识别版本**：5 和 6 合起来只升一次 `MatchRuleVersion`（104 → 105）。
- **状态约束**：5 的新结论和 6 的 `query_error`，合并成一个约束。
- **合并键**：3 和 5 共用，放在 `backend/internal/catalog/textnorm` 一类的共用位置；后合入的一方复用先合入的。
- **播客过滤视图**：2 的 `curation_eligible_releases`，7、9 改造模型队列时接上。
- **空等结论**：子任务 1 查出的原因，落在 6 范围内的，转给 6。

## 总体验收

见 `prd.md` 的“验收”一节。
