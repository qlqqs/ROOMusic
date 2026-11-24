package upnp

import (
	"context"
	"errors"
)

type CastFormat struct {
	Codec         string `json:"codec"`
	MaxSampleRate int    `json:"max_sample_rate"`
}

var ErrKind = errors.New("需要有效的局域网设备种类")
var ErrFormat = errors.New("需要有效的 Cast 编码格式与采样率上限")

func (s *Service) formats(ctx context.Context) (map[string]CastFormat, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT udn,codec,max_sample_rate FROM cast_device_formats`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := map[string]CastFormat{}
	for rows.Next() {
		var key string
		var f CastFormat
		if err := rows.Scan(&key, &f.Codec, &f.MaxSampleRate); err != nil {
			return nil, err
		}
		items[key] = f
	}
	return items, rows.Err()
}

func applyFormat(e *Entry, formats map[string]CastFormat) *CastFormat {
	if e.Kind != "cast" {
		return nil
	}
	f, ok := formats[e.UDN]
	if !ok {
		f = CastFormat{Codec: "mp3", MaxSampleRate: 48000}
	}
	e.Capabilities.StreamFormats = []string{f.Codec}
	e.Capabilities.MaxSampleRate = f.MaxSampleRate
	return &f
}

func (s *Service) requireDevice(ctx context.Context, udn string) error {
	if _, ok := s.protocols[deviceKind(udn)].registry.Lookup(udn); ok {
		return nil
	}
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM upnp_manual_devices WHERE udn=$1)`, udn).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrUnknownDevice
	}
	return nil
}

func (s *Service) SetFormat(ctx context.Context, udn string, f CastFormat) (CastFormat, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if deviceKind(udn) != "cast" || (f.Codec != "mp3" && f.Codec != "aac" && f.Codec != "flac") {
		return CastFormat{}, ErrFormat
	}
	if f.MaxSampleRate != 44100 && f.MaxSampleRate != 48000 && !(f.Codec == "flac" && f.MaxSampleRate == 96000) {
		return CastFormat{}, ErrFormat
	}
	if err := s.requireDevice(ctx, udn); err != nil {
		return CastFormat{}, err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO cast_device_formats(udn,codec,max_sample_rate) VALUES($1,$2,$3)
 ON CONFLICT(udn) DO UPDATE SET codec=excluded.codec,max_sample_rate=excluded.max_sample_rate`, udn, f.Codec, f.MaxSampleRate)
	return f, err
}
