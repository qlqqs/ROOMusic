package parser

import (
	"encoding/binary"
	"io"
	"math"
)

// applyMP3StreamFacts 只在音频区间的有界窗口内寻找首帧，优先按 VBR 头的帧数计算时长。
func applyMP3StreamFacts(reader io.ReaderAt, fileSize int64, observation *AudioObservation) {
	if reader == nil || observation == nil || fileSize <= 0 {
		return
	}
	start, end, err := mp3AudioRange(reader, fileSize)
	if err != nil || end <= start {
		return
	}
	data := make([]byte, int(min(end-start, maxMP3FrameSearchBytes)))
	if _, err := reader.ReadAt(data, start); err != nil {
		return
	}
	for offset := 0; offset+4 <= len(data); offset++ {
		bitrate, sampleRate, channels, valid := decodeMP3FrameHeader(data[offset : offset+4])
		if !valid {
			continue
		}
		frame := start + int64(offset)
		// 另读首帧头部，避免搜索窗口末尾截断 Xing/Info 或 VBRI。
		header := make([]byte, int(min(end-frame, 64)))
		if _, err := reader.ReadAt(header, frame); err != nil {
			return
		}
		version := (header[1] >> 3) & 3
		samples := 576
		if version == 3 {
			samples = 1152
		}
		frames, audioBytes := mp3VBRCounts(header, version, channels)
		duration := float64(end-frame) * 8 / float64(bitrate*1000)
		if frames > 0 {
			duration = float64(frames) * float64(samples) / float64(sampleRate)
			bytes := float64(audioBytes)
			if audioBytes == 0 {
				bytes = float64(end - frame)
			}
			bitrate = int(math.Round(bytes * 8 / duration / 1000))
		}
		observation.Bitrate = bitrate
		observation.SampleRate = sampleRate
		observation.Channels = channels
		observation.DurationSeconds = duration
		return
	}
}

// mp3VBRCounts 读取可选的帧数与字节数，不解析 TOC 或编码延迟。
func mp3VBRCounts(header []byte, version byte, channels int) (frames, audioBytes uint32) {
	sideInfo := 17
	if version == 3 {
		if channels == 2 {
			sideInfo = 32
		}
	} else if channels == 1 {
		sideInfo = 9
	}
	offset := 4 + sideInfo
	if len(header) >= offset+4 && (string(header[offset:offset+4]) == "Xing" || string(header[offset:offset+4]) == "Info") {
		if len(header) < offset+8 {
			return 0, 0
		}
		flags := binary.BigEndian.Uint32(header[offset+4 : offset+8])
		offset += 8
		if flags&1 != 0 {
			if len(header) < offset+4 {
				return 0, 0
			}
			frames = binary.BigEndian.Uint32(header[offset : offset+4])
			offset += 4
		}
		if flags&2 != 0 && len(header) >= offset+4 {
			audioBytes = binary.BigEndian.Uint32(header[offset : offset+4])
		}
		return frames, audioBytes
	}
	if len(header) >= 54 && string(header[36:40]) == "VBRI" {
		return binary.BigEndian.Uint32(header[50:54]), binary.BigEndian.Uint32(header[46:50])
	}
	return 0, 0
}
