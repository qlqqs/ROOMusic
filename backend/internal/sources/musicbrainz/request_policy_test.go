package musicbrainz

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/qlqq/roomusic/backend/internal/sources"
)

func elapsedTimer(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- time.Unix(0, int64(d))
	return ch
}

func TestCacheLifetime(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		ttl        time.Duration
		count      int
	}{
		{"positive", `{"artists":[{"id":"art"}]}`, 200, 30 * 24 * time.Hour, 1},
		{"empty", `{"artists":[]}`, 200, 24 * time.Hour, 0},
		{"notFound", "{}", 404, 24 * time.Hour, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			opts := localOptions(t, func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			})
			var clock atomic.Int64
			clock.Store(int64(123456 * time.Second))
			opts.Now = func() time.Time { return time.Unix(0, clock.Load()) }
			opts.DataDirectory = t.TempDir()
			call := func(c *Client, ctx context.Context, want int32) {
				t.Helper()
				got, err := c.SearchArtists(ctx, "测试")
				var wantErr error
				if tc.status == 404 {
					wantErr = sources.ErrNotFound
				}
				if !errors.Is(err, wantErr) || len(got) != tc.count || (tc.count > 0 && got[0].ID != "art") || hits.Load() != want {
					t.Fatalf("结果=%v 错误=%v 出站=%d，期望 %d", got, err, hits.Load(), want)
				}
			}
			call(New(opts), sources.WithBackground(t.Context()), 1)
			files, err := filepath.Glob(filepath.Join(opts.DataDirectory, "sources", "cache", "musicbrainz", "*", "*.json"))
			if err != nil || len(files) != 1 {
				t.Fatalf("磁盘缓存=%v %v", files, err)
			}
			clock.Add(int64(tc.ttl - time.Nanosecond))
			c := New(opts)
			call(c, t.Context(), 1)
			clock.Add(int64(time.Nanosecond))
			call(c, t.Context(), 2)
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				c.StartCacheCleanup(ctx)
				synctest.Wait()
				if _, err := os.Stat(files[0]); err != nil {
					t.Fatalf("启动清理误删有效文件：%v", err)
				}
				clock.Add(int64(tc.ttl))
				time.Sleep(24 * time.Hour)
				synctest.Wait()
				if _, err := os.Stat(files[0]); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("清理后文件状态=%v", err)
				}
			})
		})
	}
}

func TestCacheDirectoryAndInvalidResponse(t *testing.T) {
	for _, mode := range []string{"disabled", "unwritable", "invalid200"} {
		t.Run(mode, func(t *testing.T) {
			var hits atomic.Int32
			opts := localOptions(t, func(w http.ResponseWriter, _ *http.Request) {
				if hits.Add(1) == 1 && mode == "invalid200" {
					io.WriteString(w, `{"releases":[]}`)
					return
				}
				io.WriteString(w, `{"artists":[{"id":"ok"}]}`)
			})
			if mode != "disabled" {
				opts.DataDirectory = t.TempDir()
			}
			if mode == "unwritable" {
				dir := filepath.Join(opts.DataDirectory, "sources")
				if err := os.Mkdir(dir, 0500); err != nil {
					t.Fatal(err)
				}
				probe, err := os.CreateTemp(dir, "probe")
				switch {
				case errors.Is(err, os.ErrPermission):
					t.Cleanup(func() {
						if err := os.Chmod(dir, 0700); err != nil {
							t.Error(err)
						}
					})
				case err == nil:
					// 特权用户能绕过权限位，用同名普通文件制造无法创建缓存子目录的条件。
					if err := probe.Close(); err != nil {
						t.Fatal(err)
					}
					if err := os.RemoveAll(dir); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(dir, nil, 0600); err != nil {
						t.Fatal(err)
					}
				default:
					t.Fatal(err)
				}
			}
			c := New(opts)
			for i := 1; i <= 2; i++ {
				got, err := c.SearchArtists(sources.WithBackground(t.Context()), "查询")
				if mode == "invalid200" && i == 1 {
					if !errors.Is(err, sources.ErrTryLater) {
						t.Fatalf("结构错误返回 %v", err)
					}
					requireNoCacheFiles(t, opts.DataDirectory)
				} else if err != nil || len(got) != 1 || got[0].ID != "ok" {
					t.Fatalf("结果=%+v %v", got, err)
				}
			}
			if n := hits.Load(); n != 2 {
				t.Fatalf("实际出站 %d 次", n)
			}
		})
	}
}

