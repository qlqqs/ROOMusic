package chain

// Adapter 补偿处理器延迟，并按绝对帧位置保存区域和来源元数据。
// 不支持并发调用；Drain 开始后必须先 Reset 才能再次 Process。
type Adapter struct {
	processor Processor
	in, out   Format
	step      SignalStep
	modifies  bool

	inputFrames, outputFrames int64
	skip                      int
	draining                  bool
	input                     [][]float64
	zeros                     [][]float64
	pending                   [][]float64
	consumed                  int
	result                    Buffer
	regions                   []absoluteRegion
	metadata                  []sourceSpan
}

type absoluteRegion struct {
	start, end int64
	gain       float64
}

// 来源区间保留输入帧坐标，不能像增益区域一样先向下取整：
// 降采样时不足一帧的区间仍可能影响下一个输出帧的精确性。
type sourceSpan struct {
	start, end int64
	exact      bool
	bits       int
}

// Wrap 只接受已校验的格式与处理器；参数错误表示装配错误。
func Wrap(p Processor, in, out Format, step SignalStep, modifies bool) *Adapter {
	if in.SampleRate <= 0 || out.SampleRate <= 0 || in.Channels <= 0 || out.Channels <= 0 || p.Latency() < 0 {
		panic("chain: 处理器格式或延迟无效")
	}
	a := &Adapter{
		processor: p, in: in, out: out, step: step, modifies: modifies,
		skip: p.Latency(), input: make([][]float64, in.Channels),
		zeros: make([][]float64, in.Channels), pending: make([][]float64, out.Channels),
	}
	a.result.Samples = make([][]float64, out.Channels)
	for ch := range a.zeros {
		a.zeros[ch] = make([]float64, max(1, in.SampleRate/5))
	}
	return a
}

func (a *Adapter) Latency() int { return a.processor.Latency() }

func (a *Adapter) Step() SignalStep { return a.step }

func (a *Adapter) Process(b *Buffer) *Buffer {
	if a.draining {
		panic("chain: 冲刷开始后不能再输入")
	}
	if b.Frames < 0 || len(b.Samples) != a.in.Channels {
		panic("chain: 输入缓冲格式不符")
	}
	for ch := range a.input {
		a.input[ch] = b.Samples[ch][:b.Frames]
	}
	a.remember(b)
	a.inputFrames += int64(b.Frames)
	a.appendOutput(a.processor.Process(a.input))
	return a.emit()
}

// Drain 每次返回一个非空尾块，全部真实输入对应的帧输出完后返回 nil。
// 补零不计入 inputFrames，也不生成虚假的 Exact、SourceBits 或增益区域。
func (a *Adapter) Drain() *Buffer {
	a.draining = true
	for a.outputFrames < a.scaled(a.inputFrames) {
		if a.pendingFrames() > 0 {
			return a.emit()
		}
		// 处理器可以就地修改输入，因此每轮都重新清零。
		for _, samples := range a.zeros {
			clear(samples)
		}
		a.appendOutput(a.processor.Process(a.zeros))
	}
	return nil
}

func (a *Adapter) Reset() {
	a.processor.Reset()
	a.inputFrames, a.outputFrames = 0, 0
	a.skip = a.processor.Latency()
	a.draining = false
	a.consumed = 0
	a.regions = a.regions[:0]
	a.metadata = a.metadata[:0]
	for ch := range a.pending {
		a.pending[ch] = a.pending[ch][:0]
		a.result.Samples[ch] = nil
	}
	a.result.Frames, a.result.SourceBits = 0, 0
	a.result.Exact = false
	a.result.Regions = a.result.Regions[:0]
}

func (a *Adapter) scaled(frame int64) int64 {
	// 先除再乘，长流不会因 frame * rate 的中间结果提前溢出。
	return scaleFrames(frame, a.in.SampleRate, a.out.SampleRate)
}

func scaleFrames(frame int64, from, to int) int64 {
	inRate, outRate := int64(from), int64(to)
	return frame/inRate*outRate + frame%inRate*outRate/inRate
}

