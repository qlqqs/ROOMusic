package parser

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

func TestMP3StreamFactsUsesVBRFrameCounts(t *testing.T) {
	const frames = 101
	for _, tc := range []struct {
		name                    string
		header                  [4]byte
		marker                  string
		offset                  int
		flags, audioBytes       uint32
		samples, rate, channels int
	}{
		{"Xing MPEG-1 双声道", [4]byte{0xff, 0xfb, 0x90, 0x00}, "Xing", 36, 3, 23456, 1152, 44100, 2},
		{"Xing MPEG-2 单声道", [4]byte{0xff, 0xf3, 0x80, 0xc0}, "Xing", 13, 3, 23456, 576, 22050, 1},
		{"Info MPEG-1 单声道", [4]byte{0xff, 0xfb, 0x90, 0xc0}, "Info", 21, 3, 23456, 1152, 44100, 1},
		{"VBRI MPEG-2 双声道", [4]byte{0xff, 0xf3, 0x80, 0x00}, "VBRI", 36, 0, 23456, 576, 22050, 2},
		{"Xing 仅帧数", [4]byte{0xff, 0xfb, 0x90, 0x00}, "Xing", 36, 1, 0, 1152, 44100, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 仅帧数时，误计首尾标签须让码率从 48 跨到 49 kbps，避免取整掩盖错误。
			audio := make([]byte, 15800)
			copy(audio, tc.header[:])
			copy(audio[tc.offset:], tc.marker)
			if tc.marker == "VBRI" {
				binary.BigEndian.PutUint16(audio[40:42], 1)
				binary.BigEndian.PutUint32(audio[46:50], tc.audioBytes)
				binary.BigEndian.PutUint32(audio[50:54], frames)
			} else {
				binary.BigEndian.PutUint32(audio[tc.offset+4:tc.offset+8], tc.flags)
				binary.BigEndian.PutUint32(audio[tc.offset+8:tc.offset+12], frames)
				if tc.flags&2 != 0 {
					binary.BigEndian.PutUint32(audio[tc.offset+12:tc.offset+16], tc.audioBytes)
				}
			}
			// 仅帧数时不能把首尾标签也计入码率。
			data := bytes.Join([][]byte{repairMP3Frames(map[string]string{"TIT2": "时长测试"}), audio, testAPEv2(false), testID3v1()}, nil)
			var got AudioObservation
			applyMP3StreamFacts(bytes.NewReader(data), int64(len(data)), &got)
			wantDuration := float64(frames*tc.samples) / float64(tc.rate)
			if math.Abs(got.DurationSeconds-wantDuration) > 1e-9 {
				t.Fatalf("时长 = %.9f 秒，期望 %.9f 秒", got.DurationSeconds, wantDuration)
			}
			wantBytes := tc.audioBytes
			if wantBytes == 0 {
				wantBytes = uint32(len(audio))
			}
			wantBitrate := int(math.Round(float64(wantBytes) * 8 / wantDuration / 1000))
			if got.Bitrate != wantBitrate || got.SampleRate != tc.rate || got.Channels != tc.channels {
				t.Fatalf("音频参数 = %d kbps / %d Hz / %d 声道，期望 %d / %d / %d", got.Bitrate, got.SampleRate, got.Channels, wantBitrate, tc.rate, tc.channels)
			}
		})
	}
}

func TestMP3CBRDurationExcludesLargeArtworkAndTrailingTags(t *testing.T) {
	artwork := append([]byte("\x00image/jpeg\x00\x03\x00"), bytes.Repeat([]byte{0x5a}, 1<<20)...)
	id3v2 := repairMP3BinaryFrame("APIC", artwork)
	audio := testMP3Frames(make([]byte, 20000))
	data := bytes.Join([][]byte{id3v2, audio, testAPEv2(true), testID3v1()}, nil)
	var got AudioObservation
	applyMP3StreamFacts(bytes.NewReader(data), int64(len(data)), &got)
	wantDuration := float64(len(audio)) * 8 / 128000
	if math.Abs(got.DurationSeconds-wantDuration) > 1e-9 {
		t.Fatalf("CBR 时长 = %.9f 秒，期望去掉标签且保留小数的 %.9f 秒", got.DurationSeconds, wantDuration)
	}
	if got.Bitrate != 128 || got.SampleRate != 44100 || got.Channels != 2 {
		t.Fatalf("CBR 音频参数有误：%+v", got)
	}
}
