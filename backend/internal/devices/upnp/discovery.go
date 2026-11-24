package upnp

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"time"
)

const (
	searchInterval = 30 * time.Second
	manualLease    = 2 * searchInterval
	maxDiscovered  = 1024
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
	links, err := openLinks()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	run := &discovery{ctx: ctx, cancel: cancel, done: make(chan struct{}), jobs: make(chan *refresh, 64), pending: make(map[string]*refresh)}
	r.mu.Lock()
	r.run = run
	for _, m := range manual {
		rec := &record{device: Device{UDN: m.UDN, Address: m.Address}, manualAddress: m.Address}
		r.records[m.UDN] = rec
	}
	r.mu.Unlock()
	go r.discover(run, links)
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

func (r *Registry) discover(run *discovery, links []ssdpLink) {
	var wg sync.WaitGroup
	defer func() {
		for _, link := range links {
			link.search.Close()
			link.notify.Close()
		}
		wg.Wait()
		// 取得写锁，与已通过开启检查的手动探测同步；取消后不会再加入。
		r.mu.Lock()
		run.probes.Wait()
		r.run = nil
		clear(r.records)
		r.mu.Unlock()
		close(run.done)
	}()
	receive := func(a announcement) { r.observe(run, a) }
	for _, link := range links {
		// 组播通知在共享端口上接收，使用描述连接的本机地址。单播搜索响应有明确的本机网卡。
		local := link.search.LocalAddr().(*net.UDPAddr).AddrPort().Addr().Unmap()
		wg.Go(func() { readAnnouncements(link.notify, netip.Addr{}, true, receive) })
		wg.Go(func() { readAnnouncements(link.search, local, false, receive) })
	}
	for range 4 {
		wg.Go(func() { r.refreshDevices(run) })
	}
	group := netip.MustParseAddrPort(ssdpGroup)
	packet := searchPacket(ssdpGroup)
	ticker := time.NewTicker(searchInterval)
	defer ticker.Stop()
	for run.ctx.Err() == nil {
		for _, link := range links {
			for range 3 {
				_, _ = link.search.WriteToUDPAddrPort(packet, group)
			}
			// 同机渲染器也可位于不回送组播的网卡上。
			addr := link.search.LocalAddr().(*net.UDPAddr).AddrPort().Addr().Unmap()
			local := netip.AddrPortFrom(addr, group.Port())
			_, _ = link.search.WriteToUDPAddrPort(searchPacket(local.String()), local)
		}
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
