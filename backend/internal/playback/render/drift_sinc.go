package render

import (
	"math"
	"sync"
)

const (
	driftSincRadius = 16
	driftSincPhases = 1024
	driftKaiserBeta = 8
)

// 各相位独立归一化直流增益。整数相位直接取原样本。
var driftKernel = sync.OnceValue(func() *[driftSincPhases + 1][2 * driftSincRadius]float64 {
	var table [driftSincPhases + 1][2 * driftSincRadius]float64
	table[0][driftSincRadius-1] = 1
	table[driftSincPhases][driftSincRadius] = 1
	for phase := 1; phase < driftSincPhases; phase++ {
		row := &table[phase]
		var sum float64
		for k := range row {
			x := float64(k-driftSincRadius+1) - float64(phase)/driftSincPhases
			u := x / driftSincRadius
			// I0 的正项级数。各抽头公用的 I0(β) 在归一化时抵消。
			z := driftKaiserBeta * driftKaiserBeta * (1 - u*u) / 4
			window, term := 1.0, 1.0
			for n := 1; ; n++ {
				term *= z / float64(n*n)
				if window+term == window {
					break
				}
				window += term
			}
			row[k] = window * math.Sin(math.Pi*x) / (math.Pi * x)
			sum += row[k]
		}
		for k := range row {
			row[k] /= sum
		}
	}
	return &table
})

// 块用半开区间表示：输出边界 0、L 对应整数源边界 0、L+step。
// 第 i 帧取 i + step*i/L；相邻表项之间线性插值，避免相位量化产生阶跃。
func stretchPCM(dst, context []byte, width, frameSize, length, step int) {
	table := driftKernel()
	limit := float64(int(1) << (8*width - 1))
	copy(dst, context[driftSincRadius*frameSize:(driftSincRadius+1)*frameSize])
	for i := 1; i < length; i++ {
		base, numerator := i, i
		if step < 0 {
			base, numerator = i-1, length-i
		}
		pos := float64(numerator) * driftSincPhases / float64(length)
		phase := int(pos)
		mix := pos - float64(phase)
		left, right := &table[phase], &table[phase+1]
		var weights [2 * driftSincRadius]float64
		for k, v := range left {
			weights[k] = v + mix*(right[k]-v)
		}
		for ch := 0; ch < frameSize; ch += width {
			off := (base+1)*frameSize + ch
			var sum float64
			for _, weight := range weights {
				sum += weight * float64(pcmSample(context[off:], width))
				off += frameSize
			}
			sample := int32(min(limit-1, max(-limit, math.Round(sum))))
			out := dst[i*frameSize+ch:]
			out[0], out[1] = byte(sample), byte(sample>>8)
			if width == 3 {
				out[2] = byte(sample >> 16)
			}
		}
	}
}
