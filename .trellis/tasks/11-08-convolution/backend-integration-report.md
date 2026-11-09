# 卷积后端接入回报

日期：2026-11-09。分支：`dsp/convolution-integration`。本次没有提交 Git，没有修改 `frontend/` 或 `.trellis/workspace/`；同一工作树中前端改动归主会话所有。

## 1. 改动文件

以下为本次后端及合同文件，样本另列于第 5 节：

- `.trellis/spec/backend/convolution-contracts.md`
- `.trellis/spec/backend/index.md`
- `backend/internal/app/app.go`
- `backend/internal/app/dependency_rules_test.go`
- `backend/internal/app/dsp_api_samples_integration_test.go`
- `backend/internal/app/renderer.go`
- `backend/internal/app/zones_api_samples_integration_test.go`
- `backend/internal/app/zones_fixture_test.go`
- `backend/internal/dspsettings/httpapi/handlers.go`
- `backend/internal/dspsettings/httpapi/ir.go`
- `backend/internal/dspsettings/ir.go`
- `backend/internal/dspsettings/ir_cache.go`
- `backend/internal/dspsettings/ir_store.go`
- `backend/internal/dspsettings/irload/cfg.go`
- `backend/internal/dspsettings/irload/loader.go`
- `backend/internal/dspsettings/irload/prepare.go`
- `backend/internal/dspsettings/irload/raw.go`
- `backend/internal/dspsettings/irload/resample.go`
- `backend/internal/dspsettings/irload/wav.go`
- `backend/internal/dspsettings/irload/zip.go`
- `backend/internal/dspsettings/service.go`
- `backend/internal/playback/render/buffering_test.go`
- `backend/internal/playback/render/convolution.go`
- `backend/internal/playback/render/ffmpeg/tool.go`
- `backend/internal/playback/render/negotiate.go`
- `backend/internal/playback/render/plan.go`
- `backend/internal/playback/render/renderer.go`
- `backend/internal/playback/render/sample_rate_test.go`
- `backend/internal/playback/render/session.go`
- `backend/internal/playback/render/skeleton_test.go`
- `backend/internal/playback/render/test_helpers_test.go`
- `backend/internal/zones/commands.go`
- `backend/internal/zones/convolution.go`
- `backend/internal/zones/devices.go`
- `backend/internal/zones/ports.go`
- `backend/internal/zones/render_runtime.go`
- `backend/internal/zones/settings.go`
- `backend/internal/zones/settings_convolution.go`
- `backend/internal/zones/settings_documents.go`
- `backend/internal/zones/settings_read_integration_test.go`
- `backend/internal/zones/store.go`
- `backend/internal/zones/store_integration_test.go`
- `backend/internal/zones/store_read.go`
- `backend/internal/zones/types.go`
- `backend/migrations/0048_dsp_ir_files.down.sql`
- `backend/migrations/0048_dsp_ir_files.sql`
- `backend/migrations/0049_playback_zone_convolution.down.sql`
- `backend/migrations/0049_playback_zone_convolution.sql`

- 本回报：`.trellis/tasks/11-08-convolution/backend-integration-report.md`。

实现只接入既有纯卷积、SRC 与 cfg/zip 解析；没有重写纯卷积引擎。已有测试只调整新默认文档及异步准备同步点，新增 recorder 数据不代替设计 §9 的独立行为测试。

## 2. 接口与合同

完整合同已写入 `.trellis/spec/backend/convolution-contracts.md`，并登记 backend index。

