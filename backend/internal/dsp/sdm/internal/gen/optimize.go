package main

import (
	"math"
	"sort"
)

type vertex struct {
	x     vector
	value float64
}

// 确定性的 Nelder–Mead；初始单纯形和停机阈值对所有组相同。
func minimize(start poles, cost func(poles) float64, iterations int, adaptive bool) poles {
	n := start.n
	simplex := make([]vertex, n+1)
	evaluate := func(x vector) vertex { return vertex{x, cost(poles{n, x})} }
	simplex[0] = evaluate(start.x)
	for i := 0; i < n; i++ {
		x := start.x
		x[i] *= 1.05
		if x[i] == 0 {
			x[i] = 0.00025
		}
		simplex[i+1] = evaluate(x)
	}
	reflect, expand, contract, shrink := 1.0, 2.0, 0.5, 0.5
	xtol, ftol := 1e-11, 1e-7
	if adaptive {
		expand = 1 + 2/float64(n)
		contract = 0.75 - 1/(2*float64(n))
		shrink = 1 - 1/float64(n)
		xtol = 1e-10
		ftol = 1e-4
	}
	order := func() { sort.SliceStable(simplex, func(i, j int) bool { return simplex[i].value < simplex[j].value }) }
	order()
	for iter := 0; iter < iterations; iter++ {
		dx, df := 0.0, 0.0
		for i := 1; i <= n; i++ {
			df = math.Max(df, math.Abs(simplex[i].value-simplex[0].value))
			for j := 0; j < n; j++ {
				dx = math.Max(dx, math.Abs(simplex[i].x[j]-simplex[0].x[j]))
			}
		}
		if dx <= xtol && df <= ftol {
			break
		}
		var centroid vector
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				centroid[j] += simplex[i].x[j] / float64(n)
			}
		}
		move := func(scale float64) vertex {
			x := centroid
			for j := 0; j < n; j++ {
				x[j] += scale * (centroid[j] - simplex[n].x[j])
			}
			return evaluate(x)
		}
		r := move(reflect)
		if r.value < simplex[0].value {
			e := move(reflect * expand)
			if e.value < r.value {
				simplex[n] = e
			} else {
				simplex[n] = r
			}
		} else if r.value < simplex[n-1].value {
			simplex[n] = r
		} else {
			accepted := false
			if r.value < simplex[n].value {
				c := move(reflect * contract)
				if c.value <= r.value {
					simplex[n] = c
					accepted = true
				}
			} else {
				c := move(-contract)
				if c.value < simplex[n].value {
					simplex[n] = c
					accepted = true
				}
			}
			if !accepted {
				for i := 1; i <= n; i++ {
					x := simplex[0].x
					for j := 0; j < n; j++ {
						x[j] += shrink * (simplex[i].x[j] - simplex[0].x[j])
					}
					simplex[i] = evaluate(x)
				}
			}
		}
		order()
	}
	return poles{n, simplex[0].x}
}

type constraint struct{ h, l float64 }

func (b *problem) optimize(start poles, c constraint) poles {
	cost := func(p poles) float64 {
		if !p.valid() {
			return 1e9
		}
		noise, peak := b.spectrum(p)
		norm := impulseNorm(b.n, b.g, p)
		hp, lp := math.Max(0, peak-c.h), math.Max(0, norm-c.l)
		return noise + 1e5*(hp*hp+lp*lp)
	}
	p := start
	for round := 0; round < 3; round++ {
		p = minimize(p, cost, 6000, false)
	}
	return p
}
