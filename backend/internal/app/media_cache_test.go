package app

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/config"
)

func jsonLogger() (*slog.Logger, *lockedBuffer) {
	logs := &lockedBuffer{}
	return slog.New(slog.NewJSONHandler(logs, nil)), logs
}

func TestCacheDirectoryIsolationRefusesOverlappingLibraryRoots(t *testing.T) {
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
			err := checkCacheDirectoryIsolated(filepath.Join(testCase.data, mediaCacheDirectoryName), testCase.roots)
			if err == nil {
				t.Fatal("重叠目录未被拒绝")
			}
			if !strings.Contains(err.Error(), "ROOMUSIC_DATA_DIR") {
				t.Fatalf("the error does not tell the operator what to change: %v", err)
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

func TestCacheDirectoryIsolationAcceptsASiblingDirectoryWithTheSamePrefix(t *testing.T) {
	base := t.TempDir()
	music := filepath.Join(base, "music")
	data := filepath.Join(base, "music-data")
	for _, directory := range []string{music, data} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("create directory: %v", err)
		}
	}
	if err := checkCacheDirectoryIsolated(filepath.Join(data, mediaCacheDirectoryName), []string{music}); err != nil {
		t.Fatalf("兄弟目录不应被判为重叠：%v", err)
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

func TestRemoveLegacyMediaCacheRemovesFilesAndLogsTheirBytes(t *testing.T) {
	data := t.TempDir()
	cache := filepath.Join(data, mediaCacheDirectoryName)
	if err := os.MkdirAll(filepath.Join(cache, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	var wantBytes int64
	for name, content := range map[string]string{"original.flac": "cached audio", "nested/other.flac": "another cached file"} {
		if err := os.WriteFile(filepath.Join(cache, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		wantBytes += int64(len(content))
	}
	logger, logs := jsonLogger()
	if err := RemoveLegacyMediaCache(config.Server{DataDirectory: data, AllowedLibraryRoots: []string{t.TempDir()}}, logger); err != nil {
		t.Fatalf("删除旧缓存失败：%v", err)
	}
	if _, err := os.Stat(cache); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("旧缓存目录未删除：%v", err)
	}
	events := logEvents(t, logs, "playback.derivation.cache_removed")
	if len(events) != 1 {
		t.Fatalf("删除日志数量 = %d，期望 1", len(events))
	}
	if events[0]["bytes"] != float64(wantBytes) {
		t.Fatalf("删除字节数 = %v，期望 %d", events[0]["bytes"], wantBytes)
	}
}

func TestRemoveLegacyMediaCacheDoesNotLogWhenAbsent(t *testing.T) {
	logger, logs := jsonLogger()
	if err := RemoveLegacyMediaCache(config.Server{DataDirectory: t.TempDir()}, logger); err != nil {
		t.Fatalf("缓存不存在时不应报错：%v", err)
	}
	if logs.String() != "" {
		t.Fatalf("缓存不存在时不应记录日志：%s", logs.String())
	}
}

func TestRemoveLegacyMediaCacheRefusesOverlappingLibraryRootsWithoutDeleting(t *testing.T) {
	for _, rootInsideCache := range []bool{false, true} {
		name := "缓存位于音乐根内"
		if rootInsideCache {
			name = "音乐根位于缓存内"
		}
		t.Run(name, func(t *testing.T) {
			data := t.TempDir()
			cache := filepath.Join(data, mediaCacheDirectoryName)
			music := data
			if rootInsideCache {
				music = filepath.Join(cache, "music")
			}
			for _, directory := range []string{cache, music} {
				if err := os.MkdirAll(directory, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			files := []string{filepath.Join(cache, "cached.flac"), filepath.Join(music, "source.flac")}
			for _, file := range files {
				if err := os.WriteFile(file, []byte("keep"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			logger, logs := jsonLogger()
			if err := RemoveLegacyMediaCache(config.Server{DataDirectory: data, AllowedLibraryRoots: []string{music}}, logger); err == nil {
				t.Fatal("重叠目录未被拒绝")
			}
			for _, file := range files {
				if content, err := os.ReadFile(file); err != nil || string(content) != "keep" {
					t.Fatalf("拒绝删除后文件发生变化：content=%q err=%v", content, err)
				}
			}
			if events := logEvents(t, logs, "playback.derivation.cache_removed"); len(events) != 0 {
				t.Fatalf("拒绝删除不应记录成功日志：%v", events)
			}
		})
	}
}
