package render

import (
	"bytes"
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

func TestHLSSplitFrameDuration(t *testing.T) {
	ff := pinnedFFmpeg(t)
	const rate, channels, frames = 44100, 2, 9*44100 + 137
	pcm := make([]byte, frames*channels*2)
	encoded := ffmpegOutput(t, ff, pcm, "-f", "s16le", "-ar", "44100", "-ac", "2", "-i", "pipe:0",
		"-c:a", "alac", "-f", "mp4", "-movflags", "+empty_moov+default_base_moof+skip_trailer", "-frag_duration", "4000000", "-flush_packets", "1", "pipe:1")
	media := newHLSMedia(context.Background(), func() float64 { return 0 }, nil)
	if err := media.split(bytes.NewReader(encoded)); err != nil {
		t.Fatal(err)
	}
	if len(media.segments) < 3 {
		t.Fatalf("未产生多片输出：%d", len(media.segments))
	}
	var seconds float64
	for _, segment := range media.segments {
		seconds += segment.duration
	}
	if got := seconds * rate; math.Abs(got-frames) > 1e-6 {
		t.Fatalf("分片时长合计 = %.9f 帧，写入 %d 帧", got, frames)
	}
}

func TestHLSALACBitExact(t *testing.T) {
	ff := pinnedFFmpeg(t)
	tool := openTestTool(t, ff, ffmpeg.Options{})
	for _, bits := range []int{16, 24} {
		t.Run(fmt.Sprintf("%d位", bits), func(t *testing.T) {
			source := makeFLAC(t, ff, bits, 44100, 997, 9.125)
			r := newTestRenderer(t, tool, fixtureSource(map[string]audioFixture{"a": source}))
			s := openTestSession(t, r, renderSpec("alac"))
			select {
			case <-s.encoder.done:
			case <-time.After(testWait):
				t.Fatal("HLS 编码未结束")
			}
			media := s.HLS()
			if media == nil {
				t.Fatal("ALAC 会话没有 HLS 媒体")
			}
			playlist, err := media.Playlist(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(playlist), "#EXT-X-ENDLIST\n") {
				t.Fatalf("流结束缺少 ENDLIST：%s", playlist)
			}
			joined, err := media.Init()
			if err != nil {
				t.Fatal(err)
			}
			joined = bytes.Clone(joined)
			count := 0
			for _, line := range strings.Split(string(playlist), "\n") {
				if !strings.HasSuffix(line, ".m4s") {
					continue
				}
				fragment, err := media.Segment(int64(count))
				if err != nil {
					t.Fatal(err)
				}
				joined = append(joined, fragment...)
				count++
			}
			if count < 3 {
				t.Fatalf("未覆盖全部分片拼接：%d", count)
			}
			decoded := ffmpegOutput(t, ff, joined, "-i", "pipe:0", "-map", "0:a:0", "-c:a", "pcm_s32le", "-f", "s32le", "pipe:1")
			if got, want := md5.Sum(decoded), md5.Sum(source.pcm); got != want {
				t.Fatalf("PCM MD5 = %x，源 %x（字节 %d/%d）", got, want, len(decoded), len(source.pcm))
			}
		})
	}
}

// C 的通知确认请求已走到等待分支；只有测试显式投递 tick 才会超时。
type hlsPlaylistTicker struct {
	ticks   chan time.Time
	waiting chan struct{}
}

func (t *hlsPlaylistTicker) C() <-chan time.Time {
	select {
	case t.waiting <- struct{}{}:
	default:
	}
	return t.ticks
}
func (t *hlsPlaylistTicker) Stop() {}

func appendHLSTestSegment(h *hlsMedia) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.end += 4
	h.segments = append(h.segments, hlsSegment{duration: 4, end: h.end, data: []byte("分片")})
	h.notifyLocked()
}

func TestHLSPlaylistWait(t *testing.T) {
	for _, release := range []string{"第三片", "超时", "结束"} {
		t.Run(release, func(t *testing.T) {
			ticker := &hlsPlaylistTicker{ticks: make(chan time.Time), waiting: make(chan struct{}, 1)}
			h := newHLSMedia(context.Background(), func() float64 { return 0 }, func(d time.Duration) Ticker {
				if d != 10*time.Second {
					t.Errorf("列表等待上限 = %v", d)
				}
				return ticker
			})
			appendHLSTestSegment(h)
			appendHLSTestSegment(h)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type result struct {
				data []byte
				err  error
			}
			done := make(chan result, 1)
			go func() { data, err := h.Playlist(ctx); done <- result{data, err} }()
			select {
			case <-ticker.waiting:
			case got := <-done:
				t.Fatalf("不足三片提前返回：%s / %v", got.data, got.err)
			case <-time.After(testWait):
				t.Fatal("请求没有进入等待")
			}
			select {
			case got := <-done:
				t.Fatalf("等待未释放就返回：%s / %v", got.data, got.err)
			default:
			}
			count := 2
			switch release {
			case "第三片":
				appendHLSTestSegment(h)
				count++
			case "超时":
				select {
				case ticker.ticks <- testNow.Add(10 * time.Second):
				case <-time.After(testWait):
					t.Fatal("未接收注入的 tick")
				}
			case "结束":
				h.finish()
			}
			select {
			case got := <-done:
				if got.err != nil {
					t.Fatal(got.err)
				}
				playlist := string(got.data)
				if !strings.Contains(playlist, "#EXT-X-START:TIME-OFFSET=0,PRECISE=YES\n") || strings.Count(playlist, "#EXTINF:") != count {
					t.Fatalf("列表缺少 START 或片数不符：%s", playlist)
				}
				if strings.Contains(playlist, "#EXT-X-ENDLIST\n") != (release == "结束") {
					t.Fatalf("ENDLIST 状态不符：%s", playlist)
				}
			case <-time.After(testWait):
				t.Fatal("等待释放后未返回")
			}
		})
	}
}

func TestHLSPlaylistPrunedSegment(t *testing.T) {
	head := 0.0
	h := newHLSMedia(context.Background(), func() float64 { return head }, func(time.Duration) Ticker { return testTicker{make(chan time.Time)} })
	for range 3 {
		appendHLSTestSegment(h)
	}
	before, err := h.Playlist(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Segment(0); err != nil {
		t.Fatal(err)
	}
	head = 35 // 首片结束于 4 秒，早于播放头减 30 秒；第二片仍保留。
	h.prune()
	if _, err := h.Segment(0); !errors.Is(err, zones.ErrSegmentMissing) {
		t.Fatalf("已删分片 = %v", err)
	}
	if _, err := h.Segment(1); err != nil {
		t.Fatalf("误删保留窗口内的分片：%v", err)
	}
	after, err := h.Playlist(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("删除音频后 EVENT 条目发生变化：%s", after)
	}
}
