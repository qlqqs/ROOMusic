package dsd

import (
	"bytes"
	"math"
	"math/rand"
	"testing"
)

const testDSDRate = 2822400.0

func testFIR() []float64 {
	h := make([]float64, 96)
	copy(h, firHalf[:])
	for i := range firHalf {
		h[95-i] = firHalf[i]
	}
	return h
}

type firMetrics struct {
	ripple20, ripple24, stop176, stop328, cutoff3 float64
}

// 在 2^18 等分的奈奎斯特网格上直接求频响，不复用生成器或转换器查表。
func measureFIR(h []float64) firMetrics {
	const grid = 1 << 18
	min20, min24 := math.Inf(1), math.Inf(1)
	max20, max24, peak176, peak328 := 0.0, 0.0, 0.0, 0.0
	var m firMetrics
	for bin := 0; bin <= grid; bin++ {
		f := testDSDRate * float64(bin) / (2 * grid)
		w := math.Pi * float64(bin) / grid
		var response float64
		for i := 0; i < len(h)/2; i++ {
			response += (h[i] + h[len(h)-1-i]) * math.Cos(w*(float64(len(h)-1)/2-float64(i)))
		}
		a := math.Abs(response)
		if f <= 20000 {
			min20, max20 = math.Min(min20, a), math.Max(max20, a)
		}
		if f <= 24000 {
			min24, max24 = math.Min(min24, a), math.Max(max24, a)
		}
		if f >= 176400 {
			peak176 = math.Max(peak176, a)
		}
		if f >= 328800 {
			peak328 = math.Max(peak328, a)
		}
		if m.cutoff3 == 0 && a <= math.Pow(10, -3.0/20) {
			m.cutoff3 = f
		}
	}
	m.ripple20, m.ripple24 = 20*math.Log10(max20/min20), 20*math.Log10(max24/min24)
	m.stop176, m.stop328 = -20*math.Log10(peak176), -20*math.Log10(peak328)
	return m
}

func TestFIRSpecifications(t *testing.T) {
	h := testFIR()
	var sum float64
	for i, v := range h {
		sum += v
		if math.IsNaN(v) || math.IsInf(v, 0) || v != h[len(h)-1-i] {
			t.Fatalf("FIR 不对称：抽头 %d", i)
		}
	}
	if !(math.Abs(sum-1) <= 1e-12) {
		t.Fatalf("直流增益 %.15g，期望 1", sum)
	}
	m := measureFIR(h)
	if !(m.ripple20 <= 0.0002 && m.stop176 >= 56 && m.stop328 >= 167) {
		t.Fatalf("通带波动 %.9f dB，阻带衰减 %.6f / %.6f dB", m.ripple20, m.stop176, m.stop328)
	}
	t.Logf("%.9f %.9f %.6f %.6f %.3f", m.ripple20, m.ripple24, m.stop176, m.stop328, m.cutoff3)
}

// 按时间顺序展开比特，逐抽头相乘；历史明确为重复的 0x69，不模拟环形缓冲。
func directDSD(input []byte) []float64 {
	padded := append(bytes.Repeat([]byte{0x69}, 12), input...)
	h := testFIR()
	out := make([]float64, len(input))
	for frame := range out {
		for tap, coefficient := range h {
			bit := 96 + frame*8 + 7 - tap
			sign := -1.0
			if padded[bit/8]&(1<<(7-bit%8)) != 0 {
				sign = 1
			}
			out[frame] += sign * coefficient
		}
	}
	return out
}

func TestLookupMatchesDirectConvolutionAcrossBlocks(t *testing.T) {
	rng := rand.New(rand.NewSource(20261109))
	input, want := make([][]byte, 2), make([][]float64, 2)
	for ch := range input {
		input[ch] = make([]byte, 513)
		if _, err := rng.Read(input[ch]); err != nil {
			t.Fatal(err)
		}
		want[ch] = directDSD(input[ch])
	}
	c, err := New(2, 0)
	if err != nil {
		t.Fatal(err)
	}
	chunks := []int{1, 5, 6, 7, 15, 16, 17, 31, 127}
	for offset, block := 0, 0; offset < len(input[0]); block++ {
		n := min(chunks[block%len(chunks)], len(input[0])-offset)
		in := [][]byte{bytes.Clone(input[0][offset : offset+n]), bytes.Clone(input[1][offset : offset+n])}
		got := c.Process(in)
		if len(got) != 2 {
			t.Fatalf("声道数 %d，期望 2", len(got))
		}
		for ch := range got {
			if len(got[ch]) != n || !bytes.Equal(in[ch], input[ch][offset:offset+n]) {
				t.Fatal("输出长度不符或输入被修改")
			}
			for i, v := range got[ch] {
				if !(math.Abs(v-want[ch][offset+i]) <= 1e-12) {
					t.Fatalf("声道 %d 帧 %d：误差 %.15g", ch, offset+i, v-want[ch][offset+i])
				}
			}
		}
		offset += n
	}
}

