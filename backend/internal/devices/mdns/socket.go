// Package mdns 在局域网 IPv4 网卡上查询和公告 DNS-SD 服务。
package mdns

import (
	"errors"
	"net"
	"net/netip"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/lan"
	"golang.org/x/net/ipv4"
)

var mdnsPort = 5353
var listLinks = lanLinks
var openBrowseSocket = openSocket

func multicastGroup() *net.UDPAddr {
	return &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: mdnsPort}
}

type link struct {
	iface  net.Interface
	local  netip.Addr
	prefix netip.Prefix
}

type socket struct {
	*ipv4.PacketConn
	link      link
	links     map[int]link
	loopIndex int
}

func lanLinks(local netip.Addr) ([]link, *net.Interface, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, nil, err
	}
	var links []link
	var loop *net.Interface
	var lastErr error
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		if iface.Flags&net.FlagLoopback != 0 {
			loop = &iface
			continue
		}
		if iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			lastErr = err
			continue
		}
		for _, addr := range addrs {
			prefix, err := netip.ParsePrefix(addr.String())
			if err == nil && lan.IsLANAddress(prefix.Addr()) && (!local.IsValid() || prefix.Addr() == local) {
				links = append(links, link{iface: iface, local: prefix.Addr(), prefix: prefix})
			}
		}
	}
	if len(links) == 0 {
		return nil, loop, lastErr
	}
	return links, loop, nil
}

func openSocket(l link, loop *net.Interface) (*socket, error) {
	group := multicastGroup()
	conn, err := net.ListenMulticastUDP("udp4", &l.iface, group)
	if err != nil {
		return nil, err
	}
	s := &socket{PacketConn: ipv4.NewPacketConn(conn), link: l, links: map[int]link{l.iface.Index: l}}
	if loop != nil && s.JoinGroup(loop, group) == nil {
		s.loopIndex = loop.Index
	}
	return configureSocket(s)
}

func openQuerySocket(l link, loop *net.Interface) (*socket, error) {
	conn, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(l.local, 0)))
	if err != nil {
		return nil, err
	}
	s := &socket{PacketConn: ipv4.NewPacketConn(conn), link: l, links: map[int]link{l.iface.Index: l}}
	if loop != nil {
		s.loopIndex = loop.Index
	}
	return configureSocket(s)
}

func configureSocket(s *socket) (*socket, error) {
	err := s.SetControlMessage(ipv4.FlagInterface|ipv4.FlagDst, true)
	if err == nil {
		err = s.SetMulticastTTL(255)
	}
	if err == nil {
		err = s.SetMulticastLoopback(true)
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *socket) send(data []byte, l link, dest *net.UDPAddr) error {
	var control *ipv4.ControlMessage
	if l.local.IsValid() {
		control = &ipv4.ControlMessage{IfIndex: l.iface.Index, Src: net.IP(l.local.AsSlice())}
	}
	_, err := s.WriteTo(data, control, dest)
	return err
}

// 宿主机可能过滤物理接口上的组播回送。同机服务在回环接口加入同一个组，
// 报文仍以实际局域网地址为源，使 A 记录与来源校验对远近设备一致。
func (s *socket) multicast(data []byte, l link) error {
	group := multicastGroup()
	err := s.send(data, l, group)
	if s.loopIndex != 0 {
		l.iface.Index = s.loopIndex
		s.send(data, l, group)
	}
	return err
}

type packet struct {
	data      []byte
	peer      netip.AddrPort
	link      link
	multicast bool
	loop      bool
}

func (s *socket) read(buf []byte) (packet, error) {
	n, control, addr, err := s.ReadFrom(buf)
	if err != nil || control == nil {
		return packet{}, err
	}
	peer := addr.(*net.UDPAddr).AddrPort()
	peer = netip.AddrPortFrom(peer.Addr().Unmap(), peer.Port())
	if !lan.IsLANAddress(peer.Addr()) {
		return packet{}, nil
	}
	l, ok := s.links[control.IfIndex]
	if !ok {
		// 同机单播会从回环网卡进入，用目的 IPv4 找出公告应使用的网卡。
		for _, candidate := range s.links {
			if control.Dst.Equal(net.IP(candidate.local.AsSlice())) ||
				(control.IfIndex == s.loopIndex && candidate.local == peer.Addr()) {
				l, ok = candidate, true
				break
			}
		}
	}
	if !ok {
		return packet{}, nil
	}
	return packet{data: buf[:n], peer: peer, link: l, multicast: control.Dst.IsMulticast(), loop: control.IfIndex == s.loopIndex}, nil
}

func retryRead(err error) bool {
	var nerr net.Error
	return errors.As(err, &nerr) && nerr.Timeout()
}

// 所有时间均为各条记录的实际到期时间，同一来源的 TTL 为零表示立即下线。
type Entry struct {
	Service        string
	Instance       string
	Host           string
	IP             netip.Addr
	LocalIP        netip.Addr
	Port           uint16
	InterfaceIndex int
	TXT            map[string]string
	Expires        time.Time
}
