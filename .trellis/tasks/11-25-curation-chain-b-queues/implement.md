# 整理链 B：实施

工作目录与分支：Orca 工作目录，分支 `task/curation-b-queues`，基线 `main`。

1. Codex 实现代理（`gpt-6.2-astra`，high）：先核实扫描触发方式与根目录不可用时的处理，在本目录写 `design.md`，停下。
2. 主代理审阅设计，通过后实现代理实施，不写新测试。
3. Codex 测试代理（`gpt-6.2-sol`，high）：按验收写用例（含并发、中断恢复、限流只停一条队列），重跑 M 的度量。
4. 主代理审阅、做前端（如进度接口有变）、更新合同与 ADR 第 16.7 节、提交、合回 `main`。

验证范围上限：`go test ./internal/curation/... ./internal/sources/... ./internal/library/...`，相关集成用例用 `ROOMUSIC_TEST_RUN` 收窄，M 的度量两种模式。

回退点：调度换成队列是单独的一次合并提交；迁移只增不改。
