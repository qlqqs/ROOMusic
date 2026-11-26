package egress

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"weak"

	"golang.org/x/net/proxy"
)

// ProxyConfig 是管理员的实例代理。Bypass 只标记上游模拟服务，目标仍须通过原地址策略。
type ProxyConfig struct {
	Scheme                 string
	Host                   string
	Port                   int
	Username               string
	Password               string              `json:"-"`
	CredentialsUnavailable bool                `json:"-"`
	Bypass                 func(*url.URL) bool `json:"-"`
}

var activeProxy atomic.Pointer[ProxyConfig]
var transports sync.Map // 键为弱引用，短命的来源客户端可随请求回收。

func registerTransport(t *policyTransport) {
	ref := weak.Make(t)
	transports.Store(ref, struct{}{})
	runtime.AddCleanup(t, func(ref weak.Pointer[policyTransport]) { transports.Delete(ref) }, ref)
}

// SetProxy 以新的不可变快照切换路由，关闭上一配置的空闲连接。
func SetProxy(config *ProxyConfig) {
	if config != nil {
		snapshot := *config
		config = &snapshot
	}
	activeProxy.Store(config)
	transports.Range(func(key, _ any) bool {
		if t := key.(weak.Pointer[policyTransport]).Value(); t != nil {
			t.CloseIdleConnections()
		}
		return true
	})
}

type directAddressesKey struct{}

func (t *policyTransport) route(ctx context.Context, target *url.URL, allowed bool, cfg *ProxyConfig) (bool, []netip.Addr, error) {
	if cfg == nil || (cfg.Bypass != nil && cfg.Bypass(target)) {
		return false, nil, nil
	}
	addresses, err := t.addresses(ctx, target.Hostname(), allowed)
	if err != nil {
		if t.options.Policy == Unrestricted && ctx.Err() == nil {
			return true, nil, nil
		}
		return false, nil, err
	}
	for _, ip := range addresses {
		if IsPublicIP(ip) {
			return true, nil, nil
		}
	}
	// 直连只拨本次判定过的内网地址，避免二次解析改变路由。
	return false, addresses, nil
}

func (t *policyTransport) proxyPool() (*ProxyConfig, *http.Transport) {
	if activeProxy.Load() == nil {
		return nil, nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	cfg := activeProxy.Load()
	if cfg == nil {
		return nil, nil
	}
	if t.config != cfg {
		if t.proxied != nil {
			t.proxied.CloseIdleConnections()
		}
		t.config = cfg
		t.proxied = newProxyTransport(cfg)
	}
	return cfg, t.proxied
}

func newProxyTransport(cfg *ProxyConfig) *http.Transport {
	address := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	t := &http.Transport{ForceAttemptHTTP2: true, IdleConnTimeout: 90 * time.Second}
	if cfg.Scheme == "socks5" {
		var auth *proxy.Auth
		if cfg.Username != "" || cfg.Password != "" {
			auth = &proxy.Auth{User: cfg.Username, Password: cfg.Password}
		}
		dialer, _ := proxy.SOCKS5("tcp", address, auth, &net.Dialer{})
		t.DialContext = func(ctx context.Context, network, target string) (net.Conn, error) {
			conn, err := dialer.(proxy.ContextDialer).DialContext(ctx, network, target)
			if err != nil {
				return nil, socksError(err)
			}
			return conn, nil
		}
		return t
	}
	u := &url.URL{Scheme: "http", Host: address}
	if cfg.Username != "" || cfg.Password != "" {
		u.User = url.UserPassword(cfg.Username, cfg.Password)
	}
	t.Proxy = http.ProxyURL(u)
	t.OnProxyConnectResponse = func(_ context.Context, _ *url.URL, _ *http.Request, resp *http.Response) error {
		switch resp.StatusCode {
		case http.StatusOK:
			return nil
		case http.StatusProxyAuthRequired:
			return &Error{Code: CodeProxyAuth}
		case http.StatusGatewayTimeout:
			return context.DeadlineExceeded
		case http.StatusBadGateway:
			return errTargetUnreachable
		default:
			return &Error{Code: CodeProxyUnavailable}
		}
	}
	return t
}

var errTargetUnreachable = errors.New("代理目标不可达")

func socksError(err error) error {
	var op *net.OpError
	if errors.As(err, &op) {
		// x/net 的 SOCKS 握手没有导出认证与目标应答的错误类型。
		reason := op.Err.Error()
		switch {
		case reason == "no acceptable authentication methods", reason == "username/password authentication failed", reason == "invalid username/password", strings.HasPrefix(reason, "unsupported authentication method "):
			return &Error{Code: CodeProxyAuth}
		case reason == "unknown error network unreachable", reason == "unknown error host unreachable", reason == "unknown error connection refused", reason == "unknown error connection not allowed by ruleset":
			return errTargetUnreachable
		}
	}
	if errors.Is(err, context.Canceled) || isTimeout(err) {
		return err
	}
	return &Error{Code: CodeProxyUnavailable}
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// proxyRoundTrip 将代理应答与传输错误转换为固定类别，避免暴露代理返回的状态文本。
func proxyRoundTrip(t *http.Transport, r *http.Request) (*http.Response, error) {
	// TLS 握手或取得连接表明 CONNECT 已经完成。此前的 EOF 或无效应答来自代理。
	var connected atomic.Bool
	if t.Proxy != nil && r.URL.Scheme == "https" {
		trace := &httptrace.ClientTrace{
			TLSHandshakeStart: func() { connected.Store(true) },
			GotConn:           func(httptrace.GotConnInfo) { connected.Store(true) },
		}
		r = r.WithContext(httptrace.WithClientTrace(r.Context(), trace))
	}
	resp, err := t.RoundTrip(r)
	if err == nil {
		if t.Proxy != nil && r.URL.Scheme == "http" && resp.StatusCode == http.StatusProxyAuthRequired {
			resp.Body.Close()
			return nil, &Error{Code: CodeProxyAuth}
		}
		return resp, nil
	}
	var classified *Error
	switch {
	case errors.As(err, &classified):
		return nil, classified
	case isTimeout(err):
		return nil, context.DeadlineExceeded
	case errors.Is(err, context.Canceled):
		return nil, context.Canceled
	case errors.Is(err, errTargetUnreachable):
		return nil, errTargetUnreachable
	}
	var op *net.OpError
	if (errors.As(err, &op) && op.Op == "proxyconnect") || (t.Proxy != nil && r.URL.Scheme == "https" && !connected.Load()) {
		return nil, &Error{Code: CodeProxyUnavailable}
	}
	return nil, errTargetUnreachable
}

// ProbeProxy 用待测代理向固定公网地址发起一次请求，收到目标响应头即结束。
func ProbeProxy(ctx context.Context, cfg *ProxyConfig) string {
	if cfg.CredentialsUnavailable {
		return "proxy_auth_failed"
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://musicbrainz.org/", nil)
	t := newProxyTransport(cfg)
	defer t.CloseIdleConnections()
	resp, err := proxyRoundTrip(t, req)
	if err == nil {
		resp.Body.Close()
		return ""
	}
	var classified *Error
	switch {
	case isTimeout(err):
		return "timeout"
	case errors.As(err, &classified):
		if classified.Code == CodeProxyAuth {
			return "proxy_auth_failed"
		}
		return "proxy_unreachable"
	default:
		return "target_unreachable"
	}
}
