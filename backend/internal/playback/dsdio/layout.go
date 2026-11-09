// Package dsdio 读取 DSF/DFF 容器事实和原始 DSD 字节；不解释标签，不打开或关闭文件。
package dsdio

import (
	"fmt"
	"io"
	"math"
	"math/bits"
)

type Container string

const (
	DSF Container = "dsf"
	DFF Container = "dff"
)

type BitOrder uint8

const (
	LSBFirst BitOrder = 1
	MSBFirst BitOrder = 8
)

// Layout 的范围都是文件绝对字节偏移，右端不包含在内；Samples 是每声道有效比特数。
// Mask 使用 WAVE 扬声器位；已知布局的 Reader 输出按位从低到高排列，未知布局保持文件顺序。
// MetadataOffset 只保留 DSF 的原始指针，标签定位补偿与解释由 parser 完成。
// Layout 应由 Probe 创建，调用方不得修改后再用于同一文件。
type Layout struct {
	Container      Container
	DataStart      int64
	DataEnd        int64
	ContainerEnd   int64
	Rate           int
	Channels       int
	Mask           uint32
	DSFBlockSize   int
	BitOrder       BitOrder
	Samples        uint64
	MetadataOffset uint64

	channelOrder [32]uint8
	ordered      bool
}

// ByteFrames 返回包含末尾不足八比特样本的每声道字节数，不含 DSF 块填充。
func (l Layout) ByteFrames() int64 {
	frames := l.Samples / 8
	if l.Samples%8 != 0 {
		frames++
	}
	return int64(frames)
}

// UnsupportedEncodingError 表示容器已识别，但其音频编码不能作为原始 DSD 读取。
// 上层自行映射业务错误，dsdio 不依赖扫描或标签包。
type UnsupportedEncodingError struct {
	Encoding string
}

func (e *UnsupportedEncodingError) Error() string {
	return fmt.Sprintf("unsupported DSD encoding: %s", e.Encoding)
}

// Probe 仅检查头部与块边界，不读取整个音频负载；size 是已打开来源的实际大小。
func Probe(reader io.ReaderAt, size int64) (Layout, error) {
	if reader == nil || size < 4 {
		return Layout{}, fmt.Errorf("invalid DSD container")
	}
	var signature [4]byte
	if _, err := reader.ReadAt(signature[:], 0); err != nil {
		return Layout{}, fmt.Errorf("read DSD signature: %w", err)
	}
	switch string(signature[:]) {
	case "DSD ":
		return probeDSF(reader, size)
	case "FRM8":
		return probeDFF(reader, size)
	default:
		return Layout{}, fmt.Errorf("invalid DSD container signature")
	}
}

func (l Layout) validate() error {
	if l.Channels < 1 || l.Channels > 32 || l.Rate < 1 || l.Rate > math.MaxInt32 {
		return fmt.Errorf("invalid DSD audio facts")
	}
	if l.DataStart < 0 || l.DataEnd < l.DataStart || l.ContainerEnd < l.DataEnd {
		return fmt.Errorf("invalid DSD payload range")
	}
	if l.ByteFrames() > (l.DataEnd-l.DataStart)/int64(l.Channels) {
		return fmt.Errorf("DSD samples exceed payload")
	}
	if l.Mask != 0 && bits.OnesCount32(l.Mask) != l.Channels {
		return fmt.Errorf("invalid DSD channel mask")
	}
	switch l.Container {
	case DSF:
		if l.DSFBlockSize != 4096 || (l.BitOrder != LSBFirst && l.BitOrder != MSBFirst) {
			return fmt.Errorf("invalid DSF block size or bit order")
		}
	case DFF:
		if l.BitOrder != MSBFirst || l.DSFBlockSize != 0 {
			return fmt.Errorf("invalid DFF byte layout")
		}
	default:
		return fmt.Errorf("invalid DSD container")
	}
	return nil
}

// payloadSamples 避免极大稀疏来源的字节数乘八溢出。
func payloadSamples(bytesPerChannel int64) uint64 {
	if uint64(bytesPerChannel) > math.MaxUint64/8 {
		return math.MaxUint64
	}
	return uint64(bytesPerChannel) * 8
}
