package zones

import "encoding/json"

// DownmixSettings 是调用方独占的临时配置；共享设置保存其不可变规范文档。
type DownmixSettings struct {
	Enabled bool `json:"enabled"`
}

func DefaultDownmixDocument() SettingsDocument {
	// 固定默认值与迁移一致，仍经统一构造器校验和规范化。
	document, err := NewDownmixDocument(DownmixSettings{Enabled: true})
	if err != nil {
		panic("默认降混设置无效")
	}
	return document
}

func NewDownmixDocument(settings DownmixSettings) (SettingsDocument, error) {
	return newSettingsDocument(settings, func(settings DownmixSettings) (DownmixSettings, error) {
		return settings, nil
	})
}

// ParseDownmixDocument 要求完整对象，enabled 必须为非 null 的布尔值。
func ParseDownmixDocument(raw []byte) (SettingsDocument, error) {
	if !validDownmixShape(raw) {
		return SettingsDocument{}, ErrInvalidInput
	}
	var settings DownmixSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return SettingsDocument{}, ErrInvalidInput
	}
	return NewDownmixDocument(settings)
}

func validDownmixShape(raw []byte) bool {
	fields, ok := settingsObject(raw)
	if !ok || len(fields) != 1 {
		return false
	}
	value, exists := fields["enabled"]
	return exists && !isSettingsNull(value)
}
