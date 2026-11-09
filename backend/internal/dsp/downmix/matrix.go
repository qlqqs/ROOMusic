package downmix

import (
	"errors"
	"math"
)

// Matrix 的行按输出掩码位序排列，列按输入掩码位序排列。
type Matrix struct {
	In, Out Mask
	Gains   [][]float64 // [out][in]
}

// Build 按扬声器类别构建到 2.0 或侧环绕 5.1 的矩阵。
// 系数来自参考稿 §6.8 的相对增益与基准增益，不另做行和归一化。
func Build(src, dst Mask) (Matrix, error) {
	if src == 0 {
		return Matrix{}, errors.New("downmix: 源布局不能为空")
	}
	if dst != Stereo && dst != Surround51 {
		return Matrix{}, errors.New("downmix: 目标布局必须为 2.0 或侧环绕 5.1")
	}
	m := Matrix{In: src, Out: dst, Gains: make([][]float64, dst.Channels())}
	for out := range m.Gains {
		m.Gains[out] = make([]float64, src.Channels())
	}

	baseDB := -3.2
	if dst == Stereo {
		baseDB = -6
		if src.Channels() > 6 {
			baseDB = -6.8
		}
	}
	base := math.Pow(10, baseDB/20)
	center := math.Pow(10, -3.0/20)
	surround := math.Pow(10, -6.0/20)

	input := 0
	for speaker := Mask(1); speaker != 0; speaker <<= 1 {
		if src&speaker == 0 {
			continue
		}
		if dst == Stereo {
			switch speaker {
			case FL, FLC:
				m.Gains[0][input] = base
			case FR, FRC:
				m.Gains[1][input] = base
			case FC:
				m.Gains[0][input], m.Gains[1][input] = base*center, base*center
			case BL, SL:
				m.Gains[0][input] = base * surround
			case BR, SR:
				m.Gains[1][input] = base * surround
			case BC:
				m.Gains[0][input], m.Gains[1][input] = base*surround*center, base*surround*center
			}
		} else {
			// 0x60f 的输出顺序为 FL、FR、FC、LFE、SL、SR。
			switch speaker {
			case FL, FLC:
				m.Gains[0][input] = base
			case FR, FRC:
				m.Gains[1][input] = base
			case FC:
				m.Gains[2][input] = base
			case LFE:
				m.Gains[3][input] = base
			case BL, SL:
				m.Gains[4][input] = base
			case BR, SR:
				m.Gains[5][input] = base
			case BC:
				m.Gains[4][input], m.Gains[5][input] = base*center, base*center
			}
		}
		input++
	}
	return m, nil
}
