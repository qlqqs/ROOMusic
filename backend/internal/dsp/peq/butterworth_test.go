package peq

import (
	"fmt"
	"math"
	"testing"
)

func TestButterworthFixedQAndResponse(t *testing.T) {
	const fs, cutoff = 48000, 100
	for _, tc := range []struct {
		order int
		qs    []float64
	}{
		{1, nil}, {2, []float64{0.71}}, {3, []float64{1}},
		{4, []float64{0.54, 1.31}}, {5, []float64{0.62, 1.62}},
		{6, []float64{0.52, 0.71, 1.93}}, {8, []float64{0.51, 0.60, 0.90, 2.56}},
	} {
		for _, path := range []string{LowPass, HighPass, "ButterworthLowPass"} {
			if tc.order == 8 && path != "ButterworthLowPass" {
				continue // 八阶只属于供 DSD 使用的纯函数，不扩展 PEQ 设置范围。
			}
			t.Run(fmt.Sprintf("%s/order_%d", path, tc.order), func(t *testing.T) {
				var sections []Section
				if path == "ButterworthLowPass" {
					sections = ButterworthLowPass(tc.order, cutoff, fs)
				} else {
					sections = Design(Band{Type: path, FreqHz: cutoff, SlopeDB: 6 * tc.order}, fs)
				}
				if len(sections) != (tc.order+1)/2 {
					t.Fatalf("节数 = %d，期望 %d", len(sections), (tc.order+1)/2)
				}
				for i, s := range sections {
					if s.Channel != 0 {
						t.Errorf("第 %d 节声道 = %d，期望 0", i, s.Channel)
					}
				}
				// 二阶低通/高通在截止点的幅值等于 Q，独立验证固定 Q 表。
				for i, q := range tc.qs {
					assertNear(t, math.Pow(10, responseDB(sections[i:i+1], cutoff, fs)/20), q, 1e-9)
				}
				if tc.order%2 != 0 {
					s := sections[len(sections)-1]
					if s.A2 != 0 || s.B2 != 0 {
						t.Fatal("奇数阶的最后一节不是一阶")
					}
					assertNear(t, responseDB([]Section{s}, cutoff, fs), -10*math.Log10(2), 1e-9)
				}
				assertNear(t, responseDB(sections, cutoff, fs), -10*math.Log10(2), 0.1)
				stop, pass := float64(10*cutoff), 0.0
				if path == HighPass {
					stop, pass = cutoff/10, fs/2
				}
				assertNear(t, responseDB(sections, pass, fs), 0, 0.01)
				assertNear(t, responseDB(sections, stop, fs), -20*float64(tc.order), 0.5)
			})
		}
	}
}

func TestDesignSecondOrderExplicitQ(t *testing.T) {
	for _, kind := range []string{LowPass, HighPass} {
		for _, slope := range []int{0, 12} {
			t.Run(fmt.Sprintf("%s/slope_%d", kind, slope), func(t *testing.T) {
				const q = 1.3
				sections := Design(Band{Type: kind, FreqHz: 1000, Q: q, SlopeDB: slope}, 48000)
				if len(sections) != 1 {
					t.Fatalf("节数 = %d，期望 1", len(sections))
				}
				assertNear(t, responseDB(sections, 1000, 48000), 20*math.Log10(q), 1e-9)
			})
		}
	}
}
