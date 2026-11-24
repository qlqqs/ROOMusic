package upnp

import (
	"context"

	"github.com/qlqq/roomusic/backend/internal/devices/airplay"
)

type airplayRegistry struct{ *airplay.Registry }

func (r airplayRegistry) Start(ctx context.Context, targets []ManualDevice) error {
	devices := make([]airplay.Device, 0, len(targets))
	for _, t := range targets {
		devices = append(devices, airplay.Device{Key: t.UDN, Address: t.Address})
	}
	return r.Registry.Start(ctx, devices)
}

func airplayDevice(d airplay.Device) Device {
	// 两代协议的默认输出格式都是双声道 44.1 kHz、16 位 ALAC。
	return Device{
		Kind: "airplay", UDN: d.Key, Name: d.Name, Model: d.Model,
		Address: d.Address, LocalAddress: d.LocalAddress,
		Capabilities: Capabilities{AirPlay: &d.Info, Pause: true, Volume: "number",
			StreamFormats: []string{"alac"}, MaxSampleRate: 44100, MaxBitDepth: 16},
	}
}

func (r airplayRegistry) List() []Entry {
	rows := r.Registry.List()
	items := make([]Entry, 0, len(rows))
	for _, row := range rows {
		items = append(items, Entry{Device: airplayDevice(row.Device), Online: row.Online, Manual: row.Manual})
	}
	return items
}

func (r airplayRegistry) Lookup(key string) (Entry, bool) {
	row, ok := r.Registry.Lookup(key)
	if !ok {
		return Entry{}, false
	}
	return Entry{Device: airplayDevice(row.Device), Online: row.Online, Manual: row.Manual}, true
}

func (r airplayRegistry) Probe(ctx context.Context, address string) (Device, error) {
	d, err := airplay.Probe(ctx, address)
	if err != nil {
		return Device{}, err
	}
	return airplayDevice(d), nil
}

func (r airplayRegistry) Remember(d Device) {
	r.Registry.Remember(airplay.Device{Key: d.UDN, Name: d.Name, Model: d.Model, Address: d.Address, LocalAddress: d.LocalAddress, Info: *d.Capabilities.AirPlay})
}
