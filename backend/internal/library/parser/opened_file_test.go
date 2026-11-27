package parser

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
)

func TestParseOpenedAudioFileMatchesPathEntry(t *testing.T) {
	for _, tc := range []struct {
		name, extension, title string
		data                   []byte
	}{
		{"tagged", ".flac", "标签标题", parsertest.FLACWithAudio(t, [16]byte{1}, []byte{1, 2, 3}, "TITLE=标签标题", "ARTIST=合成艺人", "TRACKNUMBER=2")},
		{"filename", ".wav", "filename", testWAV(bytes.Repeat([]byte{0x12, 0x34}, 128), "")},
	} {
		t.Run(tc.extension, func(t *testing.T) {
			dir := t.TempDir()
			relative := filepath.Join("合成专辑", tc.name+tc.extension)
			path := filepath.Join(dir, relative)
			if err := os.Mkdir(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, tc.data, 0o644); err != nil {
				t.Fatal(err)
			}
			want, err := ParseAudioFile(path)
			if err != nil || want.Format != tc.extension || want.Title != tc.title ||
				want.SampleRate != 44100 || want.DurationSeconds <= 0 {
				t.Fatalf("路径入口解析 = %+v / %v", want, err)
			}
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if _, err := file.Seek(1, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			got, err := ParseOpenedAudioFile(file, relative)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("句柄入口解析 = %+v / %v，路径入口解析 = %+v", got, err, want)
			}
			if _, err := file.Stat(); err != nil {
				t.Errorf("调用方仍应持有打开的句柄：%v", err)
			}
		})
	}
}
