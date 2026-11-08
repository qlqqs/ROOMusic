package derivation

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 这些测试用真的 ffmpeg（ROOMUSIC_TEST_FFMPEG 指向它，推荐 scripts/ffmpeg-pinned.sh 取出的固定版本）验证无损派生。
// 未设置时跳过；本地与 CI 的脚本把“取不到 ffmpeg”当作失败，不会静默跳过。

func requireFFmpeg(t *testing.T) string {
	t.Helper()
	path := os.Getenv("ROOMUSIC_TEST_FFMPEG")
	if path == "" {
		t.Skip("ROOMUSIC_TEST_FFMPEG is not set")
	}
	return path
}

func fixturePath(name string) string {
	return filepath.Join("..", "..", "..", "testdata", "playback", name)
}

func probeFFmpeg(t *testing.T) *FFmpeg {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ffmpeg, err := ProbeFFmpeg(ctx, requireFFmpeg(t), t.TempDir())
	if err != nil {
		t.Fatalf("ProbeFFmpeg: %v", err)
	}
	return ffmpeg
}

// decodedDigest 独立地用 ffmpeg 解码文件，返回 32 位整数 PCM 的 MD5：与被测代码使用同一种摘要，但是另起的一次调用。
func decodedDigest(t *testing.T, input string) string {
	t.Helper()
	command := exec.Command(requireFFmpeg(t), "-nostdin", "-hide_banner", "-loglevel", "error", "-i", input,
		"-map", "0:a:0", "-c:a", "pcm_s32le", "-f", "md5", "pipe:1")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("decode %s: %v", input, err)
	}
	digest, err := parseDigest(string(output))
	if err != nil {
		t.Fatalf("digest of %s: %v", input, err)
	}
	return digest
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	content, err := os.ReadFile(from)
	if err != nil {
		t.Fatalf("read %s: %v", from, err)
	}
	if err := os.WriteFile(to, content, 0o600); err != nil {
		t.Fatalf("write %s: %v", to, err)
	}
}

func convertFixture(t *testing.T, ffmpeg *FFmpeg, fixture string, limit int64, expect Expect) (output string, err error) {
	t.Helper()
	source, openErr := os.Open(fixturePath(fixture))
	if openErr != nil {
		t.Fatalf("open fixture: %v", openErr)
	}
	defer source.Close()
	output = filepath.Join(t.TempDir(), "output.partial")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return output, ffmpeg.Convert(ctx, ConvertJob{Input: Input{Demuxer: "mov", Decoder: "alac"}, Source: source, Output: output, Limit: limit, Expect: expect})
}

func TestFFmpegProbeRunsTheSelfTestAndReportsTheVersion(t *testing.T) {
	path := requireFFmpeg(t)
	work := t.TempDir()
	ffmpeg, err := ProbeFFmpeg(context.Background(), path, work)
	if err != nil {
		t.Fatalf("ProbeFFmpeg: %v", err)
	}
	if !strings.HasPrefix(ffmpeg.Version(), "ffmpeg version ") || strings.Contains(ffmpeg.Version(), "\n") {
		t.Fatalf("Version = %q", ffmpeg.Version())
	}
	if entries, _ := os.ReadDir(work); len(entries) != 0 {
		t.Fatalf("the self-test left files behind: %v", entries)
	}
}

func TestFFmpegConvertsALACLosslessly(t *testing.T) {
	ffmpeg := probeFFmpeg(t)
	for _, testCase := range []struct {
		fixture string
		expect  Expect
	}{
		{"alac-16-44100.m4a", Expect{BitDepth: 16, SampleRate: 44100, Channels: 2}},
		{"alac-24-48000.m4a", Expect{BitDepth: 24, SampleRate: 48000, Channels: 2}},
	} {
		t.Run(testCase.fixture, func(t *testing.T) {
			output, err := convertFixture(t, ffmpeg, testCase.fixture, 8<<20, testCase.expect)
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			// 独立核对：输出解码后的 PCM 与源解码后的 PCM 逐位相同，参数与源一致。
			if source, converted := decodedDigest(t, fixturePath(testCase.fixture)), decodedDigest(t, output); source != converted {
				t.Fatalf("decoded PCM differs: source %s, output %s", source, converted)
			}
			file, err := os.Open(output)
			if err != nil {
				t.Fatalf("open output: %v", err)
			}
			defer file.Close()
			info, err := readFLACStreamInfo(file)
			if err != nil {
				t.Fatalf("STREAMINFO: %v", err)
			}
			if info.BitDepth != testCase.expect.BitDepth || info.SampleRate != testCase.expect.SampleRate || info.Channels != testCase.expect.Channels || info.TotalSamples <= 0 {
				t.Fatalf("STREAMINFO = %+v, want %+v", info, testCase.expect)
			}
		})
	}
}

