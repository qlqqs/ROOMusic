package upnp

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"
)

var searchInterval = 30 * time.Second

const (
	manualLease   = time.Minute
	maxDiscovered = 1024
)

type refresh struct {
	announcement
	manual       bool
	latestExpiry time.Time
}

type discovery struct {
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	jobs    chan *refresh
	probes  sync.WaitGroup
	pending map[string]*refresh // 由注册表的锁保护，旧请求不得覆盖 byebye 或移除操作。
}

func (r *Registry) Start(parent context.Context, manual []ManualDevice) error {
	r.lifecycle.Lock()
	defer r.lifecycle.Unlock()
	if err := parent.Err(); err != nil {
		return err
	}
	r.mu.RLock()
	active := r.run != nil
	r.mu.RUnlock()
	if active {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	run := &discovery{ctx: ctx, cancel: cancel, done: make(chan struct{}), jobs: make(chan *refresh, 64), pending: make(map[string]*refresh)}
	network := &ssdpNetwork{logger: r.logger.With("module", "upnp"), links: make(map[int]*ssdpLink), receive: func(a announcement) { r.observe(run, a) }}
	if err := network.rescan(ctx); err != nil {
		cancel()
		return err
	}
	r.mu.Lock()
	r.run = run
	for _, m := range manual {
		rec := &record{device: Device{UDN: m.UDN, Address: m.Address}, manualAddress: m.Address}
		r.records[m.UDN] = rec
	}
	r.mu.Unlock()
	go r.discover(run, network)
	return nil
}

func (r *Registry) Stop() {
	r.lifecycle.Lock()
	defer r.lifecycle.Unlock()
	r.mu.RLock()
	run := r.run
	r.mu.RUnlock()
	if run != nil {
		run.cancel()
		<-run.done
	}
}

type ssdpNetwork struct {
	logger  *slog.Logger
	links   map[int]*ssdpLink
	receive func(announcement)
	waiting bool
}

func (n *ssdpNetwork) rescan(ctx context.Context) error {
	wanted, err := listLinks()
	if err != nil {
		return err
	}
	if len(wanted) == 0 && !n.waiting {
		n.logger.Info("等待局域网网卡以开始 SSDP 发现", "event", "ssdp.interfaces.waiting")
	}
	n.waiting = len(wanted) == 0
	for index, l := range n.links {
		if l.addr != wanted[index] || l.stopped.Load() {
			l.close()
			delete(n.links, index)
		}
	}
	group := net.UDPAddrFromAddrPort(netip.MustParseAddrPort(ssdpGroup))
	var lastErr error
	for index, addr := range wanted {
		if n.links[index] != nil {
			continue
		}
		l, err := openLink(net.Interface{Index: index}, addr, group)
		if err != nil {
			lastErr = err
			continue
		}
		n.links[index] = l
		l.listen(ctx, index, n.logger, n.receive)
	}
	if len(n.links) == 0 {
		return lastErr
	}
	return nil
}

func (n *ssdpNetwork) search() {
	group := netip.MustParseAddrPort(ssdpGroup)
	packet := searchPacket(ssdpGroup)
	for _, link := range n.links {
		for range 3 {
			_, _ = link.search.WriteToUDPAddrPort(packet, group)
		}
		// 同机渲染器也可位于不回送组播的网卡上。
		local := netip.AddrPortFrom(link.addr, group.Port())
		_, _ = link.search.WriteToUDPAddrPort(searchPacket(local.String()), local)
	}
}

func (n *ssdpNetwork) run(ctx context.Context) {
	defer func() {
		for _, link := range n.links {
			link.close()
		}
	}()
	ticker := time.NewTicker(searchInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		n.search()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := n.rescan(ctx); err != nil {
				n.logger.Warn("SSDP 网卡刷新失败", "event", "ssdp.interfaces.failed", "error", err)
			}
		}
	}
}

