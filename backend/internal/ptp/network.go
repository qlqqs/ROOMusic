package ptp

import (
	"context"
	"errors"
	"maps"
	"net"
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/lan"
	"golang.org/x/net/ipv4"
)

var listLinks = lanLinks
var multicast = netip.MustParseAddr("224.0.1.129")

type link struct {
	index int
	ip    netip.Addr
	id    portID
}

type wire struct {
	event, general *ipv4.PacketConn
	links          map[int]link
	interfaces     map[int]netip.Addr
}

func openWire(clock uint64) (*wire, error) {
	listen := func(port int) (*ipv4.PacketConn, error) {
		c, err := net.ListenUDP("udp4", &net.UDPAddr{Port: port})
		if err != nil {
			return nil, err
		}
		p := ipv4.NewPacketConn(c)
		if err = p.SetControlMessage(ipv4.FlagInterface, true); err == nil {
			err = p.SetMulticastTTL(1)
		}
		if err != nil {
			p.Close()
			return nil, err
		}
		return p, nil
	}
	w := &wire{links: make(map[int]link)}
	var err error
	w.event, err = listen(319)
	if err != nil {
		return nil, err
	}
	w.general, err = listen(320)
	if err != nil {
		w.event.Close()
		return nil, err
	}
	if err = w.join(clock); err != nil {
		w.close()
		return nil, err
	}
	return w, nil
}

func lanLinks() (map[int]netip.Addr, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	links := make(map[int]netip.Addr)
	for _, iface := range ifaces {
		if iface.Flags&(net.FlagUp|net.FlagMulticast) != net.FlagUp|net.FlagMulticast || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			p, err := netip.ParsePrefix(a.String())
			if err == nil && lan.IsLANAddress(p.Addr()) {
				links[iface.Index] = p.Addr()
				break
			}
		}
	}
	return links, nil
}

func (w *wire) join(clock uint64) error {
	interfaces, err := listLinks()
	if err != nil {
		return err
	}
	w.interfaces = interfaces
	group := &net.UDPAddr{IP: net.IP(multicast.AsSlice())}
	for _, index := range slices.Sorted(maps.Keys(interfaces)) {
		iface := net.Interface{Index: index}
		if err := w.event.JoinGroup(&iface, group); err != nil {
			continue
		}
		if err := w.general.JoinGroup(&iface, group); err != nil {
			w.event.LeaveGroup(&iface, group)
			continue
		}
		w.links[index] = link{index, interfaces[index], portID{clock, uint16(len(w.links) + 1)}}
	}
	if len(w.links) == 0 {
		return errors.New("没有可以加入 PTP 组播的局域网 IPv4 网卡")
	}
	return nil
}

func (w *wire) close() { w.event.Close(); w.general.Close() }

type arrival struct {
	packet
	peer netip.Addr
	link link
	at   time.Time
}

func (w *wire) listen(ctx context.Context, workers *sync.WaitGroup, output chan<- arrival, failures chan<- error) {
	for event, conn := range map[bool]*ipv4.PacketConn{true: w.event, false: w.general} {
		workers.Go(func() {
			buf := make([]byte, 1500)
			for {
				n, cm, src, err := conn.ReadFrom(buf)
				now := time.Now()
				if err != nil {
					select {
					case failures <- err:
					case <-ctx.Done():
					}
					return
				}
				if cm == nil {
					continue
				}
				l, ok := w.links[cm.IfIndex]
				ip := src.(*net.UDPAddr).AddrPort().Addr().Unmap()
				if !ok || !lan.IsLANAddress(ip) || ip == l.ip {
					continue
				}
				p, ok := parse(buf[:n], event)
				if !ok || p.source.clock == l.id.clock {
					continue
				}
				p.b = append([]byte(nil), p.b...)
				select {
				case output <- arrival{p, ip, l, now}:
				case <-ctx.Done():
					return
				}
			}
		})
	}
}

func (w *wire) send(l link, dest netip.Addr, data []byte) error {
	c, port := w.general, uint16(320)
	if data[0]&15 < 8 {
		c, port = w.event, 319
	}
	cm := &ipv4.ControlMessage{IfIndex: l.index, Src: net.IP(l.ip.AsSlice())}
	_, err := c.WriteTo(data, cm, net.UDPAddrFromAddrPort(netip.AddrPortFrom(dest, port)))
	return err
}
