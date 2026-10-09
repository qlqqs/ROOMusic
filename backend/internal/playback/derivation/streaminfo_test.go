package derivation

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// streamInfoFile 构造只含 fLaC 标记与 STREAMINFO 块的字节：20 位采样率、3 位（声道 - 1）、5 位（位深 - 1）、36 位总样本数。
func streamInfoFile(sampleRate, channels, bitDepth int, totalSamples uint64) []byte {
	packed := uint64(sampleRate)<<44 | uint64(channels-1)<<41 | uint64(bitDepth-1)<<36 | totalSamples
	block := make([]byte, flacStreamInfoBytes)
	binary.BigEndian.PutUint64(block[flacStreamInfoPackedOffset:], packed)
	return append([]byte{'f', 'L', 'a', 'C', 0x80, 0, 0, flacStreamInfoBytes}, block...)
}

func TestReadFLACStreamInfo(t *testing.T) {
	info, err := readFLACStreamInfo(bytes.NewReader(streamInfoFile(48000, 2, 24, 96000)))
	if err != nil {
		t.Fatalf("readFLACStreamInfo: %v", err)
	}
	if want := (flacStreamInfo{SampleRate: 48000, Channels: 2, BitDepth: 24, TotalSamples: 96000}); info != want {
		t.Fatalf("info = %+v, want %+v", info, want)
	}
	// 36 位总样本数用满。
	info, err = readFLACStreamInfo(bytes.NewReader(streamInfoFile(1048575, 8, 32, 1<<36-1)))
	if err != nil || info.SampleRate != 1048575 || info.Channels != 8 || info.BitDepth != 32 || info.TotalSamples != 1<<36-1 {
		t.Fatalf("extreme values: %+v, %v", info, err)
	}
}

func TestReadFLACStreamInfoOfRealEncoderOutput(t *testing.T) {
	for _, testCase := range []struct {
		file                       string
		rate, channels, depth      int
		minimumSamples, maxSamples int64
	}{
		{"flac-16-44100.flac", 44100, 2, 16, 3*44100 - 1, 3*44100 + 1},
		{"flac-24-48000.flac", 48000, 2, 24, 2*48000 - 1, 2*48000 + 1},
	} {
		file, err := os.Open(filepath.Join("..", "..", "..", "testdata", "playback", testCase.file))
		if err != nil {
			t.Fatalf("open fixture: %v", err)
		}
		info, err := readFLACStreamInfo(file)
		file.Close()
		if err != nil {
			t.Fatalf("%s: %v", testCase.file, err)
		}
		if info.SampleRate != testCase.rate || info.Channels != testCase.channels || info.BitDepth != testCase.depth ||
			info.TotalSamples < testCase.minimumSamples || info.TotalSamples > testCase.maxSamples {
			t.Fatalf("%s: info = %+v", testCase.file, info)
		}
	}
}

func TestReadFLACStreamInfoRejectsMalformedHeaders(t *testing.T) {
	valid := streamInfoFile(44100, 2, 16, 1000)
	notFirst := append([]byte(nil), valid...)
	notFirst[4] = 0x84 // VORBIS_COMMENT 块排在最前面
	wrongLength := append([]byte(nil), valid...)
	wrongLength[7] = 35
	for name, data := range map[string][]byte{
		"empty":            nil,
		"too short":        valid[:20],
		"not FLAC":         append([]byte("OggS"), valid[4:]...),
		"first block type": notFirst,
		"wrong length":     wrongLength,
	} {
		if _, err := readFLACStreamInfo(bytes.NewReader(data)); err == nil {
			t.Errorf("%s: malformed header was accepted", name)
		}
	}
}

func TestStreamInfoVerify(t *testing.T) {
	good := flacStreamInfo{SampleRate: 48000, Channels: 2, BitDepth: 24, TotalSamples: 1000}
	if err := good.verify(Expect{BitDepth: 24, SampleRate: 48000, Channels: 2}); err != nil {
		t.Fatalf("matching output rejected: %v", err)
	}
	// 记录没有取得的项不核对。
	if err := good.verify(Expect{}); err != nil {
		t.Fatalf("unknown expectations must not fail: %v", err)
	}
	for name, testCase := range map[string]struct {
		info   flacStreamInfo
		expect Expect
		reason string
	}{
		"sample rate differs": {good, Expect{SampleRate: 44100}, "sample rate"},
		"channels differ":     {good, Expect{Channels: 6}, "channels"},
		"bit depth differs":   {good, Expect{BitDepth: 16}, "bit depth"},
		"8 bit":               {flacStreamInfo{SampleRate: 48000, Channels: 2, BitDepth: 8, TotalSamples: 1}, Expect{}, "bit depth"},
		"20 bit":              {flacStreamInfo{SampleRate: 48000, Channels: 2, BitDepth: 20, TotalSamples: 1}, Expect{}, "bit depth"},
		"32 bit":              {flacStreamInfo{SampleRate: 48000, Channels: 2, BitDepth: 32, TotalSamples: 1}, Expect{}, "bit depth"},
		"no samples":          {flacStreamInfo{SampleRate: 48000, Channels: 2, BitDepth: 16}, Expect{}, "no samples"},
		"no sample rate":      {flacStreamInfo{Channels: 2, BitDepth: 16, TotalSamples: 1}, Expect{}, "sample rate"},
	} {
		err := testCase.info.verify(testCase.expect)
		if err == nil || !strings.Contains(err.Error(), testCase.reason) {
			t.Errorf("%s: err = %v, want a %q error", name, err, testCase.reason)
		}
	}
}
