package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/networkproxy"
	"github.com/qlqq/roomusic/backend/internal/platform/diagnostics"
	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
)

const (
	outboundProxyPath    = "/api/v1/admin/network/proxy"
	outboundTestUser     = "proxy-fixture-user"
	outboundTestPassword = "proxy-test-synthetic+password"
)

func TestPostgreSQLOutboundProxySettings(t *testing.T) {
	logger, logs := jsonLogger()
	ring := diagnostics.NewLogRing()
	a := newIntegrationTestApplicationWithLogger(t, logger, WithDiagnostics(ring, time.Now()))
	t.Cleanup(func() { egress.SetProxy(nil) })
	admin, member := a.createUser(t, "admin"), a.createUser(t, "user")
	auth := outboundBasicAuth()
	var accepted, rejected atomic.Int32
	var refuseAuth atomic.Bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if refuseAuth.Load() || r.Header.Get("Proxy-Authorization") != auth {
			rejected.Add(1)
			http.Error(w, outboundTestPassword, http.StatusProxyAuthRequired)
			return
		}
		accepted.Add(1)
		if r.URL.String() != "http://saved-proxy.invalid/fixture" {
			t.Errorf("代理收到意外目标 %q", r.URL.String())
		}
		io.WriteString(w, `{"fixture":true}`)
	}))
	t.Cleanup(proxy.Close)
	port := proxy.Listener.Addr().(*net.TCPAddr).Port
	host := "127.0.0.1"
	body := map[string]any{"enabled": true, "scheme": "http", "host": host, "port": port, "username": outboundTestUser, "version": 1}
	request := func(method, path string, payload any) *httptest.ResponseRecorder {
		t.Helper()
		response := a.request(t, method, path, payload, &admin, nil)
		requireOutboundRedaction(t, "管理员接口", response.Body.String())
		return response
	}
	get := func() networkproxy.Settings {
		t.Helper()
		return zonesSampleDecode[networkproxy.Settings](t, request(http.MethodGet, outboundProxyPath, nil), http.StatusOK)
	}
	put := func() networkproxy.Settings {
		t.Helper()
		return zonesSampleDecode[networkproxy.Settings](t, request(http.MethodPut, outboundProxyPath, body), http.StatusOK)
	}
	readCiphertext := func() []byte {
		t.Helper()
		var value []byte
		if err := a.database.QueryRowContext(t.Context(), `SELECT password_ciphertext FROM outbound_proxy_settings WHERE id=1`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}

	initial := get()
	if initial.Enabled || initial.Scheme != "http" || initial.Host != "" || initial.Port != nil ||
		initial.Username != "" || initial.PasswordSet || initial.Version != 1 {
		t.Fatalf("GET 初始值 %+v", initial)
	}
	for _, call := range []struct{ method, path string }{
		{http.MethodGet, outboundProxyPath},
		{http.MethodPut, outboundProxyPath},
		{http.MethodPost, outboundProxyPath + "/test"},
	} {
		requireAPIError(t, a.request(t, call.method, call.path, body, &member, nil), http.StatusForbidden, "forbidden")
	}
	for _, field := range []string{"host", "port"} {
		value := body[field]
		delete(body, field)
		requireAPIError(t, request(http.MethodPut, outboundProxyPath, body), http.StatusBadRequest, "invalid_input")
		body[field] = value
	}

	client, err := egress.NewClient(egress.Options{
		Policy: egress.PublicOnly, LookupNetIP: outboundFixtureLookup, Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.CloseIdleConnections)
	body["password"] = outboundTestPassword
	saved := put()
	if !saved.Enabled || saved.Scheme != "http" || saved.Host != host || saved.Port == nil || *saved.Port != port ||
		saved.Username != outboundTestUser || !saved.PasswordSet || saved.Version != 2 {
		t.Fatalf("PUT 后的设置 %+v", saved)
	}
	requireOutboundActive(t, client, &accepted, 1)
	ciphertext := readCiphertext()
	if len(ciphertext) == 0 || bytes.Contains(ciphertext, []byte(outboundTestPassword)) {
		t.Fatal("密码列没有保存密文")
	}
	plain, err := newInstanceSecrets(a.dataDirectory).Open("instance", "outbound_proxy", ciphertext)
	if err != nil || string(plain) != outboundTestPassword {
		t.Fatal("密码列不能按实例和用途解密")
	}

	requireAPIError(t, request(http.MethodPut, outboundProxyPath, body), http.StatusConflict, "version_conflict")
	if got := get(); got.Version != saved.Version || !got.PasswordSet {
		t.Fatalf("版本冲突后的 GET 返回 %+v", got)
	}

	delete(body, "password")
	body["version"] = saved.Version
	kept := put()
	if !kept.PasswordSet || kept.Version != saved.Version+1 || !bytes.Equal(readCiphertext(), ciphertext) {
		t.Fatalf("省略密码保存后的设置 %+v", kept)
	}
	requireOutboundActive(t, client, &accepted, 2)

	egress.SetProxy(nil)
	service := networkproxy.New(a.database, newInstanceSecrets(a.dataDirectory), egress.SetProxy)
	if err := service.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	requireOutboundActive(t, client, &accepted, 3)

	source := sources.NewRequester(sources.Declaration{ID: "fixture"}, sources.Options{
		Logger:  slog.New(diagnostics.NewHandler(logger.Handler(), ring)),
		Limiter: sources.NewLimiter(0, nil, nil),
		NewClient: func(opts egress.Options) (*http.Client, error) {
			opts.LookupNetIP = outboundFixtureLookup
			return egress.NewClient(opts)
		},
	})
	const sourceEvent = "sources.request.finished"
	const authError = "proxy_auth"
	requireAuthRejected := func(want int32) {
		t.Helper()
		err := source.GetJSON(t.Context(), "http://saved-proxy.invalid/fixture", func([]byte) (bool, bool) {
			t.Error("代理拒绝后仍调用了解码器")
			return false, false
		})
		if !errors.Is(err, sources.ErrUnavailable) || rejected.Load() != want {
			t.Fatalf("来源错误 %v，代理拒绝 %d 次，期望 %d 次", err, rejected.Load(), want)
		}
	}
	refuseAuth.Store(true)
	requireAuthRejected(1)
	refuseAuth.Store(false)

	body["password"] = ""
	body["version"] = kept.Version
	cleared := put()
	if cleared.PasswordSet || cleared.Version != kept.Version+1 || readCiphertext() != nil || get().PasswordSet {
		t.Fatalf("清除密码后的设置 %+v", cleared)
	}
	requireAuthRejected(2)
	export := request(http.MethodGet, "/api/v1/admin/diagnostics", nil)
	diagnostic := zonesSampleDecode[struct {
		Logs []struct{ Fields map[string]any }
	}](t, export, http.StatusOK)
	var failures int
	for _, event := range diagnostic.Logs {
		if event.Fields["event"] == sourceEvent && event.Fields["error_code"] == authError {
			failures++
		}
	}
	requireOutboundRedaction(t, "应用日志", logs.String())
	if failures != 2 || strings.Count(logs.String(), `"error_code":"proxy_auth"`) != 2 {
		t.Fatalf("诊断导出包含 %d 条代理认证失败事件，期望导出和应用日志各有两条", failures)
	}
}

func outboundFixtureLookup(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
}

func outboundBasicAuth() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(outboundTestUser+":"+outboundTestPassword))
}

