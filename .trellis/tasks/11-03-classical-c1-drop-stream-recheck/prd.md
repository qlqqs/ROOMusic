# C1 删除播放中的定时复核

## 来源

2026-11-03 [路线调整](../../../docs/architecture/core-1-roadmap.md#2026-11-03-路线调整)的 R3（修订 ADR 0004 K3 的在途部分）：只有一个管理员，
“停用成员后他正在播的那首最多 10 秒就断”价值很低，删掉已建立传输的定时复核，只在建立传输时鉴权并核对来源。

## 范围

- 删除已建立音频传输的定时复核：`playback.RecheckingReader` 的复核间隔与 `Check`、`playback.DefaultRecheckInterval`、
  `playbackapi.Options.RecheckInterval`、`app.WithPlaybackRecheckInterval`、handler 里的 `recheck`，以及只服务于复核的
  `Service.StillPlayable`、`ReasonFor` 与中止原因 `session_invalid`、`track_unavailable`、`root_disabled`、`check_failed`。
- **保留**进程停机时中止已建立的传输：应用生命周期上下文结束时读取返回错误，`net/http` 关闭连接，`playback.stream.aborted`
  日志只剩 `shutdown` 一种原因。保留的读取器按剩下的职责命名（例如只认生命周期上下文的读取器），不保留空的复核外壳。
- 不变：每个新请求（预检与串流）照常鉴权、核对来源与目录启用；停用目录禁止播放；`private, no-store`；Range 与 416 的 JSON 信封；
  派生（ALAC → FLAC）的流程、“无人等待 10 秒后取消转换”（K4，与本任务无关）；前端行为。
- 同步文档：[播放合同](../../spec/backend/playback-contracts.md)、[无损派生合同](../../spec/backend/playback-derivation-contracts.md)
  里关于在途复核的描述，ADR 0004 正文里描述 10 秒复核为现行行为的地方（决策记录不改，2026-11-03 那一行已写明），
  路线图 C1 行的状态。

## 验收

| 编号 | 验收 |
| --- | --- |
| A1 | 代码里不再有定时复核：上面列出的符号都已删除，`grep -rn "Recheck\|StillPlayable" backend` 没有生产代码命中 |
| A2 | 停机中止仍有测试：生命周期上下文取消后，正在读取的传输返回错误、记一条 `playback.stream.aborted`（`reason=shutdown`）；客户端先断开不记录 |
| A3 | 新请求仍被拒绝：停用成员 / 撤销会话后串流与预检返回 401，停用目录后返回原有错误码；原有的这些集成用例保留并通过 |
| A4 | 删掉的是“在途切断”的用例（复核间隔、撤销后已建立的连接被切断、复核失败即关闭）；不留跳过的用例 |
| A5 | `gofmt`、`go vet`、`go test -race` 覆盖 `./internal/playback/...` 与 `./internal/app/...`，集成用例在隔离 PostgreSQL 上实际运行（PASS 而非 SKIP）；依赖规则测试通过 |
| A6 | 文档同步完成，改动的 Markdown 相对链接无断链 |

## 范围外

前端改动、派生缓存、CUE、任何新的鉴权机制。
