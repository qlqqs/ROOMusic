package zones

import "math"

// PlaybackSettings 是持久化、DTO 与渲染共享的不可变设置值。
// 文档字段只保存 SettingsDocument；临时切片或指针不存入此值。
type PlaybackSettings struct {
	Leveling         string           `json:"leveling"`
	Gapless          bool             `json:"gapless"`
	Crossfade        bool             `json:"crossfade"`
	CrossfadeSeconds int              `json:"crossfade_seconds"`
	Quality          string           `json:"quality"`
	Headroom         Headroom         `json:"headroom"`
	SampleRate       SettingsDocument `json:"sample_rate"`
}

type Headroom struct {
	Enabled       bool    `json:"enabled"`
	DB            float64 `json:"db"`
	ClipIndicator bool    `json:"clip_indicator"`
}

// ZoneSettings 与持久化设置共享同一值，复制时不共享可变数据。
type ZoneSettings = PlaybackSettings

type SettingsPatch struct {
	Leveling         *string           `json:"leveling"`
	Gapless          *bool             `json:"gapless"`
	Crossfade        *bool             `json:"crossfade"`
	CrossfadeSeconds *int              `json:"crossfade_seconds"`
	Quality          *string           `json:"quality"`
	Headroom         *Headroom         `json:"headroom"`
	SampleRate       *SettingsDocument `json:"sample_rate"`
}

// EqualSettings 统一设置相等判定；文档比较使用其规范 JSON 值。
func EqualSettings(a, b PlaybackSettings) bool { return a == b }

func defaultSettings() PlaybackSettings {
	return PlaybackSettings{Leveling: "auto", Gapless: true, CrossfadeSeconds: 5, Quality: "lossless", Headroom: defaultHeadroom(), SampleRate: DefaultSampleRateDocument()}
}

func settingsOf(p PlaybackSettings) ZoneSettings { return p }

func defaultHeadroom() Headroom { return Headroom{DB: -3} }
func validHeadroom(h Headroom) bool {
	// 与一位小数的规范值比较，避免直接对二进制浮点取余误拒合法步进。
	return finite(h.DB) && h.DB >= -30 && h.DB <= 0 && h.DB == math.Round(h.DB*10)/10
}

// ValidateSettingsPatch 校验已解码的设置取值；省略的键不参与替换。
func ValidateSettingsPatch(p SettingsPatch) error {
	if p.Leveling != nil && *p.Leveling != "off" && *p.Leveling != "auto" && *p.Leveling != "track" && *p.Leveling != "album" {
		return ErrInvalidInput
	}
	if p.CrossfadeSeconds != nil && *p.CrossfadeSeconds != 3 && *p.CrossfadeSeconds != 5 && *p.CrossfadeSeconds != 8 && *p.CrossfadeSeconds != 12 {
		return ErrInvalidInput
	}
	if p.Quality != nil && *p.Quality != "lossless" && *p.Quality != "lossy" {
		return ErrInvalidInput
	}
	if p.Headroom != nil && !validHeadroom(*p.Headroom) {
		return ErrInvalidInput
	}
	if p.SampleRate != nil {
		if _, err := ParseSampleRateDocument([]byte(p.SampleRate.canonical)); err != nil {
			return ErrInvalidInput
		}
	}
	return nil
}
func applySettings(p PlaybackSettings, patch SettingsPatch) PlaybackSettings {
	if patch.Leveling != nil {
		p.Leveling = *patch.Leveling
	}
	if patch.Gapless != nil {
		p.Gapless = *patch.Gapless
	}
	if patch.Crossfade != nil {
		p.Crossfade = *patch.Crossfade
	}
	if patch.CrossfadeSeconds != nil {
		p.CrossfadeSeconds = *patch.CrossfadeSeconds
	}
	if patch.Quality != nil {
		p.Quality = *patch.Quality
	}
	if patch.Headroom != nil {
		p.Headroom = normalizeHeadroom(*patch.Headroom)
	}
	if patch.SampleRate != nil {
		p.SampleRate = *patch.SampleRate
	}
	return p
}

func normalizeHeadroom(h Headroom) Headroom {
	// 正负零的语义相同，规范文档只保留正零。
	if h.DB == 0 {
		h.DB = 0
	}
	return h
}
