# 整理链 E：实施

工作目录与分支：Orca 工作目录，分支 `task/curation-e-model-fallback`，基线 `main`。

1. Codex 实现代理（`gpt-6.2-astra`，high）：读代码，在本目录写 `design.md`，停下。
2. 主代理审阅设计，通过后实现代理实施，不写新测试。
3. Codex 测试代理（`gpt-6.2-sol`，high）：按验收写用例；度量的受控模型端点按新任务类型扩展；重跑 M 的度量。
4. 主代理审阅、做前端（未采用的建议取代例外待办等界面在 G，E 只做必要的解码与兼容）、更新合同与 ADR 第 16.7 节、提交、合回 `main`。

验证范围上限：`go test ./internal/curation/... ./internal/steward/... ./internal/app/...`（单元），相关集成用例用 `ROOMUSIC_TEST_RUN` 收窄，M 的度量两种模式。

回退点：审查策略升版本；迁移只增不改，已有例外数据保留。
