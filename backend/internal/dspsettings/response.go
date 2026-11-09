package dspsettings

import (
	"encoding/binary"
	"math"
	"math/cmplx"
	"sort"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/dsp/peq"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

type Curve struct {
	Channels zones.PEQChannels `json:"channels"`
	Points   [][2]float64      `json:"points"`
}

type Response struct {
	Curves []Curve `json:"curves"`
}

// PEQResponse 只预览，不保存；nil 采样率使用 48 kHz，显式值须为 8–768 kHz 整数。
func (s *Service) PEQResponse(document zones.SettingsDocument, sampleRate *int) (Response, error) {
	rate := 48000
	if sampleRate != nil {
		rate = *sampleRate
	}
	if rate < 8000 || rate > 768000 {
		return Response{}, ErrInvalidInput
	}
	document, err := validatePEQ(document)
	if err != nil {
		return Response{}, err
	}
	settings, err := zones.DecodeSettingsDocument[zones.PEQSettings](document)
	if err != nil {
		return Response{}, ErrInvalidInput
	}
	groups, sections := responseGroups(settings, float64(rate))
	preamp := settings.PreampDB
	if !settings.Enabled {
		preamp = 0
	}
	result := Response{Curves: make([]Curve, 0, len(groups))}
	for _, group := range groups {
		curve := Curve{Channels: group.channels, Points: make([][2]float64, 256)}
		for i := range curve.Points {
			hz := 20 * math.Pow(1000, float64(i)/255)
			z := cmplx.Exp(complex(0, -2*math.Pi*hz/float64(rate)))
			db := preamp
			for band, designed := range sections {
				if group.mask&(uint32(1)<<band) == 0 {
					continue
				}
				for _, section := range designed {
					numerator := cmplx.Abs(complex(section.B0, 0) + complex(section.B1, 0)*z + complex(section.B2, 0)*z*z)
					denominator := cmplx.Abs(1 + complex(section.A1, 0)*z + complex(section.A2, 0)*z*z)
					// 零点可能恰落在采样频率上；以最小正数表示零幅度，保证 JSON 不含无穷值。
					db += 20 * (math.Log10(math.Max(numerator, math.SmallestNonzeroFloat64)) - math.Log10(math.Max(denominator, math.SmallestNonzeroFloat64)))
				}
			}
			curve.Points[i] = [2]float64{hz, db}
		}
		result.Curves = append(result.Curves, curve)
	}
	return result, nil
}

type responseGroup struct {
	channels zones.PEQChannels
	mask     uint32
}

func responseGroups(settings zones.PEQSettings, rate float64) ([]responseGroup, [][]peq.Section) {
	sections := make([][]peq.Section, len(settings.Bands))
	var all uint32
	explicit := make(map[int]uint32)
	if settings.Enabled {
		for index, band := range settings.Bands {
			if !band.Enabled {
				continue
			}
			parameter := peq.Band{Type: band.Type, FreqHz: band.FreqHz}
			if band.GainDB != nil {
				parameter.GainDB = *band.GainDB
			}
			if band.Q != nil {
				parameter.Q = *band.Q
			}
			if band.SlopeDB != nil {
				parameter.SlopeDB = *band.SlopeDB
			}
			sections[index] = peq.Design(parameter, rate)
			bit := uint32(1) << index
			if band.Channels.All {
				all |= bit
			} else {
				for _, channel := range band.Channels.Indices {
					explicit[channel] |= bit
				}
			}
		}
	}
	groups := []responseGroup{{channels: zones.PEQChannels{All: true}, mask: all}}
	channels := make([]int, 0, len(explicit))
	for channel := range explicit {
		channels = append(channels, channel)
	}
	sort.Ints(channels)
	byCoefficients := make(map[string]int)
	for _, channel := range channels {
		mask := all | explicit[channel]
		key := responseCoefficientsKey(mask, sections)
		if groupIndex, ok := byCoefficients[key]; ok {
			groups[groupIndex].channels.Indices = append(groups[groupIndex].channels.Indices, channel)
		} else {
			byCoefficients[key] = len(groups)
			groups = append(groups, responseGroup{channels: zones.PEQChannels{Indices: []int{channel}}, mask: mask})
		}
	}
	return groups, sections
}

// 系数多重集合忽略声道与原始段序号；级联顺序不改变频响，重复节仍逐个保留。
func responseCoefficientsKey(mask uint32, sections [][]peq.Section) string {
	keys := make([]string, 0)
	for band, designed := range sections {
		if mask&(uint32(1)<<band) == 0 {
			continue
		}
		for _, section := range designed {
			var encoded [40]byte
			for index, coefficient := range [...]float64{section.A1, section.A2, section.B0, section.B1, section.B2} {
				// 正负零具有相同效果，不能因浮点符号位拆成不同曲线。
				if coefficient == 0 {
					coefficient = 0
				}
				binary.BigEndian.PutUint64(encoded[index*8:], math.Float64bits(coefficient))
			}
			keys = append(keys, string(encoded[:]))
		}
	}
	sort.Strings(keys)
	return strings.Join(keys, "")
}
