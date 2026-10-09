package derivation

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	tempDirName  = "tmp"
	entryDirName = "v1"
	// entrySuffix 是完成品的后缀；v1/ 下只认可 <64 位小写十六进制键>.flac。
	entrySuffix   = ".flac"
	partialSuffix = ".partial"
	// persistInterval 是同一个条目把“最近使用”写回文件修改时间的最小间隔：重启后由修改时间重建淘汰顺序，
	// 但不能为每次播放都写一次文件元数据。
	persistInterval = time.Hour
	keyLength       = 64
)

type cacheEntry struct {
	size      int64
	used      time.Time
	persisted time.Time
}

// Cache 是受管派生缓存目录：<dir>/v1/<键>.flac 是完成品，<dir>/tmp/<随机>.partial 是生成中的文件。
// 完成品只经 rename 出现，所以任何可命中的文件都是完整且已核对的；索引在内存里，启动时由目录重建，
// “最近使用”持久化为文件修改时间。所有文件操作都限定在 dir 之内（os.Root），只删除 v1/ 下命名合法的条目与 tmp/ 内的文件。
type Cache struct {
	dir   string
	root  *os.Root
	max   int64
	clock clock

	mu      sync.Mutex
	entries map[string]*cacheEntry
	total   int64
}

// openCache 创建目录（权限 0700）、清空 tmp/、由 v1/ 重建索引，并在总量超过上限时按最近使用淘汰。
func openCache(dir string, maxBytes int64, clk clock) (*Cache, error) {
	if maxBytes <= 0 {
		return nil, errors.New("media cache limit must be positive")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create media cache directory: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open media cache directory: %w", err)
	}
	for _, name := range []string{tempDirName, entryDirName} {
		if err := root.Mkdir(name, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			_ = root.Close()
			return nil, fmt.Errorf("create media cache %s directory: %w", name, err)
		}
	}
	cache := &Cache{dir: dir, root: root, max: maxBytes, clock: clk, entries: map[string]*cacheEntry{}}
	if err := cache.clearTemp(); err != nil {
		_ = root.Close()
		return nil, err
	}
	if err := cache.rebuild(); err != nil {
		_ = root.Close()
		return nil, err
	}
	return cache, nil
}

// clearTemp 删除 tmp/ 内上次运行遗留的文件。单个文件删不掉不致命（下次启动再试），所以忽略删除错误。
func (c *Cache) clearTemp() error {
	directory, err := c.root.Open(tempDirName)
	if err != nil {
		return fmt.Errorf("open media cache tmp directory: %w", err)
	}
	names, err := directory.Readdirnames(-1)
	_ = directory.Close()
	if err != nil {
		return fmt.Errorf("list media cache tmp directory: %w", err)
	}
	for _, name := range names {
		_ = c.root.Remove(tempDirName + "/" + name)
	}
	return nil
}

// rebuild 把 v1/ 下命名合法的普通文件登记进索引；其他文件忽略且不删除。
func (c *Cache) rebuild() error {
	directory, err := c.root.Open(entryDirName)
	if err != nil {
		return fmt.Errorf("open media cache entries directory: %w", err)
	}
	items, err := directory.ReadDir(-1)
	_ = directory.Close()
	if err != nil {
		return fmt.Errorf("list media cache entries: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, item := range items {
		key, ok := keyFromName(item.Name())
		if !ok || !item.Type().IsRegular() {
			continue
		}
		info, err := item.Info()
		if err != nil || info.Size() <= 0 {
			continue
		}
		c.entries[key] = &cacheEntry{size: info.Size(), used: info.ModTime(), persisted: info.ModTime()}
		c.total += info.Size()
	}
	c.evictLocked("")
	return nil
}

func entryPath(key string) string { return entryDirName + "/" + key + entrySuffix }

// keyFromName 只认可 <64 位小写十六进制>.flac。
func keyFromName(name string) (string, bool) {
	key, found := strings.CutSuffix(name, entrySuffix)
	if !found || len(key) != keyLength {
		return "", false
	}
	for _, character := range key {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return "", false
		}
	}
	return key, true
}

// lookup 返回完成品的大小；不打开文件，也不更新最近使用时间。
func (c *Cache) lookup(key string) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return 0, false
	}
	return entry.size, true
}

// touch 记录一次使用：内存里总是更新，持久化的修改时间每个条目每小时最多写一次。
func (c *Cache) touch(key string) {
	now := c.clock.now()
	c.mu.Lock()
	entry, ok := c.entries[key]
	persist := ok && now.Sub(entry.persisted) >= persistInterval
	if ok {
		entry.used = now
		if persist {
			entry.persisted = now
		}
	}
	c.mu.Unlock()
	if persist {
		_ = c.root.Chtimes(entryPath(key), now, now)
	}
}

