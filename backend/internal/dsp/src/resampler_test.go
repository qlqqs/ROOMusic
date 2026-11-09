package src

import (
	"math"
	"slices"
	"testing"
)

func processBlocks(t *testing.T, r *Resampler, input [][]float64, block, l, m int) [][]float64 {
	t.Helper()
	out := make([][]float64, len(input))
	for start := 0; start < len(input[0]); start += block {
		end := min(start+block, len(input[0]))
		part := make([][]float64, len(input))
		for ch := range input {
			part[ch] = input[ch][start:end]
		}
		got := r.Process(part)
		want := end*l/m - start*l/m
		if len(got) != len(input) {
			t.Fatalf("输出声道数 = %d，期望 %d", len(got), len(input))
		}
		for ch := range got {
			if len(got[ch]) != want {
				t.Fatalf("累计输入 %d，声道 %d 本块输出 %d，期望 %d", end, ch, len(got[ch]), want)
			}
			out[ch] = append(out[ch], got[ch]...)
		}
	}
	return out
}

func TestResamplerSignalsBlocksAndReset(t *testing.T) {
	for _, tc := range []struct {
		name        string
		from, to, n int
		stages      []Stage
	}{
		{"44_1到48", 44100, 48000, 9600, []Stage{{Linear, Precise, 160, 147}}},
		{"48到44_1", 48000, 44100, 9600, []Stage{{Linear, Precise, 147, 160}}},
		{"2倍升", 44100, 88200, 9600, []Stage{{Linear, Precise, 2, 1}}},
		{"DSD512两级降", 2822400, 352800, 96000, []Stage{{Linear, Stage2, 1, 4}, {Linear, Precise, 1, 2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frequencies := []float64{1000, 8000, 16000}
			// 三个不同频点、直流和静音分占声道，同时检测串音与每声道独立历史。
			input := make([][]float64, 5)
			saved := make([][]float64, len(input))
			for ch := range input {
				input[ch] = make([]float64, tc.n)
				for i := range input[ch] {
					if ch < 3 {
						input[ch][i] = 0.5 * math.Sin(2*math.Pi*frequencies[ch]*float64(i)/float64(tc.from))
					}
					if ch == 3 {
						input[ch][i] = 1
					}
				}
				saved[ch] = slices.Clone(input[ch])
			}
			one := processBlocks(t, New(tc.stages, len(input)), input, 1, tc.to, tc.from)
			r := New(tc.stages, len(input))
			large := processBlocks(t, r, input, 9600, tc.to, tc.from)
			for ch := range input {
				if !slices.Equal(input[ch], saved[ch]) {
					t.Fatalf("输入声道 %d 被修改", ch)
				}
				if !slices.Equal(one[ch], large[ch]) {
					t.Fatalf("声道 %d 的块长 1 与 9600 输出不同", ch)
				}
			}
			// 末尾取整周期的稳态窗口，投影到目标频率；残差同时排除频率漂移和镜像。
			window := tc.to / 100
			for ch, f := range frequencies {
				values := large[ch][len(large[ch])-window:]
				sine, cosine := 0.0, 0.0
				for i, v := range values {
					s, c := math.Sincos(2 * math.Pi * f * float64(i) / float64(tc.to))
					sine += v * s
					cosine += v * c
				}
				sine *= 2 / float64(window)
				cosine *= 2 / float64(window)
				levelDB := 20 * math.Log10(math.Hypot(sine, cosine)/0.5)
				if math.IsNaN(levelDB) || math.Abs(levelDB) > 0.01 {
					t.Errorf("%.0f Hz 电平误差 %.8f dB", f, levelDB)
				}
				energy, residual := 0.0, 0.0
				for i, v := range values {
					s, c := math.Sincos(2 * math.Pi * f * float64(i) / float64(tc.to))
					delta := v - sine*s - cosine*c
					residual += delta * delta
					energy += v * v
				}
				if math.Sqrt(residual/energy) > 1e-5 {
					t.Errorf("%.0f Hz 输出不符合目标频率，归一残差 %.8g", f, math.Sqrt(residual/energy))
				}
			}
			for _, v := range large[3][len(large[3])-window:] {
				if math.Abs(v-1) > 1e-7 {
					t.Fatalf("直流稳态增益 %.12g，期望 1", v)
				}
			}
			for _, v := range large[4] {
				if v != 0 {
					t.Fatal("静音声道出现串音")
				}
			}
			// 特意在非整数相位/有待发布样本处 Reset，不能只测恰好对齐的流尾。
			r.Reset()
			dirty := make([][]float64, len(input))
			for ch := range dirty {
				dirty[ch] = input[ch][:3]
			}
			retained := r.Process(dirty)
			retainedCopy := make([][]float64, len(retained))
			for ch := range retained {
				retainedCopy[ch] = slices.Clone(retained[ch])
			}
			r.Reset()
			again := processBlocks(t, r, input, 9600, tc.to, tc.from)
			for ch := range again {
				if !slices.Equal(again[ch], large[ch]) {
					t.Fatalf("声道 %d Reset 后未恢复初始状态", ch)
				}
				if !slices.Equal(retained[ch], retainedCopy[ch]) {
					t.Fatalf("声道 %d 已返回样本在 Reset/后续调用后被改写", ch)
				}
			}
		})
	}
}

// 直接按离散 FIR 定义求和，不复用多相系数、相位累加器或长度辅助函数。
func directStages(input []float64, stages []Stage) []float64 {
	data := slices.Clone(input)
	for _, s := range stages {
		h := Kernel(s.Phase, s.Quality, s.L, s.M)
		out := make([]float64, (len(data)*s.L+s.M-1)/s.M)
		for k := range out {
			for i, x := range data {
				tap := k*s.M - i*s.L
				if tap >= 0 && tap < len(h) {
					out[k] += float64(s.L) * x * h[tap]
				}
			}
		}
		data = out
	}
	return data
}

func assertSamplesNear(t *testing.T, got, want []float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("样本数 %d，期望 %d", len(got), len(want))
	}
	for i, v := range got {
		if math.IsNaN(v) || math.Abs(v-want[i]) > 1e-12 {
			t.Fatalf("样本 %d = %.15g，期望 %.15g", i, v, want[i])
		}
	}
}

