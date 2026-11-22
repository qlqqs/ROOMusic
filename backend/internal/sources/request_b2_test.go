package sources

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/egress"
)

type requesterTestTransport func(*http.Request) (*http.Response, error)

func (fn requesterTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func immediateTick(now time.Time) <-chan time.Time {
	done := make(chan time.Time, 1)
	done <- now
	return done
}

func TestRequesterRedirectPolicy(t *testing.T) {
	const start = "https://origin.invalid/start"
	const middle = "https://middle.invalid/middle"
	const final = "https://final.invalid/end"
	for _, tc := range []struct {
		name, next string
		max        int
		code       egress.ErrorCode
	}{
		{name: "allowed", next: final, max: 2},
		{name: "host", next: "https://outside.invalid/end", max: 2, code: egress.CodeAddressNotAllowed},
		{name: "downgrade", next: "http://final.invalid/end", max: 2, code: egress.CodeSchemeNotAllowed},
		{name: "port", next: "https://final.invalid:444/end", max: 2, code: egress.CodeAddressNotAllowed},
		{name: "userinfo", next: "https://synthetic:fixture@final.invalid/end", max: 2, code: egress.CodeAddressNotAllowed},
		{name: "limit", next: final, max: 1, code: egress.CodeTooManyRedirects},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				switch r.URL.Path {
				case "/start":
					http.SetCookie(w, &http.Cookie{Name: "session", Value: "fixture"})
					http.Redirect(w, r, middle, http.StatusTemporaryRedirect)
				case "/middle":
					http.Redirect(w, r, tc.next, http.StatusFound)
				case "/end":
					w.Header().Set("Content-Type", "image/jpeg")
					io.WriteString(w, "synthetic")
				default:
					t.Errorf("意外路径 %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			var outbound []string
			local, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			localClient := server.Client()
			jar, err := cookiejar.New(nil)
			if err != nil {
				t.Fatal(err)
			}
			origin, _ := url.Parse(start)
			jar.SetCookies(origin, []*http.Cookie{{Name: "identity", Value: "fixture"}})
			baseClient := &http.Client{Jar: jar, Transport: requesterTestTransport(func(r *http.Request) (*http.Response, error) {
				outbound = append(outbound, r.URL.String())
				want := http.Header{"Accept": {"image/*"}, "User-Agent": {UserAgent()}}
				if r.Method != http.MethodGet || !reflect.DeepEqual(r.Header, want) {
					t.Errorf("请求 %s %s，头部 %v，期望 %v", r.Method, r.URL, r.Header, want)
				}
				// 所有目标都送到同一个合成 TLS 服务，使策略回归可以被命中次数发现。
				request := r.Clone(r.Context())
				request.URL.Scheme, request.URL.Host = local.Scheme, local.Host
				return localClient.Transport.RoundTrip(request)
			})}
			now := time.Unix(1700000000, 0)
			var limiterWaits []time.Duration
			limiter := NewLimiter(time.Second, func() time.Time { return now }, func(d time.Duration) <-chan time.Time {
				if d == 10*time.Second {
					return nil
				}
				limiterWaits = append(limiterWaits, d)
				now = now.Add(d)
				return immediateTick(now)
			})
			var retryWaits []time.Duration
			r := NewRequester(Declaration{ID: "fixture"}, Options{
				MaxRedirects: tc.max,
				AllowRedirect: func(u *url.URL) bool {
					return slices.Contains([]string{"origin.invalid", "middle.invalid", "final.invalid"}, u.Hostname())
				},
				NewClient: func(egress.Options) (*http.Client, error) { return baseClient, nil },
				Limiter:   limiter,
				After: func(d time.Duration) <-chan time.Time {
					retryWaits = append(retryWaits, d)
					return immediateTick(now)
				},
			})
			var wantOutbound []string
			for _, caller := range []struct {
				ctx     context.Context
				failure error
			}{
				{WithBackground(t.Context()), ErrTryLater},
				{t.Context(), ErrUnavailable},
			} {
				data, typ, err := r.GetBytes(caller.ctx, start, "image/*", 64)
				wantOutbound = append(wantOutbound, start, middle)
				if tc.code == "" {
					wantOutbound = append(wantOutbound, final)
					if err != nil || string(data) != "synthetic" || typ != "image/jpeg" {
						t.Fatalf("下载得到 %q %q，错误 %v", data, typ, err)
					}
				} else {
					var denied *egress.Error
					if !errors.Is(err, caller.failure) || !errors.Is(err, egress.ErrDenied) || !errors.As(err, &denied) || denied.Code != tc.code || data != nil || typ != "" {
						t.Fatalf("拒绝结果 %q %q，错误 %v，期望 %v 和 %s", data, typ, err, caller.failure, tc.code)
					}
				}
				if !slices.Equal(outbound, wantOutbound) || int(hits.Load()) != len(wantOutbound) || len(retryWaits) != 0 {
					t.Fatalf("出站 %v，服务命中 %d，重试等待 %v，期望 %v", outbound, hits.Load(), retryWaits, wantOutbound)
				}
			}
			if !slices.Equal(limiterWaits, []time.Duration{time.Second}) || baseClient.CheckRedirect != nil || baseClient.Jar != jar {
				t.Fatalf("两次调用的限速等待 %v，客户端有跳转检查 %t，仍持有原 CookieJar %t", limiterWaits, baseClient.CheckRedirect != nil, baseClient.Jar == jar)
			}
		})
	}
}

func TestRequesterRedirectRechecksBackgroundPermit(t *testing.T) {
	var first, next, retries atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			first.Add(1)
			http.Redirect(w, r, "/next", http.StatusTemporaryRedirect)
			return
		}
		next.Add(1)
		io.WriteString(w, "fixture")
	}))
	defer server.Close()
	closed := errors.New("合成源已关闭")
	ctx := WithBackground(t.Context(), func(context.Context) error {
		if first.Load() != 0 {
			return closed
		}
		return nil
	})
	// 自定义传输保留无端口的请求地址，并始终连接本地测试服务。
	local, _ := url.Parse(server.URL)
	base := &http.Client{Transport: requesterTestTransport(func(req *http.Request) (*http.Response, error) {
		request := req.Clone(req.Context())
		request.URL.Host = local.Host
		return server.Client().Transport.RoundTrip(request)
	})}
	r := NewRequester(Declaration{ID: "fixture"}, Options{
		MaxRedirects: 2, AllowRedirect: func(*url.URL) bool { return true },
		NewClient: func(egress.Options) (*http.Client, error) { return base, nil },
		Limiter:   NewLimiter(0, nil, nil),
		After: func(time.Duration) <-chan time.Time {
			retries.Add(1)
			return immediateTick(time.Time{})
		},
	})
	data, typ, err := r.GetBytes(ctx, "http://fixture.invalid/start", "image/*", 64)
	if !errors.Is(err, closed) || data != nil || typ != "" || first.Load() != 1 || next.Load() != 0 || retries.Load() != 0 {
		t.Fatalf("闸门关闭后收到 %q %q，错误 %v，首跳 %d，下一跳 %d，重试 %d", data, typ, err, first.Load(), next.Load(), retries.Load())
	}
}

