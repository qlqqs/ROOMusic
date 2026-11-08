package derivation

import (
	"reflect"
	"testing"
)

func TestConvertArgumentsSelectTheInputWithoutChangingTheOutputPolicy(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input Input
		args  []string
	}{
		{"ALAC", Input{Demuxer: "mov", Decoder: "alac"}, []string{"-c:a", "alac", "-f", "mov"}},
		{"APE", Input{Demuxer: "ape", Decoder: "ape"}, []string{"-c:a", "ape", "-f", "ape"}},
		{"WavPack", Input{Demuxer: "wv", Decoder: "wavpack"}, []string{"-c:a", "wavpack", "-f", "wv"}},
		{"AIFF", Input{Demuxer: "aiff"}, []string{"-f", "aiff"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 空格与 shell 元字符仍是一个路径参数，不应触发命令行拆分。
			job := ConvertJob{Input: tc.input, Output: "/cache/带 空格;$(false).partial", Limit: 123456}
			want := []string{
				"-nostdin", "-hide_banner", "-loglevel", "error", "-xerror", "-n", "-threads", "1",
				"-protocol_whitelist", "file",
			}
			want = append(want, tc.args...)
			want = append(want,
				"-i", "file:/dev/fd/3", "-map", "0:a:0", "-map_metadata", "-1", "-map_chapters", "-1",
				"-fflags", "+bitexact", "-flags:a", "+bitexact",
				"-c:a", "flac", "-compression_level", "5", "-fs", "123456", "-f", "flac", "file:"+job.Output,
				"-map", "0:a:0", "-c:a", "pcm_s32le", "-f", "md5", "pipe:1",
			)
			if got := convertArguments(job); !reflect.DeepEqual(got, want) {
				t.Fatalf("convertArguments = %#v, want %#v", got, want)
			}
		})
	}
}

func TestDecoderListParsesOnlyAudioDecoderNames(t *testing.T) {
	output := `Decoders:
 V..... = Video
 A..... = Audio
 S..... = Subtitle
 ------
 A....D alac                 ALAC (Apple Lossless Audio Codec)
 A....D ape                  Monkey's Audio
 AF...D wavpack              WavPack
 A....D pcm_s16be            PCM signed 16-bit big-endian
 A....D pcm_s24be            PCM signed 24-bit big-endian
 VFS..D h264                 H.264
 S..... ass                  ASS subtitle
 A bad                      invalid flag width
just noise
`
	want := map[string]bool{"alac": true, "ape": true, "wavpack": true, "pcm_s16be": true, "pcm_s24be": true}
	if got := parseDecoders(output); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseDecoders = %v, want %v", got, want)
	}
	if got := parseDecoders(""); len(got) != 0 {
		t.Fatalf("empty decoder output = %v", got)
	}
}
