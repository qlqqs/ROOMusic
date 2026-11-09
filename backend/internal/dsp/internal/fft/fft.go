// Package fft 提供双精度、自然频序的基 2 FFT，不依赖其他项目包。
// 正变换采用 exp(-2πijk/N)，逆变换采用 exp(+2πijk/N)。
// 两个方向都不缩放：往返得到 N 倍输入，调用方负责乘 1/N。
package fft

import (
	"math"
	"math/cmplx"
)

// Plan 是固定长度的只读旋转因子表，可由多个协程共享。
// 方法不保存工作缓冲；不同调用不得并发写同一个输入或输出切片。
type Plan struct {
	n     int
	roots []complex128
}

// New 创建长度为 n 的计划。n 必须是正的 2 的幂，否则 panic。
// 长度属于内部算法配置，调用方须在接收外部输入的边界完成校验。
func New(n int) *Plan {
	if n < 1 || n&(n-1) != 0 {
		panic("fft: length must be a positive power of two")
	}
	p := &Plan{n: n, roots: make([]complex128, n/2)}
	for k := range p.roots {
		s, c := math.Sincos(-2 * math.Pi * float64(k) / float64(n))
		p.roots[k] = complex(c, s)
	}
	return p
}

// Len 返回变换长度。
func (p *Plan) Len() int { return p.n }

// Forward 原地执行 N 点复数正变换，len(x) 必须等于 Len()。
func (p *Plan) Forward(x []complex128) { p.check(len(x)); p.transform(x, false) }

// Inverse 原地执行 N 点复数逆变换，不乘 1/N。
func (p *Plan) Inverse(x []complex128) { p.check(len(x)); p.transform(x, true) }

func (p *Plan) check(n int) {
	if n != p.n {
		panic("fft: mismatched transform length")
	}
}

func (p *Plan) transform(x []complex128, inverse bool) {
	n := len(x)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for j&bit != 0 {
			j ^= bit
			bit >>= 1
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	for size := 2; size <= n; size *= 2 {
		half, stride := size/2, p.n/size
		for base := 0; base < n; base += size {
			for k := 0; k < half; k++ {
				w := p.roots[k*stride]
				if inverse {
					w = cmplx.Conj(w)
				}
				a, b := x[base+k], w*x[base+k+half]
				x[base+k], x[base+k+half] = a+b, a-b
			}
		}
		if size == n {
			break
		}
	}
}

// RealForward 将 N 个实数变换为 N/2+1 个非负频率复数（含 DC 与 Nyquist）。
// 不修改 src；dst 长度必须为 N/2+1。内部只做 N/2 点复数 FFT，
// 每次调用分配 N/2 个复数作临时缓冲。N=1 时输出仅含 DC。
func (p *Plan) RealForward(dst []complex128, src []float64) {
	p.check(len(src))
	if len(dst) != p.n/2+1 {
		panic("fft: mismatched real spectrum length")
	}
	if p.n == 1 {
		dst[0] = complex(src[0], 0)
		return
	}
	m := p.n / 2
	z := make([]complex128, m)
	for j := range z {
		z[j] = complex(src[2*j], src[2*j+1])
	}
	p.transform(z, false)
	dst[0] = complex(real(z[0])+imag(z[0]), 0)
	dst[m] = complex(real(z[0])-imag(z[0]), 0)
	for k := 1; k < m; k++ {
		a, b := z[k], cmplx.Conj(z[m-k])
		dst[k] = 0.5 * (a + b - complex(0, 1)*p.roots[k]*(a-b))
	}
}

// RealInverse 从 N/2+1 个非负频率恢复 N 个实数，不乘 1/N。
// 不修改 src；dst 长度必须为 N。DC/Nyquist 的虚部应为零，且会被忽略。
// 内部利用共轭对称，只做 N/2 点复数逆 FFT；临时缓冲同 RealForward。
func (p *Plan) RealInverse(dst []float64, src []complex128) {
	p.check(len(dst))
	if len(src) != p.n/2+1 {
		panic("fft: mismatched real spectrum length")
	}
	if p.n == 1 {
		dst[0] = real(src[0])
		return
	}
	m := p.n / 2
	z := make([]complex128, m)
	z[0] = complex((real(src[0])+real(src[m]))/2, (real(src[0])-real(src[m]))/2)
	for k := 1; k < m; k++ {
		a, b := src[k], cmplx.Conj(src[m-k])
		z[k] = 0.5 * (a + b + complex(0, 1)*cmplx.Conj(p.roots[k])*(a-b))
	}
	p.transform(z, true)
	for j, v := range z {
		dst[2*j] = 2 * real(v)
		dst[2*j+1] = 2 * imag(v)
	}
}
