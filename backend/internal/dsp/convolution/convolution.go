// Package convolution 提供同率、等长输出的短段重叠相加矩阵卷积。
package convolution

import (
	"fmt"
	"math"

	"github.com/qlqq/roomusic/backend/internal/dsp/internal/fft"
)

const (
	MaxTaps     = 1 << 19
	MaxPaths    = 32
	MaxChannels = 8
)

// Weighted 是从零编号的声道及其线性权重。
type Weighted struct {
	Channel int
	Weight  float64
}

// Path 的 IR 必须已经处于流采样率；输入先加权求和，卷积后再分配到输出。
type Path struct {
	IR              []float64
	Inputs, Outputs []Weighted
}

// Config 的输入、输出均为 Channels 个声道。空延时切片表示全部为零。
// New 只读这些切片，构造后不再持有调用方的可变数据。
type Config struct {
	Channels          int
	Paths             []Path
	InDelay, OutDelay []int
}

// Geometry 是由最长 IR 唯一决定的 OLA 几何。
type Geometry struct {
	Taps, NFFT, Block, Bins int
}

func geometry(taps int) Geometry {
	n := 2
	for n < 2*taps {
		n *= 2
	}
	return Geometry{Taps: taps, NFFT: n, Block: n - taps + 1, Bins: n/2 + 1}
}

type spectralPath struct {
	h               []complex128
	inputs, outputs []Weighted
}

// Convolver 的处理状态由单个流独占，不可并发调用。
type Convolver struct {
	g                 Geometry
	plan              *fft.Plan
	paths             []spectralPath
	latency           int
	inDelay, outDelay []delayLine
	inputs, outputs   [][]complex128
	overlap, result   [][]float64
	time              []float64
	mixed             []complex128
}

// New 在分配 FFT 和延时缓冲之前校验全部配置。
func New(config Config) (*Convolver, error) {
	taps, latency, err := validate(config)
	if err != nil {
		return nil, err
	}
	g := geometry(taps)
	c := &Convolver{
		g: g, plan: fft.New(g.NFFT), latency: latency,
		inDelay: make([]delayLine, config.Channels), outDelay: make([]delayLine, config.Channels),
		inputs: make([][]complex128, config.Channels), outputs: make([][]complex128, config.Channels),
		overlap: make([][]float64, config.Channels), result: make([][]float64, config.Channels),
		time: make([]float64, g.NFFT), mixed: make([]complex128, g.Bins),
	}
	for ch := range c.inputs {
		if len(config.InDelay) != 0 {
			c.inDelay[ch].samples = make([]float64, config.InDelay[ch])
		}
		if len(config.OutDelay) != 0 {
			c.outDelay[ch].samples = make([]float64, config.OutDelay[ch])
		}
		c.outputs[ch] = make([]complex128, g.Bins)
		c.overlap[ch] = make([]float64, g.Taps-1)
	}
	for _, path := range config.Paths {
		clear(c.time)
		copy(c.time, path.IR)
		h := make([]complex128, g.Bins)
		c.plan.RealForward(h, c.time)
		for k := range h {
			h[k] /= complex(float64(g.NFFT), 0)
		}
		c.paths = append(c.paths, spectralPath{h: h,
			inputs: append([]Weighted(nil), path.Inputs...), outputs: append([]Weighted(nil), path.Outputs...)})
		for _, input := range path.Inputs {
			if c.inputs[input.Channel] == nil {
				c.inputs[input.Channel] = make([]complex128, g.Bins)
			}
		}
	}
	return c, nil
}

