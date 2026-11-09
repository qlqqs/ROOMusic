package zones

import (
	"bytes"
	"encoding/json"
)

// SettingsDocument 只保存经过具体设置类型规范化及校验的 JSON 对象。
// 值复制不共享可变数据；零值表示尚未构造，序列化和解码均返回 ErrInvalidInput。
type SettingsDocument struct {
	canonical string
}

func (d SettingsDocument) MarshalJSON() ([]byte, error) {
	if d.canonical == "" {
		return nil, ErrInvalidInput
	}
	return []byte(d.canonical), nil
}

// UnmarshalJSON 禁止跳过具体设置的构造入口，避免将原始 JSON 当成规范文档。
func (*SettingsDocument) UnmarshalJSON([]byte) error { return ErrInvalidInput }

// DecodeSettingsDocument 每次从零值解码，返回调用方独占的临时配置。
// T 应为该文档对应的设置类型；不复用调用方的切片或指针。
func DecodeSettingsDocument[T any](d SettingsDocument) (T, error) {
	var value T
	if d.canonical == "" {
		return value, ErrInvalidInput
	}
	if err := json.Unmarshal([]byte(d.canonical), &value); err != nil {
		var zero T
		return zero, ErrInvalidInput
	}
	return value, nil
}

// newSettingsDocument 仅供包内具体设置编解码器使用，不接受任意原始 JSON。
// normalize 拥有该类型的默认值、数值规范、数组顺序及可空字段语义。
func newSettingsDocument[T any](value T, normalize func(T) (T, error)) (SettingsDocument, error) {
	value, err := normalize(value)
	if err != nil {
		return SettingsDocument{}, err
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) == 0 || encoded[0] != '{' {
		return SettingsDocument{}, ErrInvalidInput
	}
	return SettingsDocument{canonical: string(encoded)}, nil
}

// NewHeadroomDocument 从明确类型构造规范文档，不改变 Headroom 的外部对象形状。
func NewHeadroomDocument(h Headroom) (SettingsDocument, error) {
	return newSettingsDocument(h, func(h Headroom) (Headroom, error) {
		if !validHeadroom(h) {
			return Headroom{}, ErrInvalidInput
		}
		return normalizeHeadroom(h), nil
	})
}

// ParseHeadroomDocument 要求完整对象；字段顺序、空白及数值拼写不影响规范值。
func ParseHeadroomDocument(raw []byte) (SettingsDocument, error) {
	if !validHeadroomShape(raw) {
		return SettingsDocument{}, ErrInvalidInput
	}
	h := defaultHeadroom()
	if err := json.Unmarshal(raw, &h); err != nil {
		return SettingsDocument{}, ErrInvalidInput
	}
	return NewHeadroomDocument(h)
}

// ValidateSettingsShape 统一设置键与完整文档形状；HTTP 仍负责类型错误、重复键与大小限制。
func ValidateSettingsShape(raw []byte) error {
	fields, ok := settingsObject(raw)
	if !ok {
		return ErrInvalidInput
	}
	for key, value := range fields {
		if isSettingsNull(value) {
			return ErrInvalidInput
		}
		switch key {
		case "leveling", "gapless", "crossfade", "crossfade_seconds", "quality":
		case "headroom":
			if !validHeadroomShape(value) {
				return ErrInvalidInput
			}
		case "convolution":
			if !validConvolutionShape(value) {
				return ErrInvalidInput
			}
		case "peq":
			if !validPEQShape(value) {
				return ErrInvalidInput
			}
		case "downmix":
			if !validDownmixShape(value) {
				return ErrInvalidInput
			}
		case "sample_rate":
			if !validSampleRateShape(value) {
				return ErrInvalidInput
			}
		default:
			return ErrInvalidInput
		}
	}
	return nil
}

func validHeadroomShape(raw []byte) bool {
	fields, ok := settingsObject(raw)
	required := []string{"enabled", "db", "clip_indicator"}
	if !ok || len(fields) != len(required) {
		return false
	}
	for _, key := range required {
		value, exists := fields[key]
		if !exists || isSettingsNull(value) {
			return false
		}
	}
	return true
}

func settingsObject(raw []byte) (map[string]json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	err := json.Unmarshal(raw, &fields)
	return fields, err == nil && fields != nil
}

func isSettingsNull(raw []byte) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// UnmarshalJSON 将算法完整文档送入所属解析入口，不能直接解码 SettingsDocument。
func (p *SettingsPatch) UnmarshalJSON(raw []byte) error {
	type plain SettingsPatch
	var decoded plain
	wire := struct {
		*plain
		SampleRate  json.RawMessage `json:"sample_rate"`
		Downmix     json.RawMessage `json:"downmix"`
		PEQ         json.RawMessage `json:"peq"`
		Convolution json.RawMessage `json:"convolution"`
	}{plain: &decoded}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return err
	}
	if wire.SampleRate != nil {
		document, err := ParseSampleRateDocument(wire.SampleRate)
		if err != nil {
			return err
		}
		decoded.SampleRate = &document
	}
	if wire.Downmix != nil {
		document, err := ParseDownmixDocument(wire.Downmix)
		if err != nil {
			return err
		}
		decoded.Downmix = &document
	}
	if wire.PEQ != nil {
		document, err := ParsePEQDocument(wire.PEQ)
		if err != nil {
			return err
		}
		decoded.PEQ = &document
	}
	if wire.Convolution != nil {
		document, err := ParseConvolutionDocument(wire.Convolution)
		if err != nil {
			return err
		}
		decoded.Convolution = &document
	}
	*p = SettingsPatch(decoded)
	return nil
}

// UnmarshalJSON 供 DTO 消费方恢复规范设置；共享文档本身不接受任意 JSON。
func (p *PlaybackSettings) UnmarshalJSON(raw []byte) error {
	type plain PlaybackSettings
	var decoded plain
	wire := struct {
		*plain
		SampleRate  json.RawMessage `json:"sample_rate"`
		Downmix     json.RawMessage `json:"downmix"`
		PEQ         json.RawMessage `json:"peq"`
		Convolution json.RawMessage `json:"convolution"`
	}{plain: &decoded}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return err
	}
	if wire.SampleRate != nil {
		document, err := ParseSampleRateDocument(wire.SampleRate)
		if err != nil {
			return err
		}
		decoded.SampleRate = document
	}
	if wire.Downmix != nil {
		document, err := ParseDownmixDocument(wire.Downmix)
		if err != nil {
			return err
		}
		decoded.Downmix = document
	}
	if wire.PEQ != nil {
		document, err := ParsePEQDocument(wire.PEQ)
		if err != nil {
			return err
		}
		decoded.PEQ = document
	}
	if wire.Convolution != nil {
		document, err := ParseConvolutionDocument(wire.Convolution)
		if err != nil {
			return err
		}
		decoded.Convolution = document
	}
	*p = PlaybackSettings(decoded)
	return nil
}
