package irload

import (
	"fmt"
	"math"
	"math/bits"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/dsp/convolution"
	"github.com/qlqq/roomusic/backend/internal/dsp/src"
)

// ResampleIR 将一个 IR 声道转换到流采样率，返回独立的 ceil(len(samples)*to/from)
// 个样本。同率只复制；变率固定使用线性相位 precise，并按设计 §4 归一，不按能量归一。
func ResampleIR(samples []float64, from, to int) ([]float64, error) {
	if _, err := resampledLength(len(samples), from, to); err != nil {
		return nil, err
	}
	original, magnitude, err := sampleSums(samples)
	if err != nil {
		return nil, err
	}
	if from == to {
		return slices.Clone(samples), nil
	}
	out, err := resampleAll(samples, from, to)
	if err != nil {
		return nil, err
	}
	converted, _, err := sampleSums(out)
	if err != nil {
		return nil, err
	}
	gain := float64(from) / float64(to)
	// 全零 IR 也走这个分支，避免代数和比值成为 0/0。
	if magnitude != 0 && math.Abs(original) >= 1e-3*magnitude {
		if converted == 0 {
			return nil, fmt.Errorf("irload: cannot normalize zero resampled sum")
		}
		gain = original / converted
	}
	if !finite(gain) {
		return nil, fmt.Errorf("irload: non-finite IR normalization gain")
	}
	for i := range out {
		out[i] *= gain
		if !finite(out[i]) {
			return nil, fmt.Errorf("irload: non-finite normalized IR sample")
		}
	}
	return out, nil
}

// 在生成 SRC 核或样本缓冲之前检查目标几何；乘除使用双字长整数，避免先溢出再比较。
func resampledLength(taps, from, to int) (int, error) {
	if taps < 1 || taps > convolution.MaxTaps || from <= 0 || to <= 0 {
		return 0, fmt.Errorf("irload: invalid IR length or sample rate")
	}
	hi, lo := bits.Mul64(uint64(taps), uint64(to))
	if hi >= uint64(from) {
		return 0, fmt.Errorf("irload: resampled IR exceeds tap limit")
	}
	n, remainder := bits.Div64(hi, lo, uint64(from))
	if n > convolution.MaxTaps || (n == convolution.MaxTaps && remainder != 0) {
		return 0, fmt.Errorf("irload: resampled IR exceeds tap limit")
	}
	if remainder != 0 {
		n++
	}
	return int(n), nil
}

func resampleAll(samples []float64, from, to int) (out []float64, err error) {
	// SRC 的离线接口将目录外比率作为配置错误 panic；这里只将这一明确错误转回装载错误。
	// 不捕获其他程序错误，也不回退到原采样率或其他重采样器。
	defer func() {
		if cause := recover(); cause != nil {
			if cause == src.ErrUnsupportedRatio {
				out, err = nil, src.ErrUnsupportedRatio
				return
			}
			panic(cause)
		}
	}()
	return src.ResampleAll(samples, from, to, src.Linear, src.Precise), nil
}

func sampleSums(samples []float64) (sum, magnitude float64, err error) {
	correction, absCorrection := 0.0, 0.0
	for _, v := range samples {
		if !finite(v) {
			return 0, 0, fmt.Errorf("irload: non-finite IR sample")
		}
		y := v - correction
		next := sum + y
		correction = (next - sum) - y
		sum = next
		y = math.Abs(v) - absCorrection
		next = magnitude + y
		absCorrection = (next - magnitude) - y
		magnitude = next
	}
	if !finite(sum) || !finite(magnitude) {
		return 0, 0, fmt.Errorf("irload: non-finite IR sum")
	}
	return sum, magnitude, nil
}
