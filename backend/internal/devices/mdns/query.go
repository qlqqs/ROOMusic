package mdns

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/lan"
	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/sync/semaphore"
)

const searchInterval = 30 * time.Second

// 定向查询与组播监听都绑定 UDP 5353，单播应答只交付给一个套接字。
// 串行打开临时查询套接字，避免后一台设备的查询取走前一台的应答。
var lookupSlot = semaphore.NewWeighted(1)

func question(name dnsmessage.Name, typ dnsmessage.Type) dnsmessage.Question {
	return dnsmessage.Question{Name: name, Type: typ, Class: dnsmessage.ClassINET}
}

type browser struct {
	socket  *socket
	service string
	query   []byte
	target  netip.Addr
}

func prepare(service string, target netip.Addr) (browser, error) {
	service = strings.ToLower(fqdn(service))
	name, err := dnsmessage.NewName(service)
	if err != nil {
		return browser{}, err
	}
	q := dnsmessage.Message{Questions: []dnsmessage.Question{question(name, dnsmessage.TypePTR)}}
	data, err := q.Pack()
	return browser{service: service, query: data, target: target}, err
}

// Start 在每张局域网网卡单独打开 UDP 5353，避免宿主机组播成员数上限遗漏网卡。
// 各网卡的结果串行交付；stop 取消查询并等待回调退出。
func Start(ctx context.Context, service string, receive func(Entry)) (func(), error) {
	query, err := prepare(service, netip.Addr{})
	if err != nil {
		return nil, err
	}
	links, loop, err := lanLinks(netip.Addr{})
	if err != nil {
		return nil, err
	}
	var sockets []*socket
	var lastErr error
	for _, l := range links {
		s, err := openSocket(l, loop)
		if err != nil {
			lastErr = err
			continue
		}
		sockets = append(sockets, s)
	}
	if len(sockets) == 0 && lastErr != nil {
		return nil, lastErr
	}
	ctx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	var delivery sync.Mutex
	for _, s := range sockets {
		b := query
		b.socket = s
		workers.Go(func() {
			b.run(ctx, func(e Entry) {
				delivery.Lock()
				defer delivery.Unlock()
				receive(e)
			}, searchInterval)
		})
	}
	return func() {
		cancel()
		workers.Wait()
	}, nil
}

// Lookup 仅返回指定 IP 应答且 A 记录与之相符的服务。
func Lookup(ctx context.Context, service string, ip netip.Addr) (Entry, error) {
	return LookupMatch(ctx, service, ip, func(Entry) bool { return true })
}

// LookupMatch 在同一主机代理多个服务实例时，只返回调用方要复探的实例。
func LookupMatch(ctx context.Context, service string, ip netip.Addr, accept func(Entry) bool) (Entry, error) {
	if !lan.IsLANAddress(ip) {
		return Entry{}, errors.New("mDNS 目标必须是局域网 IPv4 地址")
	}
	b, err := prepare(service, ip)
	if err != nil {
		return Entry{}, err
	}
	if err := lookupSlot.Acquire(ctx, 1); err != nil {
		return Entry{}, err
	}
	defer lookupSlot.Release(1)
	links, loop, err := lanLinks(netip.Addr{})
	if err != nil {
		return Entry{}, err
	}
	if len(links) == 0 {
		return Entry{}, errors.New("没有可用的局域网 IPv4 组播接口")
	}
	listen := links[0]
	for _, l := range links {
		if l.local == ip {
			listen = l
			break
		}
	}
	b.socket, err = openSocket(listen, loop)
	if err != nil {
		return Entry{}, err
	}
	// 单播由内核选路，保留所有可用网卡以便从应答识别实际的本机地址。
	for _, l := range links {
		b.socket.links[l.iface.Index] = l
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var match Entry
	err = b.run(ctx, func(e Entry) {
		if !match.IP.IsValid() && e.Expires.After(time.Now()) && accept(e) {
			match = e
			cancel()
		}
	}, time.Second)
	if match.IP.IsValid() {
		return match, nil
	}
	return Entry{}, err
}

func (b *browser) search() {
	if b.target.IsValid() {
		dest := net.UDPAddrFromAddrPort(netip.AddrPortFrom(b.target, 5353))
		b.socket.send(b.query, link{}, dest)
		if b.target == b.socket.link.local {
			// 同机共享 5353 时，单播可能由另一个监听者接收。
			b.socket.multicast(b.query, b.socket.link)
		}
		return
	}
	b.socket.multicast(b.query, b.socket.link)
}

type source struct {
	ip  netip.Addr
	idx int
}

func (b *browser) run(ctx context.Context, receive func(Entry), interval time.Duration) error {
	defer b.socket.Close()
	stop := context.AfterFunc(ctx, func() { b.socket.Close() })
	defer stop()
	var nextSearch, nextExpiry time.Time
	cache := make(map[source]*peerCache)
	buf := make([]byte, 9000)
	for ctx.Err() == nil {
		now := time.Now()
		if !now.Before(nextSearch) {
			b.search()
			nextSearch = now.Add(interval)
		}
		if !now.Before(nextExpiry) {
			for src, records := range cache {
				records.prune(now)
				b.resolve(records, src, now, receive)
				if len(records.records) == 0 {
					delete(cache, src)
				}
			}
			nextExpiry = now.Add(time.Second)
			b.socket.SetReadDeadline(nextExpiry)
		}
		p, err := b.socket.read(buf)
		if err != nil {
			if retryRead(err) {
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if p.peer.Port() != 5353 || (b.target.IsValid() && b.target != p.peer.Addr()) {
			continue
		}
		var msg dnsmessage.Message
		if msg.Unpack(p.data) != nil || !msg.Response || msg.RCode != dnsmessage.RCodeSuccess {
			continue
		}
		src := source{p.peer.Addr(), p.link.iface.Index}
		records := cache[src]
		if records == nil {
			if len(cache) >= 256 {
				continue
			}
			records = newPeerCache()
		}
		now = time.Now()
		records.ingest(b.service, src.ip, msg.Answers, now)
		records.ingest(b.service, src.ip, msg.Additionals, now)
		records.prune(now)
		if len(records.records) != 0 {
			cache[src] = records
		}
		b.resolve(records, src, now, receive)
	}
	return ctx.Err()
}

func (b *browser) resolve(records *peerCache, src source, now time.Time, receive func(Entry)) {
	l := b.socket.links[src.idx]
	missing := records.resolve(b.service, l, now, receive)
	retry := searchInterval
	if b.target.IsValid() {
		retry = time.Second
	}
	var query dnsmessage.Message
	for _, q := range missing {
		key := rrKey{strings.ToLower(q.Name.String()), q.Type}
		if until := records.asked[key]; !now.Before(until) {
			query.Questions = append(query.Questions, q)
			records.asked[key] = now.Add(retry)
		}
	}
	if len(query.Questions) == 0 {
		return
	}
	data, err := query.Pack()
	if err != nil {
		return
	}
	if b.target.IsValid() && b.target != l.local {
		b.socket.send(data, l, net.UDPAddrFromAddrPort(netip.AddrPortFrom(src.ip, 5353)))
	} else {
		b.socket.multicast(data, l)
	}
}
