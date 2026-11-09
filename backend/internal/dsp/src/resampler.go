package src

import (
	"math/big"
	"slices"
)

// Resampler 是有状态的 planar float64 重采样器，不支持并发调用。
// 输入不被修改；返回的切片归调用方所有，不会在后续调用中被覆盖。
// Process 不补偿延迟、不自动冲刷，累计输出严格为 floor(累计输入×总比率)。
type Resampler struct {
	stages                    []*polyphase
	channels                  int
	l, m                      int64
	latency                   int
	inputFrames, outputFrames int64
	pending                   [][]float64
}

type polyphase struct {
	l, m                int
	coefficients        [][]float64
	history             [][]float64
	received, nextInput int64
	phase               int
}

// New 构建目录核的串联转换器；空 stages 表示逐样本直通。
// 声道数、相位、质量、比率必须已在装配边界校验；无效配置会 panic。
func New(stages []Stage, channels int) *Resampler {
	if channels <= 0 {
		panic("src: channels must be positive")
	}
	r := &Resampler{channels: channels, l: 1, m: 1, pending: make([][]float64, channels)}
	total, delay := big.NewRat(1, 1), new(big.Rat)
	for _, s := range stages {
		if s.L <= 0 || s.M <= 0 || !validPhase(s.Phase) || !validQuality(s.Quality) {
			panic("src: invalid stage")
		}
		d := gcd(s.L, s.M)
		s.L /= d
		s.M /= d
		if s.L == s.M {
			continue
		}
		h := cachedKernel(s.Phase, s.Quality, s.L, s.M)
		p := &polyphase{l: s.L, m: s.M, coefficients: make([][]float64, s.L), history: make([][]float64, channels)}
		for phase := range p.coefficients {
			for k := phase; k < len(h); k += s.L {
				p.coefficients[phase] = append(p.coefficients[phase], float64(s.L)*h[k])
			}
		}
		for ch := range p.history {
			p.history[ch] = make([]float64, (len(h)+s.L-1)/s.L)
		}
		r.stages = append(r.stages, p)
		step := big.NewRat(int64(s.L), int64(s.M))
		total.Mul(total, step)
		delay.Mul(delay, step)
		if s.Phase == Linear {
			delay.Add(delay, big.NewRat(int64(len(h)-1), 2*int64(s.M)))
		}
	}
	// 只在构造时做有理数计算，避免多级半样本延迟在浮点累加后向错误方向取整。
	rounded := new(big.Int).Add(delay.Num(), new(big.Int).Rsh(new(big.Int).Set(delay.Denom()), 1))
	rounded.Quo(rounded, delay.Denom())
	if !total.Num().IsInt64() || !total.Denom().IsInt64() || !rounded.IsInt64() {
		panic("src: stage ratio or latency overflow")
	}
	r.l, r.m = total.Num().Int64(), total.Denom().Int64()
	r.latency = int(rounded.Int64())
	return r
}

// Latency 返回按最终输出采样率计的延迟，多级相加后只取整一次。
// 最小相位核按合同申报零延迟。
func (r *Resampler) Latency() int { return r.latency }

// Reset 清空历史、所有级的相位和计数以及未发布样本，保留核与配置。
func (r *Resampler) Reset() {
	r.inputFrames, r.outputFrames = 0, 0
	for _, p := range r.stages {
		p.received, p.nextInput, p.phase = 0, 0, 0
		for _, h := range p.history {
			clear(h)
		}
	}
	for ch := range r.pending {
		r.pending[ch] = nil
	}
}

func (r *Resampler) Process(in [][]float64) [][]float64 {
	if len(in) != r.channels {
		panic("src: mismatched channel count")
	}
	n := len(in[0])
	for _, samples := range in {
		if len(samples) != n {
			panic("src: mismatched channel lengths")
		}
	}
	r.inputFrames += int64(n)
	samples := in
	for _, p := range r.stages {
		samples = p.process(samples)
	}
	target := scaleFloor(r.inputFrames, r.l, r.m)
	count := int(target - r.outputFrames)
	out := make([][]float64, r.channels)
	for ch := range out {
		data := append(r.pending[ch], samples[ch]...)
		out[ch] = data[:count:count]
		r.pending[ch] = slices.Clone(data[count:])
	}
	r.outputFrames = target
	return out
}

// 内部级计算所有已有输入足以确定的样本（ceil），只在最外层按总比率 floor 发布。
// 不能让每级各自 floor：例如 1280/147 再升 2 倍时可能少一帧；逐块取整还会累积漂移。
func (p *polyphase) process(in [][]float64) [][]float64 {
	n := len(in[0])
	count := scaleCeil(p.received+int64(n), int64(p.l), int64(p.m)) - scaleCeil(p.received, int64(p.l), int64(p.m))
	out := make([][]float64, len(in))
	for ch := range out {
		out[ch] = make([]float64, 0, int(count))
	}
	historySize := len(p.history[0])
	for frame := 0; frame < n; frame++ {
		slot := int(p.received % int64(historySize))
		for ch := range in {
			p.history[ch][slot] = in[ch][frame]
		}
		for p.nextInput == p.received {
			taps := p.coefficients[p.phase]
			for ch, h := range p.history {
				value, index := 0.0, slot
				for _, tap := range taps {
					value += tap * h[index]
					index--
					if index < 0 {
						index = historySize - 1
					}
				}
				out[ch] = append(out[ch], value)
			}
			p.phase += p.m
			p.nextInput += int64(p.phase / p.l)
			p.phase %= p.l
		}
		p.received++
	}
	return out
}

func scaleFloor(n, l, m int64) int64 { return n/m*l + n%m*l/m }
func scaleCeil(n, l, m int64) int64 {
	value := scaleFloor(n, l, m)
	if n%m*l%m != 0 {
		value++
	}
	return value
}

// ResampleAll 供离线 IR 使用：丢弃申报延迟并补零冲刷，精确返回 ceil(len(x)·to/from)
// 帧。它不采用 DSD 专用的窄带 Stage2 优化；各级都保持调用方指定的滤波器。
// 无变率时也返回副本。非法采样率或不能由目录组成的比率属于调用方配置错误，会 panic。
func ResampleAll(x []float64, from, to int, phase Phase, q Quality) []float64 {
	if from <= 0 || to <= 0 || !validPhase(phase) || !validQuality(q) {
		panic("src: invalid offline conversion")
	}
	stages, err := catalogStages(from, to, phase, q)
	if err != nil {
		panic(err)
	}
	if len(stages) == 0 {
		return slices.Clone(x)
	}
	if len(x) == 0 {
		return []float64{}
	}
	r := New(stages, 1)
	wanted := int(scaleCeil(int64(len(x)), r.l, r.m))
	result := make([]float64, 0, wanted)
	skip := r.Latency()
	consume := func(samples []float64) {
		drop := min(skip, len(samples))
		skip -= drop
		samples = samples[drop:]
		result = append(result, samples[:min(len(samples), wanted-len(result))]...)
	}
	consume(r.Process([][]float64{x})[0])
	needed := scaleCeil(int64(wanted)+int64(r.Latency()), r.m, r.l) - int64(len(x))
	zeros := make([]float64, min(int64(4096), max(int64(0), needed)))
	for needed > 0 {
		count := min(int64(len(zeros)), needed)
		consume(r.Process([][]float64{zeros[:count]})[0])
		needed -= count
	}
	return result
}
