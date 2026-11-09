package dither

import "math"

// Quantizer 按帧内声道顺序共享一个 LFSR，不支持并发调用。
type Quantizer struct {
	bits  int
	seed  uint32
	lfsr  LFSR
	scale float64
}

// NewQuantizer 的位深只能为 16 或 24；非法位深表示调用方装配错误。
func NewQuantizer(bits int, seed uint32) *Quantizer {
	if bits != 16 && bits != 24 {
		panic("dither: 目标位深必须为 16 或 24")
	}
	lfsr := *NewLFSR(seed)
	return &Quantizer{bits: bits, seed: lfsr.state, lfsr: lfsr, scale: math.Ldexp(1, bits-1)}
}

// Quantize 将前 frames 帧追加为交错小端字节，返回字节和钳位样本数。
// src 必须是有限样本；直通时调用方保证样本可由目标位深精确表示。
// dst 容量足够时不分配；直通既不取整、钳位，也不推进 LFSR。
func (q *Quantizer) Quantize(dst []byte, src [][]float64, frames int, passthrough bool) ([]byte, int) {
	if frames < 0 {
		panic("dither: 帧数不能为负")
	}
	for _, channel := range src {
		if len(channel) < frames {
			panic("dither: 输入声道长度不足")
		}
	}
	bytesPerSample := q.bits / 8
	offset := len(dst)
	size := offset + frames*len(src)*bytesPerSample
	if size > cap(dst) {
		grown := make([]byte, size)
		copy(grown, dst)
		dst = grown
	} else {
		dst = dst[:size]
	}
	clipped := 0
	for frame := 0; frame < frames; frame++ {
		for _, channel := range src {
			v := channel[frame] * q.scale
			if !passthrough {
				v = math.Floor(v + q.lfsr.Triangular() + 0.5)
				if v < -q.scale {
					v = -q.scale
					clipped++
				} else if v > q.scale-1 {
					v = q.scale - 1
					clipped++
				}
			}
			if q.bits == 16 {
				pack16(dst[offset:], int32(v))
			} else {
				pack24(dst[offset:], int32(v))
			}
			offset += bytesPerSample
		}
	}
	return dst, clipped
}

func (q *Quantizer) Reset() { q.lfsr.state = q.seed }

func pack16(dst []byte, value int32) {
	dst[0] = byte(value)
	dst[1] = byte(value >> 8)
}

func pack24(dst []byte, value int32) {
	dst[0] = byte(value)
	dst[1] = byte(value >> 8)
	dst[2] = byte(value >> 16)
}
