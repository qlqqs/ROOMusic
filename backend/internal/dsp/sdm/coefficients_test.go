package sdm

import (
	"fmt"
	"math"
	"math/big"
	"math/cmplx"
	"testing"
)

func TestCoefficientNTFProperties(t *testing.T) {
	algorithms := []struct {
		name  string
		alg   Algorithm
		order int
		nodes []float64
	}{
		{"Order5", Order5, 5, []float64{0.5384693101056831, 0.9061798459386640}},
		{"Order7", Order7, 7, []float64{0.4058451513773972, 0.7415311855993945, 0.9491079123427585}},
		{"Clans5", Clans5, 5, []float64{0.5384693101056831, 0.9061798459386640}},
		{"Clans7", Clans7, 7, []float64{0.4058451513773972, 0.7415311855993945, 0.9491079123427585}},
	}
	for _, algorithm := range algorithms {
		for _, multiple := range []int{64, 128, 256, 512} {
			t.Run(fmt.Sprintf("%s/DSD%d", algorithm.name, multiple), func(t *testing.T) {
				m, err := New(algorithm.alg, multiple*44100, 1)
				if err != nil {
					t.Fatal(err)
				}
				c := m.coeff
				if c.order != algorithm.order {
					t.Fatalf("order = %d, want %d", c.order, algorithm.order)
				}
				for i := range c.a {
					if math.IsNaN(c.a[i]) || math.IsInf(c.a[i], 0) || math.IsNaN(c.g[i]) || math.IsInf(c.g[i], 0) {
						t.Fatalf("non-finite coefficient at index %d", i)
					}
					if (i%2 == 0 || i >= c.order) && c.g[i] != 0 {
						t.Fatalf("g[%d] = %g, want zero outside resonator pairs", i, c.g[i])
					}
				}

				// A 的直流特征值为 1；每个谐振器块的特征值为 1±j√g。
				// 因此零点的赫兹位置取辐角，而不是把 √g 当作数字角频率。
				for k, node := range algorithm.nodes {
					g := c.g[2*k+1]
					if !(g > 0) {
						t.Fatalf("resonator g[%d] = %g, want positive", 2*k+1, g)
					}
					frequency := math.Atan(math.Sqrt(g)) * float64(multiple*44100) / (2 * math.Pi)
					want := node * 22050
					if math.Abs(frequency/want-1) > 0.001 {
						t.Errorf("zero %d = %.12g Hz, want %.12g Hz ±0.1%%", k+1, frequency, want)
					}
				}

				// 延迟输入的线性化反馈矩阵为 F=A−B·aᵀ，NTF=det(zI−A)/det(zI−F)。
				// 按 §6.1 对 det((0.995+1e−6)·zI−F) 作精确有理数 Schur 递降，预留拟合舍入余量。
				if degree := coefficientTestSchurFailure(coefficientTestPolePolynomial(c)); degree != 0 {
					t.Errorf("poles are not all strictly inside |z| < 0.995 + 1e-6 (Schur degree %d)", degree)
				}
				if algorithm.alg == Order5 || algorithm.alg == Order7 {
					peak := coefficientTestPeakGain(c)
					if math.IsNaN(peak) || math.IsInf(peak, 0) || math.Abs(peak-1.5) > 0.01 {
						t.Errorf("NTF H-infinity = %.12g, want 1.5 ±0.01", peak)
					}
				}
			})
		}
	}
}

// coefficientTestGain 在 w=z−1 上递推行列式，避免高倍率低频处展开 z 多项式的消减误差。
// 首行反馈与单位次对角线给出 P₀=1、P₁=w+a₀、Pₙ=wPₙ₋₁+gₙ₋₂Pₙ₋₂+aₙ₋₁；
// 去掉所有 a 即为分子 Q。这直接来自生产递推，不使用生成器的极点或指标。
func coefficientTestGain(c coefficients, theta float64) float64 {
	halfSin := math.Sin(theta / 2)
	w := complex(-2*halfSin*halfSin, math.Sin(theta))
	p0, p1 := complex(1, 0), w+complex(c.a[0], 0)
	q0, q1 := complex(1, 0), w
	for n := 2; n <= c.order; n++ {
		g := complex(c.g[n-2], 0)
		p0, p1 = p1, w*p1+g*p0+complex(c.a[n-1], 0)
		q0, q1 = q1, w*q1+g*q0
	}
	return cmplx.Abs(q1 / p1)
}

