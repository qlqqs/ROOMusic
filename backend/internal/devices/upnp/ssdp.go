package upnp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/ipv4"
)

const (
	rendererTarget  = "urn:schemas-upnp-org:device:MediaRenderer:1"
	ssdpGroup       = "239.255.255.250:1900"
	ssdpPacketLimit = 8192
)

type announcement struct {
	udn, location string
	source, local netip.Addr
	expires       time.Time
	byebye        bool
}

func searchPacket(host string) []byte {
	return []byte("M-SEARCH * HTTP/1.1\r\nHOST: " + host + "\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\nST: " + rendererTarget + "\r\n\r\n")
}

func parseSSDP(data []byte, ip netip.Addr, notify bool, now time.Time) (announcement, bool) {
	var a announcement
	if len(data) > ssdpPacketLimit || !IsLANAddress(ip) {
		return a, false
	}
	r := textproto.NewReader(bufio.NewReader(bytes.NewReader(data)))
	line, err := r.ReadLine()
	if err != nil {
		return a, false
	}
	if notify {
		if line != "NOTIFY * HTTP/1.1" {
			return a, false
		}
	} else if !strings.HasPrefix(line+" ", "HTTP/1.1 200 ") {
		return a, false
	}
	h, err := r.ReadMIMEHeader()
	if err != nil {
		return a, false
	}
	target := h.Get("ST")
	if notify {
		target = h.Get("NT")
	}
	if upnpType(target, "device") != "MediaRenderer" {
		return a, false
	}
	a.udn, _, _ = strings.Cut(h.Get("USN"), "::")
	if !strings.HasPrefix(a.udn, "uuid:") || len(a.udn) <= 5 {
		return a, false
	}
	a.source = ip
	if notify {
		switch strings.ToLower(h.Get("NTS")) {
		case "ssdp:byebye":
			a.byebye = true
			return a, true
		case "ssdp:alive", "ssdp:update":
		default:
			return a, false
		}
	}
	loc, err := deviceURL(h.Get("LOCATION"), nil, ip)
	if err != nil {
		return a, false
	}
	a.location = loc.String()
	for _, directive := range strings.Split(h.Get("Cache-Control"), ",") {
		key, val, ok := strings.Cut(directive, "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "max-age") {
			sec, err := strconv.ParseUint(strings.Trim(strings.TrimSpace(val), "\""), 10, 32)
			if err != nil {
				return a, false
			}
			a.expires = now.Add(time.Duration(sec) * time.Second)
			return a, true
		}
	}
	return a, false
}

var listLinks = lanLinks

type ssdpLink struct {
	addr           netip.Addr
	stopped        atomic.Bool
	readers        sync.WaitGroup
	search, notify *net.UDPConn
}

func lanLinks() (map[int]netip.Addr, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	links := make(map[int]netip.Addr)
	var lastErr error
	for _, iface := range interfaces {
		if iface.Flags&(net.FlagUp|net.FlagMulticast) != net.FlagUp|net.FlagMulticast {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			lastErr = err
			continue
		}
		for _, addr := range addrs {
			prefix, err := netip.ParsePrefix(addr.String())
			if err != nil || !IsLANAddress(prefix.Addr()) {
				continue
			}
			links[iface.Index] = prefix.Addr()
			break
		}
	}
	if len(links) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return links, nil
}

func openLink(iface net.Interface, addr netip.Addr, group *net.UDPAddr) (*ssdpLink, error) {
	conn, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(addr, 0)))
	if err != nil {
		return nil, err
	}
	p := ipv4.NewPacketConn(conn)
	if err = p.SetMulticastInterface(&iface); err == nil {
		err = p.SetMulticastTTL(2)
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	notify, err := net.ListenMulticastUDP("udp4", &iface, group)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return &ssdpLink{addr: addr, search: conn, notify: notify}, nil
}

func (l *ssdpLink) listen(ctx context.Context, index int, logger *slog.Logger, receive func(announcement)) {
	read := func(conn *net.UDPConn, local netip.Addr, notify bool) {
		err := readAnnouncements(conn, local, notify, receive)
		if !l.stopped.Swap(true) && ctx.Err() == nil {
			logger.Warn("SSDP 接收中断，下轮重开网卡套接字", "event", "ssdp.receive.failed", "interface_index", index, "error", err)
		}
	}
	// 通知共享监听端口，由描述连接确认本机地址；搜索响应使用发出请求的网卡地址。
	l.readers.Go(func() { read(l.notify, netip.Addr{}, true) })
	l.readers.Go(func() { read(l.search, l.addr, false) })
}

func (l *ssdpLink) close() {
	l.stopped.Store(true)
	l.search.Close()
	l.notify.Close()
	l.readers.Wait()
}

func readAnnouncements(conn *net.UDPConn, local netip.Addr, notify bool, receive func(announcement)) error {
	var buf [ssdpPacketLimit + 1]byte
	for {
		n, peer, err := conn.ReadFromUDPAddrPort(buf[:])
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			return err
		}
		if a, ok := parseSSDP(buf[:n], peer.Addr(), notify, time.Now()); ok {
			a.local = local
			receive(a)
		}
	}
}

// 裸 IP 通过单播 M-SEARCH 取得设备实际的描述地址，端口与文件名由设备决定。
func unicastSearch(ctx context.Context, ip netip.Addr) (announcement, error) {
	target := netip.AddrPortFrom(ip, 1900)
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return announcement{}, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return announcement{}, err
	}
	for range 3 {
		if _, err = conn.WriteToUDPAddrPort(searchPacket(target.String()), target); err != nil {
			return announcement{}, err
		}
	}
	var buf [ssdpPacketLimit + 1]byte
	for {
		n, peer, err := conn.ReadFromUDPAddrPort(buf[:])
		if err != nil {
			return announcement{}, err
		}
		if peer.Addr() == ip {
			if a, ok := parseSSDP(buf[:n], ip, false, time.Now()); ok {
				return a, nil
			}
		}
	}
}

func probeAddress(ctx context.Context, raw string) (Device, error) {
	raw = strings.TrimSpace(raw)
	if ip, err := netip.ParseAddr(raw); err == nil {
		if !IsLANAddress(ip) {
			return Device{}, ErrDeviceAddress
		}
		a, err := unicastSearch(ctx, ip)
		if err != nil {
			return Device{}, err
		}
		return fetchAnnouncement(ctx, a)
	}
	return ManualDescription(ctx, raw)
}

func fetchAnnouncement(ctx context.Context, a announcement) (Device, error) {
	var d Device
	var err error
	if a.source.IsValid() {
		d, err = DiscoverDescription(ctx, a.location, a.source)
	} else {
		d, err = ManualDescription(ctx, a.location)
	}
	if err != nil {
		return Device{}, err
	}
	if d.UDN != a.udn {
		return Device{}, errors.New("设备描述与 SSDP 公告的 UDN 不一致")
	}
	if a.local.IsValid() {
		d.LocalAddress = a.local.String()
	}
	return d, nil
}
