# ⑤ 并发解析

父任务：[扫描子系统对标](../11-08-scan-reference-port/prd.md)，S 线第 4 项。对照依据：[gap-analysis.md](../11-08-scan-reference-port/research/gap-analysis.md) G10；参考设计 7.1、7.4、16.4 章。

## 目标

首扫和有大量变化的重扫更快：打开文件、解析标签、计算指纹这些读盘工作并发进行，写数据库仍保持一个写入者，结果与串行一致。

## 现状

- 遍历回调里同步解析（`backend/internal/library/scanner.go:318-481`），指纹逐个计算（`backend/internal/catalog/ingest/identity_plan.go:205-209`），候选逐个短事务写入，整个扫描只用一条数据库连接（`backend/internal/library/scans.go:38-60` 要求 `MaxOpenConns>1` 才能拿到独占连接）。
- OGG/Opus 解析把整个文件读进内存，没有上限（`backend/internal/library/parser/ogg.go:15`）；并发后内存占用会成倍放大。
- 取消只在显式的 `ctx.Err()` 检查处生效，所有 SQL 走 `WithoutCancel`（`scanner.go:36-52`）。

## 需求

- **R1 并发读盘**：解析与指纹计算交给固定大小的工作池（默认 4），遍历与数据库写入仍是单一 goroutine；暂存写入顺序、诊断内容与串行时一致（诊断条数上限 100 不变，截断时保留哪些按确定顺序）。
- **R2 内存上限**：OGG/Opus 解析改为流式读取或设上限，单个文件的内存占用不随文件大小线性增长；其他解析器检查同类问题。
- **R3 取消与失败**：取消后工作池尽快停止，已发出的任务结果丢弃；单个任务失败按 ① 的规则处理，不影响其他任务。
- **R4 进度**：扫描反馈的计数与百分比在并发下仍正确、单调（`scan-feedback-contracts.md` 第 3 节）。
- **R5 并发度**：默认 4，可用环境变量 `ROOMUSIC_SCAN_WORKERS` 在 1–16 调整（填错启动失败；1 等于串行），写进部署说明（design 3.1）。

## 验收

- 单测：并发与串行产生相同的暂存结果与诊断；取消能及时结束；`go test -race` 覆盖工作池。
- 大 OGG 文件的内存占用测试（或基准）证明不随文件大小线性增长。
- 真实库只读 smoke：首扫耗时前后对比。

## 不做

- 多个根目录并行扫描、多数据库写入者。

## 已裁决

- 2026-11-08 用户裁决：默认 4，环境变量可调（R5）。

## 依赖

S 线第 4 项，在 ④ 之后（两者都改遍历回调；④ 先减少需要解析的文件，⑤ 再并发剩下的）。

## 实施结果（2026-11-08）

提交：`9893f85`（新包 `platform/workpool`；未命中解析缓存的文件交给 worker 并发解析，暂存、诊断与进度仍按遍历顺序应用；指纹按页并行；OGG/Opus 逐页流式读取；`ROOMUSIC_SCAN_WORKERS` 1–16、默认 4）。随 S2 合并 `94623b5` 入 main，无迁移。

裁决与偏离（见 [backend-report.md](./backend-report.md)）：新增 `Ordered.Flush` 与 CUE 两段式接口，保证缓存命中不随并发时机变化；OGG 指纹入口原来也不是流式，一并改；OGG 不按最后 granule 新增时长，保持旧结果。

验收：

- 并发与串行等价、取消：满足。`TestPostgreSQLConcurrentScanMatchesSerialStagingDiagnosticsAndCache`（workers 1/4 的暂存、诊断与缓存计数一致，S2 最终验收通过）、`TestPostgreSQLScanPipelineCancelDiscardsInflightAndJoinsWorkers`；工作池 `TestOrdered*`、`TestMap*`（顺序、窗口、首个错误、取消后等 worker 退出）。
- `go test -race`：部分有证据。实施时 `go test -race ./internal/platform/workpool/ ./internal/library/... ./internal/platform/config/` 通过，但当时 workpool 还没有测试文件；补测后的 workpool `-race` 记录未找到。
- 大 OGG 内存：满足。`TestOggStreamingMemoryDoesNotGrowWithFileSize`；`TestOggOpusObservationsMatchHEADBaseline` 对照旧实现的字段与指纹。
- 首扫耗时：满足。music/ 首扫 `65eb019` 26.4 秒 → S2 候选 18.4 秒（与升级验收并行时的观测值，不作严格基准）。

已知局限：OGG 反复出现注释包时元数据累积、CUE 含极多 FILE 时的内存占用，保持旧接受范围（design，用户裁决接受）。
