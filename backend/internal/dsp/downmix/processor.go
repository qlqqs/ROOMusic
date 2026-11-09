package downmix

// Processor 对每帧执行矩阵点积，不改变块长，不支持并发调用。
// 输出缓冲归处理器所有，只在下次 Process 调用前有效。
type Processor struct {
	channels int
	gains    [][]float64
	output   [][]float64
}

// New 复制矩阵系数，后续修改 m 不影响处理器；非法矩阵表示装配错误。
func New(m Matrix) *Processor {
	if m.In == 0 || m.Out == 0 || len(m.Gains) != m.Out.Channels() {
		panic("downmix: 矩阵维度与布局不符")
	}
	p := &Processor{
		channels: m.In.Channels(),
		gains:    make([][]float64, len(m.Gains)),
		output:   make([][]float64, len(m.Gains)),
	}
	for out, row := range m.Gains {
		if len(row) != p.channels {
			panic("downmix: 矩阵维度与布局不符")
		}
		p.gains[out] = append([]float64(nil), row...)
	}
	return p
}

// Process 接受与输入布局匹配的等长声道，不修改输入，也不钳位输出。
// 容量增长后复用输出缓冲；零帧输入返回对应数量的零帧输出声道。
func (p *Processor) Process(in [][]float64) [][]float64 {
	if len(in) != p.channels {
		panic("downmix: 输入声道数与矩阵不符")
	}
	frames := len(in[0])
	for _, channel := range in {
		if len(channel) != frames {
			panic("downmix: 输入声道长度不等")
		}
	}
	for out, row := range p.gains {
		if cap(p.output[out]) < frames {
			p.output[out] = make([]float64, frames)
		} else {
			p.output[out] = p.output[out][:frames]
		}
		for frame := 0; frame < frames; frame++ {
			var sum float64
			for input, gain := range row {
				sum += gain * in[input][frame]
			}
			p.output[out][frame] = sum
		}
	}
	return p.output
}

// Latency 返回按输出帧计的固定延迟，矩阵降混没有延迟。
func (p *Processor) Latency() int { return 0 }

// Reset 无需重置历史状态，保留已分配的输出容量。
func (p *Processor) Reset() {}
