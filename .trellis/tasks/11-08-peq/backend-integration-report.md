# PEQ 后端接入回报

日期：2026-11-09。分支：`dsp/peq-integration`。本轮不提交、不归档；前端由主会话并行维护，本轮未修改 `frontend/`，独立行为测试由测试 agent 负责。

## 1. 改动文件

- 迁移：`backend/migrations/0047_playback_zone_peq.sql`、`.down.sql`，同时增加播放区文档与成员预设表。
- zones：`settings_peq.go`、`settings_peq_json.go`、`settings.go`、`settings_documents.go`、`store.go`、`store_read.go`。HTTP 通过已有公共形状校验入口自动接入白名单，不另写 HTTP 规则。`store_integration_test.go`、`settings_read_integration_test.go` 仅更新现有默认夹具。
- dspsettings：`service.go`、`catalog.go`、`response.go`、`presets.go`、`preset_store.go`，以及 `httpapi/{handlers,input,presets,resources}.go`。
- render：`peq.go`、`plan.go`、`negotiate.go`，`test_helpers_test.go` 仅更新已有默认夹具。
- app：`app.go`、`dependency_rules_test.go`；金样 recorder 的 `api_samples_integration_test.go`、`zones_api_samples_integration_test.go`、新增 `dsp_api_samples_integration_test.go`。新增采集入口仅服务用户明确要求的金样，不新增业务行为测试。
- 金样：`backend/internal/app/testdata/api-samples/dsp/` 新增 38 份请求/响应；`playback-zones/` 由 recorder 刷新 162 份（实际 55 份旧样本增加默认字段，另新增两份 `set-settings-peq`）。没有手工修改样本。
- 规范：新增 `.trellis/spec/backend/peq-contracts.md` 并登记 index；任务 design 记录本轮字段、预设时间和曲线裁定，implement 删除已作废的合入前整组 e2e 要求。

## 2. 对设计的差异及理由

- 无 HTTP 合同偏离。预设增加 `created_at`、`preset_id` 命名及 sample_rate 的 8000–768000 范围遵循本轮后续裁定。
- 设计指向 `session.go` 的组装已按现有骨架落在 `plan.go`，不另建链准备入口；信号复用现有 `chain.Steps()/ModifiesSamples()` 和 `hasProcessing`，无需修改 `signal.go`。实际顺序仍是 SRC/矩阵后、Tail 选择前的单 IIR。
- 有效段遇到采样率低至无法满足算法的 10 Hz 下限（`fs*0.49 < 10`）时返回现有 `ErrNoStreamFormat`，避免调用纯包的编程错误 panic；没有加降级处理或默认替代。
- 最终审阅发现 PostgreSQL text/jsonb 不接受 NUL 字符，因此预设名称与 source 字符串在入口拒绝 NUL，返回 `invalid_input` 而非写库后的 500；这是存储边界校验，不新增回退。
- 曲线按设计后的系数多重集合合并显式声道，避免参数相同但位于不同文档段的声道被错误拆开；重复节次数保留，all 曲线始终独立保留。

## 3. 实际验证命令与结果

以下 Go 命令从 `backend/` 执行。使用缓存中的固定 ffmpeg；非数据库测试时取消 `ROOMUSIC_TEST_DATABASE_URL`。全部只运行一次，无失败后的重跑。

```bash
export ROOMUSIC_TEST_FFMPEG="$(../scripts/ffmpeg-pinned.sh)"
unset ROOMUSIC_TEST_DATABASE_URL
go build ./...
go vet ./internal/dsp/peq ./internal/dspsettings/... ./internal/zones/... ./internal/playback/render ./internal/app
go test ./internal/dsp/peq ./internal/dspsettings/... ./internal/zones/... ./internal/playback/render -skip '^TestPostgreSQL' -count=1 -timeout 3m
go test ./internal/app -run '^TestPackageDependencyDirections$' -count=1 -timeout 1m
```