func coefficientTestPeakGain(c coefficients) float64 {
	const points = 6000
	var gains [points + 1]float64
	peak := 0.0
	for i := range gains {
		gains[i] = coefficientTestGain(c, math.Pi*float64(i)/points)
		if math.IsNaN(gains[i]) || math.IsInf(gains[i], 0) {
			return gains[i]
		}
		peak = math.Max(peak, gains[i])
	}
	// 在网格局部峰两侧继续黄金分割搜索，避免只在离散频点上报告最大值。
	const ratio = 0.6180339887498949
	for i := 1; i < points; i++ {
		if gains[i] <= gains[i-1] || gains[i] < gains[i+1] {
			continue
		}
		lo, hi := math.Pi*float64(i-1)/points, math.Pi*float64(i+1)/points
		x1, x2 := hi-ratio*(hi-lo), lo+ratio*(hi-lo)
		f1, f2 := coefficientTestGain(c, x1), coefficientTestGain(c, x2)
		for iteration := 0; iteration < 60; iteration++ {
			if f1 < f2 {
				lo, x1, f1 = x1, x2, f2
				x2 = lo + ratio*(hi-lo)
				f2 = coefficientTestGain(c, x2)
			} else {
				hi, x2, f2 = x2, x1, f1
				x1 = hi - ratio*(hi-lo)
				f1 = coefficientTestGain(c, x1)
			}
		}
		peak = math.Max(peak, math.Max(f1, f2))
	}
	return peak
}

// coefficientTestPolePolynomial 按升幂生成 det(r·zI−F)，r=0.995+1e−6。
// SetFloat64 精确保留入库浮点数的位值；后续全用有理数，避免临界极点求根的病态舍入。
func coefficientTestPolePolynomial(c coefficients) []*big.Rat {
	radius := big.NewRat(995001, 1000000)
	previous := []*big.Rat{big.NewRat(1, 1)}
	current := []*big.Rat{
		new(big.Rat).Sub(new(big.Rat).SetFloat64(c.a[0]), big.NewRat(1, 1)),
		radius,
	}
	for n := 2; n <= c.order; n++ {
		next := make([]*big.Rat, n+1)
		for i := range next {
			next[i] = new(big.Rat)
		}
		for i, v := range current {
			next[i].Sub(next[i], v)
			next[i+1].Add(next[i+1], new(big.Rat).Mul(radius, v))
		}
		g := new(big.Rat).SetFloat64(c.g[n-2])
		for i, v := range previous {
			next[i].Add(next[i], new(big.Rat).Mul(g, v))
		}
		next[0].Add(next[0], new(big.Rat).SetFloat64(c.a[n-1]))
		previous, current = current, next
	}
	return current
}

// coefficientTestSchurFailure 返回首个未满足严格 Schur 条件的次数，0 表示全部根在单位圆内。
// 实系数 P 的必要充分递降条件为 |常数项|<|首项|，随后以 (首项·P−常数项·反序P)/z 降阶。
func coefficientTestSchurFailure(p []*big.Rat) int {
	for degree := len(p) - 1; degree > 0; degree-- {
		constant, leading := p[0], p[degree]
		if new(big.Rat).Abs(constant).Cmp(new(big.Rat).Abs(leading)) >= 0 {
			return degree
		}
		next := make([]*big.Rat, degree)
		for i := 1; i <= degree; i++ {
			left := new(big.Rat).Mul(leading, p[i])
			right := new(big.Rat).Mul(constant, p[degree-i])
			next[i-1] = left.Sub(left, right)
		}
		p = next
	}
	return 0
}
