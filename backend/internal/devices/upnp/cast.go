package upnp

import (
	"context"
	"sync"

	"github.com/qlqq/roomusic/backend/internal/devices/cast"
)

// 两种协议共享手动登记、成员授权和物理占用，注册表各自管理网络发现。
type deviceRegistry interface {
	Start(context.Context, []ManualDevice) error
	Stop()
	List() []Entry
	Lookup(string) (Entry, bool)
	Probe(context.Context, string) (Device, error)
	Remember(Device)
	Forget(string)
}

type protocol struct {
	probes   sync.WaitGroup
	registry deviceRegistry
	ctx      context.Context
	cancel   context.CancelFunc
}

type castRegistry struct{ *cast.Registry }

func (r castRegistry) Start(ctx context.Context, targets []ManualDevice) error {
	items := make([]cast.Device, 0, len(targets))
	for _, t := range targets {
		items = append(items, cast.Device{Key: t.UDN, Address: t.Address})
	}
	return r.Registry.Start(ctx, items)
}

func castDevice(d cast.Device) Device {
	volume := "number"
	if d.Volume.ControlType == "fixed" {
		volume = "fixed"
	}
	level := d.Volume.Level
	if d.Volume.Muted {
		muted := 0.0
		level = &muted
	} else if volume == "fixed" && level == nil {
		full := 1.0
		level = &full
	}
	return Device{Kind: "cast", UDN: d.Key, Name: d.Name, Model: d.Model, Address: d.Address, LocalAddress: d.LocalAddress, initialVolume: level,
		Capabilities: Capabilities{Pause: true, Volume: volume, StreamFormats: []string{"mp3"}, FormatDeclared: true, MaxSampleRate: 48000, MaxBitDepth: 24}}
}

func (r castRegistry) List() []Entry {
	rows := r.Registry.List()
	items := make([]Entry, 0, len(rows))
	for _, row := range rows {
		items = append(items, Entry{Device: castDevice(row.Device), Online: row.Online, Manual: row.Manual})
	}
	return items
}
func (r castRegistry) Lookup(key string) (Entry, bool) {
	row, ok := r.Registry.Lookup(key)
	if !ok {
		return Entry{}, false
	}
	return Entry{Device: castDevice(row.Device), Online: row.Online, Manual: row.Manual}, true
}
func (r castRegistry) Probe(ctx context.Context, address string) (Device, error) {
	d, err := cast.Probe(ctx, address)
	if err != nil {
		return Device{}, err
	}
	return castDevice(d), nil
}
func (r castRegistry) Remember(d Device) {
	item := cast.Device{Key: d.UDN, Name: d.Name, Model: d.Model, Address: d.Address, LocalAddress: d.LocalAddress}
	item.Volume.ControlType = d.Capabilities.Volume
	item.Volume.Level = d.initialVolume
	r.Registry.Remember(item)
}
