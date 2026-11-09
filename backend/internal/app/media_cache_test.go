package app

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
