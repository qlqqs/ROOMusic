# 整理链 M：实施

工作目录与分支：Orca 工作目录，分支 `task/curation-m-metrics`，基线 `main`。

## 分工

| 步 | 执行 | 内容 |
| --- | --- | --- |
| 1 | Codex 实现代理（`gpt-6.2-astra`，high） | 受控服务的计数与 503 注入；回归曲库的样本清单与生成器；度量集成测试与排空驱动；`roomusic-curation-bench` 三个子命令。不写新测试用例（度量测试本身是交付物） |
| 2 | Codex 测试代理（`gpt-6.2-sol`，high） | 受控服务新增计数与 503 注入的单元测试；按设计第 2 节核对样本覆盖与标注；跑度量两次确认稳定；跑两项实测；把结果写进本任务 `research/` |
| 3 | 主代理 | 审阅改动与越界、核对结果、回写父任务 `research/external-services.md`、提交并合回 `main` |

## 验证命令（范围上限）

- `cd backend && mise exec -- go test ./internal/upstreammock/...`
- `mise exec -- go vet ./internal/upstreammock/... ./internal/app/... ./cmd/roomusic-curation-bench/...`（在 `backend/` 下）
- `ROOMUSIC_TEST_RUN='TestPostgreSQLCurationMetrics' ROOMUSIC_CURATION_METRICS_OUT=<临时文件> ./scripts/test-integration.sh`
- 实测：`go run ./cmd/roomusic-curation-bench mb-rate`、`tolerance`；`real-corpus` 生成到 `temp/` 下。

## 回退点

只新增测试、测试数据、受控服务的功能与一个独立命令，不改生产路径；回退即撤销合并提交。