func (r *Registry) discover(run *discovery, network *ssdpNetwork) {
	var wg sync.WaitGroup
	defer func() {
		wg.Wait()
		// 取得写锁，与已通过开启检查的手动探测同步；取消后不会再加入。
		r.mu.Lock()
		run.probes.Wait()
		r.run = nil
		clear(r.records)
		r.mu.Unlock()
		close(run.done)
	}()
	if network != nil {
		wg.Go(func() { network.run(run.ctx) })
	}
	for range 4 {
		wg.Go(func() { r.refreshDevices(run) })
	}
	ticker := time.NewTicker(searchInterval)
	defer ticker.Stop()
	for run.ctx.Err() == nil {
		r.refreshManual(run)
		select {
		case <-run.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Registry) refreshManual(run *discovery) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for udn, rec := range r.records {
		if rec.manualAddress != "" && run.pending[udn] == nil {
			a := announcement{udn: udn, location: rec.manualAddress}
			r.enqueue(run, &refresh{announcement: a, manual: true})
		}
	}
}

// SSDP 数据包的处理不等待设备的 HTTP 响应。
func (r *Registry) observe(run *discovery, a announcement) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if run.ctx.Err() != nil {
		return
	}
	rec := r.records[a.udn]
	if a.byebye {
		if rec != nil && rec.source == a.source {
			rec.advertisedUntil, rec.manualUntil = time.Time{}, time.Time{}
		}
		if pending := run.pending[a.udn]; pending != nil && (pending.source == a.source || (pending.manual && rec != nil && rec.source == a.source)) {
			delete(run.pending, a.udn)
		}
		return
	}
	now := time.Now()
	if rec != nil && rec.device.Address == a.location && !rec.refreshed.IsZero() {
		wasOnline := now.Before(rec.advertisedUntil) || now.Before(rec.manualUntil)
		rec.advertisedUntil = a.expires
		if wasOnline && run.pending[a.udn] == nil && now.Sub(rec.refreshed) < searchInterval {
			return
		}
	}
	if pending := run.pending[a.udn]; pending != nil {
		if pending.location == a.location {
			pending.latestExpiry = a.expires
		}
		return
	}
	r.enqueue(run, &refresh{announcement: a, latestExpiry: a.expires})
}

func (r *Registry) enqueue(run *discovery, job *refresh) {
	select {
	case run.jobs <- job:
		run.pending[job.udn] = job
	default:
		// 后续公告或下一轮搜索会重新排队。
	}
}

func (r *Registry) refreshDevices(run *discovery) {
	for run.ctx.Err() == nil {
		select {
		case <-run.ctx.Done():
			return
		case job := <-run.jobs:
			d, err := fetchAnnouncement(run.ctx, job.announcement)
			r.applyRefresh(run, job, d, err)
		}
	}
}

func (r *Registry) applyRefresh(run *discovery, job *refresh, d Device, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if run.pending[job.udn] != job {
		return
	}
	delete(run.pending, job.udn)
	if run.ctx.Err() != nil {
		return
	}
	rec := r.records[job.udn]
	if err != nil {
		if rec != nil && job.manual {
			rec.manualUntil = time.Time{}
		}
		return
	}
	now := time.Now()
	if rec == nil {
		if !r.makeRoom(now) {
			return
		}
		rec = &record{}
		r.records[job.udn] = rec
	}
	d.discoveryContext = run.ctx
	rec.device, rec.source, rec.refreshed = d, d.peerIP, now
	if job.manual {
		rec.manualUntil = now.Add(manualLease)
	}
	if !job.latestExpiry.IsZero() {
		rec.advertisedUntil = job.latestExpiry
	}
}

// 达到上限时优先保留手动登记和仍在线的设备。
func (r *Registry) makeRoom(now time.Time) bool {
	if len(r.records) < maxDiscovered {
		return true
	}
	var oldest *record
	for _, rec := range r.records {
		if rec.manualAddress == "" && !now.Before(rec.advertisedUntil) && (oldest == nil || rec.refreshed.Before(oldest.refreshed)) {
			oldest = rec
		}
	}
	if oldest == nil {
		return false
	}
	delete(r.records, oldest.device.UDN)
	return true
}
