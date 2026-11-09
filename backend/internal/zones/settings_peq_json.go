package zones

import (
	"bytes"
	"encoding/json"
	"math"
	"math/big"
	"strings"
)

// ParsePEQDocument 要求完整文档，source 可以省略，但所有出现的字段都不能为 null。
func ParsePEQDocument(raw []byte) (SettingsDocument, error) {
	if !validPEQShape(raw) {
		return SettingsDocument{}, ErrInvalidInput
	}
	var settings PEQSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return SettingsDocument{}, ErrInvalidInput
	}
	return NewPEQDocument(settings)
}

func validPEQShape(raw []byte) bool {
	fields, ok := settingsObject(raw)
	if !ok || !peqObjectFields(fields, []string{"enabled", "preamp_db", "bands"}, "source") {
		return false
	}
	var bands []json.RawMessage
	if err := json.Unmarshal(fields["bands"], &bands); err != nil || len(bands) > 32 {
		return false
	}
	for _, rawBand := range bands {
		band, ok := settingsObject(rawBand)
		if !ok {
			return false
		}
		var kind string
		if err := json.Unmarshal(band["type"], &kind); err != nil {
			return false
		}
		required := []string{"type", "freq_hz", "channels", "enabled"}
		var optional []string
		switch kind {
		case "peaking", "low_shelf", "high_shelf":
			required = append(required, "gain_db", "q")
		case "low_pass", "high_pass":
			required = append(required, "slope_db")
			optional = []string{"q"}
		case "band_pass", "notch", "all_pass":
			required = append(required, "q")
		default:
			return false
		}
		if !peqObjectFields(band, required, optional...) {
			return false
		}
	}
	if rawSource, exists := fields["source"]; exists {
		source, ok := settingsObject(rawSource)
		if !ok {
			return false
		}
		var kind string
		if err := json.Unmarshal(source["kind"], &kind); err != nil {
			return false
		}
		var required []string
		switch kind {
		case "opra":
			required = []string{"kind", "eq_id", "vendor", "product", "author", "details", "link"}
		case "preset":
			required = []string{"kind", "preset_id", "name"}
		case "import":
			required = []string{"kind"}
		default:
			return false
		}
		if !peqObjectFields(source, required) {
			return false
		}
	}
	return true
}

func peqObjectFields(fields map[string]json.RawMessage, required []string, optional ...string) bool {
	for _, key := range required {
		if _, exists := fields[key]; !exists {
			return false
		}
	}
	for key, value := range fields {
		if isSettingsNull(value) {
			return false
		}
		allowed := false
		for _, name := range required {
			allowed = allowed || key == name
		}
		for _, name := range optional {
			allowed = allowed || key == name
		}
		if !allowed {
			return false
		}
	}
	return true
}

func (channels PEQChannels) MarshalJSON() ([]byte, error) {
	normalized, err := normalizePEQChannels(channels)
	if err != nil {
		return nil, err
	}
	if normalized.All {
		return []byte(`"all"`), nil
	}
	return json.Marshal(normalized.Indices)
}

func (channels *PEQChannels) UnmarshalJSON(raw []byte) error {
	var all string
	if err := json.Unmarshal(raw, &all); err == nil && all == "all" {
		*channels = PEQChannels{All: true}
		return nil
	}
	var values []json.RawMessage
	if isSettingsNull(raw) || json.Unmarshal(raw, &values) != nil {
		return ErrInvalidInput
	}
	indices := make([]int, len(values))
	for i, value := range values {
		index, err := peqJSONInteger(value)
		if err != nil || index < 0 {
			return ErrInvalidInput
		}
		indices[i] = index
	}
	normalized, err := normalizePEQChannels(PEQChannels{Indices: indices})
	if err != nil {
		return err
	}
	*channels = normalized
	return nil
}

// UnmarshalJSON 接受 12、12.0 与 12e0 等等值数字，仍拒绝非整数斜率。
func (band *PEQBand) UnmarshalJSON(raw []byte) error {
	type plain PEQBand
	var decoded plain
	wire := struct {
		*plain
		SlopeDB json.RawMessage `json:"slope_db"`
	}{plain: &decoded}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return ErrInvalidInput
	}
	if wire.SlopeDB != nil {
		slope, err := peqJSONInteger(wire.SlopeDB)
		if err != nil {
			return err
		}
		decoded.SlopeDB = &slope
	}
	*band = PEQBand(decoded)
	return nil
}

// peqJSONInteger 不经 float64 转换，避免较大声道下标在规范化时丢失精度。
func peqJSONInteger(raw []byte) (int, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || (raw[0] != '-' && (raw[0] < '0' || raw[0] > '9')) || !json.Valid(raw) {
		return 0, ErrInvalidInput
	}
	// 先排除极端指数和非整数；零的尾数单独处理，避免构造巨大有理数。
	var approximate float64
	if json.Unmarshal(raw, &approximate) != nil || approximate != math.Trunc(approximate) {
		return 0, ErrInvalidInput
	}
	if approximate == 0 {
		mantissa := strings.FieldsFunc(string(raw), func(r rune) bool { return r == 'e' || r == 'E' })[0]
		if strings.Trim(mantissa, "-0.") != "" {
			return 0, ErrInvalidInput
		}
		return 0, nil
	}
	value, ok := new(big.Rat).SetString(string(raw))
	if !ok || !value.IsInt() || !value.Num().IsInt64() {
		return 0, ErrInvalidInput
	}
	integer := value.Num().Int64()
	if int64(int(integer)) != integer {
		return 0, ErrInvalidInput
	}
	return int(integer), nil
}
