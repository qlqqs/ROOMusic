# 整理链 F：实施

工作目录与分支：Orca 工作目录，分支 `task/curation-f-auto-layer`，基线 `main`。

1. Codex 实现代理（`gpt-6.2-astra`，high）：在本目录写简短的 `design.md` 后直接实施，不写新测试。
2. Codex 测试代理（`gpt-6.2-sol`，high）：按验收写用例，重跑 M 的度量。
3. 主代理审阅、更新合同与 ADR 第 16.7 节、提交、合回 `main`。

验证范围上限：`go test ./internal/curation/... ./internal/catalog/...`，相关集成用例用 `ROOMUSIC_TEST_RUN` 收窄，M 的度量两种模式。

回退点：迁移只增列；作废是受治理操作，可以撤销。
