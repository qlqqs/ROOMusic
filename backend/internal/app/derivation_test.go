package app

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/config"
)

func derivationSettings(t *testing.T, dataDirectory string, allowedRoots ...string) config.Server {
	t.Helper()
	return config.Server{
		DataDirectory: dataDirectory, AllowedLibraryRoots: allowedRoots, MediaCacheMaxBytes: 1 << 30,
		// 默认指向一个不存在的 ffmpeg：不依赖本机是否安装；需要真实 ffmpeg 的测试自己覆盖。
		FFmpegPath: filepath.Join(t.TempDir(), "no-such-ffmpeg"),
	}
}

func jsonLogger() (*slog.Logger, *lockedBuffer) {
	logs := &lockedBuffer{}
	return slog.New(slog.NewJSONHandler(logs, nil)), logs
}

func TestOpenDerivationReportsAnUnusableFFmpegAndKeepsTheApplicationRunning(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	logger, logs := jsonLogger()
	manager, err := OpenDerivation(context.Background(), derivationSettings(t, data, root), logger)
	if err != nil || manager != nil {
		t.Fatalf("OpenDerivation = %v, %v; want no manager and no error when ffmpeg is unusable", manager, err)
	}
	events := logEvents(t, logs, "playback.derivation.unavailable")
	if len(events) != 1 || events[0]["reason"] != "not_found" || events[0]["level"] != "WARN" {
		t.Fatalf("unavailable events = %v, want one not_found warning: %s", events, logs.String())
	}
	if strings.Contains(logs.String(), data) || strings.Contains(logs.String(), "no-such-ffmpeg") {
		t.Fatalf("the unavailable event leaks a path: %s", logs.String())
	}
	if ready := logEvents(t, logs, "playback.derivation.ready"); len(ready) != 0 {
		t.Fatalf("ready events = %v although ffmpeg is unusable", ready)
	}
	// 缓存目录建在数据目录之下，权限只给所有者。
	info, err := os.Stat(filepath.Join(data, "media-cache"))
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("media-cache directory: %v, %v", info, err)
	}
}

func TestFFmpegOpenDerivationOpensTheCacheWithTheConfiguredBinary(t *testing.T) {
	ffmpeg := requireTestFFmpeg(t)
	root, data := t.TempDir(), t.TempDir()
	settings := derivationSettings(t, data, root)
	settings.FFmpegPath = ffmpeg
	settings.MediaCacheMaxBytes = 512 << 20
	logger, logs := jsonLogger()
	manager, err := OpenDerivation(context.Background(), settings, logger)
	if err != nil || manager == nil {
		t.Fatalf("OpenDerivation = %v, %v; want a manager: %s", manager, err, logs.String())
	}
	t.Cleanup(func() { _ = manager.Close(5 * time.Second) })
	ready := logEvents(t, logs, "playback.derivation.ready")
	if len(ready) != 1 || ready[0]["max_bytes"] != float64(512<<20) || ready[0]["entries"] != float64(0) {
		t.Fatalf("ready events = %v: %s", ready, logs.String())
	}
	if version, _ := ready[0]["ffmpeg_version"].(string); !strings.HasPrefix(version, "ffmpeg version") {
		t.Fatalf("ready event has no ffmpeg version: %v", ready[0])
	}
	if unavailable := logEvents(t, logs, "playback.derivation.unavailable"); len(unavailable) != 0 {
		t.Fatalf("unavailable events = %v", unavailable)
	}
	if strings.Contains(logs.String(), data) {
		t.Fatalf("the ready event leaks the data directory: %s", logs.String())
	}
}

