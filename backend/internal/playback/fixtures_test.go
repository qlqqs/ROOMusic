package playback

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/library/parser"
)

// 用真实编码器写出的素材核对格式表与容器识别：扫描器解析出的（来源类别、编码）必须与格式表一致，
// 文件头必须被对应的容器识别接受。格式表只有拿到验证证据的组合才保留，这里是“素材与格式表不脱节”的一半，
// 另一半（浏览器真的能解码）由真实服务的自动化与手工实播验证。
func TestFixturesFromARealEncoderMatchTheFormatTable(t *testing.T) {
	directory := filepath.Join("..", "..", "testdata", "playback")
	for _, expected := range []struct {
		file     string
		mimeType string
		codecs   string
		inTable  bool
	}{
		{"flac-16-44100.flac", "audio/flac", "", true},
		{"flac-24-48000.flac", "audio/flac", "", true},
		// ALAC 不在旧接口的直出表中。
		{"alac-16-44100.m4a", "", "", false},
		{"alac-24-48000.m4a", "", "", false},
		{"mp3-44100.mp3", "audio/mpeg", "", true},
		{"aac-44100.m4a", "audio/mp4", "mp4a.40.2", true},
		{"wav-16-44100.wav", "audio/wav", "1", true},
		{"vorbis-44100.ogg", "audio/ogg", "vorbis", true},
		{"opus-48000.opus", "audio/ogg", "opus", true},
		// 24 位 WAV 由常见工具写成 WAVE_FORMAT_EXTENSIBLE，扫描记为 wav_extensible，不在格式表中：标注不可直接播放。
		{"wav-24-48000.wav", "", "", false},
	} {
		t.Run(expected.file, func(t *testing.T) {
			path := filepath.Join(directory, expected.file)
			observation, err := parser.ParseAudioFile(path)
			if err != nil {
				t.Fatalf("ParseAudioFile: %v", err)
			}
			format, found := (&Service{}).findFormat(observation.SourceKind, observation.Codec)
			if found != expected.inTable {
				t.Fatalf("findFormat(%s, %s) found = %v, want %v", observation.SourceKind, observation.Codec, found, expected.inTable)
			}
			if !found {
				return
			}
			if format.MIMEType != expected.mimeType || format.Codecs != expected.codecs {
				t.Errorf("format = %s %q, want %s %q", format.MIMEType, format.Codecs, expected.mimeType, expected.codecs)
			}
			file, err := os.Open(path)
			if err != nil {
				t.Fatalf("open fixture: %v", err)
			}
			defer file.Close()
			info, err := file.Stat()
			if err != nil {
				t.Fatalf("stat fixture: %v", err)
			}
			matches, err := matchesContainer(file, info.Size(), format.container)
			if err != nil || !matches {
				t.Fatalf("matchesContainer = %v, %v; the container check rejects a file the scanner accepted", matches, err)
			}
		})
	}
}

// dsdSourceContent 生成 6000 PCM 帧的单声道 DSF，供来源边界与分析流共用。
// 保留尾块填充；DSD64/128 的物理负载分别为 8/12 KiB，另有 92 字节头部。
func dsdSourceContent(rate int) []byte {
	const blockSize = 4096
	byteFrames := 6000 * (rate / 2822400)
	blocks := (byteFrames + blockSize - 1) / blockSize
	data := make([]byte, 92+blocks*blockSize)
	le := binary.LittleEndian
	copy(data, "DSD ")
	le.PutUint64(data[4:], 28)
	le.PutUint64(data[12:], uint64(len(data)))
	copy(data[28:], "fmt ")
	le.PutUint64(data[32:], 52)
	le.PutUint32(data[40:], 1)
	le.PutUint32(data[48:], 1)
	le.PutUint32(data[52:], 1)
	le.PutUint32(data[56:], uint32(rate))
	le.PutUint32(data[60:], 1)
	le.PutUint64(data[64:], uint64(byteFrames*8))
	le.PutUint32(data[72:], blockSize)
	copy(data[80:], "data")
	le.PutUint64(data[84:], uint64(len(data)-80))
	for frame := 0; frame < byteFrames; frame++ {
		data[92+frame] = byte(frame*37 + frame/251)
	}
	return data
}
