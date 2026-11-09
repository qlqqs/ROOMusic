package dsdio

import (
	"encoding/binary"
	"fmt"
	"io"
	"math/bits"
)

// Chunk 描述 DFF 的一个负载范围；End 不含偶数字节对齐的填充。
type Chunk struct {
	ID         string
	Start, End int64
}

// WalkDFFChunks 遍历已验证父块内的直接子块，不递归、不解释标签。
// parser 用它读取标签，Probe 用同一边界规则读取音频属性。
func WalkDFFChunks(reader io.ReaderAt, start, end int64, visit func(Chunk) error) error {
	if reader == nil || start < 0 || end < start || visit == nil {
		return fmt.Errorf("invalid DFF chunk range")
	}
	for position, count := start, 0; position < end; count++ {
		if count >= 10000 {
			return fmt.Errorf("too many DFF chunks")
		}
		if end-position < 12 {
			return fmt.Errorf("truncated DFF chunk header")
		}
		var header [12]byte
		if _, err := reader.ReadAt(header[:], position); err != nil {
			return fmt.Errorf("read DFF chunk header: %w", err)
		}
		position += 12
		length := binary.BigEndian.Uint64(header[4:12])
		if length > uint64(end-position) {
			return fmt.Errorf("DFF chunk exceeds parent")
		}
		chunkEnd := position + int64(length)
		padding := int64(length & 1)
		if padding > end-chunkEnd {
			return fmt.Errorf("truncated DFF chunk padding")
		}
		if err := visit(Chunk{ID: string(header[:4]), Start: position, End: chunkEnd}); err != nil {
			return err
		}
		position = chunkEnd + padding
	}
	return nil
}

func probeDFF(reader io.ReaderAt, size int64) (Layout, error) {
	var header [16]byte
	if size < int64(len(header)) {
		return Layout{}, fmt.Errorf("truncated DFF header")
	}
	if _, err := reader.ReadAt(header[:], 0); err != nil {
		return Layout{}, fmt.Errorf("read DFF header: %w", err)
	}
	length := binary.BigEndian.Uint64(header[4:12])
	if string(header[12:16]) != "DSD " || length < 4 || length > uint64(size-12) {
		return Layout{}, fmt.Errorf("invalid DFF form")
	}
	layout := Layout{Container: DFF, ContainerEnd: 12 + int64(length), BitOrder: MSBFirst}
	var foundVersion, foundProperties, foundAudio bool
	err := WalkDFFChunks(reader, 16, layout.ContainerEnd, func(chunk Chunk) error {
		switch chunk.ID {
		case "FVER":
			if foundVersion || chunk.End-chunk.Start != 4 {
				return fmt.Errorf("invalid DFF version chunk")
			}
			var version [4]byte
			if _, err := reader.ReadAt(version[:], chunk.Start); err != nil {
				return err
			}
			if version[0] != 1 {
				return fmt.Errorf("unsupported DFF version")
			}
			foundVersion = true
		case "PROP":
			if chunk.End-chunk.Start < 4 {
				return fmt.Errorf("truncated DFF property type")
			}
			var kind [4]byte
			if _, err := reader.ReadAt(kind[:], chunk.Start); err != nil {
				return err
			}
			if string(kind[:]) != "SND " {
				return nil
			}
			if foundProperties {
				return fmt.Errorf("duplicate DFF sound properties")
			}
			if err := readDFFProperties(reader, chunk.Start+4, chunk.End, &layout); err != nil {
				return err
			}
			foundProperties = true
		case "DST ":
			return &UnsupportedEncodingError{Encoding: "DST"}
		case "DSD ":
			if foundAudio {
				return fmt.Errorf("duplicate DFF audio chunk")
			}
			layout.DataStart, layout.DataEnd = chunk.Start, chunk.End
			foundAudio = true
		}
		return nil
	})
	if err != nil {
		return Layout{}, err
	}
	if !foundVersion || !foundProperties || !foundAudio {
		return Layout{}, fmt.Errorf("missing DFF audio chunks")
	}
	if (layout.DataEnd-layout.DataStart)%int64(layout.Channels) != 0 {
		return Layout{}, fmt.Errorf("incomplete DFF byte frame")
	}
	layout.Samples = payloadSamples((layout.DataEnd - layout.DataStart) / int64(layout.Channels))
	if err := layout.validate(); err != nil {
		return Layout{}, err
	}
	return layout, nil
}

func readDFFProperties(reader io.ReaderAt, start, end int64, layout *Layout) error {
	var foundRate, foundChannels, foundCompression bool
	err := WalkDFFChunks(reader, start, end, func(chunk Chunk) error {
		size := chunk.End - chunk.Start
		switch chunk.ID {
		case "FS  ":
			if foundRate || size != 4 {
				return fmt.Errorf("invalid DFF sample rate chunk")
			}
			var data [4]byte
			if _, err := reader.ReadAt(data[:], chunk.Start); err != nil {
				return err
			}
			layout.Rate = int(binary.BigEndian.Uint32(data[:]))
			foundRate = true
		case "CHNL":
			if foundChannels || size < 2 {
				return fmt.Errorf("invalid DFF channels chunk")
			}
			var data [2 + 32*4]byte
			if _, err := reader.ReadAt(data[:2], chunk.Start); err != nil {
				return err
			}
			layout.Channels = int(binary.BigEndian.Uint16(data[:2]))
			if layout.Channels < 1 || layout.Channels > 32 || size != int64(2+layout.Channels*4) {
				return fmt.Errorf("invalid DFF channel count")
			}
			if _, err := reader.ReadAt(data[2:2+layout.Channels*4], chunk.Start+2); err != nil {
				return err
			}
			setDFFChannelOrder(layout, data[2:2+layout.Channels*4])
			foundChannels = true
		case "CMPR":
			if foundCompression || size < 5 {
				return fmt.Errorf("invalid DFF compression chunk")
			}
			var data [5]byte
			if _, err := reader.ReadAt(data[:], chunk.Start); err != nil {
				return err
			}
			if string(data[:4]) == "DST " {
				return &UnsupportedEncodingError{Encoding: "DST"}
			}
			if int64(data[4]) > size-5 {
				return fmt.Errorf("truncated DFF compression name")
			}
			if string(data[:4]) != "DSD " {
				return &UnsupportedEncodingError{Encoding: string(data[:4])}
			}
			foundCompression = true
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !foundRate || !foundChannels || !foundCompression {
		return fmt.Errorf("missing DFF sound properties")
	}
	return nil
}

func setDFFChannelOrder(layout *Layout, ids []byte) {
	var speakers [32]uint32
	var mask uint32
	for ch := 0; ch < layout.Channels; ch++ {
		var speaker uint32
		switch string(ids[ch*4 : ch*4+4]) {
		case "SLFT", "MLFT":
			speaker = 0x1
		case "SRGT", "MRGT":
			speaker = 0x2
		case "C   ":
			speaker = 0x4
		case "LFE ":
			speaker = 0x8
		case "LS  ":
			speaker = 0x10
		case "RS  ":
			speaker = 0x20
		}
		// 未知或重叠声道无法用单一掩码准确描述，保留原顺序并标记未知布局。
		if speaker == 0 || mask&speaker != 0 {
			return
		}
		speakers[ch] = speaker
		mask |= speaker
	}
	layout.Mask, layout.ordered = mask, true
	for ch := 0; ch < layout.Channels; ch++ {
		index := bits.OnesCount32(mask & (speakers[ch] - 1))
		layout.channelOrder[index] = uint8(ch)
	}
}
