package derivation

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// flacStreamInfo 是 FLAC 文件开头 STREAMINFO 块的内容：转换完成后用它核对输出的音频参数与长度。
type flacStreamInfo struct {
	SampleRate   int
	Channels     int
	BitDepth     int
	TotalSamples int64
}

const (
	flacMarkerLength     = 4
	flacBlockHeaderBytes = 4
	flacStreamInfoBytes  = 34
	// STREAMINFO 中在 20 位采样率之前依次是最小/最大块大小（各 2 字节）与最小/最大帧大小（各 3 字节）。
	flacStreamInfoPackedOffset = 10
)

// readFLACStreamInfo 读取并解析开头的 STREAMINFO：FLAC 规定它必须是第一个元数据块。
func readFLACStreamInfo(reader io.ReaderAt) (flacStreamInfo, error) {
	var head [flacMarkerLength + flacBlockHeaderBytes + flacStreamInfoBytes]byte
	if _, err := reader.ReadAt(head[:], 0); err != nil {
		return flacStreamInfo{}, fmt.Errorf("read FLAC STREAMINFO: %w", err)
	}
	if string(head[:flacMarkerLength]) != "fLaC" {
		return flacStreamInfo{}, errors.New("output is not a FLAC stream")
	}
	header := head[flacMarkerLength : flacMarkerLength+flacBlockHeaderBytes]
	length := int(header[1])<<16 | int(header[2])<<8 | int(header[3])
	if header[0]&0x7f != 0 || length != flacStreamInfoBytes {
		return flacStreamInfo{}, errors.New("first FLAC metadata block is not STREAMINFO")
	}
	block := head[flacMarkerLength+flacBlockHeaderBytes:]
	// 20 位采样率、3 位（声道数 - 1）、5 位（位深 - 1）、36 位总样本数，共 64 位，高位在前。
	packed := binary.BigEndian.Uint64(block[flacStreamInfoPackedOffset : flacStreamInfoPackedOffset+8])
	return flacStreamInfo{
		SampleRate:   int(packed >> 44),
		Channels:     int(packed>>41&0x7) + 1,
		BitDepth:     int(packed>>36&0x1f) + 1,
		TotalSamples: int64(packed & (1<<36 - 1)),
	}, nil
}

// verify 核对转换输出的参数：位深只接受 16 或 24，必须有音频样本，其余与扫描记录相同（记录为 0 的项不核对）。
func (info flacStreamInfo) verify(expect Expect) error {
	if info.SampleRate <= 0 {
		return errors.New("FLAC output has no sample rate")
	}
	if info.BitDepth != 16 && info.BitDepth != 24 {
		return fmt.Errorf("FLAC output bit depth %d is not 16 or 24", info.BitDepth)
	}
	if info.TotalSamples <= 0 {
		return errors.New("FLAC output has no samples")
	}
	if expect.SampleRate > 0 && info.SampleRate != expect.SampleRate {
		return fmt.Errorf("FLAC output sample rate %d differs from the source %d", info.SampleRate, expect.SampleRate)
	}
	if expect.Channels > 0 && info.Channels != expect.Channels {
		return fmt.Errorf("FLAC output channels %d differ from the source %d", info.Channels, expect.Channels)
	}
	if expect.BitDepth > 0 && info.BitDepth != expect.BitDepth {
		return fmt.Errorf("FLAC output bit depth %d differs from the source %d", info.BitDepth, expect.BitDepth)
	}
	return nil
}
