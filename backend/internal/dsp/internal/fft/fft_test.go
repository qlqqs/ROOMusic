package fft

import (
	"fmt"
	"math"
	"math/cmplx"
	"testing"
)

func TestTransformSigns(t *testing.T) {
	for _, tt := range []struct {
		name    string
		inverse bool
		want    []complex128
	}{
		{"forward", false, []complex128{1, -1i, -1, 1i}},
		{"inverse", true, []complex128{1, 1i, -1, -1i}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			x := []complex128{0, 1, 0, 0}
			p := New(len(x))
			if tt.inverse {
				p.Inverse(x)
			} else {
				p.Forward(x)
			}
			for k := range x {
				assertComplex(t, k, x[k], tt.want[k])
			}
		})
	}
}

func TestComplexRoundTripIsNTimesInput(t *testing.T) {
	for _, n := range []int{1, 2, 8, 32} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			x := make([]complex128, n)
			for i := range x {
				x[i] = complex(float64(i%5)-1.25, float64(i%3)+0.5)
			}
			want := append([]complex128(nil), x...)
			p := New(n)
			p.Forward(x)
			p.Inverse(x)
			for i := range x {
				assertComplex(t, i, x[i], complex(float64(n), 0)*want[i])
			}
		})
	}
}

func TestRealTransformsMatchComplex(t *testing.T) {
	for _, n := range []int{1, 2, 8, 32} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			x := make([]float64, n)
			full := make([]complex128, n)
			for i := range x {
				x[i] = float64(i%7) - 2.25
				full[i] = complex(x[i], 0)
			}
			original := append([]float64(nil), x...)
			p := New(n)
			p.Forward(full)
			half := make([]complex128, n/2+1)
			p.RealForward(half, x)
			for k := range half {
				assertComplex(t, k, half[k], full[k])
			}
			spectrum := append([]complex128(nil), half...)
			got := make([]float64, n)
			p.RealInverse(got, half)
			p.Inverse(full)
			for i := range got {
				assertComplex(t, i, complex(got[i], 0), full[i])
				assertComplex(t, i, complex(got[i], 0), complex(float64(n)*original[i], 0))
				if x[i] != original[i] {
					t.Fatalf("RealForward 修改了输入样本 %d", i)
				}
			}
			for k := range half {
				if half[k] != spectrum[k] {
					t.Fatalf("RealInverse 修改了频谱 %d", k)
				}
			}
		})
	}
}

func assertComplex(t *testing.T, index int, got, want complex128) {
	t.Helper()
	err := cmplx.Abs(got - want)
	if math.IsNaN(err) || err > 1e-12 {
		t.Fatalf("索引 %d = %v, want %v（误差 %g）", index, got, want, err)
	}
}
