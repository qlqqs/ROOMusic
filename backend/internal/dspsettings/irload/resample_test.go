package irload

import (
	"errors"
	"math"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/dsp/convolution"
	"github.com/qlqq/roomusic/backend/internal/dsp/src"
)

func TestResampleIRNormalization(t *testing.T) {
	for _, tt := range []struct {
		name    string
		samples []float64
		from    int
		to      int
		nearDC  bool
	}{
		{name: "同率独立副本", samples: []float64{2, -0.5, 0.25}, from: 48000, to: 48000},
		{name: "变率长度向上取整且保留代数和", samples: []float64{2, -0.5, 0.25}, from: 44100, to: 48000},
		{name: "零直流按采样率比缩放", samples: []float64{1, -1}, from: 44100, to: 48000, nearDC: true},
		{name: "非零但接近零直流按采样率比缩放", samples: []float64{1, -0.999}, from: 44100, to: 48000, nearDC: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResampleIR(tt.samples, tt.from, tt.to)
			if err != nil {
				t.Fatalf("重采样失败：%v", err)
			}
			wantLength := (len(tt.samples)*tt.to + tt.from - 1) / tt.from
			if len(got) != wantLength {
				t.Fatalf("样本数 = %d，期望 %d", len(got), wantLength)
			}
			if tt.from == tt.to {
				if !slices.Equal(got, tt.samples) || &got[0] == &tt.samples[0] {
					t.Fatal("同率必须返回数值不变的独立副本")
				}
				return
			}
			if tt.nearDC {
				// 只核验装载层施加的增益，不重新验证SRC滤波器。
				raw := src.ResampleAll(tt.samples, tt.from, tt.to, src.Linear, src.Precise)
				for i, v := range got {
					want := raw[i] * float64(tt.from) / float64(tt.to)
					if math.IsNaN(v) || math.Abs(v-want) > 1e-12 {
						t.Fatalf("样本 %d = %g，按采样率比缩放后应为 %g", i, v, want)
					}
				}
				return
			}
			var original, converted float64
			for _, v := range tt.samples {
				original += v
			}
			for _, v := range got {
				converted += v
			}
			if math.IsNaN(converted) || math.Abs(converted-original) > 1e-9*math.Abs(original) {
				t.Fatalf("重采样代数和 = %g，原代数和 = %g", converted, original)
			}
		})
	}
}

func TestResampleIRErrors(t *testing.T) {
	for _, tt := range []struct {
		name         string
		taps         int
		from         int
		to           int
		wantTapLimit bool
	}{
		{name: "目标抽头超限", taps: convolution.MaxTaps*44100/48000 + 1, from: 44100, to: 48000, wantTapLimit: true},
		{name: "不支持的比率", taps: 3, from: 44100, to: 44101},
	} {
		t.Run(tt.name, func(t *testing.T) {
			samples := make([]float64, tt.taps)
			samples[0] = 1
			// 来源在测量前分配；只检查拒绝路径未分配目标IR大小的缓冲。
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			got, err := ResampleIR(samples, tt.from, tt.to)
			runtime.ReadMemStats(&after)
			if err == nil || got != nil {
				t.Fatalf("应返回错误且无样本，实际样本数 %d，错误 %v", len(got), err)
			}
			if tt.wantTapLimit {
				if !strings.Contains(err.Error(), "resampled IR exceeds tap limit") {
					t.Fatalf("应在重采样前拒绝目标抽头超限，实际错误：%v", err)
				}
				if allocated := after.TotalAlloc - before.TotalAlloc; allocated >= uint64(convolution.MaxTaps)*8 {
					t.Fatalf("拒绝超限IR时分配了 %d 字节，不应分配目标缓冲", allocated)
				}
			} else if !errors.Is(err, src.ErrUnsupportedRatio) {
				t.Fatalf("错误 = %v，期望 src.ErrUnsupportedRatio", err)
			}
		})
	}
}
