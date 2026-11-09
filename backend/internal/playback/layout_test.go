package playback

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestRenderWAVExplicitLayout(t *testing.T) {
	// 合成四声道 quad 扩展 WAV；不能用默认 4.0 掩码掩盖传递丢失。
	const mask = 0x33
	data := make([]byte, 68+8)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 40)
	f := data[20:60]
	binary.LittleEndian.PutUint16(f, 0xfffe)
	binary.LittleEndian.PutUint16(f[2:], 4)
	binary.LittleEndian.PutUint32(f[4:], 44100)
	binary.LittleEndian.PutUint32(f[8:], 44100*8)
	binary.LittleEndian.PutUint16(f[12:], 8)
	binary.LittleEndian.PutUint16(f[14:], 16)
	binary.LittleEndian.PutUint16(f[16:], 22)
	binary.LittleEndian.PutUint16(f[18:], 16)
	binary.LittleEndian.PutUint32(f[20:], mask)
	copy(f[24:], []byte{1, 0, 0, 0, 0, 0, 0x10, 0, 0x80, 0, 0, 0xaa, 0, 0x38, 0x9b, 0x71})
	copy(data[60:], "data")
	binary.LittleEndian.PutUint32(data[64:], 8)
	path := filepath.Join(t.TempDir(), "quad.wav")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	facts, ok, err := sniffRenderWAV(file, int64(len(data)))
	if err != nil || !ok || facts.layout != mask {
		t.Fatalf("WAV 掩码=%#x，准入=%v，错误=%v", facts.layout, ok, err)
	}
	source := &RenderSource{File: file, Codec: "wav_extensible", SampleRate: 44100, Channels: 4, BitDepth: 16}
	service := &Service{renderDecoders: layoutTestDecoders{}}
	if err := service.verifyRenderWAV(source, int64(len(data))); err != nil {
		t.Fatal(err)
	}
	if source.Layout != mask {
		t.Fatalf("RenderSource.Layout=%#x，期望 %#x", source.Layout, mask)
	}
}

type layoutTestDecoders struct{}

func (layoutTestDecoders) HasDecoder(name string) bool { return name == "pcm_s16le" }
