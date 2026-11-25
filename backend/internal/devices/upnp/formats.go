package upnp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type DeviceFormat struct {
	Volume        string `json:"volume,omitempty"`
	Codec         string `json:"codec"`
	MaxSampleRate int    `json:"max_sample_rate"`
	MaxBitDepth   int    `json:"max_bit_depth,omitempty"`
}

// SlimProto 用 null 表示跟随设备声明，其余协议保存具体采样率。
func (f DeviceFormat) MarshalJSON() ([]byte, error) {
	type fields DeviceFormat
	if f.Volume != "" && f.MaxSampleRate == 0 {
		return json.Marshal(struct {
			fields
			MaxSampleRate *int `json:"max_sample_rate"`
		}{fields: fields(f)})
	}
	return json.Marshal(fields(f))
}

type storedFormat struct {
	DeviceFormat
	paired bool
}

var ErrKind = errors.New("需要有效的局域网设备种类")
var ErrFormat = errors.New("需要有效的 Cast 编码格式与采样率上限")
var ErrAirPlayFormat = errors.New("AirPlay 支持 44.1 kHz/16 位 ALAC，AirPlay 2 还可选择 48 kHz/24 位")

func (s *Service) formats(ctx context.Context) (map[string]storedFormat, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT udn,codec,max_sample_rate,0,false,'' FROM cast_device_formats
		UNION ALL SELECT udn,'alac',COALESCE(max_sample_rate,44100),COALESCE(max_bit_depth,16),p.udn IS NOT NULL,''
		FROM airplay_device_formats f FULL JOIN airplay_pairings p USING (udn)
		UNION ALL SELECT udn,'flac',COALESCE(max_sample_rate,0),0,false,volume
		FROM slimproto_device_formats WHERE max_sample_rate IS NOT NULL OR volume <> 'number'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := map[string]storedFormat{}
	for rows.Next() {
		var key string
		var f storedFormat
		if err := rows.Scan(&key, &f.Codec, &f.MaxSampleRate, &f.MaxBitDepth, &f.paired, &f.Volume); err != nil {
			return nil, err
		}
		items[key] = f
	}
	return items, rows.Err()
}

func applyFormat(e *Entry, formats map[string]storedFormat) *DeviceFormat {
	stored := formats[e.UDN]
	f := stored.DeviceFormat
	switch e.Kind {
	case "slimproto":
		if f.Codec == "" {
			f = DeviceFormat{Codec: "flac", Volume: "number"}
		}
		if f.MaxSampleRate > 0 {
			e.Capabilities.MaxSampleRate = min(e.Capabilities.MaxSampleRate, f.MaxSampleRate)
		}
		e.Capabilities.Volume = f.Volume
		return &f
	case "cast":
		if f.Codec == "" {
			f = DeviceFormat{Codec: "mp3", MaxSampleRate: 48000}
		}
	case "airplay":
		native := e.Capabilities.AirPlay != nil && e.Capabilities.AirPlay.AirPlayAddress != ""
		if f.Codec == "" || !native {
			f = DeviceFormat{Codec: "alac", MaxSampleRate: 44100, MaxBitDepth: 16}
		}
		if info := e.Capabilities.AirPlay; info != nil {
			info.Paired = stored.paired
			info.Unsupported = info.UnavailableReason != ""
		}
		e.Capabilities.MaxBitDepth = f.MaxBitDepth
	default:
		return nil
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

func (s *Service) SetFormat(ctx context.Context, udn string, f DeviceFormat) (DeviceFormat, error) {
	if deviceKind(udn) == "slimproto" {
		return s.setSlimFormat(ctx, udn, f)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if f.Volume != "" {
		return DeviceFormat{}, ErrFormat
	}
	if deviceKind(udn) == "airplay" {
		return s.setAirPlayFormat(ctx, udn, f)
	}
	if deviceKind(udn) != "cast" || f.MaxBitDepth != 0 || (f.Codec != "mp3" && f.Codec != "aac" && f.Codec != "flac") {
		return DeviceFormat{}, ErrFormat
	}
	if f.MaxSampleRate != 44100 && f.MaxSampleRate != 48000 && !(f.Codec == "flac" && f.MaxSampleRate == 96000) {
		return DeviceFormat{}, ErrFormat
	}
	if err := s.requireDevice(ctx, udn); err != nil {
		return DeviceFormat{}, err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO cast_device_formats(udn,codec,max_sample_rate) VALUES($1,$2,$3)
 ON CONFLICT(udn) DO UPDATE SET codec=excluded.codec,max_sample_rate=excluded.max_sample_rate`, udn, f.Codec, f.MaxSampleRate)
	return f, err
}

func (s *Service) setAirPlayFormat(ctx context.Context, key string, f DeviceFormat) (DeviceFormat, error) {
	standard := f.MaxSampleRate == 44100 && f.MaxBitDepth == 16
	high := f.MaxSampleRate == 48000 && f.MaxBitDepth == 24
	if f.Codec != "alac" || !(standard || high) {
		return DeviceFormat{}, ErrAirPlayFormat
	}
	row, ok := s.protocols["airplay"].registry.Lookup(key)
	if !ok || row.Capabilities.AirPlay == nil || row.Capabilities.AirPlay.DeviceID == "" {
		var raw []byte
		err := s.db.QueryRowContext(ctx, `SELECT capabilities FROM upnp_manual_devices WHERE udn=$1 AND kind='airplay'`, key).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return DeviceFormat{}, ErrUnknownDevice
		}
		if err != nil {
			return DeviceFormat{}, err
		}
		if err = json.Unmarshal(raw, &row.Capabilities); err != nil {
			return DeviceFormat{}, err
		}
	}
	if high && (row.Capabilities.AirPlay == nil || row.Capabilities.AirPlay.AirPlayAddress == "") {
		return DeviceFormat{}, ErrAirPlayFormat
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO airplay_device_formats(udn,max_sample_rate,max_bit_depth) VALUES ($1,$2,$3)
	ON CONFLICT(udn) DO UPDATE SET max_sample_rate=excluded.max_sample_rate,max_bit_depth=excluded.max_bit_depth`, key, f.MaxSampleRate, f.MaxBitDepth)
	return f, err
}
