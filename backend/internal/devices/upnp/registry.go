package upnp

import (
	"context"
	"log/slog"
	"net/netip"
	"slices"
	"sort"
	"sync"
	"time"
)

type ManualDevice struct {
	UDN, Address string
}

type Entry struct {
	Device
	Online bool `json:"online"`
	Manual bool `json:"manual"`
}

type record struct {
	device                       Device
	manualAddress                string
	advertisedUntil, manualUntil time.Time
	refreshed                    time.Time
	source                       netip.Addr
}

func (rec record) entry(now time.Time) Entry {
	d := rec.device
	d.Capabilities.StreamFormats = slices.Clone(d.Capabilities.StreamFormats)
	return Entry{Device: d, Manual: rec.manualAddress != "", Online: now.Before(rec.advertisedUntil) || now.Before(rec.manualUntil)}
}

type Registry struct {
	logger    *slog.Logger
	lifecycle sync.Mutex
	mu        sync.RWMutex
	run       *discovery
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
	items := make([]Entry, 0, len(r.records))
	for _, rec := range r.records {
		items = append(items, rec.entry(now))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Name == items[j].Name {
			return items[i].UDN < items[j].UDN
		}
		return items[i].Name < items[j].Name
	})
	return items
}

func (r *Registry) Lookup(udn string) (Entry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.records[udn]
	if !ok {
		return Entry{}, false
	}
	return rec.entry(time.Now()), true
}

func (r *Registry) Probe(ctx context.Context, address string) (Device, error) {
	r.mu.RLock()
	run := r.run
	if run == nil || run.ctx.Err() != nil {
		r.mu.RUnlock()
		return Device{}, ErrDisabled
	}
	run.probes.Add(1)
	r.mu.RUnlock()
	defer run.probes.Done()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(run.ctx, cancel)
	defer stop()
	return probeAddress(ctx, address)
}

func (r *Registry) Remember(d Device) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.records[d.UDN]
	if rec == nil {
		rec = &record{}
		r.records[d.UDN] = rec
	}
	if r.run != nil {
		d.discoveryContext = r.run.ctx
	}
	rec.device, rec.manualAddress = d, d.Address
	rec.source = d.peerIP
	rec.refreshed = time.Now()
	rec.manualUntil = rec.refreshed.Add(manualLease)
	if r.run != nil {
		delete(r.run.pending, d.UDN)
	}
}

func (r *Registry) Forget(udn string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.records[udn]
	if rec == nil {
		return
	}
	rec.manualAddress, rec.manualUntil = "", time.Time{}
	if rec.advertisedUntil.IsZero() {
		delete(r.records, udn)
	}
	if r.run != nil {
		delete(r.run.pending, udn)
	}
}
