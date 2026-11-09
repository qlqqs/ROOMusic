// Package src 提供核目录、多相有理数重采样和采样率策略；不负责流格式协商或设置存储。
package src

import (
	"math"
	"math/cmplx"
	"slices"
	"sync"

	"github.com/qlqq/roomusic/backend/internal/dsp/internal/fft"
)

// Phase 指定核的相位类型。
type Phase int

const (
	Linear Phase = iota
	Minimum
)

// Quality 指定核的频响规格。Stage2 仅供有用带宽足够窄的级使用，不能代替普通 PCM 滤波器。
type Quality int

const (
	Smooth Quality = iota
	Precise
	Stage2
)

// Stage 描述一级转换。L/M 为升、降采样因子；New 会约分。
type Stage struct {
	Phase   Phase
	Quality Quality
	L, M    int
}

type ratio struct{ l, m int }

// 目录按升采样方向列出；降采样复用同一个原型，不另造一套截止频率。
var catalog = [...]ratio{
	{2, 1}, {3, 1}, {4, 1}, {6, 1}, {8, 1}, {16, 1}, {32, 1}, {64, 1},
	{3, 2}, {160, 147}, {320, 147}, {640, 147}, {1280, 147},
	{147, 20}, {147, 40}, {147, 80},
}

type kernelKey struct {
	phase   Phase
	quality Quality
	ratio
}
type kernelEntry struct {
	once sync.Once
	h    []float64
}

var kernels sync.Map // kernelKey -> *kernelEntry；条目初始化后只读。

// Kernel 返回直流和为 1 的原型核。支持目录比率及其逆比率；非法配置 panic。
// 同键并发只生成一次，无淘汰。返回副本，调用方可以修改而不会污染缓存。
func Kernel(phase Phase, q Quality, L, M int) []float64 {
	return slices.Clone(cachedKernel(phase, q, L, M))
}

func cachedKernel(phase Phase, q Quality, l, m int) []float64 {
	if !validPhase(phase) || !validQuality(q) || l <= 0 || m <= 0 {
		panic("src: invalid kernel configuration")
	}
	d := gcd(l, m)
	l /= d
	m /= d
	if l < m {
		l, m = m, l
	}
	if !catalogRatio(l, m, q) {
		panic("src: ratio is not in the kernel catalog")
	}
	key := kernelKey{phase, q, ratio{l, m}}
	entry, _ := kernels.LoadOrStore(key, &kernelEntry{})
	e := entry.(*kernelEntry)
	e.once.Do(func() {
		if phase == Minimum {
			e.h = minimumPhase(cachedKernel(Linear, q, l, m))
			return
		}
		if q == Stage2 {
			half := stage2Half[l]
			e.h = make([]float64, 2*len(half)-1)
			center := len(half) - 1
			for i, v := range half {
				e.h[center+i] = v
				e.h[center-i] = v
			}
			return
		}
		e.h = kaiserKernel(q, l, m)
	})
	return e.h
}

func validPhase(p Phase) bool     { return p == Linear || p == Minimum }
func validQuality(q Quality) bool { return q == Smooth || q == Precise || q == Stage2 }

func catalogRatio(l, m int, q Quality) bool {
	if l < m {
		l, m = m, l
	}
	if q == Stage2 {
		_, ok := stage2Half[l]
		return m == 1 && ok
	}
	for _, r := range catalog {
		if r.l == l && r.m == m {
			return true
		}
	}
	return false
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func kaiserKernel(q Quality, l, m int) []float64 {
	taps, beta, cutoff := 20, 20.0, 1.0
	if q == Precise {
		taps, beta, cutoff = 166, 18.68, 1/1.05
	} else if l == 147 {
		cutoff = 0.91875 * 20 / float64(m)
	}
	h := make([]float64, l*taps+1)
	center := len(h) / 2
	denom := besselI0(beta)
	for k := 0; k <= center; k++ {
		u := float64(k-center) / float64(center)
		v := cutoff * float64(k-center) / float64(l)
		sinc := 1.0
		if v != 0 {
			if v == math.Trunc(v) {
				sinc = 0
			} else {
				sinc = math.Sin(math.Pi*v) / (math.Pi * v)
			}
		}
		value := sinc * besselI0(beta*math.Sqrt(max(0, 1-u*u))) / denom
		h[k], h[len(h)-1-k] = value, value
	}
	normalize(h)
	if q == Smooth && l == 2 && m == 1 {
		// 半带结构精确保持中心 1/2 与偶数偏移零；仅重标定非中心抽头的直流和。
		h[center] = 0
		scale := 0.5 / sum(h)
		for i := range h {
			h[i] *= scale
		}
		h[center] = 0.5
	}
	return h
}

// 本任务 β≤20，I0 的正项级数稳定且不需要渐近近似。
func besselI0(x float64) float64 {
	term, total := 1.0, 1.0
	for k := 1; ; k++ {
		term *= x * x / (4 * float64(k*k))
		next := total + term
		if next == total {
			return total
		}
		total = next
	}
}

func sum(h []float64) float64 {
	total, correction := 0.0, 0.0
	for _, v := range h {
		y := v - correction
		next := total + y
		correction = (next - total) - y
		total = next
	}
	return total
}
func normalize(h []float64) {
	scale := 1 / sum(h)
	for i := range h {
		h[i] *= scale
	}
}

func minimumPhase(h []float64) []float64 {
	n := 1 << 21
	for n < 8*len(h) {
		n *= 2
	}
	p := fft.New(n)
	work := make([]complex128, n)
	for i, v := range h {
		work[i] = complex(v, 0)
	}
	p.Forward(work)
	peak := 0.0
	for _, v := range work {
		peak = max(peak, cmplx.Abs(v))
	}
	for i, v := range work {
		work[i] = complex(math.Log(max(cmplx.Abs(v), 1e-14*peak)), 0)
	}
	p.Inverse(work)
	// FFT 的逆变换不缩放；这里的 1/N 同时应用到倒谱与因果提升窗。
	inv := 1 / float64(n)
	work[0] = complex(real(work[0])*inv, 0)
	for i := 1; i < n/2; i++ {
		work[i] = complex(2*real(work[i])*inv, 0)
	}
	work[n/2] = complex(real(work[n/2])*inv, 0)
	clear(work[n/2+1:])
	p.Forward(work)
	for i, v := range work {
		work[i] = cmplx.Exp(v)
	}
	p.Inverse(work)
	result := make([]float64, len(h))
	for i := range result {
		result[i] = real(work[i]) * inv
	}
	normalize(result)
	return result
}
