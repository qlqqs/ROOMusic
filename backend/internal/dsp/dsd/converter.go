// Package dsd 把每声道 MSB 先的 DSD 字节流以 8:1 抽取为浮点 PCM。
package dsd

import (
	"fmt"
	"math"
	"math/bits"
)

//go:generate go run ./internal/gen -out fir_gen.go

// Converter 持有每声道的 FIR 历史和输出缓冲，不可并发调用。
// 不负责容器、后置低通、变率、时长裁剪或延迟补偿。
type Converter struct {
	history  [][16]byte
	position int
	gain     float64
	out      [][]float64
}

// New 创建 1–32 声道转换器；默认增益由调用方明确传入 6 dB。
func New(channels int, gainDB float64) (*Converter, error) {
	if channels < 1 || channels > 32 {
		return nil, fmt.Errorf("dsd: 声道数必须在 1–32 之间")
	}
	gain := math.Pow(10, gainDB/20)
	if math.IsNaN(gainDB) || math.IsInf(gainDB, 0) || math.IsInf(gain, 0) || gain == 0 {
		return nil, fmt.Errorf("dsd: 增益必须有限且可表示")
	}
	c := &Converter{history: make([][16]byte, channels), gain: gain, out: make([][]float64, channels)}
	c.Reset()
	return c, nil
}

// Latency 返回四舍五入后的输出帧群延迟；精确延迟为 47.5/8 帧。
func (c *Converter) Latency() int { return 6 }

// Reset 恢复无限重复 0x69 的静音历史；旧半边保持已经位反转的存储状态。
// 增益和已分配的输出容量保留，之前借出的输出不再有效。
func (c *Converter) Reset() {
	c.position = 0
	for ch := range c.history {
		for age := 0; age < 16; age++ {
			v := byte(0x69)
			if age >= 6 {
				v = reverse[v]
			}
			c.history[ch][(15-age)&15] = v
		}
		c.out[ch] = c.out[ch][:0]
	}
}

// Process 每声道输入一字节输出一个 PCM 样本，不改输入。
// 输入声道数必须与 New 一致且各声道等长；违反此内部调用合同会 panic。
// 空块不推进状态。返回切片归 Converter 所有，仅在下次 Process/Reset 前有效。
func (c *Converter) Process(in [][]byte) [][]float64 {
	if len(in) != len(c.history) {
		panic("dsd: 输入声道数不符")
	}
	frames := len(in[0])
	for _, channel := range in {
		if len(channel) != frames {
			panic("dsd: 输入声道长度不等")
		}
	}
	for ch, input := range in {
		if cap(c.out[ch]) < frames {
			c.out[ch] = make([]float64, frames)
		} else {
			c.out[ch] = c.out[ch][:frames]
		}
		history := &c.history[ch]
		position := c.position
		for i, v := range input {
			history[position] = v
			older := (position - 6) & 15
			history[older] = reverse[history[older]]
			// 新半边从 LSB（最近比特）向前积累；旧半边复用对称核。
			y := tables[0][history[position]]
			for age := 1; age < 6; age++ {
				y += tables[age][history[(position-age)&15]]
			}
			for age := 6; age < 12; age++ {
				y += tables[11-age][history[(position-age)&15]]
			}
			c.out[ch][i] = y * c.gain
			position = (position + 1) & 15
		}
	}
	c.position = (c.position + frames) & 15
	return c.out
}

var reverse = func() [256]byte {
	var table [256]byte
	for b := range table {
		table[b] = bits.Reverse8(byte(b))
	}
	return table
}()

var tables = func() [6][256]float64 {
	var table [6][256]float64
	for group := range table {
		for b := range table[group] {
			for bit := 0; bit < 8; bit++ {
				sign := -1.0
				if b&(1<<bit) != 0 {
					sign = 1
				}
				table[group][b] += sign * firHalf[8*group+bit]
			}
		}
	}
	return table
}()