// open 以只读方式打开完成品，并更新最近使用时间。文件缺失或与登记的大小不符（被外部改动）时丢弃该条目并返回 ErrNotCached：
// 命中的每个文件都必须是我们发布的那个。
func (c *Cache) open(key string) (*os.File, int64, error) {
	c.mu.Lock()
	entry, ok := c.entries[key]
	var size int64
	if ok {
		size = entry.size
	}
	c.mu.Unlock()
	if !ok {
		return nil, 0, ErrNotCached
	}
	file, err := c.root.Open(entryPath(key))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			c.forget(key, false)
			return nil, 0, ErrNotCached
		}
		return nil, 0, fmt.Errorf("open derived file: %w", err)
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != size {
		_ = file.Close()
		c.forget(key, true)
		return nil, 0, ErrNotCached
	}
	c.touch(key)
	return file, size, nil
}

// forget 把条目移出索引，remove 为真时同时删除文件。
func (c *Cache) forget(key string, remove bool) {
	c.mu.Lock()
	if entry, ok := c.entries[key]; ok {
		delete(c.entries, key)
		c.total -= entry.size
	}
	c.mu.Unlock()
	if remove {
		_ = c.root.Remove(entryPath(key))
	}
}

// newTemp 返回 tmp/ 内一个新的随机文件名（相对缓存目录的名字与绝对路径）；文件由转换器创建，此处不预先创建。
func (c *Cache) newTemp() (name, path string, err error) {
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", "", fmt.Errorf("generate temporary name: %w", err)
	}
	name = tempDirName + "/" + hex.EncodeToString(random[:]) + partialSuffix
	return name, filepath.Join(c.dir, filepath.FromSlash(name)), nil
}

// discard 删除 tmp/ 内的临时文件（已不存在也不算错误）。
func (c *Cache) discard(tempName string) {
	_ = c.root.Remove(tempName)
}

// publish 把已核对的临时文件作为完成品发布：fsync 文件、rename 到 v1/<键>.flac、fsync 目录，再登记索引并按最近使用淘汰
// 其他条目（不删刚发布的）。输出本身超过缓存上限时拒绝发布（errOutputTooLarge），不会留下可命中的文件。
func (c *Cache) publish(tempName, key string) (int64, error) {
	file, err := c.root.Open(tempName)
	if err != nil {
		return 0, fmt.Errorf("open derived output: %w", err)
	}
	info, err := file.Stat()
	if err == nil {
		err = file.Sync()
	}
	_ = file.Close()
	if err != nil {
		return 0, fmt.Errorf("sync derived output: %w", err)
	}
	size := info.Size()
	if size <= 0 {
		return 0, errors.New("derived output is empty")
	}
	if size > c.max {
		return 0, errOutputTooLarge
	}
	if err := c.root.Rename(tempName, entryPath(key)); err != nil {
		return 0, fmt.Errorf("publish derived output: %w", err)
	}
	directory, err := c.root.Open(entryDirName)
	if err == nil {
		err = directory.Sync()
		_ = directory.Close()
	}
	if err != nil {
		// 目录项没有落盘就不当作已发布：删掉它，调用方按失败处理。
		_ = c.root.Remove(entryPath(key))
		return 0, fmt.Errorf("sync media cache directory: %w", err)
	}
	now := c.clock.now()
	_ = c.root.Chtimes(entryPath(key), now, now)
	c.mu.Lock()
	if previous, ok := c.entries[key]; ok {
		c.total -= previous.size
	}
	c.entries[key] = &cacheEntry{size: size, used: now, persisted: now}
	c.total += size
	c.evictLocked(key)
	c.mu.Unlock()
	return size, nil
}

var errOutputTooLarge = errors.New("derived output exceeds the media cache limit")

// evictLocked 在总量超过上限时按最近使用从旧到新删除条目（时间相同按键排序，保持确定），keep 指定的条目不删。
// 正在传输的文件被删除后，已打开的句柄仍可读完；下一次请求会重新生成，所以不需要读者引用计数。
func (c *Cache) evictLocked(keep string) {
	for c.total > c.max {
		var oldestKey string
		var oldest *cacheEntry
		for key, entry := range c.entries {
			if key == keep {
				continue
			}
			if oldest == nil || entry.used.Before(oldest.used) || (entry.used.Equal(oldest.used) && key < oldestKey) {
				oldestKey, oldest = key, entry
			}
		}
		if oldest == nil {
			return
		}
		delete(c.entries, oldestKey)
		c.total -= oldest.size
		_ = c.root.Remove(entryPath(oldestKey))
	}
}

// stats 返回条目数与总字节数。
func (c *Cache) stats() (entries int, bytes int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries), c.total
}

func (c *Cache) close() error { return c.root.Close() }
