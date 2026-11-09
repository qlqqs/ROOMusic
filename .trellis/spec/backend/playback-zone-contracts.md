# 播放区控制面、实时与输出端合同（B2/B3）

## 1. 范围与所有权

`zones.Service` 拥有设备与播放区的内存工作副本、命令、事务、渲染会话生命周期、内部 hub 与设备清理。
`zones/httpapi` 只做来源校验、鉴权、解码和错误映射；`app.New` 是装配入口；进程在关闭数据库前经 `App.WaitForZones(ctx)` 等待生命周期回调完成。
B3 增加 SSE、输出端认领/回报/释放、渲染流 HTTP、会话事件应用与服务端计次；第 8 节记录其接口和验证接点。
运行时只支持**单进程**，不可让多个应用实例同时管理同一数据库中的播放区。
存储与队列原语见[播放区数据合同](./zones-data-contracts.md)，协议定义见[父任务合同](../../tasks/11-08-server-playback-zones/design-contract.md)。

## 2. 入口与端口

```go
func NewService(db *sql.DB, options Options) *Service
func (s *Service) Close()
func (s *Service) Snapshot(ctx context.Context, userID string) (Snapshot, error)
func (s *Service) ReadQueue(ctx context.Context, userID, zoneID string) (QueueResult, error)
func (s *Service) RegisterDevice(ctx context.Context, userID string, input RegisterDeviceInput) (RegisterDeviceResult, error)
func (s *Service) RenameDevice(ctx context.Context, userID, deviceID, name string) (DeviceResult, error)
func (s *Service) DeleteDevice(ctx context.Context, userID, deviceID string) error
func (s *Service) SetVolume(ctx context.Context, userID, deviceID string, volume float64, muted bool) (DeviceResult, error)
func (s *Service) Command(ctx context.Context, userID, zoneID string, command Command) (CommandResult, error)
```

`Options` 注入结构化 `Logger`、`Renderer`、`PlayRecorder`、`Now func() time.Time`、`AfterFunc func(time.Duration, func()) Timer`、`Random func() float64`、`NewID func() string`；`Timer` 只需 `Stop() bool`。
渲染端口的完整、权威签名位于 `backend/internal/zones/ports.go`，能力结构沿用 `store.go` 中的 `Capabilities`。
`Renderer.Open` 接收应用生命周期上下文，不得使用会随 HTTP 响应结束而取消的请求上下文。

- `RenderSession.Observe`、`NextChanged`、`Close` 不阻塞，允许在播放区锁内调用；`Close` 幂等。
- `Cursor.Next(afterEntryID)` 会取播放区锁，渲染器须在自己的协程中按渲染顺序预取，不可从 `Open` 同步回调它；同轮回查复用预选，循环前沿重新进入旧条目时开始新轮，不能跨轮重试陈旧 afterEntryID。
- `Entry.AlbumContext` 只在非随机模式、列表相邻条目有同发行时为真，不按同组猜发行。
- `Event` 的 `time/track_started/track_failed/stream_ended` 与带分段的 `signal` 到播放头才派发；`format_boundary/rebuild_needed` 与实时指标 `signal` 立即派发。起始条目的 `track_started` 由 Service 产生。
- `PlayRecorder.Record(ctx, userID, idempotencyKey, trackID, playedSeconds) error` 由 app 的 `zonePlayRecorder` 适配 `listening.Service.Record`，不让 zones 导入 listening。幂等键由服务端生成，首次提交后的重试固定键、曲目与秒数。

## 3. HTTP 与状态合同

前缀 `/api/v1/me/playback`，以下七个控制路由来自 B2；B3 的五个路由见第 8 节：

