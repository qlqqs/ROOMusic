# 播放区控制面合同（B2）

## 1. 范围与所有权

`zones.Service` 拥有设备与播放区的内存工作副本、命令、事务、渲染会话生命周期、内部 hub 与设备清理。
`zones/httpapi` 只做来源校验、鉴权、解码和错误映射；`app.New` 是装配入口；进程在关闭数据库前经 `App.WaitForZones(ctx)` 等待生命周期回调完成。
本阶段不提供 SSE、输出端认领/回报/释放、渲染流 HTTP、会话事件应用或计次，这些归 B3。
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
- `PlayRecorder.Record(ctx, userID, idempotencyKey, trackID, playedSeconds) error` 只定义端口；B3 在 app 中适配 listening 的返回值，不让 zones 导入 listening。

## 3. HTTP 与状态合同

前缀 `/api/v1/me/playback`，只有七个 B2 路由：

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
- 队列/模式变动通知 `NextChanged()`，是否需要重建由渲染会话发 `rebuild_needed` 决定，B3 应用该事件。
- Headroom 本阶段只输出默认 `{enabled:false,db:-3,clip_indicator:false}`，不新增持久列；仅接受显式默认的 `settings.headroom`，非默认值返回 `400 invalid_input`，待算法线接入后开放，不能接受后静默丢弃。
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

错误：控制面直接导入 DSP/listening；生产装配静音假渲染器掩盖真实 Renderer 缺失；在本阶段加入 claim/SSE/计次实现。
正确：端口定义在消费方 zones，app 装配真实实现；未装配时明确 503，假渲染器隔离在 `zones/zonestest`；B3 复用现有锁、状态与发布接口。