- 设置：沿用 `SettingsDocument`，用 `spec.Settings.Convolution.DecodeConvolution()`；默认 `{enabled:false,ir_id:null,before_peq:false}`。完整文档校验、持久化、响应均接通，非空 ID 不论 enabled 状态都在 plan/RegisterDevice 预检及事务内核验本人归属。
- `zones.Options.IRReferences`：`Check/Delete(ctx,tx,user,id)`、`Invalidate(user,id)`；`Service.DeleteIR(ctx,user,id)` 取得用户写锁、按 zone_id 排序的 dispatch/区锁，同一事务删除 IR、清理全部区引用并增长 revision，提交后才同步内存、失效缓存和重建。
- 删除异常保留 404，不把资源不存在转成 503。IR ID 属于他人和不存在均映射 `zones.ErrNotFound`。
- 资源：`NewService(db)` 兼容；`ConfigureIR`、`ListIRs`、`UploadIR`、`RenameIR`、`CheckIR`、事务必需的 `DeleteIR`、`InvalidateIR`、`LoadIR`。HTTP 使用可选 `WithIRDeleter`，不直接删资源表。
- IR CRUD DTO、`{items:[]}` 列表、上传 201、删除 204、六类错误按用户裁决实现；名称不唯一，trim 后 1–64 字。cfg 错误消息严格为 `cfg 第 N 行看不懂`。
- 请求文件最多 32 MiB，请求另有 1 MiB 开销；zip 最多 64 条、实际解压最多 256 MiB；解码、目标几何与临时文件都有界。上传读取 60 秒、处理 120 秒，取消/失败/成功均清理。音频仅第一个音轨，上传核验全部 cfg 引用。
- `ffmpeg.Tool.Independent(1)` 共享只读探测能力，分离 IR 解码和播放名额；loader 同时仅一次处理。最近 4 份缓存按成员、ID、摘要、目标率、声道数和布局隔离，删除后的失效代次防止在途旧装载重新填充。
- `render.Options.IRs` + app 适配已接通；Open 保留既有来源元数据准备，但不读取 IR、不做解码、SRC 核或卷积 FFT。会话协程先装载/构链，再 Reserve（WAV 一个，其他编码两个）并启动编码/解码；encoder=nil 准备态不再当来源失败。准备阶段 Close 不阻塞，Body/HLS 等待受取消控制。
- 卷积按 SRC/矩阵后、Tail 前格式装载，经 `chain.Wrap` 声明固有 Latency；默认 PEQ→卷积，before_peq 时交换。信号 `kind:convolution,quality:enhanced,compat:false`，params 为 name/taps，以及仅重采样时的原采样率。普通装载失败只跳过卷积并记脱敏警告，取消结束会话，已公布位深不变。

### 原有重建行为保持

普通 `scheduleRebuildAfter` 仍只检查 `session!=nil`：暂停但保留会话时改设置、定位等路径的重建资格不变；无会话时不产生普通重建。新增“在线＋播放意图＋当前项＋无会话＋代次不变”只属于 `scheduleIRDeletionRebuild`，用于 DeleteIR 主动关会话之后；重试也重核验该条件，不让旧定时器重开已替换会话。

### 静态审查修复

审查发现极小 cfg 能指定巨大延时，绕过文件与 taps 上限，导致卷积器分配巨量延时线。已增加 `irload.MaxDelayBytes=256 MiB` 合计 float64 延时预算：上传源率、装载目标率和单路径复制后的配置均在实际延时缓冲分配前检查，超限返回 `ir_too_large`。这是针对确定输入路径的资源边界修复，不改普通毫秒级延时算法。

根因归类为缺失资源合同及隐含假设：文件大小/taps 有界并不代表配置派生的延时线有界。此次一次定位后修复，未经历循环试改；预防机制是在装载边界检查聚合预算与复制后的几何，并写入卷积合同。后续独立测试应保留极大 cfg 延时和单路径复制放大的拒绝用例，避免只测压缩/音频字节上限。

## 3. 实际执行命令与结果

### 编译及已有受影响测试

仓库根设置固定版本工具，然后在 backend 执行：

```bash
export ROOMUSIC_TEST_FFMPEG="$(scripts/ffmpeg-pinned.sh)"
cd backend
go test ./internal/dsp/convolution ./internal/dspsettings/... ./internal/zones/... ./internal/playback/render -count=1
go test ./internal/app -run '^TestPackageDependencyDirections$' -count=1
```

均通过。真实 render 测试用固定 ffmpeg，render 包耗时 6.812 秒。没有数据库变量的这一轮不算数据库事务证据；数据库证据见下面的隔离集成。

静态审查代理还实际执行并通过：

```bash
# backend 下
go vet ./internal/dspsettings/... ./internal/zones/...
go build ./internal/dspsettings/... ./internal/zones/...
```

最后延时预算修复后只复跑对应最小包：

```bash
cd backend
go test ./internal/dspsettings/irload -count=1
```

通过（0.013 秒）；未因该纯内存预算修复重复运行集成或样本更新。

对改动 Go 文件执行 `gofmt -w`，并执行范围限定的 `git diff --check`，均通过。最终检查命令：

```bash
git diff --check -- backend .trellis/spec/backend .trellis/tasks/11-08-convolution
```

### 一次样本更新

先更新金样，使随后普通集成可校验生成结果：

