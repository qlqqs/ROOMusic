// 生成器按任务设计 §3.2 的频带目标自主求解，不读取外部系数。
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"math"
	"os"
)

const (
	halfTaps   = 48
	gridSize   = 16384
	sampleRate = 2822400.0
)

func main() {
	out := flag.String("out", "fir_gen.go", "生成的 Go 文件")
	flag.Parse()
	h, err := design()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	m := measure(h)
	if m.ripple20 > 0.0002 || m.stop176 < 56 || m.stop328 < 167 {
		fmt.Fprintf(os.Stderr, "FIR 规格未达标：%+v\n", m)
		os.Exit(1)
	}
	var b bytes.Buffer
	fmt.Fprintln(&b, "// 由 go generate 自主生成；请勿手工编辑。")
	fmt.Fprintln(&b, "// 方法与频带见任务设计 §3.2；标准库 Householder QR，无外部系数输入。")
	fmt.Fprintf(&b, "// 2^18 等分的 [0, Fs/2] 网格，Fs=2822400 Hz：\n")
	fmt.Fprintf(&b, "// 0–20 kHz 波动 %.9f dB；0–24 kHz 波动 %.9f dB。\n", m.ripple20, m.ripple24)
	fmt.Fprintf(&b, "// ≥176.4 kHz 衰减 %.6f dB；≥328.8 kHz 衰减 %.6f dB；−3 dB 点 %.3f Hz。\n", m.stop176, m.stop328, m.cutoff)
	fmt.Fprintln(&b, "package dsd\n\n// firHalf 保存 h[0:48]；另半边由 h[k] = h[95-k] 得到。\nvar firHalf = [48]float64{")
	for _, v := range h {
		fmt.Fprintf(&b, "\t%.17g,\n", v)
	}
	fmt.Fprintln(&b, "}")
	source, err := format.Source(b.Bytes())
	if err == nil {
		err = os.WriteFile(*out, source, 0644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("ripple20=%.9f dB ripple24=%.9f dB stop176=%.6f dB stop328=%.6f dB cutoff=%.3f Hz\n", m.ripple20, m.ripple24, m.stop176, m.stop328, m.cutoff)
}

// design 最小化 Σ weight(f)·(Σ x[k]cos(ω(k+½))−target(f))²。
// 列主序保存矩阵，跳过零权频点；不用会放大条件数的正规方程。
func design() ([halfTaps]float64, error) {
	var a [halfTaps][]float64
	var b []float64
	for i := 0; i < gridSize; i++ {
		f := sampleRate / 2 * float64(i) / (gridSize - 1)
		target, weight := 0.0, 0.0
		switch {
		case f <= 20000:
			target, weight = 1, 1
		case f < 176400:
			continue
		case f < 328800:
			weight = 1e-4
		default:
			weight = 1e5
		}
		scale := math.Sqrt(weight)
		omega := 2 * math.Pi * f / sampleRate
		for k := range a {
			a[k] = append(a[k], scale*math.Cos(omega*(float64(k)+0.5)))
		}
		b = append(b, target*scale)
	}
	x, err := solveQR(a, b)
	if err != nil {
		return [halfTaps]float64{}, err
	}
	sum := 0.0
	for _, v := range x {
		sum += v
	}
	var h [halfTaps]float64
	for k, v := range x {
		h[halfTaps-1-k] = v / (2 * sum)
	}
	return h, nil
}

// solveQR 原位施加 Householder 反射，同时更新右端；只回代上三角部分。
func solveQR(a [halfTaps][]float64, b []float64) ([halfTaps]float64, error) {
	for k := range a {
		column := a[k]
		norm := 0.0
		for _, v := range column[k:] {
			norm = math.Hypot(norm, v)
		}
		if norm == 0 {
			return [halfTaps]float64{}, fmt.Errorf("FIR 设计矩阵秩不足：列 %d", k)
		}
		alpha := -math.Copysign(norm, column[k])
		leading := column[k] - alpha
		// 反射向量首项为 1，尾项暂存于本列下三角。
		for i := k + 1; i < len(column); i++ {
			column[i] /= leading
		}
		tau := (alpha - column[k]) / alpha
		column[k] = alpha
		apply := func(dst []float64) {
			dot := dst[k]
			for i := k + 1; i < len(column); i++ {
				dot += column[i] * dst[i]
			}
			dot *= tau
			dst[k] -= dot
			for i := k + 1; i < len(column); i++ {
				dst[i] -= dot * column[i]
			}
		}
		for j := k + 1; j < halfTaps; j++ {
			apply(a[j])
		}
		apply(b)
	}
	var x [halfTaps]float64
	for k := halfTaps - 1; k >= 0; k-- {
		v := b[k]
		for j := k + 1; j < halfTaps; j++ {
			v -= a[j][k] * x[j]
		}
		x[k] = v / a[k][k]
	}
	return x, nil
}

type metrics struct {
	ripple20, ripple24, stop176, stop328, cutoff float64
}

func measure(h [halfTaps]float64) metrics {
	const points = 1 << 18
	lo20, hi20, lo24, hi24 := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
	max176, max328 := 0.0, 0.0
	cutoff := 0.0
	for i := 0; i <= points; i++ {
		f := sampleRate / 2 * float64(i) / points
		omega := 2 * math.Pi * f / sampleRate
		response := 0.0
		for k, v := range h {
			response += 2 * v * math.Cos(omega*(47.5-float64(k)))
		}
		amplitude := math.Abs(response)
		db := 20 * math.Log10(amplitude)
		if f <= 20000 {
			lo20, hi20 = math.Min(lo20, db), math.Max(hi20, db)
		}
		if f <= 24000 {
			lo24, hi24 = math.Min(lo24, db), math.Max(hi24, db)
		}
		if f >= 176400 {
			max176 = math.Max(max176, amplitude)
		}
		if f >= 328800 {
			max328 = math.Max(max328, amplitude)
		}
		if cutoff == 0 && db <= -3 {
			cutoff = f
		}
	}
	return metrics{hi20 - lo20, hi24 - lo24, -20 * math.Log10(max176), -20 * math.Log10(max328), cutoff}
}