func TestResetRestoresSilenceHistory(t *testing.T) {
	c, err := New(2, 6)
	if err != nil {
		t.Fatal(err)
	}
	c.Process([][]byte{bytes.Repeat([]byte{0xff}, 77), bytes.Repeat([]byte{0x00}, 77)})
	c.Reset()
	input := [][]byte{bytes.Repeat([]byte{0x69}, 32), bytes.Repeat([]byte{0x69}, 32)}
	input[1][0] = 0xa3
	got := c.Process(input)
	if len(got) != len(input) {
		t.Fatalf("声道数 %d，期望 %d", len(got), len(input))
	}
	for ch := range input {
		if len(got[ch]) != len(input[ch]) {
			t.Fatal("Reset 后输出长度不符")
		}
		want := directDSD(input[ch])
		for i, v := range got[ch] {
			if !(math.Abs(v-want[i]*math.Pow(10, 6.0/20)) <= 1e-12) {
				t.Fatalf("Reset 后声道 %d 帧 %d 未恢复静音历史或增益", ch, i)
			}
		}
	}
	if c.Latency() != 6 {
		t.Fatalf("延迟 %d，期望 6", c.Latency())
	}
}

func TestGain(t *testing.T) {
	for _, gain := range []float64{-6, 0, 6} {
		c, err := New(2, gain)
		if err != nil {
			t.Fatal(err)
		}
		out := c.Process([][]byte{bytes.Repeat([]byte{0xff}, 32), bytes.Repeat([]byte{0x00}, 32)})
		if len(out) != 2 || len(out[0]) != 32 || len(out[1]) != 32 {
			t.Fatal("输出声道数或长度不符")
		}
		for ch := range out {
			want := float64(1-2*ch) * math.Pow(10, gain/20)
			for i := 12; i < len(out[ch]); i++ {
				if !(math.Abs(out[ch][i]-want) <= 1e-12) {
					t.Fatalf("增益 %.0f dB，声道 %d 帧 %d：得到 %.15g，期望 %.15g", gain, ch, i, out[ch][i], want)
				}
			}
		}
	}
}

func TestKnown1kHzSignalLevel(t *testing.T) {
	const frequency = 1000.0
	input := make([]byte, 17640) // 50 ms DSD64。
	amplitude, accumulator := math.Pow(10, -6.0/20), 0.0
	for bit := 0; bit < len(input)*8; bit++ {
		accumulator += amplitude * math.Sin(2*math.Pi*frequency*float64(bit)/testDSDRate)
		if accumulator >= 0 {
			input[bit/8] |= 1 << (7 - bit%8)
			accumulator--
		} else {
			accumulator++
		}
	}
	c, err := New(1, 6)
	if err != nil {
		t.Fatal(err)
	}
	// 丢弃 10 ms 起始段，余下 40 个整周期通过正交投影测基波电平。
	pcm := c.Process([][]byte{input})[0][3528:]
	levelDB := 20 * math.Log10(testToneAmplitude(pcm, frequency))
	if !(math.Abs(levelDB) <= 0.1) {
		t.Fatalf("−6 dBFS 输入加 6 dB 后基波电平 %.6f dBFS，期望 0±0.1", levelDB)
	}
	// 一阶 ΔΣ 的高频残余会额外穿越零点；以 40 ms 整周期窗的频谱主峰验频。
	peak, measuredHz := 0.0, 0.0
	for hz := 25.0; hz <= 20000; hz += 25 {
		amplitude := testToneAmplitude(pcm, hz)
		if amplitude > peak {
			peak, measuredHz = amplitude, hz
		}
	}
	if measuredHz != frequency {
		t.Fatalf("可闻带频谱主峰 %.0f Hz，期望 1000", measuredHz)
	}
}

func testToneAmplitude(pcm []float64, frequency float64) float64 {
	var sine, cosine float64
	for i, v := range pcm {
		phase := 2 * math.Pi * frequency * float64(i) / (testDSDRate / 8)
		s, c := math.Sincos(phase)
		sine += v * s
		cosine += v * c
	}
	return 2 * math.Hypot(sine, cosine) / float64(len(pcm))
}