```bash
ROOMUSIC_UPDATE_API_SAMPLES=1 ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-test-convolution ROOMUSIC_TEST_RUN='^TestPostgreSQL(Zones|DSP)APIResponseSamples$' scripts/test-integration.sh
```

通过：DSP recorder 4.64 秒，Zones recorder 2.96 秒，app 合计 7.616 秒。只更新这两个所有权目录，没有手改响应内容。临时执行输出为 `/tmp/roomusic-convolution-samples.log`。

### 一次定向集成

```bash
ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-test-convolution ROOMUSIC_TEST_RUN='TestPostgreSQLZones|TestPostgreSQL.*DSP|TestPostgreSQLMigrationRollbackRoundTrip' scripts/test-integration.sh
```

通过：app 10.119 秒、postgres 46.867 秒、zones 41.085 秒。两次脚本使用独立 `roomusic-test-convolution` 项目、随机数据库与端口，结束均删除本次测试容器和卷；未操作 `roomusic-postgres-1`。临时执行输出为 `/tmp/roomusic-convolution-integration.log`。

实际匹配并通过的顶层测试：

- `TestPostgreSQLDSPAPIResponseSamples`
- `TestPostgreSQLDSPPresetsOwnership`
- `TestPostgreSQLZonesAPIResponseSamples`
- `TestPostgreSQLZonesPlayHistoryOnce`
- `TestPostgreSQLMigrationRollbackRoundTrip`
- `TestPostgreSQLZonesInvalidSettingsFallback`
- `TestPostgreSQLZonesStoreRoundTrip`
- `TestPostgreSQLZonesUserIsolation`
- `TestPostgreSQLZonesTransactionRollback`
- `TestPostgreSQLZonesControlCommands`
- `TestPostgreSQLZonesControlErrors`
- `TestPostgreSQLZonesControlTransfer`
- `TestPostgreSQLZonesControlImport`
- `TestPostgreSQLZonesControlDebounce`
- `TestPostgreSQLZonesControlIsolation`
- `TestPostgreSQLZonesControlOriginBeforeAuthentication`
- `TestPostgreSQLZonesControlCleanup`
- `TestPostgreSQLZonesHeadroomSettings`
- `TestPostgreSQLZonesHLSHTTP`
- `TestPostgreSQLZonesOutputHTTPSnapshotDeltaIsolation`
- `TestPostgreSQLZonesOutputHTTPStreamsAndHEAD`
- `TestPostgreSQLZonesOutputReleaseAndTakeover`
- `TestPostgreSQLZonesOutputLeaseAndSameSessionRecovery`
- `TestPostgreSQLZonesOutputSupersededAndRestart`
- `TestPostgreSQLZonesOutputStreamLostThreeFailedContinuations`
- `TestPostgreSQLZonesOutputRenderEvents`
- `TestPostgreSQLZonesOutputPreloadedBoundaryDoesNotSkip`
- `TestPostgreSQLZonesOutputEmptyStreamEndedBoundaryOrders`

脚本中其他包显示 `no tests to run`，不计为其行为验证。未执行全量 go test、全量集成、Playwright 或 playback-e2e；按用户要求，全套留到发版前。上述定向脚本自身遍历包，但只运行指定正则匹配的测试。

## 4. 迁移 up → down → up

`TestPostgreSQLMigrationRollbackRoundTrip` 自动发现两份真实双向迁移：

- `0048_dsp_ir_files.sql`：通过，1.74 秒。
- `0049_playback_zone_convolution.sql`：通过，1.68 秒。

既有测试分别建立迁移前 schema 快照、升级、回退、再次升级并比较结构/数据；此次新表与新列往返成功。两个录制流程也在新隔离 schema 上完整升级到 0049。没有 `ON DELETE SET NULL`。新 IR 备份恢复行为测试尚待独立测试 agent，不以 schema 往返代替备份验收。

## 5. 样本文件列表

以下路径均相对 `backend/internal/app/testdata/api-samples/`。

### 新增（14 份：六种 IR 请求/响应，非默认卷积设置请求/响应）

- `dsp/requests/ir-cfg-invalid.json`
- `dsp/requests/ir-list.json`
- `dsp/requests/ir-not-found.json`
- `dsp/requests/ir-rename.json`
- `dsp/requests/ir-too-large.json`
- `dsp/requests/ir-upload.json`
- `dsp/responses/ir-cfg-invalid.json`
- `dsp/responses/ir-list.json`
- `dsp/responses/ir-not-found.json`
- `dsp/responses/ir-rename.json`
- `dsp/responses/ir-too-large.json`
- `dsp/responses/ir-upload.json`
- `playback-zones/requests/set-settings-convolution.json`
- `playback-zones/responses/set-settings-convolution.json`

