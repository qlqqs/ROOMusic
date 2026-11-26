# 程序轮请求之间的空等

在产品提交 `38d7a63270513907e3082228b18534823df51a7b` 上，三张合成专辑从第一次扫描至三张识别完成用了 97,937.174 毫秒。六次 MusicBrainz 请求之间有五个空档，均小于一秒；最长的程序等待发生在第一个请求之前。历史真实轮十处 4–15 秒空档的具体原因仍为**未知**。

## 测量条件

样本为 editions-model-quartet（同组四版各五首）、picard-01（文件编号）和 unlisted-01（目录无此张）。三张一起扫描，纯程序、真实时钟、仅开 MB，应用轮询 2 秒、完整盘点 30 秒、扫描中复查间隔取默认 30 秒，MB 每秒一次，模拟服务每次成功响应固定延迟 100 毫秒。没有 429、503、重试或来源暂停。以首次扫描开始为零点，时刻和持续时间均为毫秒。

```sh
ROOMUSIC_CURATION_METRICS_SUITE=idle ROOMUSIC_CURATION_METRICS_OUT=../../../temp/curation-fixed/idle.json ROOMUSIC_TEST_RUN='^TestPostgreSQLCurationMetrics$' mise exec -- ./scripts/test-integration.sh
```

通过。结构化请求、扫描与队列观测保存在 `temp/curation-fixed/idle.json`，同名 `.trace` 保存 Go 运行时轨迹。回报包含整个 handler 的处理时间。程序等待区间和 HTTP 起止共用单调时钟；扫描开始结束由 PostgreSQL 的记录映射到同一时间原点。

## 六次请求和五个空档

| 序号 | 接口 | 开始 | 完成 | 在途 | 上次完成至本次开始 | 其中等限速 | 其余 |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | musicbrainz/release/lookup（200） | 92,715.491 | 92,815.975 | 100.484 | — | — | — |
| 2 | musicbrainz/release/search（200） | 93,719.317 | 93,820.268 | 100.951 | 903.342 | 694.563 | 208.779 |
| 3 | musicbrainz/release/search（200） | 94,723.035 | 94,824.162 | 101.127 | 902.767 | 892.881 | 9.886 |
| 4 | musicbrainz/release/search（200） | 95,726.837 | 95,827.810 | 100.973 | 902.675 | 796.890 | 105.785 |
| 5 | musicbrainz/release/search（200） | 96,730.613 | 96,831.003 | 100.390 | 902.803 | 892.931 | 9.872 |
| 6 | musicbrainz/artist/search（200） | 97,734.320 | 97,835.340 | 101.020 | 903.317 | 893.561 | 9.756 |

五段空档合计 4,514.904 毫秒。其中 4,170.826 毫秒落在 MB 的五个限速定时器上。trace 显示发请求的 goroutine 经 `backend/internal/sources/request.go:362` 进入 `backend/internal/sources/limiter.go:92` 的 acquire 等待。下一请求紧接各自定时器到期发出。自然间隔按请求开始时刻排定，所以 100 毫秒响应本身占用约十分之一的每秒额度。

其余 344.078 毫秒是响应结束至申请下一许可、以及许可到期至 handler 收到请求的时间。第 1→2 和第 3→4 个请求之间，队列观测到已处理专辑数增加，包含上一张收尾和下一张领取。对应代码为 `backend/internal/curation/program/queue_worker.go` 的 drainQueues、acknowledgeQueueItem 调用与 refreshItem，以及 `backend/internal/curation/program/queue_query.go` 的 nextQueueItem。采样不能把每个剩余毫秒精确拆成 SQL、Go 调度或回环传输；保留为残余。

## 第一次出站前的等待

- 手动扫描在 1.321 开始、853.459 完成。成员稳定需要 60 秒，期间识别队列可领取数为 0。两秒轮询继续运行。确认扫描在 62,599.150 开始、62,690.601 完成。触发与签名确认在 `backend/internal/library/curation_observations.go:195` 和 finishCurationObservation。
- 程序在 62,605.923 发起 30 秒等待，扫描仍在运行。队列状态为 waiting_for_scan。`backend/internal/curation/program/queue_worker.go:74` 发起确认扫描，随后 ready 看到 scan_running，`backend/internal/curation/program/worker.go:170` 的 rest 按 ScanInterval 休息，wait 串行等到 92,605.923。确认扫描本身只用 91.451 毫秒，扫描完成后定时器仍有 29,915.322 毫秒。该等待和确认扫描重叠的部分只计一次。
- 第一请求到达 92,715.491。最后请求结束到完成状态观测用 101.834 毫秒，包含写入和至多一个 100 毫秒的观察周期。

总墙钟对账（毫秒）：首次请求前 92,715.491 + HTTP 在途 604.945 + 限速 4,170.826 + 请求间残余 344.078 + 末次请求后 101.834 = **97,937.174**。六次请求没有重叠。

确认扫描与扫描休眠的衔接属于子任务 6 的排队和稳定确认范围。此次只记录归因，没有修改业务调度。
