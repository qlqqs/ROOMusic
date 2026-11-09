package zones

import (
	"encoding/json"
	"slices"
	"strings"
)

// PEQSettings 是调用方独占的临时配置；共享设置仅保存不可变规范文档。
type PEQSettings struct {
	Enabled  bool       `json:"enabled"`
	PreampDB float64    `json:"preamp_db"`
	Bands    []PEQBand  `json:"bands"`
	Source   *PEQSource `json:"source,omitempty"`
}

type PEQBand struct {
	Type     string      `json:"type"`
	FreqHz   float64     `json:"freq_hz"`
	GainDB   *float64    `json:"gain_db,omitempty"`
	Q        *float64    `json:"q,omitempty"`
	SlopeDB  *int        `json:"slope_db,omitempty"`
	Channels PEQChannels `json:"channels"`
	Enabled  bool        `json:"enabled"`
}

// PEQChannels 表示所有声道或声道下标集合，空集合表示没有匹配声道。
type PEQChannels struct {
	All     bool
	Indices []int
}

// PEQSource 仅保存套用时的来源快照，不引用运行时资源。
type PEQSource struct {
	Kind     string `json:"kind"`
	EQID     string `json:"eq_id"`
	Vendor   string `json:"vendor"`
	Product  string `json:"product"`
	Author   string `json:"author"`
	Details  string `json:"details"`
	Link     string `json:"link"`
	PresetID string `json:"preset_id"`
	Name     string `json:"name"`
}

func DefaultPEQDocument() SettingsDocument {
	document, err := NewPEQDocument(PEQSettings{})
	if err != nil {
		panic("默认参数均衡设置无效")
	}
	return document
}

func NewPEQDocument(settings PEQSettings) (SettingsDocument, error) {
	return newSettingsDocument(settings, normalizePEQ)
}

func normalizePEQ(settings PEQSettings) (PEQSettings, error) {
	if !peqInRange(settings.PreampDB, -30, 30) || len(settings.Bands) > 32 {
		return PEQSettings{}, ErrInvalidInput
	}
	if settings.PreampDB == 0 {
		settings.PreampDB = 0
	}
	// 规范化不得修改调用方的段、指针或声道切片。
	settings.Bands = append([]PEQBand{}, settings.Bands...)
	for i, band := range settings.Bands {
		if !validPEQBand(band) {
			return PEQSettings{}, ErrInvalidInput
		}
		channels, err := normalizePEQChannels(band.Channels)
		if err != nil {
			return PEQSettings{}, err
		}
		band.Channels = channels
		if band.GainDB != nil && *band.GainDB == 0 {
			gain := 0.0
			band.GainDB = &gain
		}
		settings.Bands[i] = band
	}
	if settings.Source != nil && !validPEQSource(*settings.Source) {
		return PEQSettings{}, ErrInvalidInput
	}
	return settings, nil
}

func peqInRange(value, min, max float64) bool {
	return finite(value) && value >= min && value <= max
}

func validPEQBand(band PEQBand) bool {
	if !peqInRange(band.FreqHz, 10, 40000) ||
		(band.GainDB != nil && !peqInRange(*band.GainDB, -30, 30)) ||
		(band.Q != nil && !peqInRange(*band.Q, 0.1, 30)) {
		return false
	}
	switch band.Type {
	case "peaking", "low_shelf", "high_shelf":
		return band.GainDB != nil && band.Q != nil && band.SlopeDB == nil
	case "low_pass", "high_pass":
		if band.GainDB != nil || band.SlopeDB == nil {
			return false
		}
		switch *band.SlopeDB {
		case 6, 12, 18, 24, 30, 36:
			return band.Q == nil || *band.SlopeDB == 12
		default:
			return false
		}
	case "band_pass", "notch", "all_pass":
		return band.Q != nil && band.GainDB == nil && band.SlopeDB == nil
	default:
		return false
	}
}

func normalizePEQChannels(channels PEQChannels) (PEQChannels, error) {
	if channels.All {
		if len(channels.Indices) != 0 {
			return PEQChannels{}, ErrInvalidInput
		}
		return PEQChannels{All: true}, nil
	}
	indices := append([]int{}, channels.Indices...)
	for _, index := range indices {
		if index < 0 {
			return PEQChannels{}, ErrInvalidInput
		}
	}
	slices.Sort(indices)
	return PEQChannels{Indices: slices.Compact(indices)}, nil
}

func validPEQSource(source PEQSource) bool {
	// PostgreSQL jsonb 无法保存零字符，必须在文档入口拒绝，不能等到写库时报错。
	for _, value := range [...]string{source.EQID, source.Vendor, source.Product, source.Author, source.Details, source.Link, source.PresetID, source.Name} {
		if strings.ContainsRune(value, 0) {
			return false
		}
	}
	switch source.Kind {
	case "opra":
		return source.PresetID == "" && source.Name == ""
	case "preset":
		return source.EQID == "" && source.Vendor == "" && source.Product == "" && source.Author == "" && source.Details == "" && source.Link == ""
	case "import":
		return source == (PEQSource{Kind: "import"})
	default:
		return false
	}
}

// MarshalJSON 按来源类型保留完整字段，即使署名或链接是空字符串。
func (source PEQSource) MarshalJSON() ([]byte, error) {
	if !validPEQSource(source) {
		return nil, ErrInvalidInput
	}
	switch source.Kind {
	case "opra":
		return json.Marshal(struct {
			Kind    string `json:"kind"`
			EQID    string `json:"eq_id"`
			Vendor  string `json:"vendor"`
			Product string `json:"product"`
			Author  string `json:"author"`
			Details string `json:"details"`
			Link    string `json:"link"`
		}{source.Kind, source.EQID, source.Vendor, source.Product, source.Author, source.Details, source.Link})
	case "preset":
		return json.Marshal(struct {
			Kind     string `json:"kind"`
			PresetID string `json:"preset_id"`
			Name     string `json:"name"`
		}{source.Kind, source.PresetID, source.Name})
	default:
		return json.Marshal(struct {
			Kind string `json:"kind"`
		}{source.Kind})
	}
}
