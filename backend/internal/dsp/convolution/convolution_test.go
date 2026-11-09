package convolution

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

func TestGeometryUsesLongestIR(t *testing.T) {
	for _, tt := range []struct {
		lengths []int
		want    Geometry
	}{
		{[]int{1}, Geometry{Taps: 1, NFFT: 2, Block: 2, Bins: 2}},
		{[]int{3, 1}, Geometry{Taps: 3, NFFT: 8, Block: 6, Bins: 5}},
		{[]int{1, 4096, 300}, Geometry{Taps: 4096, NFFT: 8192, Block: 4097, Bins: 4097}},
		{[]int{8192, 4096}, Geometry{Taps: 8192, NFFT: 16384, Block: 8193, Bins: 8193}},
	} {
		t.Run(fmt.Sprint(tt.lengths), func(t *testing.T) {
			config := Config{Channels: 1}
			for _, n := range tt.lengths {
				config.Paths = append(config.Paths, monoPath(make([]float64, n)))
			}
			c := newConvolver(t, config)
			if got := c.Geometry(); got != tt.want {
				t.Fatalf("Geometry() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestUnitImpulseAcrossBlockSizes(t *testing.T) {
	x := randomSamples(rand.New(rand.NewSource(1)), 2009)
	config := Config{Channels: 1, Paths: []Path{monoPath([]float64{1})}}
	block := newConvolver(t, config).Geometry().Block
	for _, size := range []int{1, 1000, block, 3 * block} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			got := processChunks(t, newConvolver(t, config), [][]float64{x}, []int{size})
			assertSamples(t, got[0], x, 1e-12)
		})
	}
}

func TestRandomIRMatchesDirectConvolution(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	h, x := randomSamples(rng, 300), randomSamples(rng, 2300)
	want := directConvolution(x, h)
	padded := make([]float64, len(want))
	copy(padded, x)
	config := Config{Channels: 1, Paths: []Path{monoPath(h)}}
	block := newConvolver(t, config).Geometry().Block
	for _, sizes := range [][]int{{1}, {1000}, {block}, {3 * block}, {1, 17, block + 1, 53}} {
		t.Run(fmt.Sprint(sizes), func(t *testing.T) {
			got := processChunks(t, newConvolver(t, config), [][]float64{padded}, sizes)
			assertSamples(t, got[0], want, 1e-10)
		})
	}
}

func TestLongIRShortQuantumImmediateOutputAndTail(t *testing.T) {
	const quantum = 9600
	rng := rand.New(rand.NewSource(3))
	h, x := randomSamples(rng, 16384), randomSamples(rng, quantum+7)
	want := directConvolution(x, h)
	padded := make([]float64, len(want)+13)
	copy(padded, x)
	c := newConvolver(t, Config{Channels: 1, Paths: []Path{monoPath(h)}})
	if c.Geometry().Block <= quantum {
		t.Fatalf("测试需要 Block > %d，实际为 %d", quantum, c.Geometry().Block)
	}
	// 每次 Process 都必须立即返回等长输出；补零显式冲刷完整尾部，不由处理器截断。
	got := processChunks(t, c, [][]float64{padded}, []int{quantum, 7, 127})
	assertSamples(t, got[0], append(want, make([]float64, 13)...), 1e-10)
}

func TestMatrixRouting(t *testing.T) {
	c := newConvolver(t, Config{Channels: 3, Paths: []Path{
		{IR: []float64{1, 2}, Inputs: []Weighted{{0, 0.5}, {1, -1}}, Outputs: []Weighted{{0, 1}, {1, 2}}},
		{IR: []float64{1}, Inputs: []Weighted{{0, 1}}, Outputs: []Weighted{{1, -0.25}}},
	}})
	x := [][]float64{{1, 2, -1, 0}, {4, -2, 3, 0}, {9, 8, 7, 0}}
	// 第一条路径先混合成 [-3.5, 3, -3.5]，再卷积；第二条路径叠加到输出 1。
	want := [][]float64{{-3.5, -4, 2.5, -7}, {-7.25, -8.5, 5.25, -14}, {0, 0, 0, 0}}
	got := processChunks(t, c, x, []int{1, 2})
	for ch := range want {
		assertSamples(t, got[ch], want[ch], 1e-12)
	}
}

func TestInputAndOutputDelaysAcrossBlocks(t *testing.T) {
	config := Config{Channels: 2, InDelay: []int{3, 1}, OutDelay: []int{2, 4}, Paths: []Path{
		{IR: []float64{1}, Inputs: []Weighted{{0, 1}, {1, 2}}, Outputs: []Weighted{{0, 1}, {1, -1}}},
	}}
	x := [][]float64{{1, 2, -1, 0, 0, 0, 0, 0, 0, 0}, {3, -2, 4, 0, 0, 0, 0, 0, 0, 0}}
	want := [][]float64{{0, 0, 0, 6, -4, 9, 2, -1, 0, 0}, {0, 0, 0, 0, 0, -6, 4, -9, -2, 1}}
	got := processChunks(t, newConvolver(t, config), x, []int{2, 1, 4})
	for ch := range want {
		assertSamples(t, got[ch], want[ch], 1e-12)
	}
}

func TestLatencyUsesFirstAbsolutePeak(t *testing.T) {
	long := make([]float64, 4096)
	long[2048] = -4
	for _, tt := range []struct {
		name string
		irs  [][]float64
		want int
	}{
		{"negative_peak", [][]float64{{1, 2}, long}, 2048},
		{"tie_in_path", [][]float64{{0, -2, 2}}, 1},
		{"tie_across_paths", [][]float64{{0, 0, -2}, {2}}, 2},
		{"zero", [][]float64{{0, 0, 0}}, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := Config{Channels: 1, InDelay: []int{7}, OutDelay: []int{11}}
			for _, h := range tt.irs {
				config.Paths = append(config.Paths, monoPath(h))
			}
			if got := newConvolver(t, config).Latency(); got != tt.want {
				t.Fatalf("Latency() = %d, want %d（不含输入/输出延时）", got, tt.want)
			}
		})
	}
}

func TestResetClearsOverlapAndDelays(t *testing.T) {
	config := Config{Channels: 1, InDelay: []int{3}, OutDelay: []int{2}, Paths: []Path{monoPath([]float64{1, -2, 3, 4})}}
	c := newConvolver(t, config)
	processChunks(t, c, [][]float64{{1, 2, 3, 4, 5, 6, 7}}, []int{2, 3})
	c.Reset()
	x := [][]float64{{0, 0, 0, 0, 0, 0, 0, 0, 0, 1, -1, 0, 0, 0, 0, 0, 0, 0, 0, 0}}
	got := processChunks(t, c, x, []int{1, 4, 2})
	want := processChunks(t, newConvolver(t, config), x, []int{1, 4, 2})
	assertSamples(t, got[0], want[0], 1e-12)
	if c.Geometry() != newConvolver(t, config).Geometry() || c.Latency() != 3 {
		t.Fatal("Reset 改变了几何或峰位置")
	}
}

func TestNewOwnsConfigurationSlices(t *testing.T) {
	config := Config{Channels: 2, InDelay: []int{1, 0}, OutDelay: []int{0, 2}, Paths: []Path{
		{IR: []float64{1, 2}, Inputs: []Weighted{{0, 1}, {1, 0.5}}, Outputs: []Weighted{{0, 1}, {1, -1}}},
	}}
	c := newConvolver(t, config)
	config.Paths[0].IR[0], config.Paths[0].IR[1] = 99, 0
	config.Paths[0].Inputs[0], config.Paths[0].Inputs[1] = Weighted{1, 7}, Weighted{0, 8}
	config.Paths[0].Outputs[0], config.Paths[0].Outputs[1] = Weighted{1, 9}, Weighted{0, 10}
	config.InDelay[0], config.OutDelay[1] = 0, 0
	config.Paths[0] = monoPath([]float64{0})
	x := [][]float64{{1, 0, 0, 0, 0}, {2, 0, 0, 0, 0}}
	got := processChunks(t, c, x, []int{1, 3})
	assertSamples(t, got[0], []float64{1, 3, 2, 0, 0}, 1e-12)
	assertSamples(t, got[1], []float64{0, 0, -1, -3, -2}, 1e-12)
	if c.Latency() != 1 {
		t.Fatalf("配置修改后 Latency() = %d, want 1", c.Latency())
	}
}

func monoPath(h []float64) Path {
	return Path{IR: h, Inputs: []Weighted{{0, 1}}, Outputs: []Weighted{{0, 1}}}
}

func newConvolver(t *testing.T, config Config) *Convolver {
	t.Helper()
	c, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func randomSamples(rng *rand.Rand, n int) []float64 {
	x := make([]float64, n)
	for i := range x {
		x[i] = 2*rng.Float64() - 1
	}
	return x
}

func directConvolution(x, h []float64) []float64 {
	y := make([]float64, len(x)+len(h)-1)
	for i, v := range x {
		for j, tap := range h {
			y[i+j] += v * tap
		}
	}
	return y
}

func processChunks(t *testing.T, c *Convolver, x [][]float64, sizes []int) [][]float64 {
	t.Helper()
	y := make([][]float64, len(x))
	for start, call := 0, 0; start < len(x[0]); call++ {
		end := min(start+sizes[call%len(sizes)], len(x[0]))
		block := make([][]float64, len(x))
		for ch := range x {
			block[ch] = append([]float64(nil), x[ch][start:end]...)
		}
		out := c.Process(block)
		if len(out) != len(x) {
			t.Fatalf("调用 %d 输出声道数 = %d, want %d", call, len(out), len(x))
		}
		for ch := range out {
			if len(out[ch]) != end-start {
				t.Fatalf("调用 %d 声道 %d 未立即返回等长输出：got %d, want %d", call, ch, len(out[ch]), end-start)
			}
			assertSamples(t, block[ch], x[ch][start:end], 0)
			// Process 的结果仅在下一次调用前有效，必须立即复制。
			y[ch] = append(y[ch], out[ch]...)
		}
		start = end
	}
	return y
}

func assertSamples(t *testing.T, got, want []float64, tolerance float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("样本数 = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if math.IsNaN(got[i]) || math.Abs(got[i]-want[i]) > tolerance {
			t.Fatalf("样本 %d = %.17g, want %.17g（误差限 %g）", i, got[i], want[i], tolerance)
		}
	}
}