func requireNoCacheFiles(t *testing.T, dir string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			t.Errorf("意外写入文件 %s", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFailureMemory(t *testing.T) {
	var hits atomic.Int32
	opts := localOptions(t, func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); w.WriteHeader(503) })
	now := time.Unix(123456, 0)
	opts.DataDirectory = t.TempDir()
	opts.Now = func() time.Time { return now }
	opts.After = elapsedTimer
	call := func(c *Client, background bool, want int32) {
		t.Helper()
		ctx := t.Context()
		wantErr := sources.ErrUnavailable
		if background {
			ctx = sources.WithBackground(ctx)
			wantErr = sources.ErrTryLater
		}
		got, err := c.SearchArtists(ctx, "失败")
		if !errors.Is(err, wantErr) || len(got) != 0 || hits.Load() != want {
			t.Fatalf("后台=%t，错误=%v，出站 %d 次，期望 %d", background, err, hits.Load(), want)
		}
	}
	c := New(opts)
	call(c, false, 1)
	call(c, true, 6)
	requireNoCacheFiles(t, opts.DataDirectory)
	now = now.Add(10*time.Minute - time.Nanosecond)
	call(c, true, 6)
	call(c, false, 7)
	call(c, true, 7)
	call(New(opts), true, 12)
	now = now.Add(time.Nanosecond)
	call(c, true, 17)
}

func TestRetrySchedule(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		status int
		header string
	}{
		{"unavailable", 503, "17"}, {"rateLimited", 429, start.Add(13 * time.Second).Format(http.TimeFormat)}, {"backoff", 503, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			opts := localOptions(t, func(w http.ResponseWriter, _ *http.Request) {
				if hits.Add(1) <= 4 {
					w.Header().Set("Retry-After", tc.header)
					w.WriteHeader(tc.status)
					return
				}
				io.WriteString(w, `{"artists":[{"id":"ok"}]}`)
			})
			now := start
			opts.Now = func() time.Time { return now }
			var delays []time.Duration
			opts.After = func(d time.Duration) <-chan time.Time {
				delays = append(delays, d)
				now = now.Add(d)
				return elapsedTimer(d)
			}
			artists, err := New(opts).SearchArtists(sources.WithBackground(t.Context()), "重试")
			if err != nil || len(artists) != 1 || artists[0].ID != "ok" || hits.Load() != 5 || len(delays) != 4 {
				t.Fatalf("结果=%+v %v，出站=%d 等待=%v", artists, err, hits.Load(), delays)
			}
			for i, delay := range delays {
				switch tc.name {
				case "unavailable":
					if delay != 17*time.Second {
						t.Fatal(delays)
					}
				case "rateLimited":
					want := time.Duration(0)
					if i == 0 {
						want = 13 * time.Second
					}
					if delay != want {
						t.Fatal(delays)
					}
				case "backoff":
					base := time.Second << (i + 1)
					if delay < base || delay >= 2*base || delay > 60*time.Second {
						t.Fatalf("第 %d 次退避 %s，基数 %s", i+1, delay, base)
					}
				}
			}
		})
	}
}

func TestRetryNetwork(t *testing.T) {
	var hits atomic.Int32
	opts := localOptions(t, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	})
	var waits int
	opts.After = func(d time.Duration) <-chan time.Time { waits++; return elapsedTimer(d) }
	got, err := New(opts).RecordingsByISRC(sources.WithBackground(t.Context()), "USABC2300001")
	if !errors.Is(err, sources.ErrTryLater) || len(got) != 0 || hits.Load() != 5 || waits != 4 {
		t.Fatalf("断连后的结果=%v 错误=%v 请求=%d 等待=%d，期望稍后再试、5 次请求、4 次等待", got, err, hits.Load(), waits)
	}
}

