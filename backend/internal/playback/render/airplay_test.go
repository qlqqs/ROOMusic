package render

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

func TestFFmpegAirPlayPushContinuousPCM(t *testing.T) {
	ff := pinnedFFmpeg(t)
	tool := openTestTool(t, ff, ffmpeg.Options{})
	for _, format := range []struct {
		rate, bits int
	}{{44100, 16}, {48000, 24}} {
		t.Run(fmt.Sprintf("%d/%d", format.rate, format.bits), func(t *testing.T) {
			a := makeFLAC(t, ff, format.bits, format.rate, 761, 0.73)
			b := makeFLAC(t, ff, format.bits, format.rate, 1097, 0.91)
			r := newTestRenderer(t, tool, fixtureSource(map[string]audioFixture{"a": a, "b": b}))
			spec := renderSpec("alac")
			spec.Format = zones.StreamFormat{Codec: "alac", MimeType: "audio/alac", Delivery: "push",
				MaxSampleRate: format.rate, MaxBitDepth: format.bits, MaxChannels: 2}
			spec.Cursor = testCursor(nextB)
			s := openTestSession(t, r, spec)
			got := s.Format()
			if got.Codec != "alac" || got.Quality != "lossless" || got.Delivery != "push" ||
				got.Channels != 2 || got.SampleRate != format.rate || got.BitDepth != format.bits || s.HLS() != nil {
				t.Fatalf("推流会话格式 = %+v", got)
			}
			raw := readBody(t, s)
			want := make([]byte, 0, (len(a.pcm)+len(b.pcm))/4*(format.bits/8))
			for _, decoded := range [][]byte{a.pcm, b.pcm} {
				for i := 0; i < len(decoded); i += 4 {
					want = append(want, decoded[i+4-format.bits/8:i+4]...)
				}
			}
			if len(raw) != len(want) || !bytes.Equal(raw, want) {
				t.Fatalf("原始 PCM 与两首源曲目不一致，实际 %d 字节，期望 %d", len(raw), len(want))
			}
			waitRenderComplete(t, s)
			total := float64(len(raw)/(format.bits/4)) / float64(format.rate)
			observe(s, total)
			var transitions int
			for _, e := range eventsThrough(t, s, "stream_ended", 0) {
				if e.Kind == "track_started" {
					transitions++
					if e.EntryID != "entry-b" || e.Transition != "gapless" ||
						e.StreamSeconds != float64(len(a.pcm)/8)/float64(format.rate) {
						t.Fatalf("推流换轨事件 = %+v", e)
					}
				}
				if e.Kind == "track_failed" || e.Kind == "format_boundary" {
					t.Fatalf("连续输出出现意外事件 %+v", e)
				}
			}
			if transitions != 1 {
				t.Fatalf("播放头推进后收到 %d 个换轨事件", transitions)
			}
		})
	}
}
