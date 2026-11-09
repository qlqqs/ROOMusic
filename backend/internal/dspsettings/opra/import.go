package opra

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// ErrImportEmpty 表示没有解析到启用的 Filter；HTTP 错误映射由接入层负责。
var ErrImportEmpty = errors.New("opra: 导入文本没有可用滤波段")

// ImportResult 是文本解析结果，不是播放区设置文档。调用方负责补齐启用、声道与来源，
// 再经 zones 的统一入口校验设置范围；文本导入不采用 OPRA 映射的范围钳位策略。
type ImportResult struct {
	PreampDB          float64
	Bands             []Band
	UnrecognizedLines []int
}

var (
	preampLine = regexp.MustCompile(`(?i)^preamp\s*:\s*(\S+)\s+dB$`)
	filterLine = regexp.MustCompile(`(?i)^filter\s+[0-9]+\s*:\s*(ON|OFF)(?:\s+(.*))?$`)
)

// ParseText 解析 AutoEq / Equalizer APO 的参数均衡文本，不保存或访问外部资源。
// 大小写不敏感，数字使用点作小数点；空白与 # 注释忽略，OFF 段跳过。
// 无法识别的非空行返回一开始计数的行号；即使返回 ErrImportEmpty，也保留解析结果。
func ParseText(text string) (ImportResult, error) {
	result := ImportResult{Bands: []Band{}, UnrecognizedLines: []int{}}
	text = strings.TrimPrefix(text, "\ufeff")
	for index, line := range strings.Split(text, "\n") {
		line, _, _ = strings.Cut(line, "#")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if match := preampLine.FindStringSubmatch(line); match != nil {
			if value, ok := importNumber(match[1]); ok {
				result.PreampDB = value
				continue
			}
		}
		if match := filterLine.FindStringSubmatch(line); match != nil {
			if strings.EqualFold(match[1], "OFF") {
				continue
			}
			if band, ok := parseFilter(match[2]); ok {
				result.Bands = append(result.Bands, band)
				continue
			}
		}
		result.UnrecognizedLines = append(result.UnrecognizedLines, index+1)
	}
	if len(result.Bands) == 0 {
		return result, ErrImportEmpty
	}
	return result, nil
}

func parseFilter(text string) (Band, bool) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return Band{}, false
	}
	var band Band
	var needGain, needQ bool
	switch strings.ToUpper(fields[0]) {
	case "PK", "PEQ", "MODAL":
		band.Type, needGain, needQ = "peaking", true, true
	case "LSC":
		band.Type, needGain, needQ = "low_shelf", true, true
	case "HSC":
		band.Type, needGain, needQ = "high_shelf", true, true
	case "LS":
		band.Type, band.Q, needGain = "low_shelf", 0.71, true
	case "HS":
		band.Type, band.Q, needGain = "high_shelf", 0.71, true
	case "LP", "LPQ":
		band.Type, band.SlopeDB = "low_pass", 12
		needQ = strings.EqualFold(fields[0], "LPQ")
	case "HP", "HPQ":
		band.Type, band.SlopeDB = "high_pass", 12
		needQ = strings.EqualFold(fields[0], "HPQ")
	case "BP":
		band.Type, needQ = "band_pass", true
	case "NO":
		band.Type, needQ = "notch", true
	case "AP":
		band.Type, needQ = "all_pass", true
	default:
		return Band{}, false
	}

	var frequency, gain, q bool
	for i := 1; i < len(fields); {
		if i+1 >= len(fields) {
			return Band{}, false
		}
		key := strings.ToUpper(fields[i])
		value, ok := importNumber(fields[i+1])
		if !ok {
			return Band{}, false
		}
		i += 2
		switch key {
		case "FC":
			if frequency || i >= len(fields) || !strings.EqualFold(fields[i], "Hz") {
				return Band{}, false
			}
			band.FreqHz, frequency = value, true
			i++
		case "GAIN":
			if gain || !needGain || i >= len(fields) || !strings.EqualFold(fields[i], "dB") {
				return Band{}, false
			}
			band.GainDB, gain = value, true
			i++
		case "Q":
			if q || !needQ {
				return Band{}, false
			}
			band.Q, q = value, true
		default:
			return Band{}, false
		}
	}
	return band, frequency && gain == needGain && q == needQ
}

func importNumber(text string) (float64, bool) {
	// ParseFloat 也接受十六进制、下划线和非有限值；这些不属于导入文本的十进制数字。
	for _, r := range text {
		if !(r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.' || r == 'e' || r == 'E') {
			return 0, false
		}
	}
	value, err := strconv.ParseFloat(text, 64)
	return value, err == nil && !math.IsNaN(value) && !math.IsInf(value, 0)
}
