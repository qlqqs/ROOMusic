package peq

import "math"

// IIR 将所有声道的节放在一个处理器中；构造后系数不变，状态由会话串行使用。
// 不可并发调用 Process 或 Reset。
type IIR struct {
	sections  []iirSection
	byChannel [][]int
	preamp    float64
}

type iirSection struct {
	Section
	s1, s2 float64
}

// NewIIR 复制系数，按各声道在 sections 中的相对顺序串联；前级增益只施加一次。
// 声道数必须为正、节的下标必须落在流内、系数与增益必须有限，否则 panic。
// 调用方在这里之前处理设置中的禁用段、全声道展开和超出流声道数的选择。
func NewIIR(channels int, sections []Section, preampDB float64) *IIR {
	gain := math.Pow(10, preampDB/20)
	if channels <= 0 || !finite(preampDB) || !finite(gain) {
		panic("peq: 声道数或前级增益无效")
	}
	f := &IIR{byChannel: make([][]int, channels), preamp: gain}
	for _, s := range sections {
		if s.Channel < 0 || s.Channel >= channels ||
			!finite(s.A1) || !finite(s.A2) || !finite(s.B0) || !finite(s.B1) || !finite(s.B2) {
			panic("peq: 滤波节或声道下标无效")
		}
		if len(f.sections) == cap(f.sections) {
			// 显式翻倍，不依赖 append 对大切片的容量增长策略。
			grown := make([]iirSection, len(f.sections), max(1, 2*cap(f.sections)))
			copy(grown, f.sections)
			f.sections = grown
		}
		f.byChannel[s.Channel] = append(f.byChannel[s.Channel], len(f.sections))
		f.sections = append(f.sections, iirSection{Section: s})
	}
	return f
}

// Process 就地处理等长 planar 样本并返回原切片，不分配缓冲；允许每声道零帧。
// 格式错误在修改任何样本之前 panic。不同声道的样本缓冲不能重叠。
func (f *IIR) Process(in [][]float64) [][]float64 {
	if len(in) != len(f.byChannel) {
		panic("peq: 输入声道数不匹配")
	}
	for _, samples := range in {
		if len(samples) != len(in[0]) {
			panic("peq: 输入声道长度不等")
		}
	}
	for ch, samples := range in {
		if f.preamp != 1 {
			for i := range samples {
				samples[i] *= f.preamp
			}
		}
		for _, index := range f.byChannel[ch] {
			s := &f.sections[index]
			s1, s2 := s.s1, s.s2
			for i, x := range samples {
				y := s.B0*x + s1
				s1 = s.B1*x - s.A1*y + s2
				s2 = s.B2*x - s.A2*y
				samples[i] = y
			}
			// 每块结束清除极小状态，避免静音尾部衰减进入非规格化数。
			if math.Abs(s1) < 1e-30 {
				s1 = 0
			}
			if math.Abs(s2) < 1e-30 {
				s2 = 0
			}
			s.s1, s.s2 = s1, s2
		}
	}
	return in
}

// Latency 返回处理器的额外缓冲延迟；IIR 不等待后续样本。
func (f *IIR) Latency() int { return 0 }

// Reset 清空全部历史状态，保留系数与前级增益。
func (f *IIR) Reset() {
	for i := range f.sections {
		f.sections[i].s1, f.sections[i].s2 = 0, 0
	}
}