func requireOutboundRedaction(t *testing.T, where, content string) {
	t.Helper()
	for _, secret := range []string{outboundTestPassword, strings.TrimPrefix(outboundBasicAuth(), "Basic "), `"password":`, `"password_ciphertext":`} {
		if strings.Contains(content, secret) {
			t.Fatalf("%s 泄露代理凭据或字段", where)
		}
	}
}

func requireOutboundActive(t *testing.T, client *http.Client, accepted *atomic.Int32, want int32) {
	t.Helper()
	resp, err := client.Get("http://saved-proxy.invalid/fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK || string(body) != `{"fixture":true}` || accepted.Load() != want {
		t.Fatalf("出站得到 HTTP %d、代理命中 %d 次、读取错误 %v", resp.StatusCode, accepted.Load(), err)
	}
}

func TestPostgreSQLOutboundProxyProbe(t *testing.T) {
	postgrestest.BaseURL(t)
	const fixtureAddressEnv = "ROOMUSIC_TEST_OUTBOUND_PROXY_SUCCESS_ADDRESS"
	successAddress := os.Getenv(fixtureAddressEnv)
	if successAddress == "" {
		// 根证书缓存属于进程。在独立测试进程中载入临时信任根。
		cert, root := outboundProbeCertificate(t)
		var hits atomic.Int32
		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodConnect || r.Host != "musicbrainz.org:443" || r.Header.Get("Proxy-Authorization") != outboundBasicAuth() {
				t.Error("探测 CONNECT 目标或认证错误")
				w.WriteHeader(http.StatusProxyAuthRequired)
				return
			}
			conn, buffered, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
			if err := buffered.Flush(); err != nil {
				t.Error(err)
				return
			}
			secure := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}})
			defer secure.Close()
			request, err := http.ReadRequest(bufio.NewReader(secure))
			if err != nil {
				t.Error(err)
				return
			}
			defer request.Body.Close()
			if request.Method != http.MethodGet || request.Host != "musicbrainz.org" || request.URL.Path != "/" || request.Header.Get("Proxy-Authorization") != "" {
				t.Error("探测目标收到错误的请求")
			}
			hits.Add(1)
			io.WriteString(secure, "HTTP/1.1 302 Found\r\nLocation: https://must-not-follow.invalid/\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		}))
		t.Cleanup(proxy.Close)
		ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
		defer cancel()
		child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPostgreSQLOutboundProxyProbe$", "-test.v")
		child.Env = append(os.Environ(), fixtureAddressEnv+"="+proxy.Listener.Addr().String(), "SSL_CERT_FILE="+root)
		output, err := child.CombinedOutput()
		if err != nil || hits.Load() != 1 {
			t.Fatalf("探测测试进程错误 %v，目标命中 %d 次\n%s", err, hits.Load(), output)
		}
		t.Logf("探测测试进程通过\n%s", output)
		return
	}

	a := newIntegrationTestApplication(t)
	t.Cleanup(func() { egress.SetProxy(nil) })
	admin := a.createUser(t, "admin")
	successPort, err := net.ResolveTCPAddr("tcp", successAddress)
	if err != nil {
		t.Fatal(err)
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	closedPort := closed.Listener.Addr().(*net.TCPAddr).Port
	closed.Close()
	type probeResult struct {
		OK        bool    `json:"ok"`
		Code      *string `json:"code"`
		LatencyMS *int64  `json:"latency_ms"`
	}
	body := map[string]any{"scheme": "http", "host": "127.0.0.1", "port": successPort.Port, "username": outboundTestUser}
	saved := make(map[string]any)
	for key, value := range body {
		saved[key] = value
	}
	saved["password"], saved["enabled"], saved["version"] = outboundTestPassword, false, 1
	put := a.request(t, http.MethodPut, outboundProxyPath, saved, &admin, nil)
	requireOutboundRedaction(t, "PUT 响应", put.Body.String())
	settings := zonesSampleDecode[networkproxy.Settings](t, put, http.StatusOK)
	if !settings.PasswordSet {
		t.Fatal("探测前未保存密码")
	}
	for _, tc := range []struct {
		name, code string
		status     int
	}{
		{name: "success"},
		{name: "unreachable", code: "proxy_unreachable"},
		{name: "authentication", code: "proxy_auth_failed", status: http.StatusProxyAuthRequired},
		{name: "target", code: "target_unreachable", status: http.StatusBadGateway},
		{name: "timeout", code: "timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			switch tc.name {
			case "success":
				// 未传密码时，探测使用库中的凭据。
			case "unreachable":
				body["port"] = closedPort
			default:
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodConnect || r.Host != "musicbrainz.org:443" {
						t.Errorf("探测收到 %s %s", r.Method, r.Host)
					}
					if tc.name == "authentication" && r.Header.Get("Proxy-Authorization") == outboundBasicAuth() {
						t.Error("探测忽略了显式提交的错误密码")
					}
					if tc.name == "timeout" {
						<-r.Context().Done()
						return
					}
					http.Error(w, outboundTestPassword, tc.status)
				}))
				t.Cleanup(server.Close)
				body["port"] = server.Listener.Addr().(*net.TCPAddr).Port
				if tc.name == "authentication" {
					body["password"] = "wrong-synthetic-password"
					t.Cleanup(func() { delete(body, "password") })
				}
			}
			response := a.request(t, http.MethodPost, outboundProxyPath+"/test", body, &admin, nil)
			requireOutboundRedaction(t, "测试连接响应", response.Body.String())
			got := zonesSampleDecode[probeResult](t, response, http.StatusOK)
			if got.OK != (tc.code == "") || (tc.code == "" && got.Code != nil) ||
				(tc.code != "" && (got.Code == nil || *got.Code != tc.code)) || got.LatencyMS == nil || *got.LatencyMS < 0 {
				t.Fatalf("连接测试结果 %s，期望原因 %q", response.Body, tc.code)
			}
			if tc.name == "timeout" && *got.LatencyMS < 9500 {
				t.Fatalf("探测提前超时，耗时 %d 毫秒", *got.LatencyMS)
			}
		})
	}
	result := a.request(t, http.MethodGet, outboundProxyPath, nil, &admin, nil)
	current := zonesSampleDecode[networkproxy.Settings](t, result, http.StatusOK)
	if current.Enabled || current.Version != settings.Version || current.Port == nil || *current.Port != successPort.Port || !current.PasswordSet {
		t.Fatalf("探测改变了已存设置 %+v", current)
	}
}

