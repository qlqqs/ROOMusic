# 子任务 M 的度量结果

子任务 M（度量与回归曲库）归档后，结果留在这里，供后续子任务对比。

- `metrics-design.md`：度量的设计（样本、受控服务、驱动、度量定义）。
- `baseline-2026-11-25.md`：现有代码的基线（oracle 与 first_candidate）及逐样本错误。
- `live-measurements-2026-11-25.md`：MusicBrainz 实际成功速度、同组版本的时长吻合率、真实标签静音曲库。
- `*.json`：原始数据。

重跑：`ROOMUSIC_TEST_RUN='TestPostgreSQLCurationMetrics' ROOMUSIC_CURATION_METRICS_OUT=<文件> ./scripts/test-integration.sh`，
`ROOMUSIC_CURATION_METRICS_MODEL=first_candidate` 切换模型模式。每个子任务合入后跑一次，数字记在该子任务目录，并在归档前抄到这里。
