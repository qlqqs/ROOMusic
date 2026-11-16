// Package egress 提供管家统一的出站 HTTP 客户端与连接地址策略。
package egress

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"time"
)

// Policy 是调用方按请求来源与用户身份选择的出站策略。
type Policy int

const (
	// PublicOnly 只允许公网地址，用于所有用户的网页抓取。
	PublicOnly Policy = iota
	// PublicOrAllowlist 允许公网或放行名单地址，用于成员填写的端点。
	PublicOrAllowlist
	// Unrestricted 不限制地址，用于管理员填写的端点。
	Unrestricted
)

// Options 是客户端的策略与连接参数，不读取环境变量。
type Options struct {
	// Policy 指定地址规则，零值为 PublicOnly。
	Policy Policy
	// Allowlist 按请求 URL 的主机名和有效端口匹配；nil 表示没有放行项。
	// 仅 PublicOrAllowlist 使用，调用方须保证并发安全。
	Allowlist func(host string, port int) bool
	// LookupNetIP 替换地址解析；nil 使用独立的 net.Resolver，network 为 ip。
	LookupNetIP func(ctx context.Context, network, host string) ([]netip.Addr, error)
	// MaxRedirects 是允许跟随的重定向次数，零表示不允许跟随。
	MaxRedirects int
	// Timeout 是覆盖重定向和响应读取的总超时；零表示不设置超时。
	Timeout time.Duration
}

// NewClient 构造只允许 HTTP 与 HTTPS、禁用环境代理的客户端。
// 调用方应保留返回的 Transport 和 CheckRedirect，以维持策略约束。
func NewClient(options Options) (*http.Client, error) {
	if options.Policy != PublicOnly && options.Policy != PublicOrAllowlist && options.Policy != Unrestricted {
		return nil, errors.New("无效的出站策略")
	}
	if options.MaxRedirects < 0 || options.Timeout < 0 {
		return nil, errors.New("出站重定向次数与超时不能为负数")
	}
	if options.LookupNetIP == nil {
		options.LookupNetIP = (&net.Resolver{}).LookupNetIP
	}
	transport := &policyTransport{options: options}
	transport.transport = &http.Transport{
		DialContext:       transport.dialContext,
		ForceAttemptHTTP2: true,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   options.Timeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if err := checkScheme(request); err != nil {
				return err
			}
			if len(via) > options.MaxRedirects {
				return &Error{Code: CodeTooManyRedirects}
			}
			return nil
		},
	}, nil
}

type allowlistKey struct{}

type policyTransport struct {
	options   Options
	transport *http.Transport
}

// RoundTrip 检查协议并把本次请求的放行结果传给拨号边界。
func (transport *policyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := checkScheme(request); err != nil {
		return nil, err
	}
	allowed, err := transport.allowed(request.URL)
	if err != nil {
		return nil, err
	}
	ctx := context.WithValue(request.Context(), allowlistKey{}, allowed)
	return transport.transport.RoundTrip(request.Clone(ctx))
}

// CloseIdleConnections 允许 http.Client 关闭底层空闲连接。
func (transport *policyTransport) CloseIdleConnections() {
	transport.transport.CloseIdleConnections()
}

func checkScheme(request *http.Request) error {
	if request.URL.Scheme != "http" && request.URL.Scheme != "https" {
		return &Error{Code: CodeSchemeNotAllowed}
	}
	return nil
}

func (transport *policyTransport) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	allowed, _ := ctx.Value(allowlistKey{}).(bool)
	addresses, err := transport.addresses(ctx, host, allowed)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{}
	for _, ip := range addresses {
		connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		err = dialErr
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, err
}

