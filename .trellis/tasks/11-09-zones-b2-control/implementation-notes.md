# B2 实现与测试交接

## 范围

只交付父任务 implement.md B2 第 1–5 步。未新增测试、未启动数据库、未提交 git、未修改 DSP；任务保持 in_progress，独立测试 agent 的第 6 步尚未完成。

## 改动文件

- `backend/internal/zones/`：新增 `ports.go`、`types.go`、`service.go`、`hub.go`、`validation.go`、`store_devices.go`、`devices.go`、`commands.go`、`import.go`、`transfer.go`、`render_runtime.go`。B1 原文件和 queue 未改。
- `backend/internal/zones/httpapi/`：新增 `handlers.go`、`input.go`。
- `backend/internal/zones/zonestest/`：新增 `renderer.go`、`session.go`、`wav.go`。
- `backend/internal/app/app.go`、`backend/cmd/roomusic/main.go`：装配控制接口与等待停机。
- `backend/internal/app/dependency_rules_test.go`：仅增加两个真实新包的依赖规则，无新测试/路由期望/API 样本修改。
- `.trellis/spec/backend/playback-zone-contracts.md`（新增）、`index.md`、`directory-structure.md`，以及本交接文件。
- 工作树中另有并行 F2 的 frontend 修改，未操作、未回退、未纳入本次文件清单。

## 渲染端口最终 Go 签名

权威源码：`backend/internal/zones/ports.go`；能力结构复用 B1 的 `store.go`。算法线实现以下接口即可，无需反向依赖 zones/httpapi。

```go
package zones

import (
	"context"
	"io"
	"time"
)

// Renderer 由算法线实现，控制面只负责协商与会话生命周期。
type Renderer interface {
	Negotiate(caps Capabilities, quality string) (StreamFormat, error)
	Open(ctx context.Context, spec RenderSpec) (RenderSession, error)
}

type StreamFormat struct {
	Codec, MimeType, Quality       string
	SampleRate, BitDepth, Channels int
}

type RenderSpec struct {
	UserID, ZoneID string
	Seq            int64
	Start          Entry
	StartSeconds   float64
	Settings       ZoneSettings
	Format         StreamFormat
	Cursor         Cursor
}

type ZoneSettings struct {
	Leveling         string   `json:"leveling"`
	Gapless          bool     `json:"gapless"`
	Crossfade        bool     `json:"crossfade"`
	CrossfadeSeconds int      `json:"crossfade_seconds"`
	Quality          string   `json:"quality"`
	Headroom         Headroom `json:"headroom"`
}

type Headroom struct {
	Enabled       bool    `json:"enabled"`
	DB            float64 `json:"db"`
	ClipIndicator bool    `json:"clip_indicator"`
}

type Entry struct {
	EntryID, TrackID string
	AlbumContext     bool
}

// Cursor 由会话自己的协程调用；Next 会取得播放区锁，不能在 Open 中同步回调。
// 自然前进跳过失败项，单曲循环返回自身，随机模式复用本轮已准备的结果。
type Cursor interface {
	Next(afterEntryID string) (Entry, bool)
}

// RenderSession 的 Observe、NextChanged、Close 不阻塞，允许在播放区锁内调用。
// 这些方法不得等待正在调用 Cursor.Next 的协程，以免与播放区锁互相等待。
type RenderSession interface {
	MimeType() string
	Format() StreamFormat // Open 返回后可取得首曲确定的具体格式。
	Body() io.Reader      // 只能读取一次；Close 后读取返回 EOF。
	Observe(sample PlayheadSample)
	NextChanged()         // 预选失效；未发出字节时重选，否则发 rebuild_needed。
	Events() <-chan Event // 带缓冲；由 zones 持续读取直到通道关闭（B3 接入）。
	Close()               // 可重复调用，同时结束 Body 和 Events。
}

// PlayheadSample 的 At 是服务端收到回报的时刻，不信任客户端墙钟。
type PlayheadSample struct {
	StreamSeq     int64
	StreamSeconds float64
	At            time.Time
	Playing       bool
}

// Segment 只携带目录身份；控制面经批量摘要投影给客户端。
type Segment struct {
	EntryID, TrackID                      string
	StreamStartSeconds, TrackStartSeconds float64
}

// Event.Kind 的完整协议由 B3 消费，不同类型的派发时机如下。
// 到达播放头才派发：
//   - time：EntryID 对应曲目，Seconds 为曲内秒数，At 为服务端采样时刻。
//   - track_started：EntryID 为新条目，Transition 为 gapless、crossfade 或 gap；
//     起始条目不发此事件，由 zones 开流时产生公告。
//   - track_failed：EntryID 为失败条目，Reason 为 PlaybackFailureReason。
//   - stream_ended：当前渲染流播放结束。
//   - signal（带 Segments）：同步更新已渲染且尚未播完的分段与信号路径。
//
// 立即派发：
//   - format_boundary：NextEntryID 为接续条目，Reason 为 format 或 not_ready；
//     可以重复发送，zones 以最新一条替换接续流。
//   - rebuild_needed：已发出的字节不能撤回，需要 zones 去抖重建。
//   - signal（不带 Segments）：削波与处理速度等实时信号更新。
//
// StreamSeconds 为流内秒数；Segments 将流内位置映射到条目与曲内位置。
type Event struct {
	Kind          string
	EntryID       string
	Seconds       float64
	StreamSeconds float64
	At            time.Time
	Transition    string
	Reason        string
	NextEntryID   string
	Segments      []Segment
	Signal        any
}

// PlayRecorder 仅定义消费方端口；B3 再接入计次与 listening 适配。
type PlayRecorder interface {
	Record(ctx context.Context, userID, idempotencyKey, trackID string, playedSeconds float64) error
}

// StreamFormatCapability 是设备声明的一种可解码流格式。
type StreamFormatCapability struct {
	Codec         string `json:"codec"`
	MIMEType      string `json:"mime_type"`
	Lossless      bool   `json:"lossless"`
	MaxSampleRate *int   `json:"max_sample_rate"`
	MaxBitDepth   *int   `json:"max_bit_depth"`
}

// Capabilities 保存设备最近一次认领时声明的能力。
type Capabilities struct {
	StreamFormats []StreamFormatCapability `json:"stream_formats"`
	MaxChannels   int                      `json:"max_channels"`
	DSD           string                   `json:"dsd"`
	Exclusive     bool                     `json:"exclusive"`
	Volume        string                   `json:"volume"`
}

```

