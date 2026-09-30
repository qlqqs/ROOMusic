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

// FLAC 夹具的 STREAMINFO 固定为 44.1 kHz、双声道、16 位、240 秒。
const (
	FLACSampleRate   = 44100
	FLACChannels     = 2
	FLACBitDepth     = 16
	FLACTotalSamples = FLACSampleRate * 240
)

// FLACStream 返回带有效 STREAMINFO（MD5 为零）和空注释块的 FLAC 头。
func FLACStream() []byte {
	streamInfo := flacStreamInfo([16]byte{})
	comments := make([]byte, 8)
	result := append([]byte{'f', 'L', 'a', 'C', 0, 0, 0, byte(len(streamInfo))}, streamInfo...)
	result = append(result, []byte{0x84, 0, 0, byte(len(comments))}...)
	return append(result, comments...)
}

// FLACWithAudio 返回带有效 STREAMINFO 的 FLAC：md5 写入 STREAMINFO 的音频 MD5 字段（全零表示编码器没有写入），
// comments 写入 VORBIS_COMMENT 块，frames 作为最后一个元数据块之后的音频帧字节。
func FLACWithAudio(t testing.TB, md5 [16]byte, frames []byte, comments ...string) []byte {
	t.Helper()
	streamInfo := flacStreamInfo(md5)
	tagged := FLACWithComments(t, comments)
	result := append([]byte{'f', 'L', 'a', 'C', 0, 0, 0, byte(len(streamInfo))}, streamInfo...)
	result = append(result, tagged[4:]...)
	return append(result, frames...)
}

func flacStreamInfo(md5 [16]byte) []byte {
	streamInfo := make([]byte, 34)
	packed := uint64(FLACSampleRate)<<44 | uint64(FLACChannels-1)<<41 | uint64(FLACBitDepth-1)<<36 | uint64(FLACTotalSamples)
	binary.BigEndian.PutUint64(streamInfo[10:18], packed)
	copy(streamInfo[18:34], md5[:])
	return streamInfo
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
