// Package dither 提供确定性的 TPDF 抖动和定点 PCM 打包。
package dither

const (
	stateMask   uint32 = 1<<31 - 1
	defaultSeed uint32 = 1
)

// LFSR 是多项式 x³¹ + x²⁸ + 1 的 31 位 Fibonacci 寄存器。
// 低位先出、右移，反馈为第 0 与第 28 位的异或。
type LFSR struct{ state uint32 }

func NewLFSR(seed uint32) *LFSR {
	seed &= stateMask
	if seed == 0 {
		seed = defaultSeed
	}
	return &LFSR{state: seed}
}

// Next 推进 31 步再取整个状态，避免相邻取值直接共享移位窗口。
func (l *LFSR) Next() uint32 {
	for i := 0; i < 31; i++ {
		feedback := (l.state ^ (l.state >> 28)) & 1
		l.state = l.state>>1 | feedback<<30
	}
	return l.state
}

func (l *LFSR) Uniform() float64 { return float64(l.Next())/(1<<31) - 0.5 }

// Triangular 的两次取值各在 ±0.5 LSB 内，差值范围为 (-1, 1) LSB。
func (l *LFSR) Triangular() float64 { return l.Uniform() - l.Uniform() }
