package parsertest

import (
	"encoding/binary"
	"math/bits"
)

// APE 返回双声道、16 位、44.1 kHz 的新版 APE 头和指定帧；MD5 全零表示没有原生摘要。
func APE(md5 [16]byte, audio []byte) []byte {
	data := make([]byte, 80)
	copy(data, "MAC ")
	binary.LittleEndian.PutUint16(data[4:6], 3990)
	binary.LittleEndian.PutUint32(data[8:12], 52)
	binary.LittleEndian.PutUint32(data[12:16], 24)
	binary.LittleEndian.PutUint32(data[16:20], 4)
	binary.LittleEndian.PutUint32(data[24:28], uint32(len(audio)))
	copy(data[36:52], md5[:])
	binary.LittleEndian.PutUint16(data[52:54], 2000)
	binary.LittleEndian.PutUint16(data[54:56], 0x20)
	binary.LittleEndian.PutUint32(data[56:60], 73728)
	binary.LittleEndian.PutUint32(data[60:64], 4410)
	binary.LittleEndian.PutUint32(data[64:68], 1)
	binary.LittleEndian.PutUint16(data[68:70], 16)
	binary.LittleEndian.PutUint16(data[70:72], 2)
	binary.LittleEndian.PutUint32(data[72:76], 44100)
	binary.LittleEndian.PutUint32(data[76:80], uint32(len(data)))
	return append(data, audio...)
}

// APELegacy 返回两帧旧头；末帧为 4410 个样本，并按标志附加峰值、seek 数与 WAV 头。
func APELegacy(version, compression, flags uint16, audio []byte) []byte {
	data := make([]byte, 32)
	copy(data, "MAC ")
	binary.LittleEndian.PutUint16(data[4:6], version)
	binary.LittleEndian.PutUint16(data[6:8], compression)
	binary.LittleEndian.PutUint16(data[8:10], flags)
	binary.LittleEndian.PutUint16(data[10:12], 2)
	binary.LittleEndian.PutUint32(data[12:16], 44100)
	binary.LittleEndian.PutUint32(data[24:28], 2)
	binary.LittleEndian.PutUint32(data[28:32], 4410)
	if flags&4 != 0 {
		data = binary.LittleEndian.AppendUint32(data, 32767)
	}
	if flags&0x10 != 0 {
		data = binary.LittleEndian.AppendUint32(data, 2)
	}
	if flags&0x20 == 0 {
		binary.LittleEndian.PutUint32(data[16:20], 12)
		data = append(data, []byte("RIFFtestWAVE")...)
	}
	firstFrame := uint32(len(data) + 8)
	data = binary.LittleEndian.AppendUint32(data, firstFrame)
	data = binary.LittleEndian.AppendUint32(data, firstFrame+uint32(len(audio)/2))
	return append(data, audio...)
}

// APEv2Item 的 Flags 直接写入条目标志，文本为 0，二进制为 2。
type APEv2Item struct {
	Key   string
	Value []byte
	Flags uint32
}

// APEv2 构造完整页脚标签，可选带匹配的头部，供 APE 和 WavPack 共用。
func APEv2(version uint32, withHeader bool, items ...APEv2Item) []byte {
	var body []byte
	for _, item := range items {
		body = binary.LittleEndian.AppendUint32(body, uint32(len(item.Value)))
		body = binary.LittleEndian.AppendUint32(body, item.Flags)
		body = append(body, item.Key...)
		body = append(body, 0)
		body = append(body, item.Value...)
	}
	footer := make([]byte, 32)
	copy(footer, "APETAGEX")
	binary.LittleEndian.PutUint32(footer[8:12], version)
	binary.LittleEndian.PutUint32(footer[12:16], uint32(len(body)+32))
	binary.LittleEndian.PutUint32(footer[16:20], uint32(len(items)))
	if withHeader {
		binary.LittleEndian.PutUint32(footer[20:24], 1<<31)
		header := append([]byte(nil), footer...)
		binary.LittleEndian.PutUint32(header[20:24], 1<<31|1<<29)
		body = append(header, body...)
	}
	return append(body, footer...)
}

// WavPackBlock 构造单个 4.10 块；total 为负表示总样本数未知。
func WavPackBlock(flags uint32, total int64, samples uint32, metadata []byte) []byte {
	data := make([]byte, 32)
	copy(data, "wvpk")
	binary.LittleEndian.PutUint32(data[4:8], uint32(24+len(metadata)))
	binary.LittleEndian.PutUint16(data[8:10], 0x410)
	if total >= 0 {
		data[11] = byte(total / 0xffffffff)
		binary.LittleEndian.PutUint32(data[12:16], uint32(total%0xffffffff))
	} else {
		binary.LittleEndian.PutUint32(data[12:16], 0xffffffff)
	}
	binary.LittleEndian.PutUint32(data[20:24], samples)
	binary.LittleEndian.PutUint32(data[24:28], flags)
	return append(data, metadata...)
}

// WavPackMetadata 构造短子块并补齐奇数字节。
func WavPackMetadata(id byte, value []byte) []byte {
	if len(value)%2 != 0 {
		id |= 0x40
	}
	data := append([]byte{id, byte((len(value) + 1) / 2)}, value...)
	if len(value)%2 != 0 {
		data = append(data, 0)
	}
	return data
}

// AIFFChunk 构造大端长度块，奇数长度后补一字节。
func AIFFChunk(id string, data []byte) []byte {
	result := binary.BigEndian.AppendUint32([]byte(id), uint32(len(data)))
	result = append(result, data...)
	if len(data)%2 != 0 {
		result = append(result, 0)
	}
	return result
}

// AIFF 返回带 COMM、元数据和 SSND 的容器，compression 非空时为 AIFF-C。
func AIFF(compression string, sampleRate, bitDepth, channels int, frames uint32, audio []byte, metadata ...[]byte) []byte {
	comm := binary.BigEndian.AppendUint16(nil, uint16(channels))
	comm = binary.BigEndian.AppendUint32(comm, frames)
	comm = binary.BigEndian.AppendUint16(comm, uint16(bitDepth))
	exponent := bits.Len32(uint32(sampleRate)) - 1
	comm = binary.BigEndian.AppendUint16(comm, uint16(16383+exponent))
	comm = binary.BigEndian.AppendUint64(comm, uint64(sampleRate)<<uint(63-exponent))
	form := "AIFF"
	if compression != "" {
		form = "AIFC"
		comm = append(comm, compression...)
		comm = append(comm, 0, 0)
	}
	body := append([]byte(form), AIFFChunk("COMM", comm)...)
	for _, chunk := range metadata {
		body = append(body, chunk...)
	}
	body = append(body, AIFFChunk("SSND", append(make([]byte, 8), audio...))...)
	return append(binary.BigEndian.AppendUint32([]byte("FORM"), uint32(len(body))), body...)
}

// ID3v23 为已有的 ID3v2.3 帧添加同步安全长度的标签头。
func ID3v23(frames []byte) []byte {
	size := len(frames)
	header := []byte{'I', 'D', '3', 3, 0, 0, byte(size >> 21 & 0x7f), byte(size >> 14 & 0x7f), byte(size >> 7 & 0x7f), byte(size & 0x7f)}
	return append(header, frames...)
}
