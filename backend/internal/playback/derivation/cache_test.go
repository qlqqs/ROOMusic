package derivation

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testKey(n int) string { return strings.Repeat(string(rune('a'+n)), keyLength) }

// writeEntry 在 v1/ 写入一个完成品文件，修改时间为 modified。
func writeEntry(t *testing.T, dir, key string, size int, modified time.Time) {
	t.Helper()
	path := filepath.Join(dir, entryDirName, key+entrySuffix)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, size), 0o600); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
}

func TestOpenCacheCreatesPrivateDirectories(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data", "media-cache")
	cache, err := openCache(dir, 1000, newFakeClock())
	if err != nil {
		t.Fatalf("openCache: %v", err)
	}
	defer cache.close()
	for _, path := range []string{dir, filepath.Join(dir, tempDirName), filepath.Join(dir, entryDirName)} {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			t.Fatalf("%s: %v", path, err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("%s permissions = %v, want no group or other access", path, info.Mode().Perm())
		}
	}
	if _, err := openCache(dir, 0, newFakeClock()); err == nil {
		t.Fatal("a non-positive limit must be rejected")
	}
}

func TestRebuildIndexesValidNamesOnlyAndClearsTemporaryFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "media-cache")
	older, newer := testTime.Add(-48*time.Hour), testTime.Add(-time.Hour)
	writeEntry(t, dir, testKey(0), 100, older)
	writeEntry(t, dir, testKey(1), 200, newer)
	valid := testKey(0)
	// 命名不合法、是目录、大小为零或不是普通文件的都不登记，也不删除。
	for _, junk := range []string{"notes.txt", strings.ToUpper(valid) + entrySuffix, valid[:63] + entrySuffix, valid + ".flac.bak", "zz" + valid[2:] + entrySuffix} {
		if err := os.WriteFile(filepath.Join(dir, entryDirName, junk), []byte("junk"), 0o600); err != nil {
			t.Fatalf("write junk: %v", err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, entryDirName, testKey(2)+entrySuffix), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, entryDirName, testKey(3)+entrySuffix), nil, 0o600); err != nil {
		t.Fatalf("write empty: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.flac")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, entryDirName, testKey(4)+entrySuffix)); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, tempDirName), 0o700); err != nil {
		t.Fatalf("mkdir tmp: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, tempDirName, "leftover.partial"), []byte("partial"), 0o600); err != nil {
		t.Fatalf("write leftover: %v", err)
	}

	cache, err := openCache(dir, 1000, newFakeClock())
	if err != nil {
		t.Fatalf("openCache: %v", err)
	}
	defer cache.close()
	entries, bytes := cache.stats()
	if entries != 2 || bytes != 300 {
		t.Fatalf("stats = %d entries, %d bytes; want 2 entries, 300 bytes", entries, bytes)
	}
	if size, ok := cache.lookup(testKey(1)); !ok || size != 200 {
		t.Fatalf("lookup = %d, %v", size, ok)
	}
	if names, _ := os.ReadDir(filepath.Join(dir, tempDirName)); len(names) != 0 {
		t.Fatalf("tmp/ was not cleared: %v", names)
	}
	for _, kept := range []string{"notes.txt", testKey(2) + entrySuffix, testKey(3) + entrySuffix, testKey(4) + entrySuffix} {
		if _, err := os.Lstat(filepath.Join(dir, entryDirName, kept)); err != nil {
			t.Fatalf("%s was removed: %v", kept, err)
		}
	}
	if content, err := os.ReadFile(outside); err != nil || string(content) != "outside" {
		t.Fatalf("a file outside the cache was touched: %q, %v", content, err)
	}
}