func outboundProbeCertificate(t *testing.T) (tls.Certificate, string) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), DNSNames: []string{"musicbrainz.org"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		BasicConstraintsValid: true, IsCA: true,
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "outbound-proxy-root.pem")
	if err := os.WriteFile(root, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, root
}

func TestPostgreSQLOutboundProxyCurationBackoff(t *testing.T) {
	f := newProgramFixture(t)
	t.Cleanup(func() { egress.SetProxy(nil) })
	f.enable(t)
	f.tagged(t)
	closed := httptest.NewServer(http.NotFoundHandler())
	port := closed.Listener.Addr().(*net.TCPAddr).Port
	closed.Close()
	body := map[string]any{"enabled": true, "scheme": "http", "host": "127.0.0.1", "port": port, "version": 1}
	saved := f.application.request(t, http.MethodPut, outboundProxyPath, body, &f.admin, nil)
	if saved.Code != http.StatusOK {
		t.Fatalf("保存不可用代理得到 HTTP %d", saved.Code)
	}

	var attempts, retryWaits atomic.Int32
	music := musicbrainz.New(musicbrainz.Options{
		Limiter: sources.NewLimiter(0, nil, nil),
		NewClient: func(opts egress.Options) (*http.Client, error) {
			attempts.Add(1)
			opts.LookupNetIP = outboundFixtureLookup
			return egress.NewClient(opts)
		},
		After: func(time.Duration) <-chan time.Time {
			retryWaits.Add(1)
			ready := make(chan time.Time, 1)
			ready <- f.clock.options().Now()
			return ready
		},
	})
	f.gate = curation.NewSources(f.application.database,
		curation.SourceRegistration{Declaration: musicbrainz.Declaration(), Releases: music, Recordings: music},
	).WithScanCheck(f.scanCheck)
	f.run(t)
	requireUnattemptedProgramState(t, f.stateFor(t, "release", f.a))
	queues, err := f.worker.QueueProgress(t.Context())
	requireCurationOK(t, err)
	queue := queues[0]
	if queue.ID != "release" || queue.State != "paused" || queue.PauseReason == nil || *queue.PauseReason != "source_unavailable" ||
		queue.Source == nil || *queue.Source != "musicbrainz" || queue.RetryAt == nil || !queue.RetryAt.Equal(f.clock.options().Now().Add(2*time.Second)) {
		t.Fatalf("代理断开后的整理队列 %+v", queue)
	}
	if attempts.Load() != 2 || retryWaits.Load() != 1 {
		t.Fatalf("真实请求尝试 %d 次、等待重试 %d 次", attempts.Load(), retryWaits.Load())
	}
}