func TestResamplerCumulativeFloorAndIRCeil(t *testing.T) {
	for _, tc := range []struct {
		name     string
		from, to int
		stages   []Stage
	}{
		{"跨族升", 44100, 48000, []Stage{{Linear, Smooth, 160, 147}}},
		{"跨族降", 48000, 44100, []Stage{{Linear, Smooth, 147, 160}}},
		{"跨族后倍增不能逐级floor", 44100, 768000, []Stage{{Linear, Smooth, 1280, 147}, {Linear, Smooth, 2, 1}}},
		{"32到44_1两级", 32000, 44100, []Stage{{Linear, Smooth, 3, 2}, {Linear, Smooth, 147, 160}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := make([]float64, 23)
			input[0], input[7], input[22] = 1, -0.5, 0.25
			saved := slices.Clone(input)
			wantFloor := len(input) * tc.to / tc.from
			wantCeil := (len(input)*tc.to + tc.from - 1) / tc.from
			expected := directStages(input, tc.stages)[:wantFloor]
			small := processBlocks(t, New(tc.stages, 1), [][]float64{input}, 1, tc.to, tc.from)[0]
			large := processBlocks(t, New(tc.stages, 1), [][]float64{input}, 9600, tc.to, tc.from)[0]
			assertSamplesNear(t, small, expected)
			if !slices.Equal(small, large) {
				t.Fatal("多级输出随块长改变")
			}
			// 两个纯升频级的先后未被合同固定，IR 的 FIR 对照采用规划器选择的顺序。
			plan, err := Choose(Request{SourceRate: tc.from, Strategy: "custom", CustomRate: tc.to, Filter: LinearSmooth, Supported: func(rate int) bool { return rate == tc.to }})
			if err != nil {
				t.Fatal(err)
			}
			delay := 0.0
			for _, stage := range plan.Stages {
				// smooth 原型长为 max(L,M)×20+1；不读取被测 Resampler 的延迟。
				delay = delay*float64(stage.L)/float64(stage.M) + 10*float64(max(stage.L, stage.M))/float64(stage.M)
			}
			latency := int(math.Round(delay))
			padded := make([]float64, ((latency+wantCeil)*tc.from+tc.to-1)/tc.to)
			copy(padded, input)
			reference := directStages(padded, plan.Stages)[latency : latency+wantCeil]
			got := ResampleAll(input, tc.from, tc.to, Linear, Smooth)
			assertSamplesNear(t, got, reference)
			if !slices.Equal(input, saved) {
				t.Fatal("ResampleAll 修改输入")
			}
		})
	}
}

func TestResamplerLatency(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stages []Stage
		want   int
	}{
		{"直通", nil, 0},
		{"线性precise升", []Stage{{Linear, Precise, 160, 147}}, 90},
		{"线性precise降", []Stage{{Linear, Precise, 147, 160}}, 83},
		{"线性smooth", []Stage{{Linear, Smooth, 2, 1}}, 20},
		{"2s半样本四舍五入", []Stage{{Linear, Stage2, 1, 2}}, 10},
		{"DSD512两级", []Stage{{Linear, Stage2, 1, 4}, {Linear, Precise, 1, 2}}, 88},
		// 83×160/147×2 + 83×2 = 346.68027… → 347，逐级取整会错误地得到 346。
		{"只在最终输出率取整一次", []Stage{{Linear, Precise, 160, 147}, {Linear, Precise, 2, 1}}, 347},
		{"最小相位申报零", []Stage{{Minimum, Precise, 160, 147}}, 0},
		{"混合相位", []Stage{{Minimum, Precise, 160, 147}, {Linear, Precise, 2, 1}}, 166},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := New(tc.stages, 1)
			if got := r.Latency(); got != tc.want {
				t.Errorf("Latency = %d，期望 %d", got, tc.want)
			}
			r.Process([][]float64{{1, 0, 0}})
			r.Reset()
			if r.Latency() != tc.want {
				t.Error("Reset 改变延迟配置")
			}
		})
	}
}

func TestResamplerPassthroughCopies(t *testing.T) {
	input := []float64{1, 0, -0.5}
	r := New(nil, 1)
	got := r.Process([][]float64{input})[0]
	assertSamplesNear(t, got, input)
	got[0] = 7
	if input[0] != 1 {
		t.Fatal("直通输出与输入共享存储")
	}
	ir := ResampleAll(input, 48000, 48000, Linear, Precise)
	assertSamplesNear(t, ir, input)
	ir[0] = 8
	if input[0] != 1 {
		t.Fatal("无变率 IR 与输入共享存储")
	}
}
