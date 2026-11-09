package sdm

import (
	"bytes"
	"fmt"
	"math"
	"testing"
)

var testAlgorithms = []struct {
	name string
	alg  Algorithm
}{
	{"标准五阶", Order5},
	{"标准七阶", Order7},
	{"Clans五阶", Clans5},
	{"Clans七阶", Clans7},
}

const testDSD64Rate = 64 * 44100

func newTestModulator(t *testing.T, alg Algorithm, rate, channels int) *Modulator {
	t.Helper()
	m, err := New(alg, rate, channels)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestFullScaleStability(t *testing.T) {
	// 共 16 组、每组三相位；复用同一波形，保持 §2.4 的浮点运算顺序和完整样本数。
	for _, multiple := range []int{64, 128, 256, 512} {
		t.Run(fmt.Sprintf("倍率%d", multiple), func(t *testing.T) {
			rate := multiple * 44100
			samples := make([]float64, (1<<19)*(multiple/64))
			for _, phase := range []struct {
				name  string
				value float64
			}{{"零", 0}, {"三π除八", 3 * math.Pi / 8}, {"三π除四", 3 * math.Pi / 4}} {
				for n := range samples {
					samples[n] = math.Sin(2*math.Pi*1000*float64(n)/float64(rate) + phase.value)
				}
				t.Run(phase.name, func(t *testing.T) {
					for _, algorithm := range testAlgorithms {
						t.Run(algorithm.name, func(t *testing.T) {
							m := newTestModulator(t, algorithm.alg, rate, 1)
							m.Process([][]float64{samples})
							if m.instabilityResets != 0 {
								t.Fatalf("满刻度输入触发 %d 次失稳复位", m.instabilityResets)
							}
						})
					}
				})
			}
		})
	}
}

func TestIntegrityFaultSurvivesReset(t *testing.T) {
	m := newTestModulator(t, Order5, testDSD64Rate, 2)
	input := [][]float64{make([]float64, 24), make([]float64, 24)}
	// 只改对象中一个系数的最低有效位，不修改入库系数。
	m.coeff.a[0] = math.Nextafter(m.coeff.a[0], math.Inf(1))
	want := bytes.Repeat([]byte{0x69}, 6)
	if got := m.Process(input); !bytes.Equal(got, want) {
		t.Fatalf("完整性故障输出 %x，期望 %x", got, want)
	}
	if m.integrityFailures != 1 {
		t.Fatalf("完整性失败计数 %d，期望 1", m.integrityFailures)
	}
	m.Reset()
	if m.integrityFailures != 1 {
		t.Fatal("Reset 改写了完整性失败计数")
	}
	if got := m.Process(input); !bytes.Equal(got, want) {
		t.Fatalf("Reset 解除故障：输出 %x，期望 %x", got, want)
	}
	if m.integrityFailures != 2 {
		t.Fatalf("再次处理后的完整性失败计数 %d，期望 2", m.integrityFailures)
	}
}

func TestPackingFormat(t *testing.T) {
	input := [][]float64{
		{1, -0.7, 0.2, 0, -1, 0.8, -0.3, 0.6, -0.9, 0.1, 0.5, -0.4, 0.7, -0.2, 0.3, -0.8},
		{-0.4, 0.9, -0.8, 0.1, 0.6, -0.2, 1, -1, 0.3, -0.7, 0.8, 0, -0.5, 0.4, -0.1, 0.2},
	}
	m := newTestModulator(t, Order5, testDSD64Rate, len(input))
	got := m.Process(input)
	if len(got) != len(input[0])*len(input)/8 {
		t.Fatalf("输出字节数 %d，期望 %d", len(got), len(input[0])*len(input)/8)
	}
	// 按设计逐样本求符号，不复用生产打包循环；逐位检查 MSB 顺序和声道位置。
	for ch, samples := range input {
		c := m.coeff
		var state [7]float64
		xPrev, yPrev := 0.0, 1.0
		for n, sample := range samples {
			var next [7]float64
			next[0] = state[0] + xPrev - yPrev
			for i := 1; i < c.order; i++ {
				next[i] = state[i] + state[i-1]
				if i+1 < c.order {
					next[i] -= c.g[i] * state[i+1]
				}
			}
			x := sample * 0.5
			v := x
			for i := 0; i < c.order; i++ {
				v += c.a[i] * next[i]
			}
			positive := v >= 0
			if bit := got[n/8*len(input)+ch]&(1<<(7-n%8)) != 0; bit != positive {
				t.Fatalf("声道 %d 样本 %d 的打包位置或符号错误", ch, n)
			}
			yPrev = -1
			if positive {
				yPrev = 1
			}
			state, xPrev = next, x
		}
	}
}

func TestConstructorParameters(t *testing.T) {
	for _, rate := range []int{0, -testDSD64Rate, 44100, 32 * 44100, 64 * 48000, 128 * 48000, 256 * 48000, 512 * 48000} {
		t.Run(fmt.Sprintf("非法采样率%d", rate), func(t *testing.T) {
			if _, err := New(Order5, rate, 1); err == nil {
				t.Fatal("非法采样率未返回错误")
			}
		})
	}
	for _, channels := range []int{0, -1} {
		t.Run(fmt.Sprintf("非法声道数%d", channels), func(t *testing.T) {
			if _, err := New(Order5, testDSD64Rate, channels); err == nil {
				t.Fatal("非法声道数未返回错误")
			}
		})
	}
}

func TestProcessRejectsUnalignedSamples(t *testing.T) {
	m := newTestModulator(t, Order5, testDSD64Rate, 1)
	defer func() {
		if recover() == nil {
			t.Fatal("样本数不是 8 的倍数时未 panic")
		}
	}()
	m.Process([][]float64{make([]float64, 9)})
}
