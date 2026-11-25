# 整理链 A：实施

工作目录与分支：Orca 工作目录，分支 `task/curation-a-authenticity`，基线 `main`。

## 分工与顺序

1. Codex 实现代理（`gpt-6.2-astra`，high）先读代码，在本目录写 `design.md`：版本与摘要拆分的数据结构与迁移、真实性取值函数的输入输出与调用点、
   分类的计算位置与查询接口、升级时各类状态怎样处理、对现有待定项与工作状态的兼容。写完停下。
2. 主代理审阅 `design.md`，有问题追加修改，通过后通知实现代理实施。实施分两段：先版本与摘要拆分，再真实性与分类。实现代理不写新测试。
3. Codex 测试代理（`gpt-6.2-sol`，high）按 `prd.md` 的验收写用例并运行；重跑 M 的度量。
4. 主代理审阅、提交、合回 `main`。

## 验证命令（范围上限）

- `cd backend && mise exec -- go test ./internal/curation/... ./internal/catalog/...`
- 相关集成测试用 `ROOMUSIC_TEST_RUN` 收窄（整理程序层、取证、迁移）。
- 度量：`ROOMUSIC_TEST_RUN='TestPostgreSQLCurationMetrics' ROOMUSIC_CURATION_METRICS_OUT=<文件> ./scripts/test-integration.sh`

## 回退点

版本与摘要拆分是单独的一段；迁移只新增列，旧列保留到后续子任务确认不再使用。