func (a *Adapter) remember(b *Buffer) {
	start, end := a.inputFrames, a.inputFrames+int64(b.Frames)
	if end > start {
		span := sourceSpan{start: start, end: end, exact: b.Exact, bits: b.SourceBits}
		n := len(a.metadata)
		if n > 0 && a.metadata[n-1].end == start && a.metadata[n-1].exact == span.exact && a.metadata[n-1].bits == span.bits {
			a.metadata[n-1].end = end
		} else {
			a.metadata = append(a.metadata, span)
		}
	}
	for _, region := range b.Regions {
		start := a.scaled(a.inputFrames + int64(region.Offset))
		end := a.scaled(a.inputFrames + int64(region.Offset) + int64(region.Frames))
		if end > start {
			a.regions = append(a.regions, absoluteRegion{start: start, end: end, gain: region.Gain})
		}
	}
}

func (a *Adapter) pendingFrames() int { return len(a.pending[0]) - a.consumed }

func (a *Adapter) appendOutput(samples [][]float64) {
	if len(samples) == 0 {
		return
	}
	if len(samples) != a.out.Channels {
		panic("chain: 处理器输出声道数不符")
	}
	frames := len(samples[0])
	drop := min(a.skip, frames)
	a.skip -= drop
	for ch := range a.pending {
		if len(samples[ch]) != frames {
			panic("chain: 处理器输出声道长度不等")
		}
		// 在下一次调用才压紧，保证上次返回的切片在本次调用前有效。
		remaining := copy(a.pending[ch], a.pending[ch][a.consumed:])
		a.pending[ch] = append(a.pending[ch][:remaining], samples[ch][drop:]...)
	}
	a.consumed = 0
}

func (a *Adapter) emit() *Buffer {
	frames := int(min(int64(a.pendingFrames()), a.scaled(a.inputFrames)-a.outputFrames))
	start, end := a.outputFrames, a.outputFrames+int64(frames)
	a.result.Frames = frames
	a.result.Regions = a.result.Regions[:0]
	a.result.Exact = frames > 0 && !a.modifies
	a.result.SourceBits = 0
	for ch := range a.pending {
		a.result.Samples[ch] = a.pending[ch][a.consumed : a.consumed+frames]
	}
	if frames == 0 {
		return &a.result
	}

	// 只汇总这次真正吐出的区间；延迟和攒块都不能借用当前输入块的元数据。
	// 输出边界落在输入帧中间时，两边的块都可能贡献样本；向外取整汇总，
	// 只释放到向下取整的结束边界，保留跨输出边界的那一帧元数据。
	sourceStart := scaleFrames(start, a.out.SampleRate, a.in.SampleRate)
	keepFrom := scaleFrames(end, a.out.SampleRate, a.in.SampleRate)
	sourceEnd := keepFrom
	if a.scaled(sourceEnd) < end {
		sourceEnd++
	}
	integerSource := true
	used := 0
	for _, span := range a.metadata {
		if span.start >= sourceEnd {
			break
		}
		if span.end > sourceStart {
			a.result.Exact = a.result.Exact && span.exact
			integerSource = integerSource && span.bits > 0
			a.result.SourceBits = max(a.result.SourceBits, span.bits)
		}
		if span.end <= keepFrom {
			used++
		}
	}
	if !integerSource {
		a.result.SourceBits = 0
	}
	a.metadata = a.metadata[:copy(a.metadata, a.metadata[used:])]

	used = 0
	for _, region := range a.regions {
		if region.start >= end {
			break
		}
		lo, hi := max(start, region.start), min(end, region.end)
		if hi > lo {
			a.result.Regions = append(a.result.Regions, GainRegion{
				Offset: int(lo - start), Frames: int(hi - lo), Gain: region.gain,
			})
		}
		if region.end <= end {
			used++
		}
	}
	a.regions = a.regions[:copy(a.regions, a.regions[used:])]
	a.consumed += frames
	a.outputFrames = end
	return &a.result
}
