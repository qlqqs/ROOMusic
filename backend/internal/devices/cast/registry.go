// Package cast 发现并控制局域网中的 Google Cast 接收端。
package cast

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/devices/cast/castv2"
	"github.com/qlqq/roomusic/backend/internal/devices/lan"
	"github.com/qlqq/roomusic/backend/internal/devices/mdns"
	"golang.org/x/sync/errgroup"
)

const service = "_googlecast._tcp.local."
const refreshInterval = 30 * time.Second

var ErrAddress = errors.New("需要局域网 IPv4 地址")

type Device struct {
	Key, Name, Model, Address, LocalAddress string
	Volume                                  castv2.Volume
}
type Entry struct {
	Device
	Manual, Online bool
}
type record struct {
	device               Device
	announced            mdns.Entry
	manual               netip.Addr
	manualUntil, checked time.Time
	verified             bool
}

func (r *record) entry(now time.Time) Entry {
	return Entry{Device: r.device, Manual: r.manual.IsValid(), Online: r.verified && (now.Before(r.announced.Expires) || now.Before(r.manualUntil))}
}

type Registry struct {
	logger    *slog.Logger
	lifecycle sync.Mutex
	mu        sync.RWMutex
	records   map[string]*record
	cancel    context.CancelFunc
	stop      func()
}

func NewRegistry() *Registry {
	return NewRegistryWithLogger(slog.Default())
}

func NewRegistryWithLogger(logger *slog.Logger) *Registry {
	return &Registry{logger: logger, records: make(map[string]*record)}
}

func deviceFrom(e mdns.Entry) (Device, bool) {
	id := strings.TrimSpace(e.TXT["id"])
	if id == "" || strings.ContainsAny(id, "/\\") || !lan.IsLANAddress(e.IP) || e.Port == 0 {
		return Device{}, false
	}
	name := strings.TrimSpace(e.TXT["fn"])
	if name == "" {
		name = e.Instance
	}
	// DNS-SD TXT 是字节串，设备身份和显示文本将写入 PostgreSQL 的 text 列。
	model := e.TXT["md"]
	for _, value := range []string{id, name, model} {
		if !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
			return Device{}, false
		}
	}
	return Device{Key: "cast:" + id, Name: name, Model: model, Address: netip.AddrPortFrom(e.IP, e.Port).String(), LocalAddress: e.LocalIP.String()}, true
}

// 手动登记也读取 TXT id，确保同一设备始终与自动发现共用身份。
func Probe(ctx context.Context, address string) (Device, error) {
	ip, err := netip.ParseAddr(address)
	if err != nil || !lan.IsLANAddress(ip) {
		return Device{}, ErrAddress
	}
	return lookup(ctx, ip, "")
}
func lookup(ctx context.Context, ip netip.Addr, key string) (Device, error) {
	entry, err := mdns.LookupMatch(ctx, service, ip, func(e mdns.Entry) bool {
		d, ok := deviceFrom(e)
		return ok && (key == "" || d.Key == key)
	})
	if err != nil {
		return Device{}, err
	}
	d, ok := deviceFrom(entry)
	if !ok || ip != entry.IP {
		return Device{}, errors.New("Cast 设备声明无效")
	}
	d.Volume, err = castv2.Probe(ctx, d.Address)
	return d, err
}

func (r *Registry) List() []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]Entry, 0, len(r.records))
	now := time.Now()
	for _, rec := range r.records {
		items = append(items, rec.entry(now))
	}
	return items
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

func (r *Registry) Remember(d Device) {
	address, err := netip.ParseAddrPort(d.Address)
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
	rec.device, rec.manual, rec.verified = d, address.Addr(), true
	rec.checked = time.Now()
	rec.manualUntil = rec.checked.Add(2 * refreshInterval)
}
func (r *Registry) Forget(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec := r.records[key]; rec != nil {
		rec.manual = netip.Addr{}
		rec.manualUntil = time.Time{}
		if !time.Now().Before(rec.announced.Expires) {
			delete(r.records, key)
		}
	}
}
func (r *Registry) Offline(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec := r.records[key]; rec != nil {
		rec.verified = false
		rec.checked = time.Now()
	}
}

