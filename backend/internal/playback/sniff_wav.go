package playback

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math/bits"
	"strconv"
)

// renderWAVFacts 只保留渲染准入所需的格式事实；decoder 为空表示容器有效但子类型不支持。
type renderWAVFacts struct {
	codec, decoder                        string
	channels, sampleRate, bits, validBits int
	blockAlign                            int
	isFloat                               bool
	layout                                uint32
}

// sniffRenderWAV 在既有容器嗅探之上核验 RIFF 边界、音频块与完整的 PCM 子类型。
// 全部读取都针对同一个已验证句柄；ReadAt 不改变交给解码器的起始偏移。
// 块数量有上限，未知块只跳过，不按声明长度分配内存。
func sniffRenderWAV(source io.ReaderAt, size int64) (renderWAVFacts, bool, error) {
	var facts renderWAVFacts
	matches, err := matchesContainer(source, size, containerWAV)
	if err != nil || !matches {
		return facts, false, err
	}
	var header [12]byte
	if ok, err := readWAVHeader(source, header[:], 0); !ok || err != nil {
		return facts, false, err
	}
	end := int64(binary.LittleEndian.Uint32(header[4:8])) + 8
	if end < 12 || end > size {
		return facts, false, nil
	}
	foundFormat, foundData := false, false
	position := int64(12)
	for chunks := 0; position < end && chunks < 4096; chunks++ {
		if end-position < 8 {
			return facts, false, nil
		}
		var chunk [8]byte
		if ok, err := readWAVHeader(source, chunk[:], position); !ok || err != nil {
			return facts, false, err
		}
		position += 8
		length := int64(binary.LittleEndian.Uint32(chunk[4:8]))
		padded := length + length%2
		if padded > end-position {
			return facts, false, nil
		}
		switch string(chunk[:4]) {
		case "fmt ":
			if foundFormat || foundData || length < 16 || length > 1<<20 {
				return facts, false, nil
			}
			var format [40]byte
			if ok, err := readWAVHeader(source, format[:min(length, int64(len(format)))], position); !ok || err != nil {
				return facts, false, err
			}
			facts, matches = parseRenderWAVFormat(format, length)
			if !matches {
				return facts, false, nil
			}
			foundFormat = true
		case "data":
			if !foundFormat || length == 0 || length%int64(facts.blockAlign) != 0 {
				return facts, false, nil
			}
			foundData = true
		}
		position += padded
	}
	return facts, position == end && foundFormat && foundData, nil
}

func readWAVHeader(source io.ReaderAt, target []byte, offset int64) (bool, error) {
	n, err := source.ReadAt(target, offset)
	if errors.Is(err, io.EOF) {
		return n == len(target), nil
	}
	return n == len(target), err
}

func parseRenderWAVFormat(format [40]byte, length int64) (renderWAVFacts, bool) {
	facts := renderWAVFacts{
		channels:   int(binary.LittleEndian.Uint16(format[2:4])),
		sampleRate: int(binary.LittleEndian.Uint32(format[4:8])),
		blockAlign: int(binary.LittleEndian.Uint16(format[12:14])),
		bits:       int(binary.LittleEndian.Uint16(format[14:16])),
	}
	facts.validBits = facts.bits
	byteRate := uint64(binary.LittleEndian.Uint32(format[8:12]))
	if facts.channels == 0 || facts.sampleRate == 0 || facts.blockAlign == 0 || facts.bits == 0 ||
		byteRate != uint64(facts.sampleRate)*uint64(facts.blockAlign) {
		return facts, false
	}
	if length == 17 || (length >= 18 && int64(binary.LittleEndian.Uint16(format[16:18])) > length-18) {
		return facts, false
	}
	tag := binary.LittleEndian.Uint16(format[:2])
	subtype := tag
	switch tag {
	case 1:
		facts.codec = "pcm"
	case 3:
		facts.codec = "ieee_float"
	case 0xfffe:
		facts.codec = "wav_extensible"
		if length < 40 || binary.LittleEndian.Uint16(format[16:18]) < 22 {
			return facts, false
		}
		facts.validBits = int(binary.LittleEndian.Uint16(format[18:20]))
		if facts.validBits < 1 || facts.validBits > facts.bits {
			return facts, false
		}
		mask := binary.LittleEndian.Uint32(format[20:24])
		facts.layout = mask
		if mask != 0 && bits.OnesCount32(mask) != facts.channels {
			return facts, false
		}
		// PCM 与 IEEE 浮点使用同一 GUID 尾部，完整匹配以拒绝碰巧同前缀的压缩格式。
		guidTail := []byte{0, 0, 0, 0, 0x10, 0, 0x80, 0, 0, 0xaa, 0, 0x38, 0x9b, 0x71}
		if !bytes.Equal(format[26:40], guidTail) {
			return facts, true
		}
		subtype = binary.LittleEndian.Uint16(format[24:26])
	default:
		return facts, false
	}
	if subtype != 1 && subtype != 3 {
		return facts, true
	}
	if facts.bits%8 != 0 || facts.blockAlign != facts.channels*(facts.bits/8) {
		return facts, false
	}
	facts.isFloat = subtype == 3
	if facts.isFloat {
		if facts.validBits != facts.bits {
			return facts, false
		}
		if facts.bits == 32 || facts.bits == 64 {
			facts.decoder = "pcm_f" + strconv.Itoa(facts.bits) + "le"
		}
	} else {
		switch facts.bits {
		case 8:
			facts.decoder = "pcm_u8"
		case 16, 24, 32:
			facts.decoder = "pcm_s" + strconv.Itoa(facts.bits) + "le"
		}
	}
	return facts, true
}