| 方法 | 路径 | 成功 |
| --- | --- | --- |
| GET | 空路径 | `200 Snapshot` |
| GET | `/zones/{zone_id}/queue` | `200 {zone_id,queue_revision,entries}` |
| POST | `/devices` | `201 {server_time,device,zone}` |
| PATCH | `/devices/{device_id}` | `200 {server_time,device}` |
| DELETE | `/devices/{device_id}` | `204` |
| PUT | `/devices/{device_id}/volume` | `200 {server_time,device}` |
| POST | `/zones/{zone_id}/commands` | `200 {server_time,zone,queue_change}`；转移增加 `target_zone`，导入增加 `imported` |

DTO 字段与命令参数遵循父合同 §2–§3：空集合输出 `[]`，nullable 字段输出 `null`；时间统一 UTC、毫秒精度。
JSON 只接受单个对象，完整请求不超过 1 MiB；HTTP 负责类型、必填/null 与未知键，Service 在加载用户和检查归属前校验字段取值。
所有访问只用 Guard 给出的用户 ID，不允许管理员越权访问。

- 同名登记追加“ 2”“ 3”；名称按字符计 1–64，追加后缀时截短基础名称以满足数据库约束。
- 新建与重启加载的设备离线，播放区暂停或空闲。在线状态只在内存中，不能根据 `last_seen_at` 猜在线。
- 队列显示信息由 `catalog.Store.LoadTrackSummaries` 每批至多 1,000 个不同曲目取得；不把显示元数据写入队列。
- 每个播放区独立互斥，转移按 zone ID 升序锁两个区；条目与状态同事务写入，成功后才换内存与发布。
- 转移保留源条目 ID、当前项、模式、位置、历史与随机袋；目标保留自己的播放设置与音量；源清空，目标直接播放。
- 导入只在队列为空且从未导入时执行；旧字符串条目 ID 映射成服务端 UUID，丢掉查不到的曲目并核对当前项与历史；当前项丢失从其后保留项开始，位置清零；没有后继或原本 current 为 null 时保留无当前项，不回到队首。
- `seek` 与正在放的会话上的 `set_settings` 共用 300 ms 去抖；暂停 5 秒后仍暂停才关闭会话；停止、清空、转移与切歌取消旧定时器。
- 所有已接受控制命令撤销 `offlinePaused` 自动恢复标记；起播清故障与连续失败计数。重复暂停不延后既有五秒期限，无操作不广播。
- 队列/模式变动通知 `NextChanged()`，是否需要重建由渲染会话发 `rebuild_needed` 决定，经同一 300 ms 定时器去抖。
- Headroom 默认 `{enabled:false,db:-3,clip_indicator:false}`；登记与 `set_settings` 接受完整 `settings.headroom` 对象，`db` 须有限、在 −30…0 内且按 0.1 步进，否则 `400 invalid_input`。省略整项保留原值；提交后经 0044 的 JSONB 列持久化，DTO 与渲染设置返回实际值；改动复用既有 300 ms 去抖，无变化不重建。存储坏值的默认回退见播放区数据合同。
- hub 按用户扇出，订阅缓冲 64 条；满则关闭该订阅，不阻塞命令。队列事件先于状态事件，B3 用订阅接口写 SSE。
- 每小时清理超过 30 天未上线且当前离线的设备，删除前持锁重新核对；覆盖尚未加载到内存的用户。

## 4. 校验与错误矩阵

检查顺序：来源 → 会话 → 路径 UUID → JSON → 字段 → 归属 → 状态冲突 → 写入。

| 情况 | HTTP/code |
| --- | --- |
| 写请求缺来源或来源不可信 | `403 origin_required/origin_forbidden` |
| 未鉴权 | `401 unauthorized` |
| 路径不是 UUID | `400 invalid_id` |
| JSON 格式、类型、大小不符 | `400 invalid_json` |
| 字段取值、必填、未知命令/设置键、转移给自己 | `400 invalid_input` |
| 不存在或不属于本人 | `404 not_found` |
| 加入命令包含不存在曲目 | `404 track_not_found`（整批拒绝） |
| 队列加入超过 10,000 | `409 queue_full` |
| 起播区或转移目标离线 | `409 zone_offline` |
| 删除在线设备 | `409 device_online` |
| 给 fixed 设备改音量 | `409 volume_fixed` |
| 未装配 Renderer 的起播操作 | `503 renderer_unavailable`，不写入新播放状态 |
| 数据库失败 | `503 database_unavailable`，保留内部错误链，不返回 SQL |