func (r *Registry) Start(parent context.Context, manual []Device) error {
	r.lifecycle.Lock()
	defer r.lifecycle.Unlock()
	if err := parent.Err(); err != nil {
		return err
	}
	if r.cancel != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	wake := make(chan struct{}, 1)
	r.mu.Lock()
	for _, d := range manual {
		if address, err := netip.ParseAddrPort(d.Address); err == nil {
			r.records[d.Key] = &record{device: d, manual: address.Addr()}
		}
	}
	r.mu.Unlock()
	stop, err := mdns.Start(ctx, service, r.logger, func(e mdns.Entry) {
		d, ok := deviceFrom(e)
		if !ok {
			return
		}
		r.mu.Lock()
		rec := r.records[d.Key]
		now := time.Now()
		if rec == nil && now.Before(e.Expires) && r.makeRoom(now) {
			rec = &record{device: d}
			r.records[d.Key] = rec
		}
		if rec != nil {
			if !now.Before(e.Expires) {
				if rec.announced.IP != e.IP && rec.manual != e.IP {
					r.mu.Unlock()
					return
				}
				rec.verified, rec.checked, rec.manualUntil = false, now, time.Time{}
			} else if rec.announced.IP != e.IP || rec.announced.Port != e.Port || !now.Before(rec.announced.Expires) {
				rec.checked = time.Time{}
				rec.verified = false
			}
			rec.announced = e
		}
		r.mu.Unlock()
		select {
		case wake <- struct{}{}:
		default:
		}
	})
	if err != nil {
		cancel()
		r.mu.Lock()
		clear(r.records)
		r.mu.Unlock()
		return err
	}
	done := make(chan struct{})
	r.cancel = cancel
	r.stop = func() { stop(); <-done }
	go func() {
		defer close(done)
		ticker := time.NewTicker(refreshInterval)
		defer ticker.Stop()
		for ctx.Err() == nil {
			r.refresh(ctx)
			select {
			case <-ctx.Done():
				return
			case <-wake:
			case <-ticker.C:
			}
		}
	}()
	return nil
}
func (r *Registry) Stop() {
	r.lifecycle.Lock()
	defer r.lifecycle.Unlock()
	if r.cancel == nil {
		return
	}
	r.cancel()
	r.stop()
	r.mu.Lock()
	clear(r.records)
	r.mu.Unlock()
	r.cancel, r.stop = nil, nil
}

// 保留已离线的登记信息，达到注册表上限时才回收自动发现的旧记录。
func (r *Registry) makeRoom(now time.Time) bool {
	if len(r.records) < 1024 {
		return true
	}
	var oldest *record
	for _, rec := range r.records {
		if !rec.manual.IsValid() && !now.Before(rec.announced.Expires) && (oldest == nil || rec.checked.Before(oldest.checked)) {
			oldest = rec
		}
	}
	if oldest == nil {
		return false
	}
	delete(r.records, oldest.device.Key)
	return true
}

func (r *Registry) refresh(ctx context.Context) {
	r.mu.Lock()
	now := time.Now()
	var jobs []record
	for _, rec := range r.records {
		if !rec.manual.IsValid() && !now.Before(rec.announced.Expires) {
			continue
		}
		if now.Sub(rec.checked) >= refreshInterval {
			rec.checked = now
			jobs = append(jobs, *rec)
		}
	}
	r.mu.Unlock()
	var workers errgroup.Group
	workers.SetLimit(4)
	for _, job := range jobs {
		if ctx.Err() != nil {
			break
		}
		workers.Go(func() error { r.verify(ctx, job); return nil })
	}
	workers.Wait()
}
func (r *Registry) verify(ctx context.Context, job record) {
	d := job.device
	var err error
	manual := !time.Now().Before(job.announced.Expires)
	if manual {
		d, err = lookup(ctx, job.manual, job.device.Key)
	} else {
		d, _ = deviceFrom(job.announced)
		d.Volume, err = castv2.Probe(ctx, d.Address)
	}
	r.mu.Lock()
	rec := r.records[job.device.Key]
	if rec != nil && rec.checked == job.checked && rec.manual == job.manual {
		rec.verified = err == nil && d.Key == job.device.Key
		if rec.verified {
			rec.device = d
			if manual {
				rec.manualUntil = time.Now().Add(2 * refreshInterval)
			}
		}
	}
	r.mu.Unlock()
}
