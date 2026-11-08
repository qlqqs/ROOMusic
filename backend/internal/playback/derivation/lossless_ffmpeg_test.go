package derivation

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestFFmpegDerivesAndCachesWavPackAIFFAndAPE(t *testing.T) {
	path := requireFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dir := filepath.Join(t.TempDir(), "cache")
	manager, err := Open(ctx, Config{Dir: dir, MaxBytes: 64 << 20, FFmpegPath: path})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close(5 * time.Second) })

	for _, tc := range []struct {
		name, sourceKind, codec, policy string
		input                           Input
		expect                          Expect
		samples                         int64
		encode                          []string
	}{
		{"wv16.wv", "wavpack_apev2", "wavpack", "wavpack-flac-lossless-v1", Input{Demuxer: "wv", Decoder: "wavpack"},
			Expect{BitDepth: 16, SampleRate: 44100, Channels: 2}, 13230, []string{"-c:a", "wavpack", "-sample_fmt", "s16p"}},
		{"wv24.wv", "wavpack_apev2", "wavpack", "wavpack-flac-lossless-v1", Input{Demuxer: "wv", Decoder: "wavpack"},
			Expect{BitDepth: 24, SampleRate: 48000, Channels: 2}, 14400, []string{"-c:a", "wavpack", "-sample_fmt", "s32p", "-bits_per_raw_sample", "24"}},
		{"aiff24.aiff", "aiff_id3", "aiff", "aiff-flac-lossless-v1", Input{Demuxer: "aiff"},
			Expect{BitDepth: 24, SampleRate: 48000, Channels: 2}, 14400, []string{"-c:a", "pcm_s24be"}},
		{"ape-16-44100.ape", "ape_apev2", "ape", "ape-flac-lossless-v1", Input{Demuxer: "ape", Decoder: "ape"},
			Expect{BitDepth: 16, SampleRate: 44100, Channels: 2}, 11025, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := filepath.Join("testdata", tc.name)
			if tc.encode != nil {
				input = filepath.Join(t.TempDir(), tc.name)
				args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i",
					fmt.Sprintf("sine=frequency=440:sample_rate=%d:duration=0.3", tc.expect.SampleRate), "-ac", "2"}
				args = append(args, tc.encode...)
				args = append(args, input)
				if output, err := exec.CommandContext(ctx, path, args...).CombinedOutput(); err != nil {
					t.Fatalf("generate %s: %v: %s", tc.name, err, output)
				}
			}
			info, err := os.Stat(input)
			if err != nil {
				t.Fatalf("stat fixture: %v", err)
			}
			if tc.encode == nil && info.Size() > 16<<10 {
				t.Fatalf("APE fixture is %d bytes, want at most 16 KiB", info.Size())
			}
			digest := decodedDigest(t, input)
			version := SourceVersion{
				Policy: tc.policy, ContentFingerprint: "fixture-pcm:" + digest, Size: info.Size(), ModifiedAt: info.ModTime(),
				SourceKind: tc.sourceKind, Codec: tc.codec, BitDepth: tc.expect.BitDepth, SampleRate: tc.expect.SampleRate, Channels: tc.expect.Channels,
			}
			request := Request{User: "lossless-test", TrackID: tc.name, Source: Source{
				Input: tc.input, Version: version,
				Open: func(context.Context) (*os.File, error) { return os.Open(input) },
				Verify: func(_ context.Context, file *os.File, _ bool) error {
					current, err := file.Stat()
					if err != nil {
						return err
					}
					if current.Size() != version.Size || !current.ModTime().Equal(version.ModifiedAt) {
						return fmt.Errorf("fixture changed during derivation")
					}
					return nil
				},
			}}
			entry, err := manager.Await(ctx, request)
			if err != nil {
				t.Fatalf("Await: %v", err)
			}
			output := filepath.Join(dir, entryDirName, entry.Key+entrySuffix)
			if got := decodedDigest(t, output); got != digest {
				t.Fatalf("decoded PCM differs: source %s, FLAC %s", digest, got)
			}
			file, size, err := manager.OpenEntry(entry.Key)
			if err != nil {
				t.Fatalf("OpenEntry: %v", err)
			}
			defer file.Close()
			stream, err := readFLACStreamInfo(file)
			if err != nil || stream.BitDepth != tc.expect.BitDepth || stream.SampleRate != tc.expect.SampleRate || stream.Channels != tc.expect.Channels || stream.TotalSamples != tc.samples {
				t.Fatalf("STREAMINFO = %+v, %v; want %+v, %d samples", stream, err, tc.expect, tc.samples)
			}
			if size != entry.Size || size <= 0 {
				t.Fatalf("cached size = %d, entry = %+v", size, entry)
			}
			// 缓存命中不再打开源：若回调被执行，第二次等待必须失败。
			request.Source.Open = func(context.Context) (*os.File, error) { return nil, fmt.Errorf("cache hit reopened the fixture") }
			if again, err := manager.Await(ctx, request); err != nil || again != entry {
				t.Fatalf("cached Await = %+v, %v; want %+v", again, err, entry)
			}
			if entries, _ := os.ReadDir(filepath.Join(dir, tempDirName)); len(entries) != 0 {
				t.Fatalf("temporary files remain: %v", entries)
			}
		})
	}
}