func TestFFmpegDoesNotRequireExpectedParameters(t *testing.T) {
	ffmpeg := probeFFmpeg(t)
	if _, err := convertFixture(t, ffmpeg, "alac-16-44100.m4a", 8<<20, Expect{}); err != nil {
		t.Fatalf("Convert without expectations: %v", err)
	}
}

func TestFFmpegReadsTheOpenedHandleEvenIfThePathIsReplaced(t *testing.T) {
	ffmpeg := probeFFmpeg(t)
	directory := t.TempDir()
	path := filepath.Join(directory, "track.m4a")
	copyFile(t, fixturePath("alac-16-44100.m4a"), path)
	source, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer source.Close()
	// 句柄打开之后，路径被换成另一个文件（AAC）：转换必须仍读取已核对的那个 inode。
	replacement := filepath.Join(directory, "replacement.m4a")
	copyFile(t, fixturePath("aac-44100.m4a"), replacement)
	if err := os.Rename(replacement, path); err != nil {
		t.Fatalf("replace: %v", err)
	}

	output := filepath.Join(directory, "output.partial")
	err = ffmpeg.Convert(context.Background(), ConvertJob{Input: Input{Demuxer: "mov", Decoder: "alac"}, Source: source, Output: output, Limit: 8 << 20, Expect: Expect{BitDepth: 16, SampleRate: 44100, Channels: 2}})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if want, got := decodedDigest(t, fixturePath("alac-16-44100.m4a")), decodedDigest(t, output); want != got {
		t.Fatalf("the output does not come from the opened handle: %s / %s", want, got)
	}
}

func TestFFmpegRejectsInputThatIsNotALAC(t *testing.T) {
	ffmpeg := probeFFmpeg(t)
	// AAC 也在 .m4a 里，但转换策略只许 ALAC：强制 ALAC 解码器使它失败，不会被当作“无损”转换。
	output, err := convertFixture(t, ffmpeg, "aac-44100.m4a", 8<<20, Expect{})
	var failed *FailedError
	if !errors.As(err, &failed) || failed.Category != FailureFFmpegExit {
		t.Fatalf("Convert error = %v, want an ffmpeg_exit failure", err)
	}
	if info, statErr := os.Stat(output); statErr == nil && info.Size() > 0 {
		// 输出可以留下（调用方删除），但不能被当作成功。
		t.Logf("a partial output remains (%d bytes), which the caller must delete", info.Size())
	}
}

