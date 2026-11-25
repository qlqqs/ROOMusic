# 整理链 C：实施

工作目录与分支：Orca 工作目录，分支 `task/curation-c-match-rules`，基线 `main`。

## 分工与顺序

1. Codex 实现代理（`gpt-6.2-astra`，high）：先对真实 MusicBrainz 做少量实测（程序自己的标识与限速器，只用公开名字），再在本目录写 `design.md`，写完停下。
2. 主代理审阅设计，通过后实现代理实施，升级 `MatchRuleVersion`。实现代理不写新测试。
3. Codex 测试代理（`gpt-6.2-sol`，high）：按验收写用例，更新受控服务与回归曲库中需要的部分（例如受控服务支持新的查询写法），重跑 M 的度量。
4. 主代理审阅、更新合同与 ADR 第 16.7 节、提交、合回 `main`。

## 验证命令（范围上限）

- `cd backend && mise exec -- go test ./internal/curation/... ./internal/sources/... ./internal/catalog/...`
- 相关集成测试用 `ROOMUSIC_TEST_RUN` 收窄。
- 度量：`ROOMUSIC_TEST_RUN='TestPostgreSQLCurationMetrics' ROOMUSIC_CURATION_METRICS_OUT=<文件> ./scripts/test-integration.sh`，两种模型模式。

## 回退点

判定规则受 `MatchRuleVersion` 控制；回退代码后版本回到 101，已写入的结果可按批撤销。
