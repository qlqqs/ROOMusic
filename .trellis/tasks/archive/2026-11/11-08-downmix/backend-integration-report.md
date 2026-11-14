# 降混后端接入回报

日期：2026-11-09。工作树为本任务的 `dsp/downmix-integration`；起点 `aec1ca3`，包含 main `0d0aa3d` 与纯包合入。未提交 Git，任务保持进行中。

## 完成内容

- 0046 添加 downmix JSONB 完整文档，默认 `{"enabled":true}`，提供回退 SQL。
- zones 沿用 sample_rate 的不可变 SettingsDocument 模式：明确配置类型、规范化、完整对象校验、patch、统一白名单、默认设置、固定列读写与坏文档按键回退/脱敏日志。
- `httpapi/input.go` 已复用 `ValidateSettingsShape`；CreateDevice 已使用数据库默认值并调用 UpdateZone，因此两者无需新增独立分支。
- sourcePCM 恢复源声道及布局；协商拒绝设备 MaxChannels 不在 1–32 的值，非 WAV 的交付上限再限到 8。
- matrix 接续 SRC 输出，作为 Stages 中的③；关闭、mono 输出或未知布局需缩减时，在量化前 Tail 选择前 N 声道。纯选择不改样本，保留 Exact/SourceBits；mono 用标准掩码 `0x4`。
- 解码器移除 `-ac`，信号移除旧 ffmpeg 降混分支。downmix 信号携带布局、mode、`by:roomusic`，评级 `high/compat=true`。
- 编码输入声明实际布局；已知多声道或非标准布局的 WAV 使用 WAVEFORMATEXTENSIBLE 记录掩码，标准 mono/stereo 保持原 44 字节头。
- render→dsp/downmix 导入登记到依赖白名单。
- 既有 API recorder 末尾增加独立播放区的 `set-settings-downmix` 非默认采集调用；不改变既有样本事件与 revision 顺序。

## 改动文件

### 后端

- `backend/migrations/0046_playback_zone_downmix.sql`
- `backend/migrations/0046_playback_zone_downmix.down.sql`
- `backend/internal/zones/settings.go`
- `backend/internal/zones/settings_documents.go`
- `backend/internal/zones/settings_downmix.go`
- `backend/internal/zones/store.go`
- `backend/internal/zones/store_read.go`
- `backend/internal/playback/render/downmix.go`
- `backend/internal/playback/render/negotiate.go`
- `backend/internal/playback/render/decoder.go`
- `backend/internal/playback/render/encode.go`
- `backend/internal/playback/render/signal.go`
- `backend/internal/app/dependency_rules_test.go`
- `backend/internal/app/zones_api_samples_integration_test.go`

### 既有测试夹具

仅补齐新增必填文档默认值，不新增测试函数或断言：

- `backend/internal/playback/render/test_helpers_test.go`
- `backend/internal/zones/store_integration_test.go`
- `backend/internal/zones/settings_read_integration_test.go`

### 文档

- `.trellis/spec/backend/playback-render-contracts.md`
- `.trellis/spec/backend/playback-zone-contracts.md`
- `.trellis/spec/backend/zones-data-contracts.md`
- `.trellis/tasks/archive/2026-11/11-08-downmix/backend-integration-report.md`

工作树内并行出现的 frontend 修改不是本后端会话所写，未撤销或改动。未读取或写入参考资料目录，未操作 `roomusic-postgres-1`。

## 设计一致性与未完成项

无算法、产品或设置协议偏离。编码出口补显式布局与扩展 WAV 头，是落实设计中“交付格式与实际布局一致”，否则 7.1→侧环绕 5.1 的掩码不能送达客户端。

**实际 API 金样已在后续授权后刷新。** 金样位于 `backend/internal/app/testdata/api-samples/playback-zones/`，不是 docs 目录。首次验证因用户禁止集成测试而暂停刷新；后续用户批准仅运行一次指定 recorder，已按骨架 §7.4 完成真实采集，未手工批量修改。结果见下文。

迁移升级→回退→再升级及存储往返未执行；不把静态审阅或跳过视为数据库验证通过。新增降混接入用例由独立测试 agent 负责，本会话未新增测试。

## 实际执行的验证

固定 ffmpeg 路径由仓库根 `scripts/ffmpeg-pinned.sh` 取得，使用已有缓存，无需容器。

以下命令从 `backend/` 执行，各运行一次：

