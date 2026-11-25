package upnp

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/devices/slimproto"
)

type slimRegistry struct{ *slimproto.Registry }

func (r slimRegistry) Start(ctx context.Context, _ []ManualDevice) error {
	return r.Registry.Start(ctx)
}
func (r slimRegistry) Remember(Device) {}
func (r slimRegistry) Forget(string)   {}
func (r slimRegistry) Probe(context.Context, string) (Device, error) {
	return Device{}, errors.ErrUnsupported
}

type SlimProtoInfo struct {
	ModelName             string `json:"model_name"`
	Firmware              string `json:"firmware"`
	DeclaredMaxSampleRate int    `json:"declared_max_sample_rate"`
	Unsupported           bool   `json:"unsupported"`
}

func slimEntry(d slimproto.Device) Entry {
	level := 1.0
	return Entry{Online: true, Device: Device{
		Kind: "slimproto", UDN: d.Key, Name: d.Name, Model: d.Model, Address: d.Address.String(),
		LocalAddress: d.LocalAddress.String(), slimPlayer: d.Player, initialVolume: &level,
		Capabilities: Capabilities{
			Pause: true, Volume: "number", StreamFormats: []string{"flac"}, FormatDeclared: true,
			MaxSampleRate: min(d.MaxSampleRate, 768000), MaxBitDepth: 24,
			SlimProto: &SlimProtoInfo{
				ModelName: d.ModelName, Firmware: d.Firmware, DeclaredMaxSampleRate: d.MaxSampleRate,
				Unsupported: !slices.Contains(d.Codecs, "flc"),
			},
		},
	}}
}

func (r slimRegistry) List() []Entry {
	rows := r.Registry.List()
	out := make([]Entry, 0, len(rows))
	for _, row := range rows {
		out = append(out, slimEntry(row))
	}
	return out
}
func (r slimRegistry) Lookup(key string) (Entry, bool) {
	row, ok := r.Registry.Lookup(key)
	if !ok {
		return Entry{}, false
	}
	return slimEntry(row), true
}

var ErrSlimProtoFormat = errors.New("SlimProto 需要 FLAC、播放器支持的采样率上限和有效的音量方式")

func (s *Service) configureSlimPlayer(ctx context.Context, key string, declared int) (bool, error) {
	var fixed bool
	err := s.db.QueryRowContext(ctx, `INSERT INTO slimproto_device_formats(udn,declared_max_sample_rate)
 VALUES($1,$2) ON CONFLICT(udn) DO UPDATE SET declared_max_sample_rate=excluded.declared_max_sample_rate
 RETURNING volume='fixed'`, key, declared).Scan(&fixed)
	return fixed, err
}

func (s *Service) setSlimFormat(ctx context.Context, key string, f DeviceFormat) (DeviceFormat, error) {
	if f.Codec != "flac" || f.MaxBitDepth != 0 || (f.Volume != "number" && f.Volume != "fixed") {
		return DeviceFormat{}, ErrSlimProtoFormat
	}
	switch f.MaxSampleRate {
	case 0, 44100, 48000, 88200, 96000, 176400, 192000, 352800, 384000, 705600, 768000:
	default:
		return DeviceFormat{}, ErrSlimProtoFormat
	}
	mac, err := net.ParseMAC(strings.TrimPrefix(key, "slim:"))
	if err != nil || len(mac) != 6 || "slim:"+mac.String() != key {
		return DeviceFormat{}, ErrSlimProtoFormat
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO slimproto_device_formats(udn,max_sample_rate,volume)
 VALUES($1,NULLIF($2,0),$3) ON CONFLICT(udn) DO UPDATE
 SET max_sample_rate=excluded.max_sample_rate,volume=excluded.volume
 WHERE slimproto_device_formats.declared_max_sample_rate IS NULL OR $2<=slimproto_device_formats.declared_max_sample_rate`, key, f.MaxSampleRate, f.Volume)
	if err != nil {
		return DeviceFormat{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return DeviceFormat{}, err
	}
	if count == 0 {
		return DeviceFormat{}, ErrSlimProtoFormat
	}
	err = s.slim.RefreshFixed(key, func() (bool, error) {
		var fixed bool
		err := s.db.QueryRowContext(ctx, `SELECT volume='fixed' FROM slimproto_device_formats WHERE udn=$1`, key).Scan(&fixed)
		return fixed, err
	})
	if err != nil {
		return DeviceFormat{}, err
	}
	if s.output != nil {
		s.output.mu.Lock()
		owner := s.output.owners[key]
		s.output.mu.Unlock()
		if owner != nil {
			if err := s.output.zones.RefreshManagedDevices(ctx, owner.actor.ID); err != nil {
				return DeviceFormat{}, err
			}
		}
	}
	return f, nil
}

func (s *Service) slimUnavailable() *string {
	if s.slim != nil && s.slim.PortInUse() {
		reason := "port_in_use"
		return &reason
	}
	return nil
}
