// Package parsertest 提供合成的最小音频夹具，供解析器与扫描测试共用。夹具不含真实音乐数据。
package parsertest

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// FLACWithComments 返回只含一个 VORBIS_COMMENT 块的最小 FLAC 头，comments 是形如 KEY=value 的文本。
func FLACWithComments(t testing.TB, comments []string) []byte {
	t.Helper()
	metadata := &bytes.Buffer{}
	writeLittleEndianString(t, metadata, "roomusic-test")
	if err := binary.Write(metadata, binary.LittleEndian, uint32(len(comments))); err != nil {
		t.Fatalf("write FLAC comment count: %v", err)
	}
	for _, comment := range comments {
		writeLittleEndianString(t, metadata, comment)
	}
	blockLength := metadata.Len()
	return append([]byte{'f', 'L', 'a', 'C', 0x84, byte(blockLength >> 16), byte(blockLength >> 8), byte(blockLength)}, metadata.Bytes()...)
}

// FLACStream 返回带有效 STREAMINFO（44.1 kHz、双声道、16 位、240 秒）和空注释块的 FLAC 头。
func FLACStream() []byte {
	streamInfo := make([]byte, 34)
	const sampleRate = 44100
	const channels = 2
	const bitDepth = 16
	const durationSeconds = 240
	totalSamples := uint64(sampleRate * durationSeconds)
	packed := uint64(sampleRate)<<44 | uint64(channels-1)<<41 | uint64(bitDepth-1)<<36 | totalSamples
	binary.BigEndian.PutUint64(streamInfo[10:18], packed)
	comments := make([]byte, 8)
	result := append([]byte{'f', 'L', 'a', 'C', 0, 0, 0, byte(len(streamInfo))}, streamInfo...)
	result = append(result, []byte{0x84, 0, 0, byte(len(comments))}...)
	return append(result, comments...)
}

func writeLittleEndianString(t testing.TB, destination *bytes.Buffer, value string) {
	t.Helper()
	if err := binary.Write(destination, binary.LittleEndian, uint32(len(value))); err != nil {
		t.Fatalf("write string length: %v", err)
	}
	if _, err := destination.WriteString(value); err != nil {
		t.Fatalf("write string: %v", err)
	}
}
