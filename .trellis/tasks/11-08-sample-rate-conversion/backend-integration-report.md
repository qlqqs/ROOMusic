# SRC 后端接入回报（2026-11-09）

## 结果与边界

完成 implement §2 第 1–3 步及本轮指定的最小验证。未提交 git；未修改 `frontend/` 或前端构建产物；未跑整组 real/mock e2e，不归档任务。

前端接口合同无偏离：`settings.sample_rate` 完整对象、默认值、五种策略、四种滤波器和十种自定义率保持约定；信号步骤仅由链 ② 生成，params 只有 `from_rate/to_rate/filter`。不可变规范文档参与 EqualSettings，临时 `custom_rate` 指针不参与比较。

按主会话两次裁定：AAC 转换目标不扩展标准集合；`Negotiate` 排除显式上限低于 44100 的 AAC 能力。不修改 zones 的 Open 错误映射，不新增错误类型，不为该现实中不存在的场景新增测试。design §6.2 已补指定裁定文字。

SRC 核在会话启动协程内构建，不在控制锁内生成；构建前后均检查取消。保留临时 `-ac` 与 CUE 截取，删除解码与编码侧变率；编码器输出 `-ar` 等于输入 PCM 率。

## 改动文件

- 迁移：`backend/migrations/0045_playback_zone_sample_rate.sql`、同名 `.down.sql`。
- zones：`settings.go`、`settings_documents.go`、新增 `settings_sample_rate.go`、`store.go`、`store_read.go`、`httpapi/input.go`。
- zones 测试：新增 `settings_sample_rate_test.go`、`httpapi/input_test.go`，扩展 `settings_read_integration_test.go` 与 `store_integration_test.go`。
- render：`negotiate.go`、`plan.go`、`session.go`、`decoder.go`、`encode.go`、`signal.go`；测试 helper 补规范默认设置，新增 `sample_rate_test.go`。
- app：`dependency_rules_test.go` 登记 render→src，并修复合并基线已有的重复 `dsp/internal/refdata` map 键；`zones_api_samples_integration_test.go` 新增非默认请求/响应。
- API 样本：仅 `backend/internal/app/testdata/api-samples/playback-zones/`，53 份已有样本增加默认键，另新增 `requests/set-settings-sample-rate.json` 与 `responses/set-settings-sample-rate.json`。结构比较确认 53 份样本除 `sample_rate` 外内容不变。
- 文档：任务 `design.md`、本回报及 `.trellis/spec/backend/{playback-render-contracts,playback-zone-contracts,zones-data-contracts}.md`。

`RegisterDevice`、`commands`、`validation`、`settingsOf`、`CreateDevice` 已通过现有统一入口获得行为，无需为清单机械修改这些文件；CreateDevice 先取列默认，再在同一事务调用 UpdateZone 保存实际设置。

## 实际验证

以下 Go 命令均在 `backend/` 执行；固定工具先在仓库根设置：

```bash
export ROOMUSIC_TEST_FFMPEG="$(scripts/ffmpeg-pinned.sh)"
```

- `go test ./internal/dsp/internal/fft ./internal/dsp/src -count=1`：src 通过；fft 显示 `[no test files]`，不算独立 FFT 测试证据。
- `go test ./internal/zones/... ./internal/playback/render -count=1`：通过；无数据库环境时 DB 用例跳过，真实数据库证据见下文。
- `go test ./internal/app -run '^TestPackageDependencyDirections$' -count=1`：首次被基线重复 map 键阻断，去重后通过。
- `go test ./internal/playback/render -run '^TestNegotiate$|^TestProcessingDepthAndDither$|^TestRenderSession$' -count=1`：通过。
- `go test ./internal/playback/render -run '^TestRenderSampleRateAAC$|^TestRenderSession$' -count=1`：取消检查修正后通过。
- `go vet ./internal/zones/... ./internal/playback/render`：通过。
- 测试子代理：`go test ./internal/playback/render -run '^TestRenderSampleRateAAC$' -count=1 -v`、相同用例加 `-race`、`go vet ./internal/playback/render`、`go build ./internal/playback/render`：通过。
- zones 子代理：`go test ./internal/zones ./internal/zones/httpapi -run 'Test(HeadroomDocumentCanonicalAndShape|SettingsDocumentIndependentDecode|SampleRate)' -count=1`、`go vet ./internal/zones ./internal/zones/httpapi`：通过。
- 本次 Go 文件 `gofmt` 检查、`git diff --check`：通过。