已装配渲染器但 `Open` 失败不是数据库错误：接受命令后保留目标项，置 `fault:transient`、意图暂停，供 `retry` 重试；协商失败置 `no_stream_format`。

## 5. 正常、基线与错误案例

正常：暂停中的源区转移到在线目标，保留位置与条目身份，目标用自己的设置从该位置开流；两个队列修订号都增加。
基线：不存在的条目操作、非 transient 的 retry、已导入后的 import 都不应制造新播放或再次导入。
错误：先关闭旧流、改变当前项，再尝试写库；数据库失败后用户会看到不可恢复的部分状态。必须先提交计算出的状态。

## 6. 验证与测试交接

B2 实现阶段不新增测试、不启动数据库；只执行用户指定的 build、局部 vet 与局部测试筛选。
独立测试 agent 应覆盖父任务 implement.md B2 第 6 步，另关注：

- 失败事务不替换内存、不广播、不关闭原会话；转移两侧锁顺序与写入回滚。
- 首次导入全部丢弃、旧字符串 ID 重映射、当前项丢弃选后继、历史筛选、第二次导入无变化。
- 连续 seek/settings 只重建一次，暂停/恢复及旧定时器取消，时钟推进不依赖真实 sleep。
- 上线状态只能经内存接点设定；能力 fixed、离线拒绝、其他用户 404，以及未装配渲染器 503。
- 随机 Cursor 的预选稳定性与多曲预取；B3 用 `AdvanceTo` 消费预选，不能重新抽签。
- 64 条订阅溢出关闭、隔离、队列先于状态；空 replace 的 entries 必须为数组。
- 1,001/10,000 条摘要批读，艺人/发行元数据刷新，500 条内联与 reload 边界。
- `zonestest.NewRenderer` 仅用于测试/开发：具体 WAV 格式、静音 Body、脚本 `Emit`、确定性 `Advance` 或 `Realtime` + `Observe`，以及幂等 Close。

消息样本由 B3/独立测试 agent 经真实 handler 生成，不手写样本，不把当前静态检查当作数据库集成验收。

## 7. 错误与正确做法

错误：控制面直接导入 DSP/listening；生产装配静音假渲染器掩盖真实 Renderer 缺失；HTTP 取消时越权关闭渲染会话。
正确：端口定义在消费方 zones，app 装配真实实现；未装配时明确 503，假渲染器隔离在 `zones/zonestest`；B3 复用现有锁、状态与发布接口。


## 8. B3 输出端与实时接口

### 8.1 签名与生命周期

```go
func (s *Service) ClaimOutput(ctx context.Context, userID, deviceID string, input ClaimOutputInput) (ClaimOutputResult, error)
func (s *Service) ReportOutput(ctx context.Context, userID, sessionID string, input ReportOutputInput) (OutputResult, error)
func (s *Service) ReleaseOutput(ctx context.Context, userID, sessionID string, input ReleaseOutputInput) error
func (s *Service) TakeStream(ctx context.Context, userID, zoneID string, seq int64) (StreamBody, error)
func (s *Service) SubscribeOutput(ctx context.Context, userID, sessionID string) (*Subscription, error)
func (s *Service) Done() <-chan struct{}
func (s *Service) Now() time.Time
func (s *Service) AfterFunc(time.Duration, func()) Timer
```

