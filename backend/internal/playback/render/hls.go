package render

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/zones"
)

type hlsSegment struct {
	duration, end float64
	data          []byte
}

// hlsMedia 保留完整 EVENT 时间线，只释放过期分片的音频字节。
// 返回的媒体字节发布后不再修改，HTTP 读者可在锁外发送。
type hlsMedia struct {
	mu        sync.Mutex
	ctx       context.Context
	head      func() float64
	newTicker func(time.Duration) Ticker
	changed   chan struct{}
	init      []byte
	segments  []hlsSegment
	firstLive int
	end       float64
	ended     bool
	closed    bool
}

func newHLSMedia(ctx context.Context, head func() float64, newTicker func(time.Duration) Ticker) *hlsMedia {
	return &hlsMedia{ctx: ctx, head: head, newTicker: newTicker, changed: make(chan struct{})}
}

func (h *hlsMedia) notifyLocked() {
	close(h.changed)
	h.changed = make(chan struct{})
}

func (h *hlsMedia) goneLocked() bool { return h.closed || h.ctx.Err() != nil }

func (h *hlsMedia) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	h.init = nil
	h.segments = nil
	h.notifyLocked()
}

func (h *hlsMedia) finish() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.goneLocked() {
		h.ended = true
		h.notifyLocked()
	}
}

func (h *hlsMedia) prune() {
	// 不持媒体锁回调会话，避免与 Close、Observe 的锁顺序相反。
	threshold := h.head() - 30
	h.mu.Lock()
	defer h.mu.Unlock()
	for h.firstLive < len(h.segments) && h.segments[h.firstLive].end < threshold {
		h.segments[h.firstLive].data = nil
		h.firstLive++
	}
}

func (h *hlsMedia) Playlist(ctx context.Context) ([]byte, error) {
	timer := h.newTicker(10 * time.Second)
	defer timer.Stop()
	timedOut := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		h.mu.Lock()
		if h.goneLocked() {
			h.mu.Unlock()
			return nil, zones.ErrStreamGone
		}
		if h.ended || len(h.segments) >= 3 || timedOut {
			data := h.playlistLocked()
			h.mu.Unlock()
			return data, nil
		}
		changed := h.changed
		h.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-h.ctx.Done():
			return nil, zones.ErrStreamGone
		case <-changed:
		case <-timer.C():
			timedOut = true
		}
	}
}

func (h *hlsMedia) playlistLocked() []byte {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:4\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXT-X-START:TIME-OFFSET=0,PRECISE=YES\n#EXT-X-INDEPENDENT-SEGMENTS\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-MAP:URI=\"init.mp4\"\n")
	for n, segment := range h.segments {
		fmt.Fprintf(&b, "#EXTINF:%.3f,\n%d.m4s\n", segment.duration, n)
	}
	if h.ended {
		b.WriteString("#EXT-X-ENDLIST\n")
	}
	return []byte(b.String())
}

func (h *hlsMedia) Init() ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.goneLocked() {
		return nil, zones.ErrStreamGone
	}
	if h.init == nil {
		return nil, zones.ErrSegmentMissing
	}
	return h.init, nil
}

func (h *hlsMedia) Segment(n int64) ([]byte, error) {
	h.prune()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.goneLocked() {
		return nil, zones.ErrStreamGone
	}
	if n < 0 || n >= int64(len(h.segments)) || h.segments[n].data == nil {
		return nil, zones.ErrSegmentMissing
	}
	return h.segments[n].data, nil
}

// split 只消费编码器的单音轨 fMP4；完整的 moof/mdat 对才会对读取者可见。
// EOF 只表示切片完成，ENDLIST 必须等编码器成功退出后由 finish 发布。
func (h *hlsMedia) split(reader io.Reader) error {
	var init, fragment []byte
	var timing mp4Timing
	var duration float64
	initialized := false
	for {
		box, err := readMP4Box(reader)
		if err == io.EOF {
			if !initialized || fragment != nil {
				return fmt.Errorf("render: MP4 初始化或分片不完整")
			}
			return nil
		}
		if err != nil {
			return err
		}
		switch box.kind {
		case "ftyp":
			if initialized || init != nil {
				return fmt.Errorf("render: MP4 ftyp 顺序无效")
			}
			init = box.raw
		case "moov":
			if initialized || init == nil {
				return fmt.Errorf("render: MP4 moov 顺序无效")
			}
			timing, err = parseMP4Timing(box)
			if err != nil {
				return err
			}
			h.mu.Lock()
			if h.goneLocked() {
				h.mu.Unlock()
				return zones.ErrStreamGone
			}
			h.init = append(init, box.raw...)
			h.notifyLocked()
			h.mu.Unlock()
			initialized = true
			init = nil
		case "moof":
			if !initialized || fragment != nil {
				return fmt.Errorf("render: MP4 moof 顺序无效")
			}
			duration, err = fragmentSeconds(box, timing)
			if err != nil {
				return err
			}
			fragment = box.raw
		case "mdat":
			if fragment == nil {
				return fmt.Errorf("render: MP4 mdat 缺少 moof")
			}
			data := append(fragment, box.raw...)
			h.mu.Lock()
			if h.goneLocked() {
				h.mu.Unlock()
				return zones.ErrStreamGone
			}
			h.end += duration
			h.segments = append(h.segments, hlsSegment{duration: duration, end: h.end, data: data})
			h.notifyLocked()
			h.mu.Unlock()
			h.prune()
			fragment = nil
		}
	}
}

var _ zones.HLSMedia = (*hlsMedia)(nil)
