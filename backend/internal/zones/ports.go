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
	Events() <-chan Event // 带缓冲；由 zones 持续读取直到通道关闭。
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

// Event.Kind 的完整协议由 zones 消费，不同类型的派发时机如下。
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

// PlayRecorder 是计次消费方端口，由 app 装配 listening 适配器。
type PlayRecorder interface {
	Record(ctx context.Context, userID, idempotencyKey, trackID string, playedSeconds float64) error
}
