package peq

import (
	"math"
	"math/cmplx"
	"slices"
	"testing"
)

func TestDesignCookbookSelfCheck(t *testing.T) {
	sections := Design(Band{Type: Peaking, FreqHz: 1000, GainDB: 6, Q: 1}, 44100)
	if len(sections) != 1 {
		t.Fatalf("节数 = %d，期望 1", len(sections))
	}
	s := sections[0]
	for _, tc := range []struct {
		name      string
		got, want float64
	}{
		{"B0", s.B0, 1.04763003},
		{"B1", s.B1, -1.88499125},
		{"B2", s.B2, 0.85665646},
		{"A1", s.A1, -1.88499125},
		{"A2", s.A2, 0.90428649},
	} {
		t.Run(tc.name, func(t *testing.T) { assertNear(t, tc.got, tc.want, 1e-8) })
	}
	if s.B1 != s.A1 {
		t.Errorf("B1 = %.17g，A1 = %.17g，期望完全相等", s.B1, s.A1)
	}
}

func TestDesignCharacteristicFrequencies(t *testing.T) {
	const fs = 48000
	type point struct{ hz, db float64 }
	for _, tc := range []struct {
		kind   string
		points []point
	}{
		{Peaking, []point{{0, 0}, {1000, 6}, {fs / 2, 0}}},
		{LowShelf, []point{{0, 6}, {1000, 3}, {fs / 2, 0}}},
		{HighShelf, []point{{0, 0}, {1000, 3}, {fs / 2, 6}}},
		{LowPass, []point{{0, 0}, {1000, -10 * math.Log10(2)}}},
		{HighPass, []point{{1000, -10 * math.Log10(2)}, {fs / 2, 0}}},
		{BandPass, []point{{1000, 0}}},
		{Notch, []point{{0, 0}, {fs / 2, 0}}},
		{AllPass, []point{{0, 0}, {100, 0}, {1000, 0}, {10000, 0}, {fs / 2, 0}}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			q := 1.0
			if tc.kind == LowPass || tc.kind == HighPass {
				q = 1 / math.Sqrt2
			}
			sections := Design(Band{Type: tc.kind, FreqHz: 1000, GainDB: 6, Q: q, SlopeDB: 12}, fs)
			for _, p := range tc.points {
				got := responseDB(sections, p.hz, fs)
				if math.IsNaN(got) || math.Abs(got-p.db) > 0.01 {
					t.Errorf("%g Hz：%g dB，期望 %g ± 0.01 dB", p.hz, got, p.db)
				}
			}
			var zeros []float64
			switch tc.kind {
			case LowPass:
				zeros = []float64{fs / 2}
			case HighPass:
				zeros = []float64{0}
			case BandPass:
				zeros = []float64{0, fs / 2}
			case Notch:
				zeros = []float64{1000}
			}
			for _, hz := range zeros {
				if got := responseDB(sections, hz, fs); math.IsNaN(got) || got > -100 {
					t.Errorf("%g Hz：%g dB，期望 ≤ -100 dB", hz, got)
				}
			}
		})
	}
}

func TestDesignFrequencyClamp(t *testing.T) {
	const fs = 44100
	for _, kind := range []string{Peaking, LowShelf, HighShelf, LowPass, HighPass, BandPass, Notch, AllPass} {
		t.Run(kind, func(t *testing.T) {
			for _, bounds := range [][2]float64{{1, 10}, {30000, 0.49 * fs}} {
				band := Band{Type: kind, FreqHz: bounds[0], GainDB: 6, Q: 1, SlopeDB: 36}
				got := Design(band, fs)
				band.FreqHz = bounds[1]
				if want := Design(band, fs); !slices.Equal(got, want) {
					t.Errorf("%g Hz 未按 %g Hz 钳位计算", bounds[0], bounds[1])
				}
			}
		})
	}
	t.Run("ButterworthLowPass", func(t *testing.T) {
		for _, bounds := range [][2]float64{{1, 10}, {30000, 0.49 * fs}} {
			if !slices.Equal(ButterworthLowPass(8, bounds[0], fs), ButterworthLowPass(8, bounds[1], fs)) {
				t.Errorf("%g Hz 未按 %g Hz 钳位计算", bounds[0], bounds[1])
			}
		}
	})
}

func TestDesignIndependentSections(t *testing.T) {
	for _, band := range []Band{
		{Type: Peaking, FreqHz: 1000, GainDB: 6, Q: 1},
		{Type: LowPass, FreqHz: 1000, SlopeDB: 36},
	} {
		t.Run(band.Type, func(t *testing.T) {
			first := Design(band, 48000)
			want := slices.Clone(first)
			second := Design(band, 48000)
			for i := range first {
				first[i] = Section{B0: 42, Channel: 2}
			}
			if !slices.Equal(second, want) || !slices.Equal(Design(band, 48000), want) {
				t.Fatal("修改一次 Design 的结果影响了其他调用")
			}
		})
	}
}

// 直接求 H(e^jw)，不经过 IIR 或实现中的系数设计辅助函数。
func responseDB(sections []Section, hz, fs float64) float64 {
	z := cmplx.Exp(complex(0, -2*math.Pi*hz/fs))
	h := complex(1, 0)
	for _, s := range sections {
		h *= (complex(s.B0, 0) + complex(s.B1, 0)*z + complex(s.B2, 0)*z*z) /
			(1 + complex(s.A1, 0)*z + complex(s.A2, 0)*z*z)
	}
	return 20 * math.Log10(cmplx.Abs(h))
}

func assertNear(t *testing.T, got, want, tolerance float64) {
	t.Helper()
	if math.IsNaN(got) || math.Abs(got-want) > tolerance {
		t.Fatalf("得到 %.17g，期望 %.17g ± %g", got, want, tolerance)
	}
}
