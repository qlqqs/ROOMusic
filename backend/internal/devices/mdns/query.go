package mdns

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/lan"
	"golang.org/x/net/dns/dnsmessage"
)

var searchInterval = 30 * time.Second

func question(name dnsmessage.Name, typ dnsmessage.Type) dnsmessage.Question {
	return dnsmessage.Question{Name: name, Type: typ, Class: dnsmessage.ClassINET}
}

type browser struct {
	socket  *socket
	service string
	id      uint16
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
	if target.IsValid() {
		q.ID = uint16(1 + rand.Uint32N(65535))
	}
	data, err := q.Pack()
	return browser{service: service, query: data, target: target, id: q.ID}, err
}

// Start 在每张局域网网卡单独打开 UDP 5353，避免宿主机组播成员数上限遗漏网卡。
// 各网卡的结果串行交付；stop 取消查询并等待回调退出。
func Start(ctx context.Context, service string, logger *slog.Logger, receive func(Entry)) (func(), error) {
	query, err := prepare(service, netip.Addr{})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	var delivery sync.Mutex
	deliver := func(e Entry) {
		delivery.Lock()
		defer delivery.Unlock()
		receive(e)
	}
	session := browseSession{logger: logger.With("module", "mdns"), query: query, receive: deliver, workers: make(map[int]*browseWorker)}
	if err := session.rescan(ctx); err != nil {
		cancel()
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(searchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				for _, w := range session.workers {
					w.stop()
				}
				return
			case <-ticker.C:
				if err := session.rescan(ctx); err != nil {
					session.logger.Warn("mDNS 网卡刷新失败", "event", "mdns.interfaces.failed", "error", err)
				}
			}
		}
	}()
	return func() { cancel(); <-done }, nil
}

type browseSession struct {
	logger  *slog.Logger
	query   browser
	receive func(Entry)
	workers map[int]*browseWorker
	waiting bool
}

func (s *browseSession) rescan(ctx context.Context) error {
	links, loop, err := listLinks(netip.Addr{})
	if err != nil {
		return err
	}
	if len(links) == 0 && !s.waiting {
		s.logger.Info("等待局域网网卡以开始 mDNS 发现", "event", "mdns.interfaces.waiting", "service", s.query.service)
	}
	s.waiting = len(links) == 0
	wanted := make(map[int]link)
	for _, l := range links {
		if _, exists := wanted[l.iface.Index]; !exists {
			wanted[l.iface.Index] = l
		}
	}
	for index, w := range s.workers {
		if w.local == wanted[index].local {
			select {
			case <-w.done:
			default:
				continue
			}
		}
		w.stop()
		delete(s.workers, index)
	}
	var lastErr error
	for index, l := range wanted {
		if s.workers[index] != nil {
			continue
		}
		b := s.query
		b.socket, err = openBrowseSocket(l, loop)
		if err != nil {
			lastErr = err
			continue
		}
		workerCtx, stop := context.WithCancel(ctx)
		w := &browseWorker{local: l.local, cancel: stop, done: make(chan struct{})}
		s.workers[index] = w
		go func() {
			defer close(w.done)
			if err := b.run(workerCtx, s.receive, searchInterval); err != nil && workerCtx.Err() == nil {
				s.logger.Warn("mDNS 接收中断，下轮重开网卡套接字", "event", "mdns.receive.failed", "interface_index", index, "error", err)
			}
		}()
	}
	if len(s.workers) == 0 {
		return lastErr
	}
	return nil
}

type browseWorker struct {
	local  netip.Addr
	cancel context.CancelFunc
	done   chan struct{}
}

func (w *browseWorker) stop() {
	w.cancel()
	<-w.done
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
	links, loop, err := listLinks(netip.Addr{})
	if err != nil {
		return Entry{}, err
	}
	if len(links) == 0 {
		return Entry{}, errors.New("没有可用的局域网 IPv4 组播接口")
	}
	b.socket, err = openQuerySocket(linkForTarget(links, ip), loop)
	if err != nil {
		return Entry{}, err
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

func linkForTarget(links []link, target netip.Addr) link {
	best := links[0]
	bits := -1
	for _, l := range links {
		if l.local == target {
			return l
		}
		if l.prefix.Contains(target) && l.prefix.Bits() > bits {
			best, bits = l, l.prefix.Bits()
		}
	}
	return best
}

func (b *browser) search(data []byte, l link) {
	if b.target.IsValid() {
		dest := net.UDPAddrFromAddrPort(netip.AddrPortFrom(b.target, uint16(mdnsPort)))
		if b.target != l.local {
			b.socket.send(data, l, dest)
			return
		}
		// 本机单播按本地路由交付，同时发组播让共享监听端口的服务都能应答。
		b.socket.send(data, link{}, dest)
	}
	b.socket.multicast(data, l)
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
			b.search(b.query, b.socket.link)
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
		if int(p.peer.Port()) != mdnsPort || (b.target.IsValid() && b.target != p.peer.Addr()) {
			continue
		}
		var msg dnsmessage.Message
		if msg.Unpack(p.data) != nil || (b.target.IsValid() && msg.ID != b.id) || !msg.Response || msg.RCode != dnsmessage.RCodeSuccess {
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
	query := dnsmessage.Message{Header: dnsmessage.Header{ID: b.id}}
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
	b.search(data, l)
}
