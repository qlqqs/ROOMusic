package peq

import (
	"fmt"
	"math"
	"slices"
	"testing"
)

func TestIIRImpulseChannelsAndPreamp(t *testing.T) {
	sections := iirTestSections()
	input := [][]float64{make([]float64, 128), make([]float64, 128), make([]float64, 128)}
	input[0][0] = 1
	input[2][0], input[2][1] = 0.5, -0.25
	want := directFormI(input, sections, -6)
	f := NewIIR(3, sections, -6)
	got := f.Process(input)
	if f.Latency() != 0 {
		t.Errorf("Latency = %d，期望 0", f.Latency())
	}
	for ch := range input {
		if &got[ch][0] != &input[ch][0] {
			t.Fatalf("声道 %d 未就地处理", ch)
		}
		for i := range got[ch] {
			assertNear(t, got[ch][i], want[ch][i], 1e-12)
		}
	}
	for _, sample := range got[1] {
		if sample != 0 {
			t.Fatal("静音声道混入了其他声道的状态")
		}
	}
}

func TestIIRCascadeOrder(t *testing.T) {
	sections := []Section{
		{B0: 2, B1: 0.5, B2: 0.25, Channel: 0},
		{B0: 3, B1: 0.25, Channel: 1},
		{B0: 4, B1: 0.25, B2: 0.5, Channel: 0},
	}
	f := NewIIR(2, sections, 0)
	got := f.Process([][]float64{{1}, {2}})
	assertNear(t, got[0][0], 8, 0)
	assertNear(t, got[1][0], 6, 0)
	// 线性节交换顺序不改变总传递函数；中间状态才能区分实际执行顺序。
	for i, want := range [][2]float64{{0.5, 0.25}, {0.5, 0}, {0.5, 1}} {
		assertNear(t, f.sections[i].s1, want[0], 0)
		assertNear(t, f.sections[i].s2, want[1], 0)
	}
}

func TestIIRReset(t *testing.T) {
	sections := iirTestSections()
	f := NewIIR(3, sections, -6)
	f.Process([][]float64{{1, 0, 0}, {0, -1, 0}, {0, 0, 1}})
	f.Reset()
	for _, samples := range f.Process([][]float64{{0, 0}, {0, 0}, {0, 0}}) {
		for _, sample := range samples {
			if sample != 0 {
				t.Fatal("Reset 后仍有历史状态输出")
			}
		}
	}
	input := [][]float64{{1, 0, -0.5, 0}, {-1, 0.25, 0, 0}, {0, 1, 0, 0}}
	want := NewIIR(3, sections, -6).Process(clonePlanar(input))
	got := f.Process(input)
	for ch := range got {
		if !slices.Equal(got[ch], want[ch]) {
			t.Errorf("声道 %d：Reset 后与全新处理器不同", ch)
		}
	}
}

func TestIIRBlockEquivalenceLargeSignal(t *testing.T) {
	input := make([][]float64, 3)
	for ch := range input {
		input[ch] = make([]float64, 257)
		for i := range input[ch] {
			input[ch][i] = 8*math.Sin(float64(i+1)*0.17*float64(ch+1)) + 0.2
		}
	}
	sections := iirTestSections()
	want := NewIIR(3, sections, -6).Process(clonePlanar(input))
	for _, size := range []int{1, 7, 64} {
		t.Run(fmt.Sprintf("block_%d", size), func(t *testing.T) {
			got := clonePlanar(input)
			f := NewIIR(3, sections, -6)
			for start := 0; start < len(got[0]); start += size {
				end := min(start+size, len(got[0]))
				f.Process([][]float64{got[0][start:end], got[1][start:end], got[2][start:end]})
			}
			for ch := range got {
				if !slices.Equal(got[ch], want[ch]) {
					t.Errorf("声道 %d：分块与整块结果不同", ch)
				}
			}
		})
	}
}

func TestIIRFlushTinyStatesAtBlockEnd(t *testing.T) {
	// 两个延迟抽头分别留下正负状态，验证严格小于阈值才清零，且不在块内清零。
	sections := []Section{{B0: 1, B1: 1, B2: -1}}
	for _, amplitude := range []float64{0.5e-30, -0.5e-30, 1e-30, -1e-30, 2e-30} {
		t.Run(fmt.Sprintf("amplitude_%g", amplitude), func(t *testing.T) {
			whole := NewIIR(1, sections, 0).Process([][]float64{{amplitude, 0, 0}})
			if !slices.Equal(whole[0], []float64{amplitude, amplitude, -amplitude}) {
				t.Fatalf("块内极小状态被提前清零：%v", whole[0])
			}
			f := NewIIR(1, sections, 0)
			f.Process([][]float64{{amplitude}})
			tail := f.Process([][]float64{{0, 0}})[0]
			want := []float64{amplitude, -amplitude}
			if math.Abs(amplitude) < 1e-30 {
				want = []float64{0, 0}
			}
			if !slices.Equal(tail, want) {
				t.Errorf("块末清零后的尾部 = %v，期望 %v", tail, want)
			}
		})
	}
}

func iirTestSections() []Section {
	peak := Design(Band{Type: Peaking, FreqHz: 1000, GainDB: 6, Q: 1}, 48000)[0]
	other := peak
	other.Channel = 1
	low := Design(Band{Type: LowPass, FreqHz: 3000, SlopeDB: 12}, 48000)[0]
	return []Section{peak, other, low}
}

func clonePlanar(in [][]float64) [][]float64 {
	out := make([][]float64, len(in))
	for ch := range in {
		out[ch] = slices.Clone(in[ch])
	}
	return out
}

// 使用输入/输出历史的直接 I 型递推，避免复制被测处理器的转置 II 型状态更新。
func directFormI(in [][]float64, sections []Section, preampDB float64) [][]float64 {
	out := clonePlanar(in)
	for ch, samples := range out {
		for i := range samples {
			samples[i] *= math.Pow(10, preampDB/20)
		}
		for _, s := range sections {
			if s.Channel != ch {
				continue
			}
			var x1, x2, y1, y2 float64
			for i, x := range samples {
				y := s.B0*x + s.B1*x1 + s.B2*x2 - s.A1*y1 - s.A2*y2
				samples[i] = y
				x2, x1, y2, y1 = x1, x, y1, y
			}
		}
	}
	return out
}