func TestRebuildEvictsTheOldestEntriesOverTheLimit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "media-cache")
	writeEntry(t, dir, testKey(0), 400, testTime.Add(-3*time.Hour))
	writeEntry(t, dir, testKey(1), 400, testTime.Add(-2*time.Hour))
	writeEntry(t, dir, testKey(2), 400, testTime.Add(-time.Hour))
	cache, err := openCache(dir, 1000, newFakeClock())
	if err != nil {
		t.Fatalf("openCache: %v", err)
	}
	defer cache.close()
	if _, ok := cache.lookup(testKey(0)); ok {
		t.Fatal("the oldest entry survived")
	}
	if _, err := os.Stat(filepath.Join(dir, entryDirName, testKey(0)+entrySuffix)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the evicted file still exists: %v", err)
	}
	if entries, bytes := cache.stats(); entries != 2 || bytes != 800 {
		t.Fatalf("stats = %d, %d", entries, bytes)
	}
}

func publishFile(t *testing.T, cache *Cache, key string, size int) {
	t.Helper()
	name, path, err := cache.newTemp()
	if err != nil {
		t.Fatalf("newTemp: %v", err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte{'y'}, size), 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	if _, err := cache.publish(name, key); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func TestPublishEvictsTheLeastRecentlyUsedAndKeepsTheNewEntry(t *testing.T) {
	clock := newFakeClock()
	cache, err := openCache(filepath.Join(t.TempDir(), "media-cache"), 1000, clock)
	if err != nil {
		t.Fatalf("openCache: %v", err)
	}
	defer cache.close()
	publishFile(t, cache, testKey(0), 400)
	clock.advance(time.Minute)
	publishFile(t, cache, testKey(1), 400)
	clock.advance(time.Minute)
	// 使用 A 之后 B 变成最久未用。
	file, _, err := cache.open(testKey(0))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	file.Close()
	clock.advance(time.Minute)
	publishFile(t, cache, testKey(2), 400)

	if _, ok := cache.lookup(testKey(1)); ok {
		t.Fatal("the least recently used entry was not evicted")
	}
	for _, key := range []string{testKey(0), testKey(2)} {
		if _, ok := cache.lookup(key); !ok {
			t.Fatalf("entry %s was evicted", key[:1])
		}
	}
	if entries, bytes := cache.stats(); entries != 2 || bytes != 800 {
		t.Fatalf("stats = %d, %d", entries, bytes)
	}
}

func TestPublishNeverEvictsTheEntryItJustPublished(t *testing.T) {
	cache, err := openCache(filepath.Join(t.TempDir(), "media-cache"), 1000, newFakeClock())
	if err != nil {
		t.Fatalf("openCache: %v", err)
	}
	defer cache.close()
	publishFile(t, cache, testKey(0), 600)
	publishFile(t, cache, testKey(1), 900)
	if _, ok := cache.lookup(testKey(1)); !ok {
		t.Fatal("the new entry was evicted")
	}
	if _, ok := cache.lookup(testKey(0)); ok {
		t.Fatal("the older entry was kept although the cache is over its limit")
	}
}

func TestPublishRejectsAnOutputLargerThanTheLimit(t *testing.T) {
	cache, err := openCache(filepath.Join(t.TempDir(), "media-cache"), 1000, newFakeClock())
	if err != nil {
		t.Fatalf("openCache: %v", err)
	}
	defer cache.close()
	name, path, err := cache.newTemp()
	if err != nil {
		t.Fatalf("newTemp: %v", err)
	}
	if err := os.WriteFile(path, make([]byte, 1001), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := cache.publish(name, testKey(0)); !errors.Is(err, errOutputTooLarge) {
		t.Fatalf("publish error = %v, want errOutputTooLarge", err)
	}
	if _, ok := cache.lookup(testKey(0)); ok {
		t.Fatal("an oversized output became visible")
	}
	if _, err := os.Stat(filepath.Join(cache.dir, entryDirName, testKey(0)+entrySuffix)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an oversized output was renamed into v1/: %v", err)
	}
}

func TestTouchPersistsTheLastUseAtMostOncePerHour(t *testing.T) {
	clock := newFakeClock()
	cache, err := openCache(filepath.Join(t.TempDir(), "media-cache"), 1000, clock)
	if err != nil {
		t.Fatalf("openCache: %v", err)
	}
	defer cache.close()
	publishFile(t, cache, testKey(0), 100)
	path := filepath.Join(cache.dir, entryDirName, testKey(0)+entrySuffix)
	modified := func() time.Time {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		return info.ModTime()
	}
	published := modified()
	if !published.Equal(testTime) {
		t.Fatalf("modification time after publish = %v, want %v", published, testTime)
	}

	clock.advance(30 * time.Minute)
	cache.touch(testKey(0))
	if !modified().Equal(published) {
		t.Fatal("the modification time was rewritten within an hour")
	}
	clock.advance(31 * time.Minute)
	cache.touch(testKey(0))
	if want := testTime.Add(61 * time.Minute); !modified().Equal(want) {
		t.Fatalf("modification time = %v, want %v", modified(), want)
	}
	// 未登记的键不报错。
	cache.touch(testKey(5))
}

func TestOpenDropsAnEntryWhoseFileWasChangedOrRemoved(t *testing.T) {
	cache, err := openCache(filepath.Join(t.TempDir(), "media-cache"), 1000, newFakeClock())
	if err != nil {
		t.Fatalf("openCache: %v", err)
	}
	defer cache.close()
	publishFile(t, cache, testKey(0), 100)
	publishFile(t, cache, testKey(1), 100)
	if err := os.WriteFile(filepath.Join(cache.dir, entryDirName, testKey(0)+entrySuffix), make([]byte, 50), 0o600); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if err := os.Remove(filepath.Join(cache.dir, entryDirName, testKey(1)+entrySuffix)); err != nil {
		t.Fatalf("remove: %v", err)
	}
	for _, key := range []string{testKey(0), testKey(1)} {
		if _, _, err := cache.open(key); !errors.Is(err, ErrNotCached) {
			t.Fatalf("open(%s) error = %v, want ErrNotCached", key[:1], err)
		}
		if _, ok := cache.lookup(key); ok {
			t.Fatalf("entry %s is still indexed", key[:1])
		}
	}
	if _, err := os.Stat(filepath.Join(cache.dir, entryDirName, testKey(0)+entrySuffix)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a modified entry file was kept: %v", err)
	}
	if entries, bytes := cache.stats(); entries != 0 || bytes != 0 {
		t.Fatalf("stats = %d, %d, want empty", entries, bytes)
	}
}

func TestKeyFromName(t *testing.T) {
	valid := strings.Repeat("0123456789abcdef", 4)
	for name, want := range map[string]bool{
		valid + ".flac":                          true,
		valid:                                    false,
		valid + ".partial":                       false,
		strings.ToUpper(valid) + ".flac":         false,
		valid[:63] + ".flac":                     false,
		valid + "0.flac":                         false,
		strings.Repeat("g", keyLength) + ".flac": false,
		".flac":                                  false,
	} {
		if _, ok := keyFromName(name); ok != want {
			t.Errorf("keyFromName(%q) = %v, want %v", name, ok, want)
		}
	}
}

func TestNewTempNamesAreUniqueAndInsideTmp(t *testing.T) {
	cache, err := openCache(filepath.Join(t.TempDir(), "media-cache"), 1000, newFakeClock())
	if err != nil {
		t.Fatalf("openCache: %v", err)
	}
	defer cache.close()
	seen := map[string]bool{}
	for range 50 {
		name, path, err := cache.newTemp()
		if err != nil {
			t.Fatalf("newTemp: %v", err)
		}
		if seen[name] || !strings.HasPrefix(name, tempDirName+"/") || !strings.HasSuffix(name, partialSuffix) {
			t.Fatalf("name %q is repeated or malformed", name)
		}
		if filepath.Dir(path) != filepath.Join(cache.dir, tempDirName) {
			t.Fatalf("path %q is not inside tmp/", path)
		}
		seen[name] = true
	}
}
