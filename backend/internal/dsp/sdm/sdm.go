// Package sdm 将 DSD 采样率上的 PCM 样本调制为交错的单比特字节流。
package sdm

import (
	"fmt"
	"math"
)

//go:generate go run ./internal/gen -out coeffs_gen.go

// Algorithm 指定噪声整形的阶数与系数族。
type Algorithm int

const (
	Order5 Algorithm = iota
	Order7
	Clans5
	Clans7
)

type coefficients struct {
	order int
	a     [7]float64
	g     [7]float64
}

type channelState struct {
	s            [7]float64
	xPrev, yPrev float64
}

// Modulator 独占各声道的历史及输出缓冲，不支持并发调用。
// 必须通过 New 构造；不同 Process 调用之间保留调制状态。
type Modulator struct {
	alg      Algorithm
	multiple int
	coeff    coefficients
	states   []channelState
	output   []byte
	checksum uint64

	integrityFault bool

	integrityFailures uint64
	instabilityResets uint64
}

// New 只接受 44.1 kHz 族的实际 DSD 采样率（64、128、256、512 倍 44100 Hz），以及正声道数。
// 输入必须已升采样至目标 DSD 比特率；本包不做采样率转换。
func New(alg Algorithm, dsdRate int, channels int) (*Modulator, error) {
	if alg < Order5 || alg > Clans7 {
		return nil, fmt.Errorf("sdm: 无效算法 %d", alg)
	}
	var rateIndex int
	switch dsdRate {
	case 64 * 44100:
		rateIndex = 0
	case 128 * 44100:
		rateIndex = 1
	case 256 * 44100:
		rateIndex = 2
	case 512 * 44100:
		rateIndex = 3
	default:
		return nil, fmt.Errorf("sdm: 不支持的 DSD 采样率 %d Hz", dsdRate)
	}
	if channels <= 0 {
		return nil, fmt.Errorf("sdm: 声道数必须为正数，实际为 %d", channels)
	}
	m := &Modulator{
		alg:      alg,
		multiple: dsdRate / 44100,
		coeff:    generatedCoefficients[alg][rateIndex],
		states:   make([]channelState, channels),
	}
	for i := range m.states {
		m.states[i].yPrev = 1
	}
	m.checksum = m.integrityChecksum()
	return m, nil
}

// Process 接受等长的分声道样本，每声道长度必须是 8 的倍数。
// 每 8 个样本按 MSB 先打包，字节按声道交错；返回缓冲归 Modulator 所有，
// 只保证在下一次 Process 或 Reset 前有效。非法输入形状表示调用方编程错误。
func (m *Modulator) Process(in [][]float64) []byte {
	if len(in) != len(m.states) {
		panic("sdm: 输入声道数与构造参数不一致")
	}
	frames := len(in[0])
	if frames%8 != 0 {
		panic("sdm: 每声道样本数必须是 8 的倍数")
	}
	for _, samples := range in {
		if len(samples) != frames {
			panic("sdm: 输入声道必须等长")
		}
	}
	size := frames / 8 * len(in)
	if cap(m.output) < size {
		m.output = make([]byte, size)
	} else {
		m.output = m.output[:size]
	}

	// 必须在按阶数索引状态或使用系数之前校验；失败不推进历史，也不修复校验字。
	if m.integrityFault || m.integrityChecksum() != m.checksum {
		m.integrityFault = true
		for i := range m.output {
			m.output[i] = 0x69
		}
		m.integrityFailures++
		return m.output
	}

	order := m.coeff.order
	for channel, samples := range in {
		state := &m.states[channel]
		for frame := 0; frame < frames; frame += 8 {
			var packed byte
			for bit := 0; bit < 8; bit++ {
				x := samples[frame+bit] * 0.5
				if x > 0.5 {
					x = 0.5
				} else if x < -0.5 {
					x = -0.5
				}

				// 使用上一样本的输入误差驱动积分器，使信号传递函数为 1。
				var d [7]float64
				d[0] = state.s[0] - m.coeff.g[0]*state.s[1] + state.xPrev - state.yPrev
				for i := 1; i < order-1; i++ {
					d[i] = state.s[i] + state.s[i-1] - m.coeff.g[i]*state.s[i+1]
				}
				d[order-1] = state.s[order-1] + state.s[order-2]
				v := x
				for i := 0; i < order; i++ {
					v += m.coeff.a[i] * d[i]
				}
				y := -1.0
				packed <<= 1
				if v >= 0 {
					y = 1
					packed |= 1
				}
				state.s = d
				state.xPrev = x
				state.yPrev = y
			}
			m.output[frame/8*len(in)+channel] = packed
			if state.unstable() {
				*state = channelState{yPrev: 1}
				m.instabilityResets++
			}
		}
	}
	// xPrev 属于合法演进的受校验历史，每块正常结束后重新保存。
	m.checksum = m.integrityChecksum()
	return m.output
}

// Latency 返回以 DSD 比特样本计的固定信号延迟。
func (m *Modulator) Latency() int { return 0 }

// Reset 清除所有声道历史，保留累计诊断计数及已分配的缓冲容量。
// 完整性故障一经发现即锁存，Reset 不会重新签封损坏参数或解除故障。
func (m *Modulator) Reset() {
	if m.integrityChecksum() != m.checksum {
		m.integrityFault = true
	}
	for i := range m.states {
		m.states[i] = channelState{yPrev: 1}
	}
	m.output = m.output[:0]
	if !m.integrityFault {
		m.checksum = m.integrityChecksum()
	}
}

func (s *channelState) unstable() bool {
	for _, v := range s.s {
		// 反向比较同时覆盖 NaN、无穷大和超过阈值的有限状态。
		if !(math.Abs(v) <= 1e9) {
			return true
		}
	}
	return !(math.Abs(s.xPrev) <= 1e9) || !(math.Abs(s.yPrev) <= 1e9)
}

func (m *Modulator) integrityChecksum() uint64 {
	h := uint64(14695981039346656037)
	h = checksumWord(h, uint64(m.alg))
	h = checksumWord(h, uint64(m.multiple))
	h = checksumWord(h, uint64(m.coeff.order))
	for _, v := range m.coeff.a {
		h = checksumWord(h, math.Float64bits(v))
	}
	for _, v := range m.coeff.g {
		h = checksumWord(h, math.Float64bits(v))
	}
	for _, state := range m.states {
		h = checksumWord(h, math.Float64bits(state.xPrev))
	}
	return h
}

// checksumWord 按固定小端字节序追加一个字，避免依赖机器字长与内存布局。
func checksumWord(h, word uint64) uint64 {
	for i := 0; i < 8; i++ {
		h ^= uint64(byte(word))
		h *= 1099511628211
		word >>= 8
	}
	return h
}
