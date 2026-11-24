package fakerenderer

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/lan"
	"golang.org/x/net/ipv4"
	"golang.org/x/sys/unix"
)

var group = net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 1900}

func advertisedAddr(listener net.Listener) (*net.Interface, net.IP, string, error) {
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return nil, nil, "", fmt.Errorf("需要 TCP 监听地址")
	}
	ip := addr.IP
	if ip.IsUnspecified() {
		c, err := net.DialUDP("udp4", nil, &group)
		if err != nil {
			return nil, nil, "", err
		}
		ip = c.LocalAddr().(*net.UDPAddr).IP
		c.Close()
	}
	a, ok := netip.AddrFromSlice(ip)
	if !ok || !lan.IsLANAddress(a.Unmap()) {
		return nil, nil, "", fmt.Errorf("SSDP 公告需要局域网 IPv4")
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, nil, "", err
	}
	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addrNet := range addrs {
			if subnet, ok := addrNet.(*net.IPNet); ok && subnet.IP.Equal(ip) {
				return &iface, ip, "http://" + net.JoinHostPort(ip.String(), strconv.Itoa(addr.Port)), nil
			}
		}
	}
	return nil, nil, "", fmt.Errorf("找不到公告网卡")
}

// 多个假渲染器和控制点可以共享 1900 端口。
func listenSSDP(ctx context.Context, addr string) (net.PacketConn, error) {
	config := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
		var sockErr error
		if err := c.Control(func(fd uintptr) { sockErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1) }); err != nil {
			return err
		}
		return sockErr
	}}
	return config.ListenPacket(ctx, "udp4", addr)
}

func searchTarget(data []byte, udn string) string {
	req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(data)))
	if err != nil || req.Method != "M-SEARCH" || req.RequestURI != "*" || !strings.EqualFold(strings.Trim(req.Header.Get("Man"), `"`), "ssdp:discover") {
		return ""
	}
	st := req.Header.Get("ST")
	switch st {
	case deviceType, udn, "upnp:rootdevice":
		return st
	case "ssdp:all":
		return deviceType
	}
	return ""
}

func (r *Renderer) serveSearches(recv net.PacketConn, send *net.UDPConn, location string) error {
	udn := r.State().UDN
	var packet [8192]byte
	for {
		n, addr, err := recv.ReadFrom(packet[:])
		if err != nil {
			return err
		}
		st := searchTarget(packet[:n], udn)
		if st == "" {
			continue
		}
		usn := udn
		if st != udn {
			usn += "::" + st
		}
		msg := fmt.Sprintf("HTTP/1.1 200 OK\r\nCACHE-CONTROL: max-age=1800\r\nEXT:\r\nLOCATION: %s\r\nSERVER: ROOMusic/1 UPnP/1.0 FakeRenderer/1\r\nST: %s\r\nUSN: %s\r\n\r\n", location, st, usn)
		if _, err := send.WriteTo([]byte(msg), addr); err != nil {
			return err
		}
	}
}

func (r *Renderer) announce(c *net.UDPConn, location, event string) error {
	msg := fmt.Sprintf("NOTIFY * HTTP/1.1\r\nHOST: %s\r\nNT: %s\r\nNTS: ssdp:%s\r\nUSN: %s::%s\r\n", group.String(), deviceType, event, r.State().UDN, deviceType)
	if event == "alive" {
		msg += "CACHE-CONTROL: max-age=1800\r\nLOCATION: " + location + "\r\nSERVER: ROOMusic/1 UPnP/1.0 FakeRenderer/1\r\n"
	}
	_, err := c.WriteToUDP([]byte(msg+"\r\n"), &group)
	return err
}

// Serve 接管 HTTP 监听器。ready 收到公告中的 HTTP 基址时，SSDP 和 HTTP 监听均已就绪。
func (r *Renderer) Serve(ctx context.Context, listener net.Listener, ready func(string)) error {
	defer listener.Close()
	defer r.Close()
	iface, ip, base, err := advertisedAddr(listener)
	if err != nil {
		return err
	}
	recv, err := listenSSDP(ctx, ":1900")
	if err != nil {
		return err
	}
	defer recv.Close()
	if err := ipv4.NewPacketConn(recv).JoinGroup(iface, &group); err != nil {
		return err
	}
	// 内核优先将发往公告 IP 的单播交给精确绑定的套接字，避免同机控制点的通配监听抢收。
	unicast, err := listenSSDP(ctx, net.JoinHostPort(ip.String(), strconv.Itoa(group.Port)))
	if err != nil {
		return err
	}
	defer unicast.Close()
	send, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip})
	if err != nil {
		return err
	}
	defer send.Close()
	multicast := ipv4.NewPacketConn(send)
	if err := multicast.SetMulticastInterface(iface); err != nil {
		return err
	}
	if err := multicast.SetMulticastTTL(2); err != nil {
		return err
	}
	if err := multicast.SetMulticastLoopback(true); err != nil {
		return err
	}

	server := &http.Server{Handler: r.Handler(), ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
	defer server.Close()
	done := make(chan error, 3)
	location := base + "/description.xml"
	go func() { done <- server.Serve(listener) }()
	go func() { done <- r.serveSearches(recv, send, location) }()
	go func() { done <- r.serveSearches(unicast, send, location) }()
	if err := r.announce(send, location, "alive"); err != nil {
		return err
	}
	defer r.announce(send, location, "byebye")
	if ready != nil {
		ready(base)
	}
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-done:
			if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		case <-ticker.C:
			if err := r.announce(send, location, "alive"); err != nil {
				return err
			}
		}
	}
}
