// Package peq 提供双二阶系数设计与多声道 IIR，不依赖播放区设置或渲染器。
package peq

import "math"

const (
	Peaking   = "peaking"
	LowShelf  = "low_shelf"
	HighShelf = "high_shelf"
	LowPass   = "low_pass"
	HighPass  = "high_pass"
	BandPass  = "band_pass"
	Notch     = "notch"
	AllPass   = "all_pass"
)

// Band 是单段算法参数，不是持久化设置。调用方筛选启用的段并展开声道。
// 低通/高通的 SlopeDB 为 6、12、18、24、30、36；仅二阶可用 Q 覆盖固定 Q。
// SlopeDB 为零且 Q 为正时也表示显式 Q 的二阶低通/高通。
// 其他类型使用 Q，只有峰值与架类使用 GainDB。
type Band struct {
	Type    string
	FreqHz  float64
	GainDB  float64
	Q       float64
	SlopeDB int
}

// Section 的分母首项已归一为 1，系数顺序为 (a1, a2, b0, b1, b2)。
// Channel 是单个零起始声道下标；同一滤波器用于多声道时由调用方复制节。
type Section struct {
	A1, A2, B0, B1, B2 float64
	Channel            int
}

// Design 返回独立的系数切片，Channel 均为零，频率钳到 [10, 0.49*fs]。
// 这里只接受调用方已校验的算法参数；无效类型、Q、斜率或采样率属于编程错误并 panic。
// 用户设置的范围校验由设置拥有者负责，不在纯算法包复制。
func Design(band Band, fs float64) []Section {
	f := clampFrequency(band.FreqHz, fs)
	if band.Type == LowPass || band.Type == HighPass {
		if (band.SlopeDB == 0 || band.SlopeDB == 12) && band.Q != 0 {
			requireQ(band.Q)
			return []Section{passSection(band.Type, 2, f, fs, band.Q)}
		}
		if band.SlopeDB < 6 || band.SlopeDB > 36 || band.SlopeDB%6 != 0 {
			panic("peq: 低通/高通斜率无效")
		}
		return butterworth(band.Type, band.SlopeDB/6, f, fs)
	}
	return []Section{cookbook(band, f, fs)}
}

func cookbook(band Band, f, fs float64) Section {
	requireQ(band.Q)
	w := 2 * math.Pi * f / fs
	sin, cos := math.Sincos(w)
	alpha := sin / (2 * band.Q)
	switch band.Type {
	case Peaking, LowShelf, HighShelf:
		if !finite(band.GainDB) {
			panic("peq: 增益必须有限")
		}
		if band.GainDB == 0 {
			return Section{B0: 1}
		}
		a := math.Pow(10, band.GainDB/40)
		if band.Type == Peaking {
			return normalized(1+alpha/a, -2*cos, 1-alpha/a,
				1+alpha*a, -2*cos, 1-alpha*a)
		}
		// Cookbook 的 Q 形式，不把 Q 当作架类斜率 S；转折频率处为 GainDB/2。
		t := 2 * math.Sqrt(a) * alpha
		if band.Type == LowShelf {
			return normalized((a+1)+(a-1)*cos+t,
				-2*((a-1)+(a+1)*cos), (a+1)+(a-1)*cos-t,
				a*((a+1)-(a-1)*cos+t), 2*a*((a-1)-(a+1)*cos), a*((a+1)-(a-1)*cos-t))
		}
		return normalized((a+1)-(a-1)*cos+t,
			2*((a-1)-(a+1)*cos), (a+1)-(a-1)*cos-t,
			a*((a+1)+(a-1)*cos+t), -2*a*((a-1)+(a+1)*cos), a*((a+1)+(a-1)*cos-t))
	case BandPass:
		return normalized(1+alpha, -2*cos, 1-alpha, alpha, 0, -alpha)
	case Notch:
		return normalized(1+alpha, -2*cos, 1-alpha, 1, -2*cos, 1)
	case AllPass:
		return normalized(1+alpha, -2*cos, 1-alpha, 1-alpha, -2*cos, 1+alpha)
	default:
		panic("peq: 滤波器类型无效")
	}
}

func normalized(a0, a1, a2, b0, b1, b2 float64) Section {
	return Section{A1: a1 / a0, A2: a2 / a0, B0: b0 / a0, B1: b1 / a0, B2: b2 / a0}
}

func clampFrequency(f, fs float64) float64 {
	if !finite(f) || !finite(fs) || fs < 10/0.49 {
		panic("peq: 频率或采样率无效")
	}
	return math.Max(10, math.Min(f, 0.49*fs))
}

func requireQ(q float64) {
	if !finite(q) || q <= 0 {
		panic("peq: Q 必须为有限正数")
	}
}

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }
