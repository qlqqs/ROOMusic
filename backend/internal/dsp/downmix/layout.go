// Package downmix 提供按扬声器位置构建的多声道降混矩阵。
package downmix

import (
	"math/bits"
	"strconv"
)

// Mask 使用 WAVEFORMATEXTENSIBLE 的 32 位扬声器掩码；置位从低到高对应流内声道顺序。
// 未知布局为 0，默认布局由装配层提供。
type Mask uint32

// 扬声器位置；更高位的声道保留在输入顺序中，但不参与降混。
const (
	FL Mask = 1 << iota
	FR
	FC
	LFE
	BL
	BR
	FLC
	FRC
	BC
	SL
	SR
)

// 矩阵支持的目标布局；5.1 使用侧环绕 SL/SR。
const (
	Stereo     Mask = FL | FR
	Surround51 Mask = FL | FR | FC | LFE | SL | SR
)

// Channels 返回布局中置位的声道数，未知布局返回 0。
func (m Mask) Channels() int { return bits.OnesCount32(uint32(m)) }

// String 返回用于信号路径的布局显示名，不识别的布局只显示声道数。
func (m Mask) String() string {
	switch m {
	case 0x4:
		return "1.0"
	case Stereo:
		return "2.0"
	case 0x7:
		return "3.0"
	case 0x107, 0x33, 0x603:
		return "4.0"
	case 0x607, 0x37:
		return "5.0"
	case Surround51, 0x3f:
		return "5.1"
	case 0x70f:
		return "6.1"
	case 0x637:
		return "7.0"
	case 0x63f:
		return "7.1"
	default:
		return strconv.Itoa(m.Channels()) + " 声道"
	}
}

// Target 返回需要降到的布局；关闭、声道够用或输出不足双声道时不降混。
// 未知源布局不构建矩阵，由装配层决定最终声道选择。
func Target(src Mask, outMaxChannels int, enabled bool) (dst Mask, ok bool) {
	channels := src.Channels()
	if !enabled || outMaxChannels < 2 || channels <= outMaxChannels {
		return 0, false
	}
	if outMaxChannels >= 6 && channels > 6 {
		return Surround51, true
	}
	return Stereo, true
}