### 更新（56 份既有 Zones 响应/事件，增加默认卷积文档）

- `playback-zones/events/zone-all-failed.json`
- `playback-zones/events/zone-autoplay-blocked.json`
- `playback-zones/events/zone-failures-stopped.json`
- `playback-zones/events/zone-next-stream.json`
- `playback-zones/events/zone-no-stream-format.json`
- `playback-zones/events/zone-play-recorded.json`
- `playback-zones/events/zone-queue-restored.json`
- `playback-zones/events/zone-signal.json`
- `playback-zones/events/zone-track-failed.json`
- `playback-zones/events/zone-track-started.json`
- `playback-zones/events/zone-transient-failure.json`
- `playback-zones/responses/claim-fixed.json`
- `playback-zones/responses/claim-playing.json`
- `playback-zones/responses/claim-replacement.json`
- `playback-zones/responses/claim-takeover.json`
- `playback-zones/responses/claim-target.json`
- `playback-zones/responses/claim.json`
- `playback-zones/responses/clear-queue.json`
- `playback-zones/responses/ended.json`
- `playback-zones/responses/enqueue.json`
- `playback-zones/responses/fault-autoplay-blocked.json`
- `playback-zones/responses/fault-stream-lost.json`
- `playback-zones/responses/import-unchanged.json`
- `playback-zones/responses/import.json`
- `playback-zones/responses/move-entry.json`
- `playback-zones/responses/next-stream-playing.json`
- `playback-zones/responses/next.json`
- `playback-zones/responses/pause.json`
- `playback-zones/responses/play-empty-format.json`
- `playback-zones/responses/play-entry.json`
- `playback-zones/responses/play-failures.json`
- `playback-zones/responses/play-next.json`
- `playback-zones/responses/play-no-stream-format.json`
- `playback-zones/responses/play-tracks.json`
- `playback-zones/responses/play-transient.json`
- `playback-zones/responses/play.json`
- `playback-zones/responses/previous.json`
- `playback-zones/responses/register-device.json`
- `playback-zones/responses/register-target.json`
- `playback-zones/responses/remove-entry.json`
- `playback-zones/responses/retry.json`
- `playback-zones/responses/seek.json`
- `playback-zones/responses/set-mode.json`
- `playback-zones/responses/set-settings-downmix.json`
- `playback-zones/responses/set-settings-headroom.json`
- `playback-zones/responses/set-settings-peq.json`
- `playback-zones/responses/set-settings-sample-rate.json`
- `playback-zones/responses/set-settings.json`
- `playback-zones/responses/snapshot-next-stream.json`
- `playback-zones/responses/snapshot.json`
- `playback-zones/responses/status-buffering.json`
- `playback-zones/responses/status-loading.json`
- `playback-zones/responses/status-paused.json`
- `playback-zones/responses/status-playing.json`
- `playback-zones/responses/stop.json`
- `playback-zones/responses/transfer.json`

前端合同登记由主会话处理，本会话未修改前端。

## 6. 验收证据边界与遗留问题

- 上传 recorder 使用自主合成的 48 kHz、单声道、4 抽头 WAV，得到 paths=1、taps=4；cfg 无效、超限和跨成员 not_found 响应已通过真实 handler 生成并在定向集成复核。该上传样本不等于真实会话的卷积音频验收。
- 纯包/已有 irload 测试通过，但此次未额外执行本机参考对照，不声明参考数据代数和、Latency 或实际短量子指标已另行验收。没有读取或输出参考稿原文/系数。
- 设计 §9 的新测试仍交独立测试 agent：真实 WAV/FLAC/AIFF/M4A 小 IR、多 cfg 全部核验、32 MiB/取消清理、目标率延时预算、缓存删除失效、准备期锁与取消、跨成员选择/删除后续命令、卷积节点顺序与信号/Latency，以及备份恢复小 IR。
- 既有 `src.ResampleAll` 不接受 context；PrepareContext 在每条 IR 调用前后检查取消，单次 SRC 计算不能中途取消，没有另起游离协程。会话 Close 本身仍立即返回；Renderer.Close 会等待已启动工作收束。
- 没有新增配置、环境变量、外部存储或前端构建产物；没有提交、归档任务或更新工作日志。