## 与设计的具体化与阶段限制

- 为未装配渲染器的 503 明确错误码 `renderer_unavailable`；归属和离线冲突仍按合同先检查。
- Headroom 不建列，固定默认关、−3 dB、削波指示关；B2 仅接受显式默认值，非默认值返回 invalid_input，待算法线接入持久化后开放，不做仅内存的假保存。
- Cursor 按渲染顺序预取，同轮回查复用结果；条目 ID 在循环中重现时视为新轮前沿。当前端口不带访问代次，不支持跨轮重试陈旧的 afterEntryID，B3 应用事件时仍须用会话代次栅栏。
- 64 字符名称碰撞时截短基础名称再追加序号，避免追加后超过既有数据库约束。
- `set_mode` 走 `NextChanged`，不能在 B2 提前消费 rebuild_needed；该事件的应用与重建由 B3 接上。
- 静音假渲染器只用于测试与开发装配，不模拟真实 DSP/交叉混音或格式边界；这些可通过 Emit 脚本提供。WAV 的读取不充当播放时钟。

## B3 接入点

- `Service.Subscribe`/内部 `Hub.Publish`：增加 SSE 的快照、心跳、凭据复核与输出端订阅状态，不重写命令广播。
- `SetOutputState(ctx,userID,deviceID,OutputState)`：仅提供内存在线与能力更新接点；认领、租约、输出会话栅栏仍由 B3 定义。测试可通过它让设备在线，不伪造 last_seen_at。
- 所有已接受控制命令清掉 offlinePaused；起播清故障并将 consecutiveFailures 置零，失败事件的递增和阈值处理留给 B3。
- `openSession`、`zoneRuntime.session/nextSession`、`generation`、`stream/nextStream`：在这里持续消费 Events、检查会话代次、原区锁下应用事件；自然换曲用 queue.AdvanceTo，不再抽随机数。
- `scheduleRebuild`/`schedulePause`：复用计时器与版本栅栏；输出端回报喂 session.Observe，不能持区锁调用 Cursor.Next。
- `openSession(..., newVisit)` 与 stop/转移分支是访问生命周期的接入位置，目前没有 visit 计次状态。
- `PlayRecorder` 端口已定义，但未写计次；B3 的 app 适配器负责把 listening.Record 的返回值转换为 error。
- app 当前只装配 nil Renderer；算法线到位后在同一组合根接真实 Renderer，不改控制面协议。

## 给测试 agent

- 在外部 `zones_test` 中导入 `zones/zonestest`，用真实隔离数据库、可控 Now/AfterFunc/Random/NewID 与 SetOutputState；内部 zones 测试不能再导入反向依赖 zones 的 zonestest。
- `zonestest.Renderer.Sessions()` 检查 Open 次数、Spec、Format、Close；Emit 脚本发事件，Advance 按时长前进（每 200 ms time、后续 track_started、尾部 stream_ended），Realtime 按 Observe.playing 自动前进。测试须消费 Events，通道有界且不会丢事件。
- 优先覆盖全部命令、事务失败不改变内存/广播/旧会话、双向转移并发、导入丢项与一次性、连续 seek/settings、暂停 5 秒、随机预选、同名设备与清理、用户隔离和错误检查顺序。
- 加入 10001 项应 queue_full；play_tracks 的参数上限仍是 10000。测试 1000/1001 摘要分批与 500/501 replace/reload。
- HTTP 通过真实 Guard/handlers 核对必填/null/类型/未知键；B3 再生成真实 handler 样本，不能手写 DTO 金样。

## 自查

实际执行（不包含数据库与全量测试）：

```sh
cd backend && go build ./... && go vet ./internal/zones/... ./internal/app && go test ./internal/zones/... ./internal/app -run 'TestPackageDependencyDirections|^TestZones' -count=1
```

初次与最终限定命令均通过。中途有一次在仓库根目录误执行 go build，因不在 Go module 中退出，随后在 backend 目录重跑通过。审核 agent 另执行并通过了以下子集静态命令（超出原定的精确命令清单，未运行额外测试）：

```sh
cd backend
go vet ./internal/zones/httpapi ./internal/zones/zonestest ./internal/app
go build ./internal/zones/httpapi ./internal/zones/zonestest ./internal/app
```

当前 zones 根包无匹配测试，httpapi/zonestest 无测试文件，queue 与 app 的匹配项通过。这不是 B2 数据库集成验收。


## 审核收敛

修正了随机预选重复抽签、转移期间快照不一致、无操作广播、重复暂停重置关闭期限、命令切态前位置锚点、导入无后继错误回到队首、HTTP/核心 Headroom 默认值不一致，以及停机未等待 Service 关闭。
返回 DTO 的 revision/queue_revision 与消息先后、跨用户与事务失败的动态证据仍须由独立测试 agent 补齐。
