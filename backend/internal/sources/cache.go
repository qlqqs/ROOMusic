package sources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type entry struct {
	StoredAt  time.Time `json:"stored_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Status    int       `json:"status"`
	Body      string    `json:"body"`
}

type cache struct {
	dir, source string
	now         func() time.Time
	logger      *slog.Logger
	mu          sync.Mutex
	failures    map[string]time.Time
	nextSweep   time.Time
}

func cacheKey(address string) string {
	key := sha256.Sum256([]byte("GET " + address))
	return hex.EncodeToString(key[:])
}

func (c *cache) path(key string) string { return filepath.Join(c.dir, key[:2], key+".json") }

func (c *cache) warning(ctx context.Context) {
	if c.logger != nil {
		c.logger.WarnContext(ctx, "外部源缓存读写失败", "event", "sources.cache.error", "module", "sources", "message", "外部源缓存读写失败", "source", c.source)
	}
}

func readEntry(path string) (entry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return entry{}, err
	}
	var e entry
	err = json.Unmarshal(b, &e)
	return e, err
}

func (c *cache) lookup(ctx context.Context, key string, background, readResponse bool) (entry, bool, bool) {
	if c.dir == "" {
		return entry{}, false, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if background && c.now().Before(c.failures[key]) {
		return entry{}, false, true
	}
	if !readResponse {
		return entry{}, false, false
	}
	e, err := readEntry(c.path(key))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			c.warning(ctx)
		}
		return entry{}, false, false
	}
	return e, (e.Status == 200 || e.Status == 404) && c.now().Before(e.ExpiresAt), false
}

func (c *cache) fail(key string) {
	if c.dir == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if !now.Before(c.nextSweep) {
		for k, until := range c.failures {
			if !now.Before(until) {
				delete(c.failures, k)
			}
		}
		c.nextSweep = now.Add(10 * time.Minute)
	}
	c.failures[key] = now.Add(10 * time.Minute)
}

func (c *cache) store(ctx context.Context, key string, status int, body []byte, empty, background bool) {
	if c.dir == "" {
		return
	}
	ttl := 30 * 24 * time.Hour
	if status == 404 || empty {
		ttl = 24 * time.Hour
	}
	now := c.now()
	e := entry{StoredAt: now, ExpiresAt: now.Add(ttl), Status: status, Body: string(body)}
	c.mu.Lock()
	defer c.mu.Unlock()
	if background {
		delete(c.failures, key)
	}
	if err := writeEntry(c.path(key), e); err != nil {
		c.warning(ctx)
	}
}

func writeEntry(path string, e entry) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".entry-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	err = json.NewEncoder(tmp).Encode(e)
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp.Name(), path)
}

func (c *cache) clean(ctx context.Context) {
	err := filepath.WalkDir(c.dir, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				c.warning(ctx)
			}
			return nil
		}
		if !d.Type().IsRegular() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		e, err := readEntry(path)
		if err == nil && !c.now().Before(e.ExpiresAt) {
			err = os.Remove(path)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			c.warning(ctx)
		}
		return nil
	})
	if err != nil && ctx.Err() == nil {
		c.warning(ctx)
	}
}
