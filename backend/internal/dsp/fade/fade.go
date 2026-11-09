// Package fade 提供按窗口绝对位置计算的交叉淡化纯函数。
package fade

import "math"

// Warmup 表示每曲处理器的预热比例。第一批共享链跨曲目连续运行，
// 不另加预热段；整个混音窗口都按下列公式计算。
const Warmup = 0.3

// FadeIn 的自变量为窗口归一化位置，范围 [0, 1]。
func FadeIn(x float64) float64 { return math.Sqrt(math.Sin(math.Pi * x / 2)) }

// FadeOut 的自变量为窗口归一化位置，范围 [0, 1]。
func FadeOut(x float64) float64 {
	c := math.Cos(math.Pi * x / 2)
	c *= c
	return c * c
}

// AlignDown8 向下取整到 8 帧边界。
func AlignDown8(frames int) int { return frames &^ 7 }

// Mix 把本次等长的 a、b 混入 dst，三者声道数相同，帧数由 dst 决定。
// start 是本次在整个 total 帧窗口内的起点；允许 dst 与 a 或 b 原位重合。
// 两个输入已施加各自电平化，调用方负责把输出缓冲的 Exact 清为 false。
func Mix(dst, a, b [][]float64, start, total int) {
	if len(dst) != len(a) || len(dst) != len(b) || total <= 0 || start < 0 || start > total {
		panic("fade: 混音格式或窗口无效")
	}
	if len(dst) == 0 {
		return
	}
	frames := len(dst[0])
	if frames > total-start {
		panic("fade: 混音超出窗口")
	}
	for ch := range dst {
		if len(dst[ch]) != frames || len(a[ch]) != frames || len(b[ch]) != frames {
			panic("fade: 混音声道长度不等")
		}
	}
	for i := 0; i < frames; i++ {
		x := float64(start+i) / float64(total)
		in, out := FadeIn(x), FadeOut(x)
		for ch := range dst {
			dst[ch][i] = a[ch][i]*out + b[ch][i]*in
		}
	}
}
