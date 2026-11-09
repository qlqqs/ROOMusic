package render

import (
	"errors"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

func TestNegotiate(t *testing.T) {
	tool := openTestTool(t, pinnedFFmpeg(t), ffmpeg.Options{})
	flac := zones.StreamFormatCapability{Codec: "flac", MIMEType: "audio/flac", Lossless: true}
	wav := zones.StreamFormatCapability{Codec: "wav", MIMEType: "audio/wav", Lossless: true}
	opus := zones.StreamFormatCapability{Codec: "opus", MIMEType: `audio/ogg; codecs="opus"`}
	aac := zones.StreamFormatCapability{Codec: "aac", MIMEType: "audio/aac"}
	for _, tc := range []struct {
		name, quality                  string
		formats                        []zones.StreamFormatCapability
		tool                           *ffmpeg.Tool
		codec, actualQuality, delivery string
	}{
		{"lossless有对应格式", "lossless", []zones.StreamFormatCapability{opus, flac, wav}, tool, "flac", "lossless", "stream"},
		{"lossless回退lossy", "lossless", []zones.StreamFormatCapability{opus, aac}, tool, "opus", "lossy", "stream"},
		{"lossy有对应格式", "lossy", []zones.StreamFormatCapability{flac, aac, opus}, tool, "aac", "lossy", "stream"},
		{"lossy回退lossless", "lossy", []zones.StreamFormatCapability{wav, flac}, tool, "wav", "lossless", "stream"},
		{"lossless无可用格式", "lossless", []zones.StreamFormatCapability{{Codec: "unknown"}}, tool, "", "", ""},
		{"lossy无可用格式", "lossy", nil, tool, "", "", ""},
		{"编码器不可用回退WAV", "lossy", []zones.StreamFormatCapability{opus, flac, wav}, nil, "wav", "lossless", "stream"},
		{"编码器均不可用", "lossless", []zones.StreamFormatCapability{flac, opus}, nil, "", "", ""},
		{"HLS候选只配HLS", "lossy", []zones.StreamFormatCapability{{Codec: "flac", MIMEType: "application/vnd.apple.mpegurl", Lossless: true}, {Codec: "aac", MIMEType: "application/vnd.apple.mpegurl"}}, tool, "aac", "lossy", "hls"},
		{"长流候选不配HLS", "lossy", []zones.StreamFormatCapability{{Codec: "alac", MIMEType: "audio/mp4", Lossless: true}, aac}, tool, "aac", "lossy", "stream"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRenderer(t, tc.tool, nil)
			got, err := r.Negotiate(zones.Capabilities{StreamFormats: tc.formats, MaxChannels: 2}, tc.quality)
			if tc.codec == "" {
				if !errors.Is(err, ErrNoStreamFormat) {
					t.Fatalf("Negotiate = %+v, %v", got, err)
				}
				return
			}
			if err != nil || got.Codec != tc.codec || got.Quality != tc.actualQuality || got.MimeType == "" || got.Delivery != tc.delivery {
				t.Fatalf("Negotiate = %+v, %v", got, err)
			}
			if (got.MimeType == "application/vnd.apple.mpegurl") != (tc.delivery == "hls") {
				t.Fatalf("MIME 与交付不一致：%+v", got)
			}
		})
	}
	t.Run("设备上限不串扰", func(t *testing.T) {
		r := newTestRenderer(t, tool, nil)
		caps := func(rate, bits, channels int) zones.Capabilities {
			return zones.Capabilities{MaxChannels: channels, StreamFormats: []zones.StreamFormatCapability{{Codec: "flac", MIMEType: "audio/flac", Lossless: true, MaxSampleRate: &rate, MaxBitDepth: &bits}}}
		}
		first, err := r.Negotiate(caps(192000, 24, 6), "lossless")
		if err != nil {
			t.Fatal(err)
		}
		second, err := r.Negotiate(caps(96000, 16, 2), "lossless")
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			format               zones.StreamFormat
			rate, bits, channels int
		}{{first, 192000, 24, 6}, {second, 96000, 16, 2}} {
			spec := renderSpec("flac")
			spec.Format = tc.format
			concrete, _, err := concreteFormat(spec, Track{Codec: "flac", SampleRate: 192000, BitDepth: 24, Channels: 6})
			if err != nil || concrete.SampleRate != tc.rate || concrete.BitDepth != tc.bits || concrete.Channels != tc.channels {
				t.Errorf("交错协商后的实际格式 = %+v, %v", concrete, err)
			}
		}
	})
}
