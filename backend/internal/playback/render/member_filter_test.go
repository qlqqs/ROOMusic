package render

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

func TestMemberStreamFLACRoundTrip(t *testing.T) {
	ff := pinnedFFmpeg(t)
	newFilter := NewMemberStreamFilter(openTestTool(t, ff, ffmpeg.Options{}))
	const frames = 12345
	for _, tc := range []struct{ bits, rate int }{{16, 44100}, {24, 96000}} {
		t.Run(fmt.Sprintf("%dbit", tc.bits), func(t *testing.T) {
			width := tc.bits / 8
			raw := fmt.Sprintf("s%dle", tc.bits)
			input := driftInput(frames, width)
			flac := ffmpegOutput(t, ff, input, "-f", raw, "-ar", strconv.Itoa(tc.rate), "-ac", "2", "-i", "pipe:0", "-c:a", "flac", "-f", "flac", "pipe:1")
			format := zones.StreamFormat{Delivery: "stream", Codec: "flac", Channels: 2, BitDepth: tc.bits, SampleRate: tc.rate}
			for _, adjust := range []int{0, 4, -4} {
				t.Run(fmt.Sprintf("%+d", adjust), func(t *testing.T) {
					f := newFilter(format)
					if adjust != 0 {
						f.Adjust(int64(adjust))
					}
					ctx, cancel := context.WithTimeout(t.Context(), testWait)
					defer cancel()
					var encoded bytes.Buffer
					if err := f.Filter(ctx, io.NopCloser(bytes.NewReader(flac)), &encoded); err != nil {
						t.Fatal(err)
					}
					pcm := ffmpegOutput(t, ff, encoded.Bytes(), "-f", "flac", "-i", "pipe:0", "-c:a", "pcm_"+raw, "-f", raw, "pipe:1")
					checkDriftPCM(t, pcm, input, width, adjust)
				})
			}
		})
	}
}
