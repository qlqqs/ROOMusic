package render

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
)

func TestFFmpegCastMP3ConstantBitrateAndSampleRate(t *testing.T) {
	ff := pinnedFFmpeg(t)
	tool := openTestTool(t, ff, ffmpeg.Options{})
	fixture := makeFLAC(t, ff, 24, 96000, 997, 0.4)
	r := newTestRenderer(t, tool, fixtureSource(map[string]audioFixture{"a": fixture}))
	for _, tc := range []struct {
		name  string
		rate  int
		index uint32
	}{
		{"48kHz", 48000, 1},
		{"44.1kHz", 44100, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := renderSpec("mp3")
			spec.Format.MimeType = "audio/mpeg"
			spec.Format.MaxSampleRate = tc.rate
			s := openTestSession(t, r, spec)
			data := readBody(t, s)
			if got := s.Format(); got.Codec != "mp3" || got.MimeType != "audio/mpeg" || got.SampleRate != tc.rate || got.BitDepth != 0 {
				t.Fatalf("MP3 交付格式 = %+v", got)
			}
			if bytes.HasPrefix(data, []byte("ID3")) {
				if len(data) < 10 {
					t.Fatal("ID3 头被截断")
				}
				size := int(data[6])<<21 | int(data[7])<<14 | int(data[8])<<7 | int(data[9])
				if size > len(data)-10 {
					t.Fatalf("ID3 标签长度 %d 超出音频", size)
				}
				data = data[10+size:]
			}
			frames := 0
			for len(data) > 0 {
				if len(data) < 4 {
					t.Fatalf("第 %d 帧的 MPEG 头被截断", frames+1)
				}
				header := binary.BigEndian.Uint32(data)
				// MPEG-1 Layer III 中比特率索引 14 表示 320 kbps。
				if header&0xfffe0000 != 0xfffa0000 || (header>>12)&15 != 14 || (header>>10)&3 != tc.index {
					t.Fatalf("第 %d 帧的 MPEG 头为 %08x，期望 %d Hz、320 kbps", frames+1, header, tc.rate)
				}
				size := 144*320000/tc.rate + int((header>>9)&1)
				if len(data) < size {
					t.Fatalf("第 %d 帧长度 %d，期望 %d", frames+1, len(data), size)
				}
				data = data[size:]
				frames++
			}
			if frames < 15 || frames > 20 {
				t.Fatalf("0.4 秒 MP3 编码出 %d 帧", frames)
			}
		})
	}
}

func TestFFmpegCastSingleTrackStream(t *testing.T) {
	ff := pinnedFFmpeg(t)
	tool := openTestTool(t, ff, ffmpeg.Options{})
	first := makeFLAC(t, ff, 16, 44100, 997, 0.4)
	second := makeFLAC(t, ff, 16, 44100, 733, 0.6)
	r := newTestRenderer(t, tool, fixtureSource(map[string]audioFixture{"a": first, "b": second}))
	spec := renderSpec("flac")
	spec.Cursor, spec.SingleTrack = testCursor(nextB), true
	s := openTestSession(t, r, spec)
	events := eventsThrough(t, s, "format_boundary", 0)
	if event := events[len(events)-1]; event.NextEntryID != "entry-b" || event.Reason != "format" {
		t.Fatalf("同格式下一首的流边界 = %+v", event)
	}
	assertTransitionPCM(t, transitionPCM(t, ff, readBody(t, s)), first.pcm)
	assertBoundaryEnd(t, s)

	spec.Seq++
	spec.Start, _ = nextB("entry-a")
	s = openTestSession(t, r, spec)
	assertTransitionPCM(t, transitionPCM(t, ff, readBody(t, s)), second.pcm)
}