结果：build、vet、既有测试与依赖规则全部通过。`dsp/peq` 0.004s，`dspsettings/opra` 2.029s，zones 相关包各约 0.01s，render 6.681s，app 依赖检查 0.039s。新增 dspsettings 核心/httpapi 当前没有独立测试文件，不能将其 `[no test files]` 说成单元验证；接口代表流程由本轮 recorder 经过真实 app/数据库采集。

数据库验证复用 `scripts/test-integration.sh` 的隔离约定：全局锁、独立项目 `roomusic-test-peq`、随机端口和库名、固定 PostgreSQL 18 镜像、退出清理。临时执行脚本只运行下列包/用例，未调用脚本的全包测试扫描：

```bash
go test ./internal/platform/postgres -run '^TestPostgreSQLMigrationRollbackRoundTrip$/^0047_playback_zone_peq.sql$' -count=1 -v -timeout 3m
ROOMUSIC_UPDATE_API_SAMPLES=1 go test ./internal/app -run '^TestPostgreSQL(Zones|DSP)APIResponseSamples$' -count=1 -v -timeout 3m
```

结果：

- 0047 升级→回退→再升级通过，1.65s，只匹配该迁移。
- `TestPostgreSQLDSPAPIResponseSamples` 通过，3.12s，生成 38 份金样。
- `TestPostgreSQLZonesAPIResponseSamples` 通过，2.96s，刷新 162 份金样。
- `docker compose --env-file /dev/null -f compose.test.yaml -p roomusic-test-peq down -v --remove-orphans` 已完成；按该项目标签查询容器、卷、网络均为空。
- `gofmt` 与 `git diff --check -- backend .trellis` 通过。

时序说明：最后审阅补充的 NUL 字符拒绝在 17:44:14 UTC 落盘，晚于 build/vet/单元测试完成（17:44:06），早于已通过的数据库/金样采集完成（17:44:56）。最终代码由后一次采集重新编译通过；遵循不重复要求，未为这项局部校验重跑前一组验证，不把前一组结果冒称为该末次修改的单独行为验证。

没有运行全量集成、e2e、race，也没有重复采集；遵循本轮限定测试量。未操作 `roomusic-postgres-1`。本机执行日志保存在 `/tmp/roomusic-peq-verify-go.log`、`/tmp/roomusic-peq-verify-db.log`（临时文件，不作为仓库依赖）。

## 4. OPRA 首次加载常驻内存实测

在独立 Go 进程调用 `opra.LoadEmbedded()` 一次，加载前后执行 `runtime.GC()` 与 `debug.FreeOSMemory()`，读取 `runtime.MemStats.HeapAlloc` 和 `/proc/self/status` 的 VmRSS。临时探针以 `go run ./peq-memory-probe` 运行，已移除探针目录，无测试文件或生产诊断代码残留。

| 指标 | 实测 |
| --- | --- |
| 首次加载耗时 | 1.986369517 秒 |
| 厂商 / 产品 / EQ | 714 / 6230 / 12594 |
| 加载前保留堆 | 194,296 字节 |
| 加载后保留堆 | 18,708,112 字节 |
| 新增保留堆 | 18,513,816 字节，17.66 MiB |
| 加载前 / 后 VmRSS | 3,660 / 27,564 KiB |
| RSS 增量 / 加载后总量 | 23.34 / 26.92 MiB |

这是该独立进程 GC 后的保留堆与驻留量，不是压缩包大小，也不是整台服务的总占用或峰值。原始输出：`/tmp/roomusic-peq-opra-memory.log`。

沿用已合入纯包的 OPRA 内嵌数据，未重新下载或修改：gzip 1,028,984 字节，SHA-256 `1e59c500c51f4c45140b55ad535d0f80ad9b2defd8d04c319b27ce381f9f6908`；原始上游 SHA-256 `5c4cf9e2f71c696bf4fc5b4d1a46c57f31c6d0041552c2146fcd1413596fd210`。来源与 CC BY-SA 4.0 完整许可继续随二进制嵌入。

## 5. 后续边界

本轮后端接入已完成。前端构建/验证、独立新增行为测试以及最终合入由主会话继续协调；本轮不声称已经完成这些工作。任务保持进行中，未执行 git commit。