新增渲染用例按最新裁定仅一条 192→48 kHz AAC：用户请求 384 kHz 被交付约束覆盖；实际输入提交、用户节点和编码输入帧数符合比例；outputPCM/format 均为 48000；真实 ADTS 头声明 48 kHz；信号只有一次 sample_rate，兼容评级正确、无旧 convert，事件时长正确。无转换 MD5 复用 TestRenderSession，不读 `/proc`、不分平台、不新增生产测试接口。

仓库根实际执行：

```bash
ROOMUSIC_UPDATE_API_SAMPLES=1 ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-test-src \
  ROOMUSIC_TEST_RUN='^TestPostgreSQLZonesAPIResponseSamples$' scripts/test-integration.sh

ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-test-src \
  ROOMUSIC_TEST_RUN='TestPostgreSQLZones|TestPostgreSQL.*DSP|TestPostgreSQLMigrationRollbackRoundTrip' \
  scripts/test-integration.sh
```

两条均成功；后一条不带更新变量，API 金样复跑通过。使用脚本的全局 flock，不绕过锁。脚本只管理 `roomusic-test-src` 隔离容器/卷，结束均清理；未操作用户 `roomusic-postgres-1`。

`TestPostgreSQLMigrationRollbackRoundTrip/0045_playback_zone_sample_rate.sql` 通过（2.70 秒）：真实 0044→0045→0044→0045，并由既有往返测试比较 schema。整个往返测试按用户指定正则覆盖 0031–0045；未增加迁移执行器测试或新框架。

实际匹配并通过的 26 个顶层用例（没有 `TestPostgreSQL.*DSP` 命名的额外用例，不把零匹配算作该类验证）：

```text
TestPostgreSQLZonesAPIResponseSamples
TestPostgreSQLZonesPlayHistoryOnce
TestPostgreSQLMigrationRollbackRoundTrip
TestPostgreSQLZonesInvalidSettingsFallback
TestPostgreSQLZonesStoreRoundTrip
TestPostgreSQLZonesUserIsolation
TestPostgreSQLZonesTransactionRollback
TestPostgreSQLZonesControlCommands
TestPostgreSQLZonesControlErrors
TestPostgreSQLZonesControlTransfer
TestPostgreSQLZonesControlImport
TestPostgreSQLZonesControlDebounce
TestPostgreSQLZonesControlIsolation
TestPostgreSQLZonesControlOriginBeforeAuthentication
TestPostgreSQLZonesControlCleanup
TestPostgreSQLZonesHeadroomSettings
TestPostgreSQLZonesHLSHTTP
TestPostgreSQLZonesOutputHTTPSnapshotDeltaIsolation
TestPostgreSQLZonesOutputHTTPStreamsAndHEAD
TestPostgreSQLZonesOutputReleaseAndTakeover
TestPostgreSQLZonesOutputLeaseAndSameSessionRecovery
TestPostgreSQLZonesOutputSupersededAndRestart
TestPostgreSQLZonesOutputStreamLostThreeFailedContinuations
TestPostgreSQLZonesOutputRenderEvents
TestPostgreSQLZonesOutputPreloadedBoundaryDoesNotSkip
TestPostgreSQLZonesOutputEmptyStreamEndedBoundaryOrders
```

测试日志在 `/tmp/roomusic-src-api-samples.log`、`/tmp/roomusic-src-integration.log`（本机临时证据，不入库）。

## 未覆盖项

- 未跑全仓 Go 测试、整组 real/mock e2e、前端检查/构建或真机验收，按职责与最小范围留给主会话。
- 未重跑 `refcompare`：本次不修改核、FFT、重采样器或纯包规划器；不把纯包此前的对照结果冒充本轮结果。
- 按主会话最新裁定不新增 Opus 专用接入测试，只保留一条 AAC 变率用例及既有无转换 MD5；没有为 AAC 极低上限新增测试。
- 未读取、复制或输出本机参考资料内容。
