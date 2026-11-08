package parser

import (
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestFFmpegLosslessParserParameters(t *testing.T) {
	ffmpeg := os.Getenv("ROOMUSIC_TEST_FFMPEG")
	if ffmpeg == "" {
		t.Skip("未设置 ROOMUSIC_TEST_FFMPEG")
	}
	for _, tc := range []struct {
		name, extension, source, codec string
		rate, bits                     int
		args                           []string
	}{
		{"WavPack16", ".wv", "wavpack_apev2", "wavpack", 44100, 16, []string{"-c:a", "wavpack", "-sample_fmt", "s16p"}},
		{"WavPack24", ".wv", "wavpack_apev2", "wavpack", 48000, 24, []string{"-c:a", "wavpack", "-sample_fmt", "s32p", "-bits_per_raw_sample", "24"}},
		{"WavPack浮点", ".wv", "wavpack_apev2", "wavpack_float", 48000, 32, []string{"-c:a", "wavpack", "-sample_fmt", "fltp"}},
		{"AIFF24", ".aiff", "aiff_id3", "aiff", 48000, 24, []string{"-c:a", "pcm_s24be", "-write_id3v2", "1"}},
		{"AIFFCsowt", ".aif", "aiff_id3", "aiff", 44100, 16, []string{"-c:a", "pcm_s16le", "-f", "aiff", "-write_id3v2", "1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			path := filepath.Join(t.TempDir(), "track"+tc.extension)
			args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=" + strconv.Itoa(tc.rate) + ":duration=0.3", "-ac", "2"}
			args = append(args, tc.args...)
			args = append(args, "-metadata", "title=测试标题", "-metadata", "artist=测试艺人", "-metadata", "album=测试专辑", "-metadata", "track=3", path)
			if output, err := exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput(); err != nil {
				t.Fatalf("生成夹具失败：%v，%s", err, output)
			}
			obs, err := ParseAudioFile(path)
			if err != nil {
				t.Fatal(err)
			}
			assertLosslessFacts(t, obs, tc.source, tc.codec, tc.rate, tc.bits, 2, .3)
			if obs.Title != "测试标题" || obs.Artist != "测试艺人" || obs.Album != "测试专辑" || obs.TrackNumber != 3 {
				t.Fatalf("真实文件标签映射有误：%+v", obs)
			}
			if fingerprint := fingerprintOf(t, path); fingerprint == "" {
				t.Fatal("真实文件没有内容指纹")
			}
		})
	}
}

func TestAPERealFixture(t *testing.T) {
	// 固定素材由 Monkey's Audio 生成，与真实派生测试共用，不复制到解析器目录。
	path := filepath.Join("..", "..", "playback", "derivation", "testdata", "ape-16-44100.ape")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 16<<10 {
		t.Fatalf("APE 素材超过 16 KiB：%d", info.Size())
	}
	header, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(header) < 6 || string(header[:4]) != "MAC " || binary.LittleEndian.Uint16(header[4:6]) != 3990 {
		t.Fatal("固定 APE 素材不是版本 3990")
	}
	obs, err := ParseAudioFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertLosslessFacts(t, obs, "ape_apev2", "ape", 44100, 16, 2, .25)
	if fingerprint := fingerprintOf(t, path); fingerprint == "" || fingerprint != obs.ContentFingerprint {
		t.Fatalf("真实 APE 摘要不一致：%s / %s", fingerprint, obs.ContentFingerprint)
	}
}
