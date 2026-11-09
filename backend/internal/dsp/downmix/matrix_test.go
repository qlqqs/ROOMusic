package downmix_test

import (
	"math"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/dsp/downmix"
)

type matrixCase struct {
	name string
	src  downmix.Mask
	dst  downmix.Mask
	want [][]float64
}

func standardMatrices() []matrixCase {
	// 直接按设计 §2.2 的绝对 dB 取值，不复用构建器的相对增益规则。
	front51 := math.Pow(10, -6.0/20)
	center51 := math.Pow(10, -9.0/20)
	surround51 := math.Pow(10, -12.0/20)
	front71 := math.Pow(10, -6.8/20)
	center71 := math.Pow(10, -9.8/20)
	surround71 := math.Pow(10, -12.8/20)
	gain51 := math.Pow(10, -3.2/20)
	return []matrixCase{
		{"5.0侧环绕到2.0", 0x607, 0x3, [][]float64{
			{front51, 0, center51, surround51, 0},
			{0, front51, center51, 0, surround51},
		}},
		{"5.0后环绕到2.0", 0x37, 0x3, [][]float64{
			{front51, 0, center51, surround51, 0},
			{0, front51, center51, 0, surround51},
		}},
		{"5.1侧环绕到2.0", 0x60f, 0x3, [][]float64{
			{front51, 0, center51, 0, surround51, 0},
			{0, front51, center51, 0, 0, surround51},
		}},
		{"5.1后环绕到2.0", 0x3f, 0x3, [][]float64{
			{front51, 0, center51, 0, surround51, 0},
			{0, front51, center51, 0, 0, surround51},
		}},
		{"7.1到2.0", 0x63f, 0x3, [][]float64{
			{front71, 0, center71, 0, surround71, 0, surround71, 0},
			{0, front71, center71, 0, 0, surround71, 0, surround71},
		}},
		{"7.1到5.1", 0x63f, 0x60f, [][]float64{
			{gain51, 0, 0, 0, 0, 0, 0, 0},
			{0, gain51, 0, 0, 0, 0, 0, 0},
			{0, 0, gain51, 0, 0, 0, 0, 0},
			{0, 0, 0, gain51, 0, 0, 0, 0},
			{0, 0, 0, 0, gain51, 0, gain51, 0},
			{0, 0, 0, 0, 0, gain51, 0, gain51},
		}},
	}
}

func TestBuildStandardMatrices(t *testing.T) {
	for _, tt := range standardMatrices() {
		t.Run(tt.name, func(t *testing.T) {
			assertMatrix(t, tt)
		})
	}
}

func TestBuildSpeakerOrder(t *testing.T) {
	front4 := math.Pow(10, -6.0/20)
	center4 := math.Pow(10, -9.0/20)
	backCenter4 := math.Pow(10, -15.0/20)
	front6 := math.Pow(10, -6.8/20)
	center6 := math.Pow(10, -9.8/20)
	surround6 := math.Pow(10, -12.8/20)
	backCenter6 := math.Pow(10, -15.8/20)
	gain51 := math.Pow(10, -3.2/20)
	backCenter51 := math.Pow(10, -6.2/20)
	tests := []matrixCase{
		{"4.0的BC双写", 0x107, 0x3, [][]float64{
			{front4, 0, center4, backCenter4},
			{0, front4, center4, backCenter4},
		}},
		// 输入顺序为 FL、FR、FC、LFE、BC、SL、SR。
		{"6.1的BC双写到2.0", 0x70f, 0x3, [][]float64{
			{front6, 0, center6, 0, backCenter6, surround6, 0},
			{0, front6, center6, 0, backCenter6, 0, surround6},
		}},
		{"6.1的BC并入两侧环绕", 0x70f, 0x60f, [][]float64{
			{gain51, 0, 0, 0, 0, 0, 0},
			{0, gain51, 0, 0, 0, 0, 0},
			{0, 0, gain51, 0, 0, 0, 0},
			{0, 0, 0, gain51, 0, 0, 0},
			{0, 0, 0, 0, backCenter51, gain51, 0},
			{0, 0, 0, 0, backCenter51, 0, gain51},
		}},
		// 输入顺序为 FL、FR、FC、FLC、FRC、0x800、0x80000000；高位保留零系数列。
		{"前中左右与顶部到2.0", 0x800008c7, 0x3, [][]float64{
			{front6, 0, center6, front6, 0, 0, 0},
			{0, front6, center6, 0, front6, 0, 0},
		}},
		{"前中左右与顶部到5.1", 0x800008c7, 0x60f, [][]float64{
			{gain51, 0, 0, gain51, 0, 0, 0},
			{0, gain51, 0, 0, gain51, 0, 0},
			{0, 0, gain51, 0, 0, 0, 0},
			{0, 0, 0, 0, 0, 0, 0},
			{0, 0, 0, 0, 0, 0, 0},
			{0, 0, 0, 0, 0, 0, 0},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertMatrix(t, tt)
		})
	}
}

