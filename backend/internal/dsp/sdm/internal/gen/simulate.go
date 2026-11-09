package main

import "math"

// 生成器按延迟输入结构仿真；幅度指生产路径缩放后的内部输入 x。
// 为测量超出实际 ±0.5 输入范围的稳定余量，仿真不钳位，也不执行失稳复位。
// 展开固定的 5/7 阶算式以降低优化器数十亿次样本迭代的成本。
func (b *problem) stable(a vector, amplitude float64) bool {
	var s vector
	xp, yp := 0.0, 1.0
	if b.n == 5 {
		for _, sample := range b.sine {
			x := amplitude * sample
			d0 := s[0] + xp - yp
			d1 := s[1] + s[0] - b.g[1]*s[2]
			d2 := s[2] + s[1]
			d3 := s[3] + s[2] - b.g[3]*s[4]
			d4 := s[4] + s[3]
			v := x + a[0]*d0 + a[1]*d1 + a[2]*d2 + a[3]*d3 + a[4]*d4
			yp = -1
			if v >= 0 {
				yp = 1
			}
			xp = x
			if !(math.Abs(d0) <= 1e8 && math.Abs(d1) <= 1e8 && math.Abs(d2) <= 1e8 && math.Abs(d3) <= 1e8 && math.Abs(d4) <= 1e8) {
				return false
			}
			s[0], s[1], s[2], s[3], s[4] = d0, d1, d2, d3, d4
		}
	} else {
		for _, sample := range b.sine {
			x := amplitude * sample
			d0 := s[0] + xp - yp
			d1 := s[1] + s[0] - b.g[1]*s[2]
			d2 := s[2] + s[1]
			d3 := s[3] + s[2] - b.g[3]*s[4]
			d4 := s[4] + s[3]
			d5 := s[5] + s[4] - b.g[5]*s[6]
			d6 := s[6] + s[5]
			v := x + a[0]*d0 + a[1]*d1 + a[2]*d2 + a[3]*d3 + a[4]*d4 + a[5]*d5 + a[6]*d6
			yp = -1
			if v >= 0 {
				yp = 1
			}
			xp = x
			if !(math.Abs(d0) <= 1e8 && math.Abs(d1) <= 1e8 && math.Abs(d2) <= 1e8 && math.Abs(d3) <= 1e8 && math.Abs(d4) <= 1e8 && math.Abs(d5) <= 1e8 && math.Abs(d6) <= 1e8) {
				return false
			}
			s[0], s[1], s[2], s[3], s[4], s[5], s[6] = d0, d1, d2, d3, d4, d5, d6
		}
	}
	return true
}

func (b *problem) stability(a vector, last int) int {
	for level := 45; level <= last; level++ {
		if !b.stable(a, float64(level)/100) {
			return level - 1
		}
	}
	return last
}
