//go:build unix

package derivation

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDecoderProbePreservesALACAndReportsListFailure(t *testing.T) {
	for _, tc := range []struct {
		name, decoderCommand string
		unavailable          bool
	}{
		{"available", "printf ' A....D ape Monkey\n AF...D wavpack WavPack\n A....D pcm_s16be PCM\n'", false},
		{"command fails after partial output", "printf ' A....D ape Monkey\n'; echo '/private/decoder-path' >&2; exit 1", true},
		{"empty list", "exit 0", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 假进程只模拟自检的输出合同，不依赖本机安装 ffmpeg。
			flac := filepath.Join(t.TempDir(), "selftest.flac")
			if err := os.WriteFile(flac, streamInfoFile(44100, 2, 16, 11025), 0o600); err != nil {
				t.Fatal(err)
			}
			script := writeScript(t, fmt.Sprintf(`
case "$2" in
  -version) echo 'ffmpeg version decoder-test'; exit 0 ;;
  -decoders) %s; exit 0 ;;
esac
for arg in "$@"; do
  case "$arg" in
    file:*.partial) cp '%s' "${arg#file:}" || exit 1 ;;
  esac
done
echo 'MD5=0123456789abcdef0123456789abcdef'
`, tc.decoderCommand, flac))
			var logs bytes.Buffer
			manager, err := Open(context.Background(), Config{
				Dir: filepath.Join(t.TempDir(), "cache"), MaxBytes: 1 << 20, FFmpegPath: script,
				Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
			})
			if err != nil {
				t.Fatalf("Open must retain self-tested ALAC: %v", err)
			}
			t.Cleanup(func() { _ = manager.Close(time.Second) })
			if !manager.Supports("alac") {
				t.Error("self-tested ALAC was removed by decoder-list probing")
			}
			for _, decoder := range []string{"ape", "wavpack", "pcm_s16be"} {
				if got := manager.Supports(decoder); got != !tc.unavailable {
					t.Errorf("Supports(%q) = %v, unavailable = %v", decoder, got, tc.unavailable)
				}
			}
			if manager.Supports("missing") {
				t.Error("an unlisted decoder is supported")
			}
			const event = "playback.derivation.decoders_unavailable"
			wantLogs := 0
			if tc.unavailable {
				wantLogs = 1
			}
			if count := strings.Count(logs.String(), event); count != wantLogs {
				t.Errorf("unavailable events = %d, want %d: %s", count, wantLogs, logs.String())
			}
			for _, private := range []string{script, flac, "/private/decoder-path"} {
				if strings.Contains(logs.String(), private) {
					t.Errorf("decoder-list log leaks path %q", private)
				}
			}
		})
	}
}
