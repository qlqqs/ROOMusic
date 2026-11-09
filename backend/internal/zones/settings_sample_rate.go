package zones

import (
	"encoding/json"
	"math"
)

// SampleRateSettings 是调用方独占的临时配置；共享设置保存其不可变规范文档。
type SampleRateSettings struct {
	Strategy   string `json:"strategy"`
	CustomRate *int   `json:"custom_rate"`
	Filter     string `json:"filter"`
}

func DefaultSampleRateDocument() SettingsDocument {
	// 固定默认值与迁移一致，仍经统一构造器校验和规范化。
	document, err := NewSampleRateDocument(SampleRateSettings{Strategy: "none", Filter: "linear_precise"})
	if err != nil {
		panic("默认采样率设置无效")
	}
	return document
}

func NewSampleRateDocument(settings SampleRateSettings) (SettingsDocument, error) {
	return newSettingsDocument(settings, normalizeSampleRate)
}

func normalizeSampleRate(settings SampleRateSettings) (SampleRateSettings, error) {
	switch settings.Strategy {
	case "none", "max_sample_rate", "power_of_2", "dsd", "custom":
	default:
		return SampleRateSettings{}, ErrInvalidInput
	}
	switch settings.Filter {
	case "linear_precise", "minimum_precise", "linear_smooth", "minimum_smooth":
	default:
		return SampleRateSettings{}, ErrInvalidInput
	}
	if settings.CustomRate != nil {
		switch *settings.CustomRate {
		case 44100, 48000, 88200, 96000, 176400, 192000, 352800, 384000, 705600, 768000:
		default:
			return SampleRateSettings{}, ErrInvalidInput
		}
	}
	if settings.Strategy == "custom" && settings.CustomRate == nil {
		settings.Strategy = "none"
	}
	return settings, nil
}

// ParseSampleRateDocument 要求完整对象；只允许 custom_rate 为 null。
func ParseSampleRateDocument(raw []byte) (SettingsDocument, error) {
	if !validSampleRateShape(raw) {
		return SettingsDocument{}, ErrInvalidInput
	}
	var wire struct {
		Strategy   string   `json:"strategy"`
		CustomRate *float64 `json:"custom_rate"`
		Filter     string   `json:"filter"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return SettingsDocument{}, ErrInvalidInput
	}
	settings := SampleRateSettings{Strategy: wire.Strategy, Filter: wire.Filter}
	if wire.CustomRate != nil {
		// 与其他设置沿用浮点语义，兼容 48000.0 / 48e3，拒绝非整数。
		rate := *wire.CustomRate
		if rate < 44100 || rate > 768000 || rate != math.Trunc(rate) {
			return SettingsDocument{}, ErrInvalidInput
		}
		value := int(rate)
		settings.CustomRate = &value
	}
	return NewSampleRateDocument(settings)
}

func validSampleRateShape(raw []byte) bool {
	fields, ok := settingsObject(raw)
	if !ok || len(fields) != 3 {
		return false
	}
	for _, key := range []string{"strategy", "custom_rate", "filter"} {
		value, exists := fields[key]
		if !exists || (key != "custom_rate" && isSettingsNull(value)) {
			return false
		}
	}
	return true
}
