package dither

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand/v2"
	"testing"
)

func TestLFSRPeriodAndStride(t *testing.T) {
	const seed uint32 = 0x1234567
	l := NewLFSR(seed)
	state := seed
	for i := 0; i < 10000; i++ {
		for bit := 0; bit < 31; bit++ {
			low, tap := state&1, (state>>28)&1
			state = state>>1 | (low^tap)<<30
		}
		if got := l.Next(); got != state {
			t.Fatalf("第 %d 次取值=%x, want %x", i, got, state)
		}
	}

	// 用基向量建立 Next 的线性变换，再平方求幂；不遍历整个周期。
	apply := func(transform [31]uint32, state uint32) uint32 {
		var result uint32
		for bit, column := range transform {
			if state&(1<<bit) != 0 {
				result ^= column
			}
		}
		return result
	}
	var transform [31]uint32
	for bit := range transform {
		transform[bit] = NewLFSR(1 << bit).Next()
	}
	if apply(transform, seed) == seed {
		t.Fatal("种子不能是固定点")
	}
	state = seed
	// 2³¹−1 是素数：非固定点经该次数变换回到自身，周期就只能是该素数。
	for exponent := uint32(1<<31 - 1); exponent > 0; exponent >>= 1 {
		if exponent&1 != 0 {
			state = apply(transform, state)
		}
		var squared [31]uint32
		for bit := range squared {
			squared[bit] = apply(transform, transform[bit])
		}
		transform = squared
	}
	if state != seed {
		t.Fatalf("完整周期后状态=%x, want %x", state, seed)
	}
}

func TestTriangularDistribution(t *testing.T) {
	const count = 1000000
	triangular, uniform := NewLFSR(1), NewLFSR(1)
	var sum, squares float64
	for i := 0; i < count; i++ {
		u := uniform.Uniform()
		if !(u > -0.5 && u < 0.5) {
			t.Fatalf("Uniform=%g 越界", u)
		}
		d := triangular.Triangular()
		if !(d > -1 && d < 1) {
			t.Fatalf("Triangular=%g 越界", d)
		}
		sum += d
		squares += d * d
	}
	mean := sum / count
	variance := squares/count - mean*mean
	if math.Abs(mean) >= 0.005 || math.Abs(variance-1.0/6) > (1.0/6)*0.02 {
		t.Fatalf("三角分布均值=%g, 方差=%g; want 0, 1/6", mean, variance)
	}
}

func TestQuantizePassthrough(t *testing.T) {
	for _, bits := range []int{16, 24} {
		t.Run(fmt.Sprint(bits), func(t *testing.T) {
			q := NewQuantizer(bits, 0x1234567)
			rng := rand.New(rand.NewPCG(1, 2))
			const frames = 32
			samples := [][]float64{make([]float64, frames), make([]float64, frames)}
			var want []byte
			for i := 0; i < frames; i++ {
				for ch := range samples {
					value := int32(rng.Uint32()) >> (32 - bits)
					samples[ch][i] = float64(value<<(32-bits)) / (1 << 31)
					for j := 0; j < bits/8; j++ {
						want = append(want, byte(value>>(8*j)))
					}
				}
			}
			before := q.lfsr.state
			got, clipped := q.Quantize(nil, samples, frames, true)
			if !bytes.Equal(got, want) || clipped != 0 {
				t.Fatalf("直通=%x, 削波=%d; want %x", got, clipped, want)
			}
			if q.lfsr.state != before {
				t.Fatalf("直通推进了 LFSR：%x -> %x", before, q.lfsr.state)
			}
		})
	}
}

func TestQuantizeLinearizesSmallSignal(t *testing.T) {
	const count = 100000
	q := NewQuantizer(16, 1)
	src := [][]float64{{0.3 / (1 << 15)}}
	dst := make([]byte, 0, 2)
	var sum float64
	seen := make(map[int16]bool)
	for i := 0; i < count; i++ {
		var clipped int
		dst, clipped = q.Quantize(dst[:0], src, 1, false)
		value := int16(binary.LittleEndian.Uint16(dst))
		if value < -1 || value > 1 || clipped != 0 {
			t.Fatalf("小信号输出=%d, 削波=%d", value, clipped)
		}
		sum += float64(value)
		seen[value] = true
	}
	if mean := sum / count; math.Abs(mean-0.3) >= 0.01 || len(seen) < 2 {
		t.Fatalf("小信号输出均值=%g, 取值种类=%d; want 均值 0.3 且输出不恒定", mean, len(seen))
	}
}

func TestQuantizeClipping(t *testing.T) {
	for _, bits := range []int{16, 24} {
		t.Run(fmt.Sprint(bits), func(t *testing.T) {
			q := NewQuantizer(bits, 1)
			got, clipped := q.Quantize(nil, [][]float64{{1.5, -1.5}}, 2, false)
			var want []byte
			for _, value := range []int32{1<<(bits-1) - 1, -1 << (bits - 1)} {
				for j := 0; j < bits/8; j++ {
					want = append(want, byte(value>>(8*j)))
				}
			}
			if !bytes.Equal(got, want) || clipped != 2 {
				t.Fatalf("钳位=%x, 削波=%d; want %x, 2", got, clipped, want)
			}
			if _, clipped = q.Quantize(got[:0], [][]float64{{0, 0}}, 2, false); clipped != 0 {
				t.Fatalf("零输入削波数=%d", clipped)
			}
		})
	}
}

func TestQuantizeSteadyStateAllocations(t *testing.T) {
	q := NewQuantizer(24, 1)
	src := [][]float64{{0, 0.25, -0.25}, {0.5, -0.5, 0}}
	dst := make([]byte, 0, 2*3*3)
	allocs := testing.AllocsPerRun(100, func() {
		dst, _ = q.Quantize(dst[:0], src, 3, false)
	})
	if allocs != 0 {
		t.Fatalf("复用输出缓冲时每次分配=%g, want 0", allocs)
	}
}