`StreamBody` 只包含 `Body io.Reader` 与 `MimeType string`，不把 `RenderSession.Close` 交给 HTTP。
`SubscribeOutput` 的 `Events` 首条已原子入队 `snapshot`，handler 不再另取快照；`Close` 解除输出端在线关联。
`Subscription.Done` 在取消或缓冲溢出时关闭，HTTP 不排空剩余旧事件。`Service.Done` 是所有长响应的应用生命周期边界。

### 8.2 请求、响应与流合同

以下路径均省略 `/api/v1/me/playback`，完整 DTO 的字段、nullable 及能力形状以父合同 §2、§4 为准：

| 方法/路径 | 请求 | 成功 |
| --- | --- | --- |
| `POST /devices/{device_id}/output` | `{protocol:1, capabilities, takeover:boolean, playing:{entry_id,position_seconds}?}` | `200 {server_time,protocol:1,session_id,device,zone}` |
| `POST /outputs/{session_id}/reports` | `{kind,status,stream_seq:integer?,stream_seconds:number,fault:string?}` | `200 {server_time,device,zone}` |
| `POST /outputs/{session_id}/release` | `{stream_seq:integer?,stream_seconds:number}` | `204`；未知会话也成功 |
| `GET /zones/{zone_id}/streams/{seq}` | 无 | `200` 原始音频 |
| `GET /events[?output_session=UUID]` | 无 | SSE，首条 `snapshot` |

- `kind` 为 `status/ended/fault`；状态为 `loading/playing/paused/buffering`；故障为 `autoplay_blocked/stream_lost`，非故障回报的 `fault` 为 null。
- 两条长响应路由的 HEAD 在鉴权后返回 `405 method_not_allowed`、`Allow: GET`，不订阅、不取流；防止 Go 的 GET 隐式匹配 HEAD 消耗单次流。
- Flush 由 `http.NewResponseController` 进入已有 `recordingResponseWriter.FlushError`；该方法继续委托底层 ResponseController，不能调用无返回值的 Flush 吞掉错误。
- 首曲打不开时允许 `Open` 返回零值 Format、Body EOF 的会话；此流投影 `format: null`，不伪造零采样率格式。异步 `track_failed/format_boundary` 仍需消费，空流的 ended 不能提前关闭会话吞掉接续；前端 decoder 必须接受 null。`StreamFormat` 未来新增的内部设备上限字段不参与对外投影。
- 流只允许当前/接续且各取一次，忽略 Range；原样读取 Body，每次最多 32 KiB，读到即写出并 Flush。
  头为实际 MIME、`Accept-Ranges: none`、`Cache-Control: no-store`、`X-Content-Type-Options: nosniff`、`X-Accel-Buffering: no`，无 Content-Length。
- 客户端取消使 handler 返回，不关闭会话；唯一 reader 如阻塞在 Read，等会话 Close 后 EOF 结束，不加额外回收定时器。
- SSE 为 `event: 类型\ndata: 单行JSON\n\n`，不带 id；类型为 `snapshot/zone/queue/device/device_removed`。
  每 15 秒 `: ping\n\n`，每 60 秒重新认证，失败结束流而不是往流里追加 JSON 错误信封。
- 当前有效 `output_session` 的订阅维持在线；缺省、无效、其他用户的会话参数按普通控制流处理，不泄漏他人会话。
- 订阅容量 64，满即断开；队列变化先于状态变化，重连靠全量快照而非重放。

### 8.3 校验与错误

沿第 4 节检查顺序；新增错误：

| 条件 | HTTP/code |
| --- | --- |
| 协议版本不支持 | `400 unsupported_protocol` |
| 输出会话已被本人同设备的新认领取代 | `409 output_superseded` |
| 回报的输出会话不存在、已释放或服务重启丢失 | `404 output_unknown` |
| 非本人的已知输出会话、设备或播放区 | `404 not_found` |
| seq 已过期、非当前/接续或已取用 | `410 stream_gone` |

报告旧 seq 只续租并返回最新状态，不反向覆盖当前流。释放旧会话不能停掉新会话。

### 8.4 在线、会话事件与计次