```bash
go build ./...
go vet ./internal/zones ./internal/zones/httpapi ./internal/playback/render ./internal/app
env -u ROOMUSIC_TEST_DATABASE_URL \
  ROOMUSIC_TEST_FFMPEG=/home/qlqq/.cache/roomusic/ffmpeg-11a44711684c/ffmpeg \
  go test ./internal/zones ./internal/zones/httpapi ./internal/playback/render -count=1
go test ./internal/app -run '^TestPackageDependencyDirections$' -count=1
```

全部返回 0。zones 0.009 秒、zones/httpapi 0.007 秒、playback/render 6.568 秒，app 依赖方向测试 0.038 秒。显式清除测试数据库地址，确保 zones 中的数据库用例跳过而非连接任何用户数据库；上述时间仅是本轮已有单元测试的 Go 输出。

修改的 Go 文件已 gofmt；仓库根 `git diff --check -- backend .trellis/spec/backend` 通过。静态审阅复核了矩阵顺序、Tail/Exact、位深、布局、完整文档与 SQL 参数，并复核了扩展 WAV 头偏移及 GUID。

首次验证按用户限制未运行集成脚本；后续仅执行下文获准的单个 recorder。未运行任何 e2e、全量 Go 测试或 race，未重复运行验证。不归档任务或调用会自动提交的收尾脚本。


## 后续授权：金样刷新与前端合同核对

用户批准只运行一次指定 recorder，使用独立项目，结束后清理；前端仅核对合同，缺项由主会话修改。实际执行如下，均只运行一次。

### 金样刷新

仓库根：

```bash
ROOMUSIC_UPDATE_API_SAMPLES=1 \
ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-test-downmix \
ROOMUSIC_TEST_KEEP_DB=false \
ROOMUSIC_TEST_RUN='^TestPostgreSQLZonesAPIResponseSamples$' \
scripts/test-integration.sh
```

返回 0；唯一匹配并执行的测试为 `TestPostgreSQLZonesAPIResponseSamples`，3.33 秒通过。脚本仍按现有方式调用 `go test -v ./... -run <精确正则>`，其他包均没有匹配测试，不把它们记作验证通过；没有执行其他集成测试。

recorder 以 playback-zones 为所有权范围重写 160 个样本；实际变更 54 个既有文件，新增 2 个文件。既有变更仅添加默认 `downmix:{enabled:true}`（transfer 的两个 zone 各添加一次）。新增请求提交 `downmix:{enabled:false}`，新增响应为 HTTP 200，完整设置中的 downmix 也为 false。

脚本结束已删除 `roomusic-test-downmix-postgres-1` 及其数据卷。分别以 `com.docker.compose.project=roomusic-test-downmix` 标签查询容器、卷、网络，均为空；没有对 `roomusic-postgres-1` 执行操作。没有再启动库或重跑 recorder。

### 前端核对

`frontend/`：

```bash
npx vitest run src/features/player/zones src/shared/api src/test/api-contract.test.ts
```

结果：13 个测试文件中 12 通过、1 失败；684 个测试通过、1 失败、1 跳过，耗时 3.82 秒。唯一失败是 `src/test/api-contract.test.ts:830` 的“每个样本都有契约，每个契约都有样本”，报告两个新增样本尚未登记。现有 zones/shared API 解码测试通过；新增非默认响应因未登记，尚未进入该合同文件的响应解码用例，不能宣称它已经完成前端合同验证。

交给前端主会话补充的两项（本后端会话未修改 frontend，也未复跑）：

```ts
// src/test/api-contract.test.ts 的 successContracts
"playback-zones/responses/set-settings-downmix": {
  status: 200, decode: decodeCommandResult, preservesWireShape: false,
},

// 同文件的 requestContracts，沿用 set-settings-sample-rate 的模式
"playback-zones/requests/set-settings-downmix": {},
```

原有一条仓外巨型艺人样本用例因未配置目录而跳过，与本次改动无关。此次不运行前端构建、e2e、其他集成测试、迁移回退或重复测试。样本金样 `git diff --check` 通过。

完整本机日志：`/tmp/roomusic-downmix-api-samples.log`、`/tmp/roomusic-downmix-frontend-contract.log`，不作为仓库产物提交。

### 实际变更的金样文件

以下路径均相对于 `backend/internal/app/testdata/api-samples/`：

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
- `playback-zones/requests/set-settings-downmix.json`（新增）
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
- `playback-zones/responses/set-settings-downmix.json`（新增）
- `playback-zones/responses/set-settings-headroom.json`
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
