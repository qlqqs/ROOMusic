package dsdio

import (
	"encoding/binary"
	"fmt"
	"io"
	"math/bits"
)

func probeDSF(reader io.ReaderAt, size int64) (Layout, error) {
	var header [92]byte
	if size < int64(len(header)) {
		return Layout{}, fmt.Errorf("truncated DSF header")
	}
	if _, err := reader.ReadAt(header[:], 0); err != nil {
		return Layout{}, fmt.Errorf("read DSF header: %w", err)
	}
	le := binary.LittleEndian
	if le.Uint64(header[4:12]) != 28 || string(header[28:32]) != "fmt " || le.Uint64(header[32:40]) != 52 || string(header[80:84]) != "data" {
		return Layout{}, fmt.Errorf("invalid DSF chunk headers")
	}
	if le.Uint64(header[12:20]) < uint64(len(header)) {
		return Layout{}, fmt.Errorf("invalid DSF file size")
	}
	if le.Uint32(header[40:44]) != 1 || le.Uint32(header[44:48]) != 0 {
		return Layout{}, fmt.Errorf("unsupported DSF format")
	}
	layout := Layout{
		Container: DSF, DataStart: 92, ContainerEnd: size,
		Channels: int(le.Uint32(header[52:56])), Rate: int(le.Uint32(header[56:60])),
		Samples: le.Uint64(header[64:72]), DSFBlockSize: int(le.Uint32(header[72:76])),
		MetadataOffset: le.Uint64(header[20:28]),
	}
	bitOrder := le.Uint32(header[60:64])
	if bitOrder != 1 && bitOrder != 8 {
		return Layout{}, fmt.Errorf("invalid DSF bit order")
	}
	layout.BitOrder = BitOrder(bitOrder)
	if layout.Channels < 1 || layout.Channels > 32 {
		return Layout{}, fmt.Errorf("invalid DSF channel count")
	}
	channelType := le.Uint32(header[48:52])
	masks := [...]uint32{0, 0x4, 0x3, 0x7, 0x33, 0xf, 0x37, 0x3f}
	if channelType < uint32(len(masks)) {
		layout.Mask = masks[channelType]
		if layout.Mask != 0 && bits.OnesCount32(layout.Mask) != layout.Channels {
			return Layout{}, fmt.Errorf("DSF channel type disagrees with count")
		}
	}
	dataSize := le.Uint64(header[84:92])
	if dataSize < 12 {
		return Layout{}, fmt.Errorf("invalid DSF data chunk size")
	}
	// data 长度超出真实文件时只截到 EOF；文件头的总长不能扩大可读范围。
	payload := min(dataSize-12, uint64(size-layout.DataStart))
	layout.DataEnd = layout.DataStart + int64(payload)
	layout.Samples = min(layout.Samples, payloadSamples(int64(payload)/int64(layout.Channels)))
	if err := layout.validate(); err != nil {
		return Layout{}, err
	}
	return layout, nil
}
