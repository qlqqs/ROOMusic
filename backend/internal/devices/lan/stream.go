package lan

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

func ValidStreamURI(value string) bool {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "http" || u.Hostname() == "" || u.User != nil {
		return false
	}
	ip, err := netip.ParseAddr(u.Hostname())
	return err != nil || IsLANAddress(ip)
}

// 连接已校验的解析结果，拉取长流时只限制建连与响应头等待。
func NewStreamClient() *http.Client {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	tr := &http.Transport{ResponseHeaderTimeout: 5 * time.Second, DisableCompression: true,
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				ip = ip.Unmap()
				if IsLANAddress(ip) {
					return dialer.DialContext(ctx, "tcp4", net.JoinHostPort(ip.String(), port))
				}
			}
			return nil, fmt.Errorf("流地址应使用局域网 IPv4")
		}}
	return &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