func TestRequesterGetBytesStatusAndLimits(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		declared int
		wantErr  error
	}{
		{name: "missing", status: http.StatusNotFound, body: "404", wantErr: ErrNotFound},
		{name: "declared_oversize", status: http.StatusOK, body: "12", declared: 5, wantErr: ErrTryLater},
		{name: "stream_oversize", status: http.StatusOK, body: "12345", wantErr: ErrTryLater},
		{name: "at_limit", status: http.StatusOK, body: "1234"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.Header().Set("Content-Type", "image/png")
				if tc.declared > 0 {
					w.Header().Set("Content-Length", strconv.Itoa(tc.declared))
				}
				w.WriteHeader(tc.status)
				if tc.declared == 0 {
					w.(http.Flusher).Flush()
				}
				io.WriteString(w, tc.body)
			}))
			defer server.Close()
			r := NewRequester(Declaration{ID: "fixture"}, Options{
				NewClient: func(egress.Options) (*http.Client, error) { return server.Client(), nil },
				Limiter:   NewLimiter(0, nil, nil),
				After: func(d time.Duration) <-chan time.Time {
					t.Errorf("状态 %d 或超限触发重试，等待 %v", tc.status, d)
					return immediateTick(time.Time{})
				},
			})
			data, typ, err := r.GetBytes(WithBackground(t.Context()), server.URL, "image/*", 4)
			if !errors.Is(err, tc.wantErr) || hits.Load() != 1 {
				t.Fatalf("错误 %v，出站 %d 次，期望 %v 且一次出站", err, hits.Load(), tc.wantErr)
			}
			if tc.wantErr == nil {
				if string(data) != tc.body || typ != "image/png" {
					t.Fatalf("收到 %q %q", data, typ)
				}
			} else if data != nil || typ != "" {
				t.Fatalf("失败仍返回 %q %q", data, typ)
			}
		})
	}
}

