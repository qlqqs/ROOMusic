# 整理链 D：实施

工作目录与分支：Orca 工作目录，分支 `task/curation-d-patrol`，基线 `main`。

1. Codex 实现代理（`gpt-6.2-astra`，high）：在本目录写 `design.md` 后直接实施，不写新测试。
2. Codex 测试代理（`gpt-6.2-sol`，high）：按验收写用例；受控模型端点支持巡检与本地整理；去掉度量里“名字已巡检”的临时注入；重跑 M 的度量。
3. 主代理审阅、做前端（设置里的“重新巡检”与必要的解码；完整界面在 G）、更新合同与 ADR、提交、合回 `main`。

验证范围上限：`go test ./internal/curation/... ./internal/catalog/... ./internal/app/...`（单元），相关集成用例用 `ROOMUSIC_TEST_RUN` 收窄，M 的度量两种模式。