- 在线租约 45 秒，每 5 秒扫描；释放立即离线。离线时保留位置、关闭会话，原意图播放则记 `offline_paused`。
- 同会话 `playing` 回报可恢复离线暂停；释放后 10 秒内 `takeover` 可接手；重启首次加载标记离线暂停，认领 `playing` 在条目仍存在时续播。控制命令清掉自动恢复标记。
- 事件消费者按会话身份/代次拒绝陈旧事件；`track_started` 用 `queue.AdvanceTo` 消费既有预选，不重新随机；起始条目公告仍由开流产生。
- `time` 更新位置、计次和连续失败计数，位置最多每 10 秒写库，不逐条广播；`track_failed` 连续 5 次停止。
- 当前流的 `format_boundary` 开接续流，重复事件替换旧接续流；预载 B 提前宣布 C 时仅保存最新后继目标，B 晋升后才开 C（协议只有当前/接续两槽，不能跳过 B）。预载的 `rebuild_needed` 立即安排该会话自身的 300 ms 重建，保持当前出声流；signal 存在所属会话，晋升时切换。`stream_ended` 在无接续时暂停并回到 0；`rebuild_needed` 经重建去抖；`signal` 按事件顺序原样覆盖，并同步 segments。
- `stream_lost` 依 segments 将流内位置映射回曲内位置续流，延续访问；连续 3 次失败暂停为 transient。
- 计次门槛为已知正时长的一半与 240 秒取较小值；未知时长沿原 ListenTracker 使用 240 秒。只累计播放中的正前进量，超过墙钟流逝 + 1 秒视为跳变；新访问最多记录一次，续流/重建重置采样基准而非累计值。
- 记录失败在后续 time 重试，冻结首次提交秒数防止 listening 幂等冲突；成功公告 `play_recorded`。
- 已知设计缺口：visit UUID、累计及 recorded 未持久化，重启后的续播只能重新建立基准，**不能保证跨重启的同次访问不重计**；现有表未定义这些列，本轮未擅自扩表。旧输出身份索引没有淘汰规则，保留到进程结束，以维持旧身份的 409/404 语义。

### 8.5 案例、验证与禁止事项

正常：输出端取流，回报播放头，事件自然推进曲目并计次；重复回报不重复计次。
基线：过期 seq 回报只续租、第二个取流请求 410、未知 release 204、未关联输出的 SSE 不使设备在线。
错误：HTTP 断开直接 Close session；SSE 溢出仍排空积压；seek 的位置跳变计入播放时长；重试同一幂等键却增大秒数。
正确：生命周期由 zones 管理，HTTP 只退出；溢出用 Done 结束；访问重建重置基准；固定待记录载荷重试。

第 9 步独立测试需覆盖：原子快照/增量/用户隔离/溢出与关停；45 秒租约、10 秒接手、5 秒暂停关闭；重启与取代；单次取流/32 KiB及时Flush/取消；全部会话事件与连续失败门槛；Visit 的暂停/seek/续流/循环/转移以及计次失败重试。
时钟、定时器与 ID 用 `zones.Options.Now/AfterFunc/NewID` 注入；HTTP 心跳与复核复用该时钟，不真实等待。
API 样本由独立测试经真实 handler 生成到 `backend/internal/app/testdata/api-samples/playback-zones/`，生成约定见 B3 `implementation-notes.md`；独立测试已新增 `TestPostgreSQLZonesAPIResponseSamples`，结果与本轮限定范围见 [B3 测试记录](../../tasks/11-09-zones-b3-realtime-output/test-notes.md)。

测试接点注意：`zonestest.Session.Emit` 的确认可能晚于终结事件触发的 `Close`；仅终结事件允许 `zonestest.ErrClosed`，且仍须等待相应状态公告作为成功证据。不能把全部 Emit 错误统一忽略，也不能仅凭 Emit 返回成功认定事件已应用。
