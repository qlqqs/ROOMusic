package airplay

import (
	"context"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/mdns"
)

const refreshInterval = 30 * time.Second

type Entry struct {
	Device
	Manual, Online bool
}

type announcement struct {
	device  Device
	expires time.Time
}

type origin struct {
	service, instance string
	ip                netip.Addr
	ifindex           int
}

type manualTarget struct {
	address netip.Addr
	device  Device
	expires time.Time
}

type record struct {
	last      Device
	announced map[origin]announcement
	manual    *manualTarget
}

func (r *record) discovered(now time.Time) Device {
	var native, classic announcement
	for from, a := range r.announced {
		if !now.Before(a.expires) {
			continue
		}
		preferred := &native
		if from.service == raopService {
			preferred = &classic
		}
		if preferred.expires.Before(a.expires) || (preferred.expires.Equal(a.expires) && a.device.Address < preferred.device.Address) {
			*preferred = a
		}
	}
	return merge(native.device, classic.device)
}

func (r *record) entry(now time.Time) Entry {
	d := r.discovered(now)
	if d.Key == "" && r.manual != nil && now.Before(r.manual.expires) {
		d = r.manual.device
	}
	entry := Entry{Device: d, Online: d.Key != "", Manual: r.manual != nil}
	if !entry.Online {
		entry.Device = r.last
	}
	return entry
}

type Registry struct {
	logger    *slog.Logger
	lifecycle sync.Mutex
	stop      func()
	mu        sync.RWMutex
	records   map[string]*record
}

func NewRegistry() *Registry {
	return NewRegistryWithLogger(slog.Default())
}

func NewRegistryWithLogger(logger *slog.Logger) *Registry {
	return &Registry{logger: logger, records: make(map[string]*record)}
}

func (r *Registry) List() []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	now := time.Now()
	rows := make([]Entry, 0, len(r.records))
	for _, rec := range r.records {
		rows = append(rows, rec.entry(now))
	}
	return rows
}

func (r *Registry) Lookup(key string) (Entry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.records[key]
	if !ok {
		return Entry{}, false
	}
	return rec.entry(time.Now()), true
}

func (r *Registry) receive(e mdns.Entry) {
	d, ok := parseEntry(e)
	if !ok {
		return
	}
	now := time.Now()
	from := origin{service: e.Service, instance: e.Instance, ip: e.IP, ifindex: e.InterfaceIndex}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.records[d.Key]
	if rec == nil {
		if !now.Before(e.Expires) {
			return
		}
		if len(r.records) >= 1024 {
			for key, old := range r.records {
				if old.manual == nil && !old.entry(now).Online {
					delete(r.records, key)
					break
				}
			}
			if len(r.records) >= 1024 {
				return
			}
		}
		rec = &record{}
		r.records[d.Key] = rec
	}
	for source, a := range rec.announced {
		if !now.Before(a.expires) {
			delete(rec.announced, source)
		}
	}
	if now.Before(e.Expires) {
		if rec.announced == nil {
			rec.announced = make(map[origin]announcement)
		}
		rec.announced[from] = announcement{device: d, expires: e.Expires}
	} else {
		delete(rec.announced, from)
		if rec.manual != nil && rec.manual.address == e.IP {
			// 失效时一并撤销还未返回的旧手动查询。
			rec.manual = &manualTarget{address: rec.manual.address, device: rec.manual.device}
		}
	}
	if live := rec.discovered(now); live.Key != "" {
		rec.last = live
	}
}

func (r *Registry) Remember(d Device) {
	r.remember(d, time.Now().Add(2*refreshInterval))
}

func (r *Registry) remember(d Device, expires time.Time) {
	addr, err := netip.ParseAddrPort(d.Address)
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.records[d.Key]
	if rec == nil {
		rec = &record{}
		r.records[d.Key] = rec
	}
	rec.last = d
	rec.manual = &manualTarget{address: addr.Addr(), device: d, expires: expires}
}

func (r *Registry) Forget(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec := r.records[key]; rec != nil {
		rec.manual = nil
		if !rec.entry(time.Now()).Online {
			delete(r.records, key)
		}
	}
}

func (r *Registry) Start(parent context.Context, manual []Device) error {
	r.lifecycle.Lock()
	defer r.lifecycle.Unlock()
	if err := parent.Err(); err != nil {
		return err
	}
	if r.stop != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	for _, d := range manual {
		r.remember(d, time.Time{})
	}
	var workers sync.WaitGroup
	var stops []func()
	stop := func() {
		cancel()
		for _, stopBrowse := range stops {
			stopBrowse()
		}
		workers.Wait()
		r.mu.Lock()
		clear(r.records)
		r.mu.Unlock()
	}
	for _, service := range []string{airplayService, raopService} {
		stopBrowse, err := mdns.Start(ctx, service, r.logger, r.receive)
		if err != nil {
			stop()
			return err
		}
		stops = append(stops, stopBrowse)
	}
	workers.Go(func() {
		ticks := time.NewTicker(refreshInterval)
		defer ticks.Stop()
		for {
			r.refreshManual(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticks.C:
			}
		}
	})
	r.stop = stop
	return nil
}

func (r *Registry) Stop() {
	r.lifecycle.Lock()
	defer r.lifecycle.Unlock()
	if r.stop != nil {
		r.stop()
		r.stop = nil
	}
}

func (r *Registry) refreshManual(ctx context.Context) {
	now := time.Now()
	r.mu.RLock()
	targets := make(map[string]*manualTarget)
	for key, rec := range r.records {
		if rec.manual != nil && rec.discovered(now).Key == "" {
			targets[key] = rec.manual
		}
	}
	r.mu.RUnlock()
	// mdns 按目标串行持有单播查询套接字，避免同时查询多个地址时抢走应答。
	for key, target := range targets {
		if ctx.Err() != nil {
			return
		}
		d, err := lookup(ctx, target.address, key)
		r.mu.Lock()
		if rec := r.records[key]; rec != nil && rec.manual == target {
			if err == nil {
				target.device, target.expires = d, time.Now().Add(2*refreshInterval)
				rec.last = d
			} else {
				target.expires = time.Time{}
			}
		}
		r.mu.Unlock()
	}
}
