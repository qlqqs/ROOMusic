package slimproto

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/lan"
	"golang.org/x/net/ipv4"
)

func allowedPeer(ip netip.Addr) bool { return lan.IsLANAddress(ip) || ip.Is4() && ip.IsLoopback() }

type link struct {
	index  int
	prefix netip.Prefix
}

var listLinks = func() ([]link, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var links []link
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, err
		}
		for _, addr := range addrs {
			prefix, err := netip.ParsePrefix(addr.String())
			if err == nil && allowedPeer(prefix.Addr()) {
				links = append(links, link{iface.Index, prefix})
			}
		}
	}
	return links, nil
}

type discoverySocket struct {
	conn   *net.UDPConn
	failed atomic.Bool
}
type discoveryPacket struct {
	socket *discoverySocket
	iface  int
	kind   byte
	peer   netip.AddrPort
}
type discovery struct {
	ctx       context.Context
	name      string
	logger    *slog.Logger
	wg        *sync.WaitGroup
	sockets   map[link]*discoverySocket
	waiting   bool
	broadcast *discoverySocket
	packets   chan discoveryPacket
}

func newDiscovery(ctx context.Context, name string, logger *slog.Logger, wg *sync.WaitGroup) *discovery {
	return &discovery{ctx: ctx, name: name, logger: logger, wg: wg, sockets: make(map[link]*discoverySocket), packets: make(chan discoveryPacket, 64)}
}
func (d *discovery) close() {
	for _, s := range d.sockets {
		s.conn.Close()
	}
	if d.broadcast != nil {
		d.broadcast.conn.Close()
	}
}

func (d *discovery) refresh() error {
	links, err := listLinks()
	if err != nil {
		return err
	}
	if len(links) == 0 && !d.waiting {
		d.logger.Info("等待 SlimProto 局域网网卡", "event", "slimproto.interfaces.waiting", "module", "slimproto")
	}
	d.waiting = len(links) == 0
	wanted := make(map[link]bool, len(links))
	for _, l := range links {
		wanted[l] = true
	}
	for l, s := range d.sockets {
		if !wanted[l] || s.failed.Load() {
			s.conn.Close()
			delete(d.sockets, l)
		}
	}
	for _, l := range links {
		if d.sockets[l] != nil {
			continue
		}
		s, err := d.open(l.prefix.Addr(), false)
		if err != nil {
			return err
		}
		d.sockets[l] = s
	}
	// 单播地址上的套接字收不到发往 255.255.255.255 的请求。
	// 单独接收这个目的地址，从入站网卡的单播套接字回复。
	if d.broadcast != nil && (len(links) == 0 || d.broadcast.failed.Load()) {
		d.broadcast.conn.Close()
		d.broadcast = nil
	}
	if d.broadcast == nil && len(links) > 0 {
		d.broadcast, err = d.open(netip.AddrFrom4([4]byte{255, 255, 255, 255}), true)
	}
	return err
}

func (d *discovery) open(ip netip.Addr, broadcast bool) (*discoverySocket, error) {
	conn, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(ip, listenPort)))
	if err != nil {
		return nil, err
	}
	var packet *ipv4.PacketConn
	if broadcast {
		packet = ipv4.NewPacketConn(conn)
		if err := packet.SetControlMessage(ipv4.FlagInterface, true); err != nil {
			conn.Close()
			return nil, err
		}
	}
	s := &discoverySocket{conn: conn}
	d.wg.Go(func() {
		var buf [1450]byte
		for {
			var n, index int
			var peer netip.AddrPort
			var err error
			if packet != nil {
				var addr net.Addr
				var cm *ipv4.ControlMessage
				n, cm, addr, err = packet.ReadFrom(buf[:])
				if err == nil && cm != nil {
					index = cm.IfIndex
					peer = addr.(*net.UDPAddr).AddrPort()
				}
			} else {
				n, peer, err = conn.ReadFromUDPAddrPort(buf[:])
			}
			if err != nil {
				if d.ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
					d.logger.Warn("SlimProto 发现接收中断", "event", "slimproto.receive.failed", "module", "slimproto", "error", err)
				}
				s.failed.Store(true)
				return
			}
			if n == 0 || !allowedPeer(peer.Addr().Unmap()) || (buf[0] != 'e' && buf[0] != 'd') {
				continue
			}
			select {
			case d.packets <- discoveryPacket{s, index, buf[0], peer}:
			case <-d.ctx.Done():
				return
			}
		}
	})
	return s, nil
}

func (d *discovery) respond(p discoveryPacket) {
	var out *discoverySocket
	best := -2
	for l, s := range d.sockets {
		if s == p.socket {
			out = s
			break
		}
		if p.socket != d.broadcast || p.iface != l.index {
			continue
		}
		bits := -1
		if l.prefix.Contains(p.peer.Addr().Unmap()) {
			bits = l.prefix.Bits()
		}
		if bits > best {
			best, out = bits, s
		}
	}
	if out == nil {
		return
	}
	reply := []byte{'E'}
	if p.kind == 'd' {
		reply = make([]byte, 18)
		reply[0] = 'D'
		i := 1
		for _, r := range d.name {
			if i > 16 {
				break
			}
			if r > 255 {
				r = '?'
			}
			reply[i] = byte(r)
			i++
		}
	}
	out.conn.SetWriteDeadline(time.Now().Add(time.Second))
	if _, err := out.conn.WriteToUDPAddrPort(reply, p.peer); err != nil && d.ctx.Err() == nil {
		d.logger.Warn("SlimProto 发现应答失败", "event", "slimproto.discovery.reply_failed", "module", "slimproto", "error", err)
	}
}
