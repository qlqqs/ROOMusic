package library

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/library/parser"
	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
)

func TestPostgreSQLLibraryReadFileTypes(t *testing.T) {
	f := newLibraryFixture(t)
	root := insertScannerRoot(t, f, "browse-file-types")
	service := scannerTestApplication(f)

	for _, tc := range []struct {
		name, encoding, text string
		raw                  []byte
	}{
		{"utf8.txt", "utf-8", "合成说明\r\n第二行\n", []byte("合成说明\r\n第二行\n")},
		{"gbk.log", "gbk", "专辑\n", []byte{0xd7, 0xa8, 0xbc, 0xad, '\n'}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeFixtureFile(t, root, tc.name, tc.raw)
			got, err := service.ReadLibraryFile(t.Context(), root.ID, tc.name)
			if err != nil || got.Path != tc.name || got.Type != "text" || got.Encoding != tc.encoding ||
				got.Text == nil || *got.Text != tc.text || got.SizeBytes != int64(len(tc.raw)) {
				t.Fatalf("读取文本 = %+v / %v，期望 %s 的 %q（%d 字节）", got, err, tc.encoding, tc.text, len(tc.raw))
			}
		})
	}

	t.Run("image", func(t *testing.T) {
		raw := pngArtworkOfWidth(7)
		writeFixtureFile(t, root, "cover.png", raw)
		got, err := service.ReadLibraryFile(t.Context(), root.ID, "cover.png")
		if err != nil || got.Path != "cover.png" || got.Type != "image" || got.Format != "png" ||
			got.Width != 7 || got.Height != 1 || got.SizeBytes != int64(len(raw)) {
			t.Fatalf("读取图片 = %+v / %v，期望 %d 字节的 7×1 PNG", got, err, len(raw))
		}
	})

	t.Run("other", func(t *testing.T) {
		const relative = "booklet.pdf"
		raw := []byte("%PDF-1.7\n合成文档内容")
		writeFixtureFile(t, root, relative, raw)
		got, err := service.ReadLibraryFile(t.Context(), root.ID, relative)
		if err != nil || got.Path != relative || got.Type != "other" || got.SizeBytes != int64(len(raw)) {
			t.Fatalf("读取其他文件 = %+v / %v", got, err)
		}
		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		if keys := slices.Sorted(maps.Keys(fields)); !slices.Equal(keys, []string{"path", "size_bytes", "type"}) {
			t.Errorf("其他文件仅返回相对路径、类型与大小，实际为 %s", encoded)
		}
	})
}

func TestPostgreSQLLibraryReadAudioOmitsPrivatePayloads(t *testing.T) {
	f := newLibraryFixture(t)
	root := insertScannerRoot(t, f, "browse-file-audio")
	service := scannerTestApplication(f)
	const (
		relative = "song.mp3"
		title    = "合成曲目"
		comment  = "用于辨认发行的合成注释"
		lyrics   = "合成歌词正文不得进入工具结果"
	)
	artwork := pngArtworkOfWidth(9)
	legacyBytes := []byte{0xcd, 0xf5, 0xb7, 0xc6}
	var frames []byte
	frame := func(id string, value []byte) {
		frames = append(frames, id...)
		frames = binary.BigEndian.AppendUint32(frames, uint32(len(value)))
		frames = append(frames, 0, 0)
		frames = append(frames, value...)
	}
	frame("TIT2", append([]byte{3}, title...))
	frame("TPE1", append([]byte{0}, legacyBytes...))
	frame("TRCK", []byte{3, '3'})
	frame("TPOS", []byte{3, '2'})
	frame("COMM", append([]byte{3, 'e', 'n', 'g', 0}, comment...))
	frame("USLT", append([]byte{3, 'e', 'n', 'g', 0}, lyrics...))
	frame("APIC", append([]byte("\x00image/png\x00\x03\x00"), artwork...))
	audio := make([]byte, 417)
	copy(audio, []byte{0xff, 0xfb, 0x90, 0x00})
	raw := append(parsertest.ID3v23(frames), bytes.Repeat(audio, 8)...)
	writeFixtureFile(t, root, relative, raw)

	parsed, err := parser.ParseAudioFile(filepath.Join(root.Path, relative))
	if err != nil || parsed.Lyrics != lyrics || !bytes.Equal(parsed.Artwork, artwork) || !bytes.Equal(parsed.LegacyText["artist"], legacyBytes) {
		t.Fatalf("合成夹具必须实际解析出歌词、封面与旧编码原字节；解析错误 %v，歌词 %q，封面 %d 字节，旧编码 %x", err, parsed.Lyrics, len(parsed.Artwork), parsed.LegacyText["artist"])
	}
	got, err := service.ReadLibraryFile(t.Context(), root.ID, relative)
	if err != nil || got.Path != relative || got.Type != "audio" || got.Format != "mp3" ||
		got.SizeBytes != int64(len(raw)) || got.Audio == nil || got.Audio.Codec != "mp3" ||
		got.Audio.SampleRate != 44100 || got.Audio.Channels != 2 || got.Audio.DurationSeconds <= 0 {
		t.Fatalf("音频属性 = %+v / %v", got, err)
	}
	if got.Tags["title"] != title || got.Tags["track_number"] != 3 || got.Tags["disc_number"] != 2 {
		t.Errorf("音频标题与碟轨号 = %+v", got.Tags)
	}
	if comments, _ := got.Tags["comments"].([]metadata.ScanComment); len(comments) != 1 || comments[0].Value != comment {
		t.Errorf("音频应返回已解析的注释，实际为 %#v", got.Tags["comments"])
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		root.Path, lyrics, base64.StdEncoding.EncodeToString(artwork), base64.StdEncoding.EncodeToString(legacyBytes),
		`"lyrics"`, `"lyricssource"`, `"artwork"`, `"artworkmime"`, `"legacytext"`, `"legacy_text"`,
	} {
		if strings.Contains(strings.ToLower(string(encoded)), strings.ToLower(forbidden)) {
			t.Errorf("音频 JSON 包含禁止投影的数据 %q", forbidden)
		}
	}
}