func TestFFmpegRejectsDamagedInput(t *testing.T) {
	ffmpeg := probeFFmpeg(t)
	whole, err := os.ReadFile(fixturePath("alac-24-48000.m4a"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	for name, content := range map[string][]byte{
		"truncated": whole[:len(whole)/2],
		"garbage":   []byte(strings.Repeat("not an audio file ", 100)),
		"empty":     {},
	} {
		t.Run(name, func(t *testing.T) {
			damaged := filepath.Join(t.TempDir(), "damaged.m4a")
			if err := os.WriteFile(damaged, content, 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			source, err := os.Open(damaged)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer source.Close()
			err = ffmpeg.Convert(context.Background(), ConvertJob{Input: Input{Demuxer: "mov", Decoder: "alac"}, Source: source, Output: filepath.Join(t.TempDir(), "out.partial"), Limit: 8 << 20})
			var failed *FailedError
			if !errors.As(err, &failed) || failed.Category != FailureFFmpegExit {
				t.Fatalf("Convert error = %v, want an ffmpeg_exit failure", err)
			}
		})
	}
}

func TestFFmpegReportsAnOutputThatReachesTheSizeLimit(t *testing.T) {
	ffmpeg := probeFFmpeg(t)
	// -fs 只让 ffmpeg 在达到上限后停止写入而不报错：输出被截断，必须被识别为超限，不能当作成功。
	_, err := convertFixture(t, ffmpeg, "alac-24-48000.m4a", 2048, Expect{})
	var failed *FailedError
	if !errors.As(err, &failed) || failed.Category != FailureOutputLimit {
		t.Fatalf("Convert error = %v, want an output_limit failure", err)
	}
}

func TestFFmpegRejectsOutputWhoseParametersDiffer(t *testing.T) {
	ffmpeg := probeFFmpeg(t)
	for name, expect := range map[string]Expect{
		"sample rate": {SampleRate: 48000},
		"bit depth":   {BitDepth: 24},
		"channels":    {Channels: 1},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := convertFixture(t, ffmpeg, "alac-16-44100.m4a", 8<<20, expect)
			var failed *FailedError
			if !errors.As(err, &failed) || failed.Category != FailureVerifyMismatch {
				t.Fatalf("Convert error = %v, want a verify_mismatch failure", err)
			}
		})
	}
}

func TestFFmpegNeverOverwritesAnExistingOutput(t *testing.T) {
	ffmpeg := probeFFmpeg(t)
	source, err := os.Open(fixturePath("alac-16-44100.m4a"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer source.Close()
	output := filepath.Join(t.TempDir(), "existing.partial")
	if err := os.WriteFile(output, []byte("precious"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	err = ffmpeg.Convert(context.Background(), ConvertJob{Input: Input{Demuxer: "mov", Decoder: "alac"}, Source: source, Output: output, Limit: 8 << 20})
	var failed *FailedError
	if !errors.As(err, &failed) || failed.Category != FailureFFmpegExit {
		t.Fatalf("Convert error = %v, want an ffmpeg_exit failure", err)
	}
	if content, _ := os.ReadFile(output); string(content) != "precious" {
		t.Fatalf("an existing file was overwritten: %q", content)
	}
}

func TestFFmpegManagerDerivesCachesAndServesALAC(t *testing.T) {
	path := requireFFmpeg(t)
	dir := filepath.Join(t.TempDir(), "media-cache")
	manager, err := Open(context.Background(), Config{Dir: dir, MaxBytes: 64 << 20, FFmpegPath: path})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close(5 * time.Second) })

	fixture := fixturePath("alac-24-48000.m4a")
	info, err := os.Stat(fixture)
	if err != nil {
		t.Fatalf("stat fixture: %v", err)
	}
	version := SourceVersion{
		Policy:             policyALACFLAC,
		ContentFingerprint: "m4a-mdat-v1:test", Size: info.Size(), ModifiedAt: info.ModTime(),
		SourceKind: "m4a", Codec: "alac", BitDepth: 24, SampleRate: 48000, Channels: 2,
	}
	request := Request{User: "user-a", TrackID: "track-1", Source: Source{
		Input:   Input{Demuxer: "mov", Decoder: "alac"},
		Version: version,
		Open:    func(context.Context) (*os.File, error) { return os.Open(fixture) },
		Verify: func(_ context.Context, file *os.File, _ bool) error {
			current, err := file.Stat()
			if err != nil || current.Size() != version.Size {
				return errors.New("source changed")
			}
			return nil
		},
	}}

	entry, err := manager.Await(context.Background(), request)
	if err != nil {
		t.Fatalf("Await: %v", err)
	}
	file, size, err := manager.OpenEntry(entry.Key)
	if err != nil {
		t.Fatalf("OpenEntry: %v", err)
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil || int64(len(content)) != size || size != entry.Size {
		t.Fatalf("read %d bytes (%v), size %d, entry %d", len(content), err, size, entry.Size)
	}
	if string(content[:4]) != "fLaC" {
		t.Fatalf("the derived file is not FLAC: %q", content[:4])
	}
	published := filepath.Join(dir, entryDirName, entry.Key+entrySuffix)
	if source, derived := decodedDigest(t, fixture), decodedDigest(t, published); source != derived {
		t.Fatalf("decoded PCM differs: source %s, derived %s", source, derived)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, tempDirName)); len(entries) != 0 {
		t.Fatalf("temporary files remain: %v", entries)
	}

	again, err := manager.Await(context.Background(), request)
	if err != nil || again != entry {
		t.Fatalf("second Await = %+v, %v; want the cached %+v", again, err, entry)
	}
	stats := manager.Stats()
	if stats.Entries != 1 || stats.Bytes != size || !strings.HasPrefix(stats.Version, "ffmpeg version ") {
		t.Fatalf("Stats = %+v", stats)
	}
}
