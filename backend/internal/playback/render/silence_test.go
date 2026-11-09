package render

import (
	"crypto/md5"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

func TestRenderSilentFLACStartsWithinRenderWindow(t *testing.T) {
	ff := pinnedFFmpeg(t)
	tool := openTestTool(t, ff, ffmpeg.Options{})
	const duration = 180
	for _, tc := range []struct {
		rate, channels int
	}{
		{44100, 2},
		{8000, 1},
	} {
		t.Run(fmt.Sprintf("%dHz_16bit_%dch", tc.rate, tc.channels), func(t *testing.T) {
			// 精确零是最容易触发压缩后字节不足的素材，不能用加噪声掩盖起播死锁。
			original := make([]byte, duration*tc.rate*tc.channels*2)
			path := filepath.Join(t.TempDir(), "silence.flac")
			ffmpegOutput(t, ff, original, "-f", "s16le", "-ar", strconv.Itoa(tc.rate), "-ac", strconv.Itoa(tc.channels), "-i", "pipe:0", "-c:a", "flac", path)
			fixture := audioFixture{path: path, track: Track{Codec: "flac", SourceKind: "file", SampleRate: tc.rate, Channels: tc.channels, BitDepth: 16, DurationSeconds: duration}}
			r := newTestRenderer(t, tool, fixtureSource(map[string]audioFixture{"a": fixture}))
			spec := renderSpec("flac")
			spec.Settings.Leveling = "off"
			spec.Settings.Headroom.Enabled = false
			s := openTestSession(t, r, spec)

			prefix := make([]byte, 32*1024)
			firstRead := make(chan error, 1)
			type result struct {
				data []byte
				err  error
			}
			bodyDone := make(chan result, 1)
			ended := make(chan zones.Event, 1)
			var workers sync.WaitGroup
			workers.Add(2)
			go func() {
				defer workers.Done()
				_, err := io.ReadFull(s.Body(), prefix)
				firstRead <- err
				if err != nil {
					return
				}
				data, err := io.ReadAll(s.Body())
				bodyDone <- result{data, err}
			}()
			go func() {
				defer workers.Done()
				for event := range s.Events() {
					if event.Kind == "stream_ended" {
						ended <- event
						return
					}
				}
			}()
			joined := make(chan struct{})
			go func() { workers.Wait(); close(joined) }()
			t.Cleanup(func() {
				s.Close()
				select {
				case <-joined:
				case <-time.After(testWait):
					t.Error("取消后 Body 或 Events 消费协程未退出")
				}
			})

			// 不回报播放头也必须读满 32KiB；允许超过 20 秒，不足 64KiB 的有限流可以自然结束。
			select {
			case err := <-firstRead:
				if err != nil {
					t.Fatalf("首个 32KiB 读取失败：%v", err)
				}
			case <-time.After(testWait):
				t.Fatal("未推进播放头时未提供完整 32KiB")
			}
			s.mu.Lock()
			haveSample := s.haveSample
			s.mu.Unlock()
			if haveSample {
				t.Fatal("首个 32KiB 读取依赖播放头回报")
			}

			// 分步放行已渲染的播放头，同时消费 Body 与 Events，避免事件背压伪装成编码停滞。
			for seconds := 10; seconds <= duration; seconds += 10 {
				waitUntil(t, fmt.Sprintf("渲染到 %d 秒", seconds), func() bool {
					s.mu.Lock()
					defer s.mu.Unlock()
					return s.written >= int64(seconds*tc.rate)
				})
				observe(s, float64(seconds))
			}
			var encoded []byte
			select {
			case got := <-bodyDone:
				if got.err != nil {
					t.Fatalf("完整 Body：%v", got.err)
				}
				encoded = append(prefix, got.data...)
			case <-time.After(testWait):
				t.Fatal("推进到曲尾后 Body 未结束")
			}
			select {
			case event := <-ended:
				if event.StreamSeconds != duration {
					t.Fatalf("流尾位置 = %g，期望 %d", event.StreamSeconds, duration)
				}
			case <-time.After(testWait):
				t.Fatal("完整流缺少 stream_ended")
			}
			pcm := ffmpegOutput(t, ff, encoded, "-i", "pipe:0", "-map", "0:a:0", "-c:a", "pcm_s16le", "-f", "s16le", "pipe:1")
			if len(pcm) != len(original) || md5.Sum(pcm) != md5.Sum(original) {
				t.Fatalf("静音 PCM 长度/MD5 = %d/%x，期望 %d/%x", len(pcm), md5.Sum(pcm), len(original), md5.Sum(original))
			}
		})
	}
}