func TestOpenDerivationRefusesACacheDirectoryThatOverlapsALibraryRoot(t *testing.T) {
	base := t.TempDir()
	music := filepath.Join(base, "music")
	if err := os.MkdirAll(music, 0o755); err != nil {
		t.Fatalf("create music root: %v", err)
	}
	inside := filepath.Join(music, "data")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatalf("create data directory: %v", err)
	}
	// 音乐根位于缓存目录之内：数据目录 base/data，缓存目录 base/data/media-cache，音乐根放在它里面。
	cacheDirectory := filepath.Join(base, "data", "media-cache")
	rootInsideCache := filepath.Join(cacheDirectory, "music")
	if err := os.MkdirAll(rootInsideCache, 0o755); err != nil {
		t.Fatalf("create nested music root: %v", err)
	}
	link := filepath.Join(base, "link-to-music-data")
	if err := os.Symlink(inside, link); err != nil {
		t.Skipf("symlinks are not available: %v", err)
	}

	for name, testCase := range map[string]struct {
		data  string
		roots []string
	}{
		"data directory inside the music root":                 {inside, []string{music}},
		"data directory is the music root":                     {music, []string{music}},
		"music root inside the cache directory":                {filepath.Join(base, "data"), []string{rootInsideCache}},
		"cache directory reached through a symbolic link":      {link, []string{music}},
		"a second root overlaps even if the first does not":    {inside, []string{t.TempDir(), music}},
		"data directory that does not exist yet, in the music": {filepath.Join(music, "not-yet", "data"), []string{music}},
	} {
		t.Run(name, func(t *testing.T) {
			logger, logs := jsonLogger()
			manager, err := OpenDerivation(context.Background(), derivationSettings(t, testCase.data, testCase.roots...), logger)
			if err == nil || manager != nil {
				t.Fatalf("OpenDerivation = %v, %v; want a configuration error", manager, err)
			}
			if !strings.Contains(err.Error(), "ROOMUSIC_DATA_DIR") {
				t.Fatalf("the error does not tell the operator what to change: %v", err)
			}
			if logs.String() != "" {
				t.Fatalf("a refused configuration logged: %s", logs.String())
			}
		})
	}

	// 拒绝发生在创建目录之前：音乐根里没有因此多出任何东西。
	for _, directory := range []string{music, inside} {
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatalf("read %s: %v", directory, err)
		}
		for _, entry := range entries {
			if entry.Name() == "media-cache" || entry.Name() == "not-yet" {
				t.Fatalf("a refused configuration still created %s inside the music root", entry.Name())
			}
		}
	}
}

func TestOpenDerivationAcceptsASiblingDirectoryWithTheSamePrefix(t *testing.T) {
	base := t.TempDir()
	music := filepath.Join(base, "music")
	data := filepath.Join(base, "music-data")
	for _, directory := range []string{music, data} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("create directory: %v", err)
		}
	}
	logger, _ := jsonLogger()
	manager, err := OpenDerivation(context.Background(), derivationSettings(t, data, music), logger)
	if err != nil || manager != nil {
		t.Fatalf("OpenDerivation = %v, %v; want no overlap error for music-data next to music (and no manager without ffmpeg)", manager, err)
	}
}

func TestResolveForCreateKeepsTheMissingTail(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	target := filepath.Join(base, "real")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("create directory: %v", err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks are not available: %v", err)
	}
	got, err := resolveForCreate(filepath.Join(link, "missing", "deeper"))
	if err != nil || got != filepath.Join(target, "missing", "deeper") {
		t.Fatalf("resolveForCreate = %q, %v; want %q", got, err, filepath.Join(target, "missing", "deeper"))
	}
	if got, err := resolveForCreate(target); err != nil || got != target {
		t.Fatalf("resolveForCreate(existing) = %q, %v; want %q", got, err, target)
	}
}

func TestIsWithin(t *testing.T) {
	for _, testCase := range []struct {
		parent, child string
		want          bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b", "/a/b/c", true},
		{"/a/b", "/a/b/c/d", true},
		{"/a/b", "/a/bc", false},
		{"/a/b", "/a", false},
		{"/a/b", "/x/b", false},
		{"/", "/a", true},
	} {
		if got := isWithin(testCase.parent, testCase.child); got != testCase.want {
			t.Errorf("isWithin(%q, %q) = %v, want %v", testCase.parent, testCase.child, got, testCase.want)
		}
	}
}