func TestRequesterGetBytesRetryAndFailureMemory(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var hits atomic.Int32
	dateHeader := now.Add(7 * time.Second).Format(http.TimeFormat)
	var healthy atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := hits.Add(1)
		switch {
		case healthy.Load():
			w.Header().Set("Content-Type", "image/jpeg")
			io.WriteString(w, "fixture")
		case attempt == 1:
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)
		case attempt == 2:
			w.Header().Set("Retry-After", dateHeader)
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()
	var waits []time.Duration
	r := NewRequester(Declaration{ID: "fixture"}, Options{
		DataDirectory: t.TempDir(), Limiter: NewLimiter(0, nil, nil),
		NewClient: func(egress.Options) (*http.Client, error) { return server.Client(), nil },
		Now:       func() time.Time { return now },
		After: func(d time.Duration) <-chan time.Time {
			waits = append(waits, d)
			now = now.Add(d)
			return immediateTick(now)
		},
	})
	ctx := WithBackground(t.Context())
	for _, elapsed := range []time.Duration{0, 9*time.Minute + 59*time.Second} {
		now = now.Add(elapsed)
		data, typ, err := r.GetBytes(ctx, server.URL, "image/*", 64)
		if !errors.Is(err, ErrTryLater) || data != nil || typ != "" || hits.Load() != 5 || !slices.Equal(waits, []time.Duration{3 * time.Second, 4 * time.Second, time.Second, time.Second}) {
			t.Fatalf("经过 %v 后收到 %q %q，错误 %v，出站 %d 次，重试等待 %v", elapsed, data, typ, err, hits.Load(), waits)
		}
	}
	now = now.Add(2 * time.Second)
	healthy.Store(true)
	if data, typ, err := r.GetBytes(ctx, server.URL, "image/*", 64); err != nil || string(data) != "fixture" || typ != "image/jpeg" || hits.Load() != 6 {
		t.Fatalf("失败记忆过期后收到 %q %q，错误 %v，出站 %d 次", data, typ, err, hits.Load())
	}
}

func TestRequesterGetBytesDoesNotCacheResponse(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"hit":`+strconv.Itoa(int(n))+`}`)
	}))
	defer server.Close()
	dir := t.TempDir()
	r := NewRequester(Declaration{ID: "fixture"}, Options{
		DataDirectory: dir, Limiter: NewLimiter(0, nil, nil),
		NewClient: func(egress.Options) (*http.Client, error) { return server.Client(), nil },
	})
	ctx := WithBackground(t.Context())
	for _, n := range []string{"1", "2"} {
		data, typ, err := r.GetBytes(ctx, server.URL, "application/json", 128)
		if err != nil || string(data) != `{"hit":`+n+`}` || typ != "application/json" {
			t.Fatalf("收到 %q %q，错误 %v，期望命中编号 %s", data, typ, err, n)
		}
	}
	for range 2 {
		if data, typ, err := r.GetBytes(ctx, server.URL+"/missing", "image/*", 128); !errors.Is(err, ErrNotFound) || data != nil || typ != "" {
			t.Fatalf("404 收到 %q %q，错误 %v", data, typ, err)
		}
	}
	if n := hits.Load(); n != 4 {
		t.Fatalf("成功和 404 各请求两次，实际出站 %d 次", n)
	}
	files, err := filepath.Glob(filepath.Join(dir, "sources", "cache", "fixture", "*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("二进制响应留下缓存文件 %v，错误 %v", files, err)
	}
	if err := r.GetJSON(ctx, server.URL, func(body []byte) (bool, bool) {
		return false, string(body) == `{"hit":5}`
	}); err != nil {
		t.Fatalf("同地址的 JSON 缓存预热失败：%v", err)
	}
	if files, err := os.ReadDir(filepath.Join(dir, "sources", "cache", "fixture")); err != nil || len(files) != 1 {
		t.Fatalf("预热得到的缓存 %v，错误 %v", files, err)
	}
	if data, typ, err := r.GetBytes(ctx, server.URL, "application/json", 128); err != nil || string(data) != `{"hit":6}` || typ != "application/json" || hits.Load() != 6 {
		t.Fatalf("有 JSON 缓存时收到 %q %q，错误 %v，出站 %d 次", data, typ, err, hits.Load())
	}
}
