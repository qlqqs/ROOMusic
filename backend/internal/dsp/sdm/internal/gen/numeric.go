package main

import (
	"fmt"
	"math"
	"math/cmplx"
	"sort"
)

const maxOrder = 7

type vector [maxOrder]float64

// 每个共轭极点对占半径、角度两项，末项是实极点。
type poles struct {
	n int
	x vector
}

func (p poles) valid() bool {
	for i := 0; i < p.n-1; i += 2 {
		if !(p.x[i] > 0 && p.x[i] < 0.995 && p.x[i+1] > 0 && p.x[i+1] < math.Pi) {
			return false
		}
	}
	return math.Abs(p.x[p.n-1]) < 0.995
}

func (p poles) roots() []complex128 {
	roots := make([]complex128, 0, p.n)
	for i := 0; i < p.n-1; i += 2 {
		z := cmplx.Rect(p.x[i], p.x[i+1])
		roots = append(roots, z, cmplx.Conj(z))
	}
	return append(roots, complex(p.x[p.n-1], 0))
}

func legendre(n int, x float64) (float64, float64) {
	prev, cur := 1.0, x
	for k := 2; k <= n; k++ {
		prev, cur = cur, (float64(2*k-1)*x*cur-float64(k-1)*prev)/float64(k)
	}
	return cur, float64(n) * (x*cur - prev) / (x*x - 1)
}

func resonators(n, rate int) vector {
	roots := make([]float64, 0, n/2)
	for k := 1; k <= n/2; k++ {
		x := math.Cos(math.Pi * (float64(k) - 0.25) / (float64(n) + 0.5))
		for j := 0; j < 30; j++ {
			y, dy := legendre(n, x)
			dx := y / dy
			x -= dx
			if math.Abs(dx) < 1e-15 {
				break
			}
		}
		roots = append(roots, x)
	}
	sort.Float64s(roots)
	var g vector
	for i, r := range roots {
		t := math.Tan(2 * math.Pi * r * 22050 / float64(rate))
		g[2*i+1] = t * t
	}
	return g
}

// 避免在高倍率下用相近多项式相减，Q、D 都按一阶/二阶因子求值。
func numerator(z complex128, n int, g vector) complex128 {
	t := z - 1
	q := t
	for i := 1; i < n-1; i += 2 {
		q *= t*t + complex(g[i], 0)
	}
	return q
}

func denominator(z complex128, p poles) complex128 {
	d := z - complex(p.x[p.n-1], 0)
	for i := 0; i < p.n-1; i += 2 {
		r, t := p.x[i], p.x[i+1]
		d *= z*z - complex(2*r*math.Cos(t), 0)*z + complex(r*r, 0)
	}
	return d
}

type frequencyPoint struct{ z, q complex128 }
type problem struct {
	n, rate   int
	g         vector
	band, out []frequencyPoint
	sine      []float64
}

func newProblem(n, rate int) *problem {
	b := &problem{n: n, rate: rate, g: resonators(n, rate)}
	for _, spec := range []struct {
		count  int
		lo, hi float64
		dst    *[]frequencyPoint
	}{
		{512, 0, 20000, &b.band}, {6000, 20000, float64(rate) / 2, &b.out},
	} {
		for i := 0; i < spec.count; i++ {
			f := spec.lo + (spec.hi-spec.lo)*float64(i)/float64(spec.count-1)
			z := cmplx.Rect(1, 2*math.Pi*f/float64(rate))
			*spec.dst = append(*spec.dst, frequencyPoint{z, numerator(z, n, b.g)})
		}
	}
	b.sine = make([]float64, (1<<19)*(rate/(44100*64)))
	for i := range b.sine {
		b.sine[i] = math.Sin(2 * math.Pi * 1000 * float64(i) / float64(rate))
	}
	return b
}

func (b *problem) spectrum(p poles) (float64, float64) {
	sum, peak := 0.0, 0.0
	for _, point := range b.band {
		h := point.q / denominator(point.z, p)
		sum += real(h)*real(h) + imag(h)*imag(h)
	}
	for _, point := range b.out {
		h := point.q / denominator(point.z, p)
		mag := real(h)*real(h) + imag(h)*imag(h)
		if mag > peak {
			peak = mag
		}
	}
	return 10 * math.Log10(sum/float64(len(b.band))), math.Sqrt(peak)
}

type section struct{ b1, b2, a1, a2, s1, s2 float64 }

func impulse(n int, g vector, p poles, count int) []float64 {
	sections := make([]section, 0, (n+1)/2)
	for i := 0; i < n-1; i += 2 {
		r, t := p.x[i], p.x[i+1]
		sections = append(sections, section{b1: -2, b2: 1 + g[i+1], a1: -2 * r * math.Cos(t), a2: r * r})
	}
	sections = append(sections, section{b1: -1, a1: -p.x[n-1]})
	out := make([]float64, count)
	for k := range out {
		x := 0.0
		if k == 0 {
			x = 1
		}
		for i := range sections {
			s := &sections[i]
			y := x + s.s1
			s.s1 = s.b1*x - s.a1*y + s.s2
			s.s2 = s.b2*x - s.a2*y
			x = y
		}
		out[k] = x
	}
	return out
}

