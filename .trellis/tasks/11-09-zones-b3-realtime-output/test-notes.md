# B3 独立测试与接口样本结果

## 边界

基于 `182eb87`，只实现父任务 `implement.md` B3 第 7 步样本、第 9 步代表性测试；额外覆盖用户指定的空流两种顺序、续流本身三次失败、预载 B 宣布 C 不跳过 B，以及一条 HEAD 不消耗流。
未修改业务代码、前端或迁移，未暂存、提交或归档。原前端计次测试仅经 `git show 9344074:frontend/src/features/player/engine/listen-tracker.test.ts` 只读参考。

## 测试文件

新增：

- `backend/internal/zones/output_fixture_test.go`：复用 B2 fixture，脚本渲染器与事件同步。
- `backend/internal/zones/output_http_integration_test.go`：真实 HTTP/SSE 首快照、命令增量、用户隔离、当前/接续流、单次取用、旧 seq 410、HEAD。
- `backend/internal/zones/output_integration_test.go`：释放、45 秒租约/5 秒扫描、同会话恢复、10 秒接手、取代/重启、续流三次失败、自然换曲/边界/结束，以及指定追加路径。
- `backend/internal/zones/listen_tracker_test.go`：历史计次规则表驱动测试；同条目循环包括第一轮已计次后第二轮再计次。旧 `mediaDuration` 回填在服务端无对应输入，改为目录时长权威断言，不引入浏览器媒体时长。
- `backend/internal/app/zones_fixture_test.go`：隔离库、真实 Guard/handler/middleware、注入时钟、真实 SSE 逐帧读取。
- `backend/internal/app/zones_listening_integration_test.go`：20 秒曲目播放 11 秒写一行，继续回报与 time 后仍一行，保存秒数不变。
- `backend/internal/app/zones_api_samples_integration_test.go`：真实请求/响应/SSE、204 与流头专用采集、全部命令与错误矩阵。

修改：

- `backend/internal/zones/control_fixture_test.go`：时钟互斥保护，回调在解锁后执行。
- `backend/internal/zones/control_integration_test.go`：既有用例改用时钟访问方法，不改变断言。
- `backend/internal/app/api_samples_integration_test.go`：`ownsSample` 独占识别 `playback-zones/`，扩充所有权断言。

## 样本

目录：`backend/internal/app/testdata/api-samples/playback-zones/`。

共 **153 个 JSON 文件**：70 个请求、69 个响应、14 个事件样本。包含实际 SSE GET 请求、五类 SSE 事件及各类 zone 公告、`play_recorded`、接续流、`Stream.format:null`、空数组/null、204 与真实音频响应头。请求来自实际发送字节；事件来自真实 HTTP SSE 帧；不手写 DTO 金样。

## 实际验证命令与结果

数据库只使用独占 Compose 项目 `roomusic-test-zones`、随机端口/数据库，持有 `/tmp/roomusic-playback-e2e.lock`；测试 schema 独立。以下 Go 命令均在 `backend/` 执行。数据库环境来自本轮启动的实例，不读取生产 `.env`。

```sh
ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-test-zones \
  docker compose --env-file /dev/null -f compose.test.yaml \
  -p roomusic-test-zones up -d --wait postgres

# 纯规则，无数据库；由测试实现子 agent 执行一次。
env -u ROOMUSIC_TEST_DATABASE_URL go test ./internal/zones -run '^TestZonesListen' -count=1

# 所有权；首次编译修正测试类型名后通过。
go test ./internal/app -run '^TestAPISampleRecorderOwnership$' -count=1

# 真实 listening 集成。
go test -v ./internal/app -run '^TestPostgreSQLZonesPlayHistoryOnce$' -count=1

# 本包全部 zones 数据库用例，含 B2 的局部回归。
go test -v ./internal/zones -run '^TestPostgreSQLZones' -count=1 -timeout=180s

# 按交接约定，生成后比对。
ROOMUSIC_UPDATE_API_SAMPLES=1 go test ./internal/app -run '^TestPostgreSQLZonesAPIResponseSamples$' -count=1
go test ./internal/app -run '^TestPostgreSQLZonesAPIResponseSamples$' -count=1

# 静态复核补齐既有历史循环断言后，仅运行这一条改动用例。
env -u ROOMUSIC_TEST_DATABASE_URL go test -v ./internal/zones \
  -run '^TestZonesListenVisits$/^同条目循环重新开始$' -count=1

# 结束后清理本轮独占容器与卷。
docker compose --env-file /dev/null -f compose.test.yaml \
  -p roomusic-test-zones down -v --remove-orphans
```

| 验证 | 最终结果 |
| --- | --- |
| 纯计次规则 | 4 个顶层测试，29 个子用例，加无子用例的冻结重试测试，共 30 个场景通过 |
| 样本所有权 | 1 个顶层、10 个子用例通过 |
| 真实历史计次 | 1 个测试通过 |
| `TestPostgreSQLZones` | 20 个顶层、34 个子用例通过，0 失败、0 跳过；其中新增输出测试 9 个顶层、14 个场景 |
| 最终样本生成与比对 | 各 1 个测试通过；153 个文件逐字一致 |
| 补齐后的已计次循环断言 | 1 个子用例通过 |

限定变更文件的 `gofmt -l` 与 `git diff --check` 通过。未运行全量、前端、真实渲染器或额外压力/竞态演练，遵守用户最小测试集约束。

## 测试开发阶段的问题

- 首次 app 编译失败：测试用了不存在的 `PlayingInput` 和错误的能力字段拼写；已改为 `PlayingOutput` / `MIMEType`。属于测试编写错误，未修改业务。
- 首次样本生成 1 个测试失败：准备 `queue_full` 时的一万条 insert 事件超过采集器帧上限，随后读取 SSE 得到提前结束。容量准备本身不属于要采集的事件样本；改为这段前关闭采集，清空后重新连接再采集 `device_removed`，并补充 scanner 错误诊断。
- 第一版样本生成及比对各通过后，静态复核指出缺少实际 SSE 请求样本及终结事件 ACK/Close 潜在竞态。补齐后重新生成/比对各一次，全部通过；这次重跑针对修改，不是重复演练。
- 终结 `stream_ended`、第五次 `track_failed` 可在 `Emit` ACK 前关闭会话；样本只在这两个调用点允许 `ErrClosed`，仍以 `all_failed` / `failures_stopped` SSE 作为成功证据，其他事件保持严格报错。

## 实现缺陷

本次限定测试未发现业务实现缺陷。实现交接已说明的跨重启访问累计/幂等身份未持久化、旧输出身份无淘汰规则仍是原有设计限制；本轮没有扩表或改变这些约束。
