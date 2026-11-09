package fft_test

import (
	"math"
	"math/cmplx"
	"math/rand/v2"
	"strconv"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/dsp/internal/fft"
)

func TestFFTForwardAndInverse(t *testing.T) {
	for n := 8; n <= 1024; n *= 2 {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(1, uint64(n)))
			input := make([]complex128, n)
			for j := range input {
				input[j] = complex(2*rng.Float64()-1, 2*rng.Float64()-1)
			}
			got := append([]complex128(nil), input...)
			plan := fft.New(n)
			plan.Forward(got)
			for k, value := range got {
				var want complex128
				for j, sample := range input {
					// 利用周期性约化相角，避免 DFT 基准的大相角舍入误差。
					angle := -2 * math.Pi * float64((j*k)%n) / float64(n)
					s, c := math.Sincos(angle)
					want += sample * complex(c, s)
				}
				if delta := cmplx.Abs(value - want); delta > 1e-12 {
					t.Fatalf("频点 %d：FFT 与 DFT 的误差 %g > 1e-12", k, delta)
				}
			}

			plan.Inverse(got)
			for j, value := range got {
				// 逆变换不缩放，往返后由调用方除以 N。
				if delta := cmplx.Abs(value/complex(float64(n), 0) - input[j]); delta > 1e-12 {
					t.Fatalf("样本 %d：逆变换往返误差 %g > 1e-12", j, delta)
				}
			}
		})
	}
}