func impulseNorm(n int, g vector, p poles) float64 {
	sum := 0.0
	for _, h := range impulse(n, g, p, 16384) {
		sum += math.Abs(h)
	}
	return sum
}

func (b *problem) butterworth() poles {
	makePoles := func(cutoff float64) poles {
		// 双线性变换的预畸变频率；稳定的左半平面 Butterworth 原型。
		w := math.Tan(math.Pi * cutoff / 2)
		var p poles
		p.n = b.n
		idx := 0
		for k := 0; k < b.n; k++ {
			analog := cmplx.Rect(w, math.Pi*float64(2*k+1+b.n)/float64(2*b.n))
			z := (1 + analog) / (1 - analog)
			if imag(z) > 1e-12 {
				p.x[idx] = cmplx.Abs(z)
				p.x[idx+1] = cmplx.Phase(z)
				idx += 2
			} else if math.Abs(imag(z)) <= 1e-12 {
				p.x[b.n-1] = real(z)
			}
		}
		return p
	}
	lo, hi := 0.001, 0.45
	for i := 0; i < 55; i++ {
		mid := (lo + hi) / 2
		p := makePoles(mid)
		_, peak := b.spectrum(p)
		if peak < 1.5 {
			lo = mid
		} else {
			hi = mid
		}
	}
	return makePoles((lo + hi) / 2)
}

// 稀疏 (zI-A)u=B 的解析解：一个直流积分器，随后是谐振器对。
func resolvent(z complex128, n int, g vector) [maxOrder]complex128 {
	var u [maxOrder]complex128
	t := z - 1
	u[0] = 1 / t
	for i := 1; i < n-1; i += 2 {
		base := u[i-1] / (t*t + complex(g[i], 0))
		u[i] = t * base
		u[i+1] = base
	}
	return u
}

func realize(n int, g vector, p poles) (vector, error) {
	rows := 6 * n
	matrix := make([][]float64, rows)
	rhs := make([]float64, rows)
	for i := 0; i < 3*n; i++ {
		z := cmplx.Rect(1.05, 0.3+2.5*float64(i)/float64(3*n-1))
		u := resolvent(z, n, g)
		target := denominator(z, p)/numerator(z, n, g) - 1
		matrix[i] = make([]float64, n)
		matrix[i+3*n] = make([]float64, n)
		for j := 0; j < n; j++ {
			matrix[i][j] = real(u[j])
			matrix[i+3*n][j] = imag(u[j])
		}
		rhs[i] = real(target)
		rhs[i+3*n] = imag(target)
	}
	return householder(matrix, rhs, n)
}

// Householder QR 不形成法方程，避免把条件数平方。
func householder(a [][]float64, y []float64, n int) (vector, error) {
	rows := len(y)
	for k := 0; k < n; k++ {
		norm := 0.0
		for i := k; i < rows; i++ {
			norm = math.Hypot(norm, a[i][k])
		}
		if norm < 1e-15 {
			return vector{}, fmt.Errorf("QR 第 %d 列秩不足", k)
		}
		alpha := -math.Copysign(norm, a[k][k])
		v := make([]float64, rows-k)
		for i := k; i < rows; i++ {
			v[i-k] = a[i][k]
		}
		v[0] -= alpha
		vv := 0.0
		for _, x := range v {
			vv += x * x
		}
		for j := k; j < n; j++ {
			dot := 0.0
			for i := k; i < rows; i++ {
				dot += v[i-k] * a[i][j]
			}
			dot *= 2 / vv
			for i := k; i < rows; i++ {
				a[i][j] -= dot * v[i-k]
			}
		}
		dot := 0.0
		for i := k; i < rows; i++ {
			dot += v[i-k] * y[i]
		}
		dot *= 2 / vv
		for i := k; i < rows; i++ {
			y[i] -= dot * v[i-k]
		}
	}
	var x vector
	for i := n - 1; i >= 0; i-- {
		v := y[i]
		for j := i + 1; j < n; j++ {
			v -= a[i][j] * x[j]
		}
		x[i] = v / a[i][i]
	}
	return x, nil
}

// 结构冲激与因子化 Q/D 冲激交叉核验；不使用非线性保护来掩盖错误。
func verifyRealization(n int, a, g vector, p poles) (float64, error) {
	want := impulse(n, g, p, 16384)
	var state vector
	prev, maxErr := 0.0, 0.0
	for k, target := range want {
		var d vector
		d[0] = state[0] - g[0]*state[1] - prev
		for j := 1; j < n-1; j++ {
			d[j] = state[j] + state[j-1] - g[j]*state[j+1]
		}
		d[n-1] = state[n-1] + state[n-2]
		y := 0.0
		for j := 0; j < n; j++ {
			y += a[j] * d[j]
		}
		if k == 0 {
			y++
		}
		err := math.Abs(y - target)
		if err > maxErr {
			maxErr = err
		}
		state = d
		prev = y
	}
	if !(maxErr <= 1e-9) {
		return maxErr, fmt.Errorf("冲激最大误差 %.3g 超过 1e-9", maxErr)
	}
	return maxErr, nil
}
