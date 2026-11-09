package peq

import "math"

// ButterworthLowPass 设计固定 Q、保留两位小数的近似 Butterworth 低通。
// 支持 PEQ 的 1–6 阶及 DSD 后置低通的 8 阶；其他阶数属于编程错误并 panic。
// 返回节的 Channel 均为零，调用方负责按实际声道展开。频率钳位与 Design 相同。
func ButterworthLowPass(order int, f, fs float64) []Section {
	if (order < 1 || order > 6) && order != 8 {
		panic("peq: Butterworth 阶数无效")
	}
	return butterworth(LowPass, order, clampFrequency(f, fs), fs)
}

func butterworth(kind string, order int, f, fs float64) []Section {
	sections := make([]Section, 0, (order+1)/2)
	for k := 1; k <= order/2; k++ {
		angle := float64(2*k-1) * math.Pi / float64(2*order)
		if order%2 != 0 {
			angle = float64(k) * math.Pi / float64(order)
		}
		q := math.Round(100/(2*math.Cos(angle))) / 100
		sections = append(sections, passSection(kind, 2, f, fs, q))
	}
	if order%2 != 0 {
		sections = append(sections, passSection(kind, 1, f, fs, 0))
	}
	return sections
}

func passSection(kind string, order int, f, fs, q float64) Section {
	k := math.Tan(math.Pi * f / fs)
	if order == 1 {
		b := k / (1 + k)
		if kind == HighPass {
			b = 1 / (1 + k)
			return Section{A1: (k - 1) / (k + 1), B0: b, B1: -b}
		}
		return Section{A1: (k - 1) / (k + 1), B0: b, B1: b}
	}
	k2 := k * k
	n := 1 / (1 + k/q + k2)
	s := Section{A1: 2 * (k2 - 1) * n, A2: (1 - k/q + k2) * n}
	if kind == HighPass {
		s.B0, s.B1, s.B2 = n, -2*n, n
	} else {
		s.B0, s.B1, s.B2 = k2*n, 2*k2*n, k2*n
	}
	return s
}
