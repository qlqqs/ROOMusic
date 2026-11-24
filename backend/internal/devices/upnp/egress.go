// Package upnp 发现并控制局域网中的 UPnP 媒体渲染器。
package upnp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/lan"
)

const (
	descriptionLimit = 256 << 10
	soapLimit        = 64 << 10
	deviceTimeout    = 5 * time.Second
)

var ErrDeviceAddress = errors.New("设备地址必须是来源主机上的局域网 HTTP 地址")

func IsLANAddress(ip netip.Addr) bool {
	return lan.IsLANAddress(ip)
}

func parseDeviceURL(raw string, base *url.URL) (*url.URL, error) {
	if raw == "" {
		return nil, ErrDeviceAddress
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, ErrDeviceAddress
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	if u.Hostname() == "" || u.Scheme != "http" || u.User != nil || u.Fragment != "" {
		return nil, ErrDeviceAddress
	}
	if port := u.Port(); port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n == 0 {
			return nil, ErrDeviceAddress
		}
	}
	return u, nil
}

// SSDP 通告只接受与 UDP 来源相等的 IPv4 字面地址。
func deviceURL(raw string, base *url.URL, source netip.Addr) (*url.URL, error) {
	u, err := parseDeviceURL(raw, base)
	if err != nil {
		return nil, err
	}
	ip, err := netip.ParseAddr(u.Hostname())
	if err != nil || !IsLANAddress(ip) || ip != source.Unmap() {
		return nil, ErrDeviceAddress
	}
	return u, nil
}

func sameDeviceURL(raw string, base *url.URL, host string) (*url.URL, error) {
	u, err := parseDeviceURL(raw, base)
	if err != nil || !strings.EqualFold(u.Hostname(), host) {
		return nil, ErrDeviceAddress
	}
	return u, nil
}

// 手动描述地址按次解析；后续请求固定使用本次选定的局域网 IPv4。
func resolveDevice(ctx context.Context, host string) (netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		if IsLANAddress(ip) {
			return ip, nil
		}
		return netip.Addr{}, ErrDeviceAddress
	}
	ctx, cancel := context.WithTimeout(ctx, deviceTimeout)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
	if err != nil {
		return netip.Addr{}, err
	}
	for _, addr := range addresses {
		if ip := addr.Unmap(); IsLANAddress(ip) {
			return ip, nil
		}
	}
	return netip.Addr{}, ErrDeviceAddress
}

// 目的地址已固定为 IPv4 字面量；禁用代理、空闲连接及重定向。
var deviceHTTP = &http.Client{
	Transport:     &http.Transport{DisableKeepAlives: true, MaxResponseHeaderBytes: 64 << 10},
	Timeout:       deviceTimeout,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func readResponse(req *http.Request, peerIP netip.Addr, limit int64) ([]byte, int, error) {
	if !IsLANAddress(peerIP) {
		return nil, 0, ErrDeviceAddress
	}
	// HTTP Host 仍为设备声明的主机与端口，TCP 连接不再经过 DNS。
	req.Host = req.URL.Host
	address := peerIP.String()
	if port := req.URL.Port(); port != "" {
		address = net.JoinHostPort(address, port)
	}
	req.URL.Host = address
	resp, err := deviceHTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, 0, err
	}
	if int64(len(data)) > limit {
		return nil, 0, fmt.Errorf("设备响应超过 %d 字节", limit)
	}
	return data, resp.StatusCode, nil
}

func fetchDescription(ctx context.Context, u *url.URL, peerIP netip.Addr) ([]byte, string, error) {
	var local string
	trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		if addr, ok := info.Conn.LocalAddr().(*net.TCPAddr); ok {
			local = addr.IP.String()
		}
	}}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", err
	}
	data, status, err := readResponse(req, peerIP, descriptionLimit)
	if err != nil {
		return nil, "", err
	}
	if status != http.StatusOK {
		return nil, "", fmt.Errorf("设备描述返回 HTTP %d", status)
	}
	return data, local, nil
}