func TestResponseSizeLimits(t *testing.T) {
	const data = `{"artists":[{"id":"a"}]}`
	for _, tc := range []struct {
		name       string
		limit      int
		background bool
	}{{"chat", 2 << 20, false}, {"background", 8 << 20, true}} {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			opts := localOptions(t, func(w http.ResponseWriter, _ *http.Request) {
				n := tc.limit
				if hits.Add(1) > 1 {
					n++
				}
				io.WriteString(w, data+strings.Repeat(" ", n-len(data)))
			})
			c := New(opts)
			ctx := t.Context()
			wantErr := sources.ErrUnavailable
			if tc.background {
				ctx = sources.WithBackground(ctx)
				wantErr = sources.ErrTryLater
			}
			got, err := c.SearchArtists(ctx, "边界")
			if err != nil || len(got) != 1 {
				t.Fatalf("上限内结果=%v %v", got, err)
			}
			got, err = c.SearchArtists(ctx, "超限")
			if !errors.Is(err, wantErr) || len(got) != 0 || hits.Load() != 2 {
				t.Fatalf("超过 1 字节后结果=%v %v 出站=%d", got, err, hits.Load())
			}
		})
	}
}

func TestSourceLogPrivacy(t *testing.T) {
	var logs bytes.Buffer
	var hits atomic.Int32
	opts := localOptions(t, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		io.WriteString(w, `{"artists":[{"id":"private-response-marker"}]}`)
	})
	opts.BaseURL += "/private-path-marker"
	opts.DataDirectory = t.TempDir()
	opts.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	c := New(opts)
	for range 2 {
		if _, err := c.SearchArtists(t.Context(), "privateQueryMarker"); err != nil {
			t.Fatal(err)
		}
	}
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if hits.Load() != 1 || len(lines) != 2 || !strings.Contains(lines[0], `"event":"sources.request.finished"`) || !strings.Contains(lines[1], `"event":"sources.cache.hit"`) {
		t.Fatalf("请求与缓存事件=%v", lines)
	}
	for _, line := range lines {
		if slices.ContainsFunc([]string{"privateQueryMarker", "private-path-marker", "private-response-marker", "/ws/2", "query=", "fmt=json", "limit=10"}, func(s string) bool { return strings.Contains(line, s) }) {
			t.Fatalf("日志含私有内容 %s", line)
		}
	}
}

func TestSharedOutboundInterval(t *testing.T) {
	var clock atomic.Int64
	stamp := func() time.Time { return time.Unix(0, clock.Load()) }
	const interval = time.Second
	if Declaration().MinInterval != interval {
		t.Fatalf("声明的最小间隔 %s", Declaration().MinInterval)
	}
	arrivals := make(chan time.Time, 4)
	opts := localOptions(t, func(w http.ResponseWriter, _ *http.Request) {
		arrivals <- stamp()
		io.WriteString(w, `{"artists":[]}`)
	})
	opts.Limiter = sources.NewLimiter(interval, stamp, func(d time.Duration) <-chan time.Time {
		if d == 10*time.Second {
			return nil
		}
		clock.Add(int64(d))
		return elapsedTimer(d)
	})
	client := New(opts)
	var previous time.Time
	for _, ctx := range []context.Context{t.Context(), sources.WithBackground(t.Context()), t.Context(), sources.WithBackground(t.Context())} {
		if _, err := client.SearchArtists(ctx, "同一个源"); err != nil {
			t.Fatal(err)
		}
		at := <-arrivals
		if !previous.IsZero() && at.Sub(previous) < interval {
			t.Fatalf("相邻两次出站间隔 %s", at.Sub(previous))
		}
		previous = at
	}
}
