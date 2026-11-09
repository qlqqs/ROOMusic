# B3 根包实现交接

## 范围与状态

只修改 `backend/internal/zones` 根包，未修改 `httpapi`、`queue`、`zonestest`、app、DSP、main.go、frontend 或测试。没有 git add/commit，没有启动数据库。代码已就绪，交主会话独立检查与限定验证；这不是测试通过声明。

新增文件：

- `output_types.go`：协议输入/响应、错误、租约常量与生命周期/计时入口。
- `outputs.go`：认领、回报、释放、单次取流、分段位置换算。
- `output_lifecycle.go`：五秒扫描、离线暂停、原子快照订阅与输出关联。
- `render_events.go`：当前/接续会话身份、事件消费、接续替换/切换、失败停止与续流重试。
- `listen_tracker.go`：原前端计次规则移植与冻结的待提交参数。

修改文件：`service.go`、`hub.go`、`render_runtime.go`、`commands.go`、`transfer.go`。既有 Renderer / RenderSession / Cursor / PlayRecorder 端口签名未改。

## HTTP 公开接点

```go
ClaimOutput(ctx context.Context, userID, deviceID string, input ClaimOutputInput) (ClaimOutputResult, error)
ReportOutput(ctx context.Context, userID, sessionID string, input ReportOutputInput) (OutputResult, error)
ReleaseOutput(ctx context.Context, userID, sessionID string, input ReleaseOutputInput) error
TakeStream(ctx context.Context, userID, zoneID string, seq int64) (StreamBody, error)
SubscribeOutput(ctx context.Context, userID, sessionID string) (*Subscription, error)
Done() <-chan struct{}
Now() time.Time
AfterFunc(time.Duration, func()) Timer
```

DTO 完整字段在 `output_types.go`，JSON 使用合同原名。`StreamBody` 是 `{Body io.Reader, MimeType string}`。
`Subscription.Events` 第一条已经是 `snapshot`，HTTP 不应再单独调用 Snapshot；`Subscription.Done` 在溢出、主动 Close 或 Hub.Close 时立即关闭，Close 幂等。
新增错误为 `ErrUnsupportedProtocol`、`ErrOutputSuperseded`、`ErrOutputUnknown`、`ErrStreamGone`；其他用户已知会话返回 `ErrNotFound`。

## 状态、时序与注入点

- 复用 `Options.Now/AfterFunc/NewID/Random`。扫描、重建、暂停关闭和续流重试均使用注入计时器；初次加载仍是 B2 的暂停、离线与 `offlinePaused`。
- 输出会话身份索引使用独立互斥；索引中的归属不变，可变状态受区锁保护。认领事务提交后才取代旧会话。旧会话分为 superseded 与 released；重启后的索引为空。
- 当前与接续流各自保存单次领取标记，代次 + 具体句柄/seq 同时阻挡旧事件和旧 Cursor。接续流预载只更新自身分段，播放头事件须等客户端报告接续流 playing 后才应用。
- 每区 `dispatch` 仍保证广播顺序。快照订阅持用户注册表写锁，并按区 ID 顺序持全部 dispatch 与区锁，先排完已有增量，再注册含快照的订阅，避免快照之后混入旧增量。
- SSE 连接数用原子计数；溢出直接解除保活，不等待 handler 排空缓冲。只有真实 output 存在的区才参与租约扫描，不改变 B2 `SetOutputState` 的测试用法。
- 在线条件为连接数非零或距回报不超过 45 秒；释放立即离线并保存释放位置。takeover 窗口 10 秒；离线同会话的 playing 回报可依据旧分段恢复，控制命令和转移仍撤销恢复资格。
- 渲染非时间事件事务失败时，消费者不越过该事件，释放区锁后按注入时钟一秒重试；关闭/代次变化使旧重试失效。时间位置正常最多十秒写一次；状态命令、换曲和开流仍先提交对应状态。
- `track_started` 用 `queue.AdvanceTo` 消费实际条目，不重新抽随机数。格式边界可以先于播放头到达（候选不要求紧跟当前可听条目），后一次关闭并替换之前的接续会话。
- 续流失败最多三次后暂停；暂时无法 Open 时以一秒计时重试。接续失败重试使用独立计时器，不阻塞旧流事件；只有实际换上恢复目标的 playing/time 才清失败累计。
- 所有渲染消费者受服务生命周期管理，Service.Close 关闭会话并等待消费者结束。

## 计次

只读 `git show HEAD:frontend/src/features/player/engine/listen-tracker.ts` 移植，未恢复/修改已被 F2 删除的文件。
门槛 `min(D/2, 240)`，未知时长 240 秒；仅 playing 累加向前且不超过墙钟差加一秒的增量。停止、清空、自然播完结束访问；切歌/循环/转移目标新访问；暂停/换流/定位/接手延续累计但清基准。
达到门槛第一次尝试即冻结 `pendingSeconds`，后续 `PlayRecorder.Record` 失败重试保持同 UUID、同曲目、同秒数；成功后一次 `play_recorded`。用户 ID 始终是播放区所属用户，不导入 listening。

## 与合同的具体化及待核对点

- 输出身份（包括已取代/已释放的身份）留在当前进程内，以区分同用户的 superseded/unknown 与跨用户 not_found；进程重启统一 unknown。当前没有对长期活跃设备的旧身份索引另设数量上限。
- 非时间事件的数据库失败重试间隔具体化为一秒；预载异常提前产生播放头事件最多暂存 64 条，超出后用原通道背压，不无限增加内存。
- 访问累计仍为内存状态，重启后同条目重新建立基准；不增加 B1 表列，不声称可跨进程保留原幂等 UUID 或未提交累计。
- 渲染器仍须遵守端口约束：Open 不同步回调 Cursor；Close/Observe/NextChanged 不阻塞；Close 最终关闭 Events 并使 Body 结束。
- 请独立检查重点覆盖：快照/增量并发，接续替换后旧事件/旧 Cursor，暂停与过期重试，三次续流与五次曲目失败边界，事务失败不改内存，播放记录结果不明后的相同参数重试。

## 实际验证

只执行了目标文件 `gofmt -w`（最后一次全部成功）和只读源码/差异检查，没有执行 build、vet 或测试。主会话统一运行用户限定命令；未运行全量测试是用户明确要求最小范围且实现/测试分工独立。


## 独立复核后的补充（以最终源码及 implementation-notes 为准）

- 增加 `dto_clone.go`、`next_rebuild.go`；并修改 `devices.go` 的并发读取、`types.go` 的可空格式、`ports.go` 的过时注释。
- 深复制返回投影；广播在区锁外、dispatch 锁内；修复位置锚点、过期回报、暂停关闭期限、接续事件退出与三次真正续流失败的边界。
- 输出端分段恢复逐段消费 AdvanceTo，正确保留历史/随机袋；预载 B 的 rebuild_needed 只重建 B，B 提前宣布 C 时先记目标、B 晋升后才开 C。
- 根据用户后续裁决，首曲打不开的空会话输出 format:null，保留协商 quality/MIME；空流 ended/stream_lost 不提前关闭会话，等待失败/边界事件，接续首次 playing 后才晋升。
- 当前已知缺口：跨重启 visit UUID/累计/recorded 未持久化，不保证同次访问跨重启仍只记一次；旧输出身份索引生命周期未定义。未擅自扩表或增设淘汰上限。
