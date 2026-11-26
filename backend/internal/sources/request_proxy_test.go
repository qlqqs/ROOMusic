package sources

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/egress"
)

func TestRequesterOutboundProxyBackoff(t *testing.T) {
	for _, tc := range []struct {
		name string
		code egress.ErrorCode
	}{
		{"unavailable", egress.CodeProxyUnavailable},
		{"authentication", egress.CodeProxyAuth},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(func() { egress.SetProxy(nil) })
			const user, password = "fixture", "source-proxy-synthetic+password"
			auth := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Proxy-Authorization") != auth {
					w.WriteHeader(http.StatusProxyAuthRequired)
					return
				}
				io.WriteString(w, `{"fixture":true}`)
			}))
			t.Cleanup(server.Close)
			working := egress.ProxyConfig{
				Scheme: "http", Host: "127.0.0.1", Port: server.Listener.Addr().(*net.TCPAddr).Port,
				Username: user, Password: password,
			}
			bad := working
			if tc.code == egress.CodeProxyUnavailable {
				closed := httptest.NewServer(http.NotFoundHandler())
				bad.Port = closed.Listener.Addr().(*net.TCPAddr).Port
				closed.Close()
			} else {
				bad.Password += "-wrong"
			}
			egress.SetProxy(&bad)

			var logs bytes.Buffer
			now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
			clock := func() time.Time { return now }
			var retries, decoded, recovered int
			r := NewRequester(Declaration{ID: "fixture"}, Options{
				DataDirectory: t.TempDir(),
				Logger:        slog.New(slog.NewJSONHandler(&logs, nil)),
				Limiter:       NewLimiter(0, nil, nil),
				Now:           clock,
				After: func(time.Duration) <-chan time.Time {
					retries++
					return immediateTick(now)
				},
				NewClient: func(options egress.Options) (*http.Client, error) {
					options.LookupNetIP = func(context.Context, string, string) ([]netip.Addr, error) {
						return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
					}
					return egress.NewClient(options)
				},
			})
			queue := NewQueuePolicy(clock)
			ctx := queue.Context(t.Context(), QueueNormal, func(string) { recovered++ })
			call := func() error {
				return r.GetJSON(ctx, "http://source.invalid/fixture", func(body []byte) (bool, bool) {
					decoded++
					return false, string(body) == `{"fixture":true}`
				})
			}
			for range 2 {
				err := call()
				var pause *QueuePause
				if !errors.As(err, &pause) || !errors.Is(err, ErrTryLater) || errors.Is(err, ErrNotFound) ||
					pause.Source != "fixture" || pause.Reason != "source_unavailable" || !pause.RetryAt.Equal(now.Add(2*time.Second)) {
					t.Fatalf("退避错误 %v，状态 %+v", err, pause)
				}
			}
			if pause := queue.Pause("fixture"); pause == nil || decoded != 0 || recovered != 0 || retries != 1 {
				t.Fatalf("来源退避 %+v，解码 %d 次，恢复 %d 次，重试等待 %d 次", pause, decoded, recovered, retries)
			}
			if count := strings.Count(logs.String(), `"error_code":"`+string(tc.code)+`"`); count != 2 {
				t.Fatalf("代理失败日志 %d 条，期望 2 条", count)
			}
			if strings.Contains(logs.String(), password) || strings.Contains(logs.String(), auth) {
				t.Fatal("来源日志泄露代理凭据")
			}

			egress.SetProxy(&working)
			now = now.Add(2 * time.Second)
			if err := call(); err != nil || decoded != 1 || recovered != 1 || queue.Pause("fixture") != nil {
				t.Fatalf("退避结束后请求错误 %v，解码 %d 次，恢复 %d 次", err, decoded, recovered)
			}
		})
	}
}