func assertMatrix(t *testing.T, tt matrixCase) {
	t.Helper()
	m, err := downmix.Build(tt.src, tt.dst)
	if err != nil {
		t.Fatal(err)
	}
	if m.In != tt.src || m.Out != tt.dst || len(m.Gains) != len(tt.want) {
		t.Fatalf("矩阵布局或行数不符：In=%#x Out=%#x rows=%d", uint32(m.In), uint32(m.Out), len(m.Gains))
	}
	for out, row := range tt.want {
		if len(m.Gains[out]) != len(row) {
			t.Fatalf("输出 %d：列数=%d，期望 %d", out, len(m.Gains[out]), len(row))
		}
		for in, want := range row {
			if got := m.Gains[out][in]; !(math.Abs(got-want) <= 1e-9) {
				t.Errorf("Gains[%d][%d]=%.12g，期望 %.12g", out, in, got, want)
			}
		}
	}
}

func TestProcessSeparatedSines(t *testing.T) {
	const sampleRate, frames = 48000, 48000
	for _, tt := range standardMatrices() {
		// 验收只需三种标准场景；后环绕别名与 5.0 已由系数测试覆盖。
		if tt.src != 0x60f && tt.src != 0x63f {
			continue
		}
		t.Run(tt.name, func(t *testing.T) {
			m, err := downmix.Build(tt.src, tt.dst)
			if err != nil {
				t.Fatal(err)
			}
			in := make([][]float64, len(tt.want[0]))
			for channel := range in {
				in[channel] = make([]float64, frames)
				frequency := float64(300 + 200*channel)
				for frame := range in[channel] {
					in[channel][frame] = math.Sin(2 * math.Pi * frequency * float64(frame) / sampleRate)
				}
			}
			out := downmix.New(m).Process(in)
			if len(out) != len(tt.want) {
				t.Fatalf("输出声道数=%d，期望 %d", len(out), len(tt.want))
			}
			for channel, samples := range out {
				if len(samples) != frames {
					t.Fatalf("输出 %d：帧数=%d，期望 %d", channel, len(samples), frames)
				}
				for input, want := range tt.want[channel] {
					frequency := float64(300 + 200*input)
					amplitude := sineAmplitude(samples, frequency, sampleRate)
					if want == 0 {
						if !(amplitude <= 1e-6) {
							t.Errorf("输出 %d，%g Hz：幅度=%g，超过 −120 dB", channel, frequency, amplitude)
						}
					} else if errorDB := 20 * math.Log10(amplitude/want); !(math.Abs(errorDB) <= 0.01) {
						t.Errorf("输出 %d，%g Hz：幅度=%.12g，期望 %.12g，偏差=%g dB", channel, frequency, amplitude, want, errorDB)
					}
				}
			}
		})
	}
}

func sineAmplitude(samples []float64, frequency, sampleRate float64) float64 {
	var real, imaginary float64
	for frame, sample := range samples {
		sine, cosine := math.Sincos(2 * math.Pi * frequency * float64(frame) / sampleRate)
		real += sample * cosine
		imaginary -= sample * sine
	}
	return 2 * math.Hypot(real, imaginary) / float64(len(samples))
}