// IsPublicIP 按 ADR 0005 判断实际 IP，排除带 zone 的地址及本地、特殊用途网段。
// IPv4 映射的 IPv6 地址先还原为 IPv4，再应用相同规则。
func IsPublicIP(ip netip.Addr) bool {
	if ip.Zone() != "" {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	blocked := [...]netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),       // 当前网络地址，不能作为公网目标。
		netip.MustParsePrefix("100.64.0.0/10"),   // 运营商级 NAT 的共享地址空间。
		netip.MustParsePrefix("192.0.0.0/24"),    // IETF 协议分配的特殊用途地址。
		netip.MustParsePrefix("192.0.2.0/24"),    // 文档示例地址 TEST-NET-1。
		netip.MustParsePrefix("192.88.99.0/24"),  // 已弃用的 6to4 中继地址。
		netip.MustParsePrefix("198.18.0.0/15"),   // 网络设备基准测试地址。
		netip.MustParsePrefix("198.51.100.0/24"), // 文档示例地址 TEST-NET-2。
		netip.MustParsePrefix("203.0.113.0/24"),  // 文档示例地址 TEST-NET-3。
		netip.MustParsePrefix("240.0.0.0/4"),     // 保留地址空间，包含有限广播地址。
		netip.MustParsePrefix("::/96"),           // IPv4 兼容地址，可内嵌非公网 IPv4。
		netip.MustParsePrefix("::ffff:0:0:0/96"), // IPv4 转换地址，可内嵌非公网 IPv4。
		netip.MustParsePrefix("64:ff9b::/96"),    // NAT64 知名前缀，可内嵌私有 IPv4。
		netip.MustParsePrefix("64:ff9b:1::/48"),  // NAT64 本地用途前缀，可内嵌私有 IPv4。
		netip.MustParsePrefix("2002::/16"),       // 6to4 隧道地址，可转向非公网 IPv4。
		netip.MustParsePrefix("2001::/32"),       // Teredo 隧道地址，可转向非公网 IPv4。
		netip.MustParsePrefix("2001:2::/48"),     // IPv6 基准测试地址。
		netip.MustParsePrefix("2001:10::/28"),    // 已弃用的 ORCHID 标识地址。
		netip.MustParsePrefix("2001:db8::/32"),   // IPv6 文档示例地址。
		netip.MustParsePrefix("3fff::/20"),       // IPv6 文档示例地址。
		netip.MustParsePrefix("100::/64"),        // IPv6 丢弃用途地址。
	}
	for _, prefix := range blocked {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

// CheckEndpoint 在开始响应前检查端点的当前解析结果，不建立连接。
// client 必须由 NewClient 构造；实际拨号及重定向仍会重新检查，预检不授予后续请求权限。
func CheckEndpoint(ctx context.Context, client *http.Client, endpoint string) error {
	transport, ok := client.Transport.(*policyTransport)
	if !ok {
		return errors.New("出站预检需要策略客户端")
	}
	address, err := url.Parse(endpoint)
	if err != nil || address.Hostname() == "" {
		return &Error{Code: CodeAddressNotAllowed}
	}
	if err := checkScheme(&http.Request{URL: address}); err != nil {
		return err
	}
	allowed, err := transport.allowed(address)
	if err != nil {
		return err
	}
	_, err = transport.addresses(ctx, address.Hostname(), allowed)
	return err
}

func (transport *policyTransport) allowed(address *url.URL) (bool, error) {
	port := 80
	if address.Scheme == "https" {
		port = 443
	}
	if explicit := address.Port(); explicit != "" {
		var err error
		port, err = strconv.Atoi(explicit)
		if err != nil || port < 1 || port > 65535 {
			return false, &Error{Code: CodeAddressNotAllowed}
		}
	}
	if transport.options.Policy == Unrestricted {
		return true, nil
	}
	if transport.options.Policy == PublicOrAllowlist && transport.options.Allowlist != nil {
		return transport.options.Allowlist(address.Hostname(), port), nil
	}
	return false, nil
}

func (transport *policyTransport) addresses(ctx context.Context, host string, allowed bool) ([]netip.Addr, error) {
	var addresses []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		addresses = []netip.Addr{ip}
	} else {
		var err error
		addresses, err = transport.options.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
	}
	if len(addresses) == 0 {
		return nil, &net.DNSError{Err: "no addresses", Name: host, IsNotFound: true}
	}
	// 混合解析结果中只要有禁用地址就拒绝，不让地址顺序决定策略结果。
	if !allowed {
		for _, ip := range addresses {
			if !IsPublicIP(ip) {
				return nil, &Error{Code: CodeAddressNotAllowed}
			}
		}
	}
	return addresses, nil
}