func validate(c Config) (taps, latency int, err error) {
	if c.Channels < 1 || c.Channels > MaxChannels || len(c.Paths) < 1 || len(c.Paths) > MaxPaths {
		return 0, 0, fmt.Errorf("convolution: invalid channel or path count")
	}
	for _, delays := range [][]int{c.InDelay, c.OutDelay} {
		if len(delays) != 0 && len(delays) != c.Channels {
			return 0, 0, fmt.Errorf("convolution: delay count must match channels")
		}
		for _, n := range delays {
			if n < 0 {
				return 0, 0, fmt.Errorf("convolution: negative delay")
			}
		}
	}
	peak := 0.0
	for i, p := range c.Paths {
		if len(p.IR) == 0 || len(p.IR) > MaxTaps {
			return 0, 0, fmt.Errorf("convolution: path %d has invalid tap count", i)
		}
		taps = max(taps, len(p.IR))
		for k, v := range p.IR {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return 0, 0, fmt.Errorf("convolution: path %d has non-finite IR", i)
			}
			// 峰相等时保留按路径顺序遇到的第一个抽头；全零 IR 的延迟为零。
			if math.Abs(v) > peak {
				peak, latency = math.Abs(v), k
			}
		}
		for _, weights := range [][]Weighted{p.Inputs, p.Outputs} {
			for _, w := range weights {
				if w.Channel < 0 || w.Channel >= c.Channels || math.IsNaN(w.Weight) || math.IsInf(w.Weight, 0) {
					return 0, 0, fmt.Errorf("convolution: path %d has invalid routing", i)
				}
			}
		}
	}
	return taps, latency, nil
}

// Geometry 返回几何副本。
func (c *Convolver) Geometry() Geometry { return c.g }

// Latency 以本节点输出帧计，只取 IR 峰位置，不含用户输入/输出延时。
func (c *Convolver) Latency() int { return c.latency }

// Process 不修改输入，返回同声道数、同帧数的结果；结果只在下一次调用前有效。
// 输入必须是配置声道数的等长 planar 切片，形状错误属于调用方程序错误。
// 任意短段立即处理，不等待攒满 Block；空块不推进任何状态。
func (c *Convolver) Process(block [][]float64) [][]float64 {
	if len(block) != len(c.inputs) {
		panic("convolution: mismatched channel count")
	}
	n := len(block[0])
	for _, samples := range block {
		if len(samples) != n {
			panic("convolution: mismatched frame count")
		}
	}
	for ch := range c.result {
		if cap(c.result[ch]) < n {
			c.result[ch] = make([]float64, n)
		} else {
			c.result[ch] = c.result[ch][:n]
		}
	}
	for start := 0; start < n; {
		end := start + min(c.g.Block, n-start)
		c.processSegment(block, start, end)
		start = end
	}
	return c.result
}

func (c *Convolver) processSegment(block [][]float64, start, end int) {
	n := end - start
	for ch, spectrum := range c.inputs {
		if spectrum == nil {
			continue
		}
		clear(c.time)
		c.inDelay[ch].process(c.time[:n], block[ch][start:end])
		c.plan.RealForward(spectrum, c.time)
	}
	for _, spectrum := range c.outputs {
		clear(spectrum)
	}
	for _, p := range c.paths {
		clear(c.mixed)
		for _, w := range p.inputs {
			for k, v := range c.inputs[w.Channel] {
				c.mixed[k] += complex(w.Weight, 0) * v
			}
		}
		for k := range c.mixed {
			c.mixed[k] *= p.h[k]
		}
		for _, w := range p.outputs {
			for k, v := range c.mixed {
				c.outputs[w.Channel][k] += complex(w.Weight, 0) * v
			}
		}
	}
	for ch, spectrum := range c.outputs {
		c.plan.RealInverse(c.time, spectrum)
		for k, v := range c.overlap[ch] {
			c.time[k] += v
		}
		// 先叠加旧尾，再从本段末尾截取新尾：短段也保留尚未消费的旧尾。
		copy(c.overlap[ch], c.time[n:n+c.g.Taps-1])
		c.outDelay[ch].process(c.result[ch][start:end], c.time[:n])
	}
}

// Reset 清除重叠与延时历史，保留已装载的频谱和工作缓冲。
func (c *Convolver) Reset() {
	for ch := range c.overlap {
		clear(c.overlap[ch])
		c.inDelay[ch].reset()
		c.outDelay[ch].reset()
	}
}
