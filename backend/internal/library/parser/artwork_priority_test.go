package parser

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
)

func TestAC7_FLAC与Ogg读取内嵌图片且键名忽略大小写(t *testing.T) {
	picture := artworkPNG(t, 1, color.NRGBA{R: 255, A: 255})
	block := parsertest.FLACPicture(3, "image/png", 1, 1, picture)
	observation := parseArtworkFixture(t, "track.flac", parsertest.FLACWithPictures(block))
	requireArtwork(t, observation, picture, "image/png")
	for _, format := range artworkCommentFormats() {
		t.Run(format.name, func(t *testing.T) {
			for _, key := range []string{"METADATA_BLOCK_PICTURE", "metadata_block_picture", "MeTaDaTa_BlOcK_PiCtUrE"} {
				t.Run(key, func(t *testing.T) {
					data := format.build(t, "TITLE=图片后的标题", key+"="+base64.StdEncoding.EncodeToString(block), "ALBUM=图片后的专辑")
					got := parseArtworkFixture(t, format.file, data)
					requireArtwork(t, got, picture, "image/png")
					if got.Title != "图片后的标题" || got.Album != "图片后的专辑" {
						t.Fatalf("普通标签丢失：标题=%q，专辑=%q", got.Title, got.Album)
					}
				})
			}
		})
	}
}

func TestAC7_Opus跨页读取超过一MiB的图片注释(t *testing.T) {
	// 不压缩的有效 PNG 使注释自然超过旧上限，无需伪造图片长度或填充 Base64。
	picture := artworkPNG(t, 512, color.NRGBA{G: 255, A: 255})
	block := parsertest.FLACPicture(3, "image/png", 512, 512, picture)
	comment := "mEtAdAtA_bLoCk_PiCtUrE=" + base64.StdEncoding.EncodeToString(block)
	if len(comment) <= 1<<20 {
		t.Fatalf("合成图片注释未超过旧上限：%d 字节", len(comment))
	}
	data := lyricsOpusStream("TITLE=大封面之前", comment, "ALBUM=大封面之后")
	if pages := countOggPages(t, data); pages <= 3 {
		t.Fatalf("大注释未跨页：只有 %d 页", pages)
	}
	got := parseArtworkFixture(t, "track.opus", data)
	requireArtwork(t, got, picture, "image/png")
	if got.Title != "大封面之前" || got.Album != "大封面之后" || got.Codec != "opus" {
		t.Fatalf("大封面的前后标签或格式丢失：标题=%q，专辑=%q，格式=%q", got.Title, got.Album, got.Codec)
	}
}

func TestAC7_普通注释仍保持一MiB上限(t *testing.T) {
	for _, format := range artworkCommentFormats() {
		t.Run(format.name, func(t *testing.T) {
			for _, testCase := range []struct {
				name string
				key  string
				size int
				fail bool
			}{
				{"恰好上限仍可读取", "COMMENT", 1 << 20, false},
				{"普通键超过上限报错", "COMMENT", (1 << 20) + 1, true},
				{"图片键前缀不享受放宽", "METADATA_BLOCK_PICTURE_EXTRA", (1 << 20) + 1, true},
			} {
				t.Run(testCase.name, func(t *testing.T) {
					entry := testCase.key + "=" + strings.Repeat("x", testCase.size-len(testCase.key)-1)
					path := writeArtworkFixture(t, format.file, format.build(t, entry, "TITLE=普通注释之后"))
					got, err := ParseAudioFile(path)
					if testCase.fail {
						if err == nil || !strings.Contains(err.Error(), "invalid Vorbis comment") {
							t.Fatalf("超过上限应报注释错误，实际为 %v", err)
						}
						return
					}
					if err != nil || got.Title != "普通注释之后" {
						t.Fatalf("上限内注释阻断解析：标题=%q，错误=%v", got.Title, err)
					}
				})
			}
		})
	}
}

func TestAC7_外链图片被忽略且后续内嵌图片仍可读取(t *testing.T) {
	picture := artworkPNG(t, 1, color.NRGBA{B: 255, A: 255})
	// 外链声明配合有效图片字节，保证拒绝原因是 MIME，而非图片数据为空。
	linked := parsertest.FLACPicture(3, "-->", 1, 1, picture)
	valid := parsertest.FLACPicture(0, "image/png", 1, 1, picture)
	t.Run("FLAC块", func(t *testing.T) {
		requireArtwork(t, parseArtworkFixture(t, "track.flac", parsertest.FLACWithPictures(linked)), nil, "")
		requireArtwork(t, parseArtworkFixture(t, "track.flac", parsertest.FLACWithPictures(linked, valid)), picture, "image/png")
	})
	for _, format := range artworkCommentFormats() {
		t.Run(format.name, func(t *testing.T) {
			comment := "METADATA_BLOCK_PICTURE=" + base64.StdEncoding.EncodeToString(linked)
			requireArtwork(t, parseArtworkFixture(t, format.file, format.build(t, comment)), nil, "")
			data := format.build(t, comment, "METADATA_BLOCK_PICTURE="+base64.StdEncoding.EncodeToString(valid))
			requireArtwork(t, parseArtworkFixture(t, format.file, data), picture, "image/png")
		})
	}
}

func TestAC7_畸形图片注释不阻断后续标签(t *testing.T) {
	picture := artworkPNG(t, 1, color.NRGBA{A: 255})
	block := parsertest.FLACPicture(3, "image/png", 1, 1, picture)
	for _, testCase := range []struct {
		name  string
		value string
	}{
		{"非法Base64", "%%%"},
		{"类型字段截断", base64.StdEncoding.EncodeToString(block[:3])},
		{"MIME字段截断", base64.StdEncoding.EncodeToString(block[:9])},
		{"描述字段截断", base64.StdEncoding.EncodeToString(block[:23])},
		{"图片数据截断", base64.StdEncoding.EncodeToString(block[:len(block)-1])},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			for _, format := range artworkCommentFormats() {
				t.Run(format.name, func(t *testing.T) {
					got := parseArtworkFixture(t, format.file, format.build(t, "METADATA_BLOCK_PICTURE="+testCase.value, "TITLE=保留标题"))
					requireArtwork(t, got, nil, "")
					if got.Title != "保留标题" {
						t.Fatalf("畸形图片阻断普通标签：%q", got.Title)
					}
				})
			}
		})
	}
}

func TestAC8_MP3与FLAC正面优先否则取首张(t *testing.T) {
	first := artworkPNG(t, 1, color.NRGBA{R: 255, A: 255})
	second := artworkPNG(t, 1, color.NRGBA{B: 255, A: 255})
	for _, testCase := range []struct {
		name       string
		firstType  byte
		secondType byte
		wantSecond bool
	}{
		{"封底在前正面在后取正面", 4, 3, true},
		{"两个类型零保留首张", 0, 0, false},
		{"正面在前封底在后保留正面", 3, 4, false},
		{"两个正面保留首张", 3, 3, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			want := first
			if testCase.wantSecond {
				want = second
			}
			for _, major := range []byte{3, 4} {
				name := "ID3v23"
				if major == 4 {
					name = "ID3v24"
				}
				t.Run(name, func(t *testing.T) {
					data := id3Tag(major, 0, artworkAPIC(major, testCase.firstType, first), artworkAPIC(major, testCase.secondType, second))
					data = append(data, testMP3Frames(bytes.Repeat([]byte{0x11}, 100))...)
					requireArtwork(t, parseArtworkFixture(t, "track.mp3", data), want, "image/png")
				})
			}
			firstBlock := parsertest.FLACPicture(uint32(testCase.firstType), "image/png", 1, 1, first)
			secondBlock := parsertest.FLACPicture(uint32(testCase.secondType), "image/png", 1, 1, second)
			t.Run("FLAC的PICTURE块", func(t *testing.T) {
				requireArtwork(t, parseArtworkFixture(t, "track.flac", parsertest.FLACWithPictures(firstBlock, secondBlock)), want, "image/png")
			})
			for _, format := range artworkCommentFormats() {
				t.Run(format.name+"的图片注释", func(t *testing.T) {
					data := format.build(t, "METADATA_BLOCK_PICTURE="+base64.StdEncoding.EncodeToString(firstBlock), "METADATA_BLOCK_PICTURE="+base64.StdEncoding.EncodeToString(secondBlock))
					requireArtwork(t, parseArtworkFixture(t, format.file, data), want, "image/png")
				})
			}
		})
	}
}

func TestAC8_M4A两个covr取第一个(t *testing.T) {
	second := artworkPNG(t, 1, color.NRGBA{G: 255, A: 255})
	// 复用的 M4A 辅助已写入首个 JPEG covr，再追加 PNG covr，区分字节与声明 MIME。
	data := lyricsM4A("mp4a", repairM4AAtom("covr", repairM4AData(14, second)))
	requireArtwork(t, parseArtworkFixture(t, "track.m4a", data), []byte{0xff, 0xd8, 0xff, 0xd9}, "image/jpeg")
}

func TestAC9_WAV读取ID3标题和封面(t *testing.T) {
	picture := artworkPNG(t, 1, color.NRGBA{R: 255, A: 255})
	for _, chunkID := range []string{"id3 ", "ID3 "} {
		t.Run(chunkID, func(t *testing.T) {
			tag := id3Tag(4, 0, titleFrame(4, "WAV内嵌标题"), artworkAPIC(4, 3, picture))
			got := parseArtworkFixture(t, "track.wav", artworkWAV(testWAVChunk(chunkID, tag)))
			requireArtwork(t, got, picture, "image/png")
			if got.Title != "WAV内嵌标题" {
				t.Fatalf("未读取 ID3 标题：%q", got.Title)
			}
		})
	}
}

func TestAC9_WAV合并以ID3为准且不受块顺序影响(t *testing.T) {
	picture := artworkPNG(t, 1, color.NRGBA{R: 255, A: 255})
	textFrame := func(id, value string) []byte {
		return id3Frame(4, id, [2]byte{}, append([]byte{3}, value...))
	}
	tag := id3Tag(4, 0, titleFrame(4, "ID3标题"), textFrame("TPE1", "ID3艺人"),
		textFrame("TXXX", "PRODUCER\x00ID3制作人"), artworkAPIC(4, 3, picture))
	info := []byte("INFO")
	for _, field := range [][2]string{
		{"INAM", "INFO标题"}, {"IPRD", "仅INFO有的专辑"}, {"IART", "INFO艺人"},
		{"IPRO", "INFO制作人"}, {"IWRI", "仅INFO有的作词"},
	} {
		info = append(info, testWAVChunk(field[0], append([]byte(field[1]), 0))...)
	}
	id3Chunk, infoChunk := testWAVChunk("id3 ", tag), testWAVChunk("LIST", info)
	var baseline *AudioObservation
	for _, testCase := range []struct {
		name   string
		chunks [][]byte
	}{
		{"INFO在ID3之前", [][]byte{infoChunk, id3Chunk}},
		{"INFO在ID3之后", [][]byte{id3Chunk, infoChunk}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := parseArtworkFixture(t, "track.wav", artworkWAV(testCase.chunks...))
			requireArtwork(t, got, picture, "image/png")
			if got.Title != "ID3标题" || got.Album != "仅INFO有的专辑" || got.Artist != "ID3艺人" {
				t.Fatalf("ID3优先或INFO补缺失败：标题=%q，专辑=%q，艺人=%q", got.Title, got.Album, got.Artist)
			}
			requireCredits(t, got.Credits, []metadata.Credit{
				{Role: "lyricist", Name: "仅INFO有的作词", Source: "tag"},
				{Role: "producer", Name: "ID3制作人", Source: "tag"},
			})
			roles := map[string][]string{}
			for _, evidence := range got.ArtistTagEvidence {
				roles[evidence.Role] = append(roles[evidence.Role], evidence.RawValue)
			}
			wantRoles := map[string][]string{
				"track_artist": {"ID3艺人"}, "producer": {"ID3制作人"}, "lyricist": {"仅INFO有的作词"},
			}
			if !reflect.DeepEqual(roles, wantRoles) {
				t.Fatalf("同角色INFO证据不应叠加：实际=%#v，期望=%#v", roles, wantRoles)
			}
			if baseline != nil && !reflect.DeepEqual(got, *baseline) {
				t.Fatal("相同内容因INFO和ID3块的顺序不同而产生不同观察")
			}
			baseline = &got
		})
	}
}

func artworkPNG(t *testing.T, size int, fill color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.SetNRGBA(x, y, fill)
		}
	}
	var result bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.NoCompression}
	if err := encoder.Encode(&result, img); err != nil {
		t.Fatalf("生成合成PNG：%v", err)
	}
	return result.Bytes()
}

func artworkAPIC(major, pictureType byte, picture []byte) []byte {
	payload := append([]byte{0}, []byte("image/png\x00")...)
	payload = append(payload, pictureType, 0)
	return id3Frame(major, "APIC", [2]byte{}, append(payload, picture...))
}

func artworkWAV(metadata ...[]byte) []byte {
	body := append([]byte("WAVE"), testWAVChunk("fmt ", testWAVFormat())...)
	for _, chunk := range metadata {
		body = append(body, chunk...)
	}
	body = append(body, testWAVChunk("data", make([]byte, 16))...)
	return append(binary.LittleEndian.AppendUint32([]byte("RIFF"), uint32(len(body))), body...)
}

func artworkCommentFormats() []struct {
	name  string
	file  string
	build func(testing.TB, ...string) []byte
} {
	return []struct {
		name  string
		file  string
		build func(testing.TB, ...string) []byte
	}{
		{"FLAC", "track.flac", func(t testing.TB, entries ...string) []byte { return parsertest.FLACWithComments(t, entries) }},
		{"Ogg的Vorbis", "track.ogg", func(_ testing.TB, entries ...string) []byte { return lyricsVorbisStream(entries...) }},
		{"Ogg的Opus", "track.opus", func(_ testing.TB, entries ...string) []byte { return lyricsOpusStream(entries...) }},
	}
}

func writeArtworkFixture(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("写入合成样本：%v", err)
	}
	return path
}

func parseArtworkFixture(t *testing.T, name string, data []byte) AudioObservation {
	t.Helper()
	got, err := ParseAudioFile(writeArtworkFixture(t, name, data))
	if err != nil {
		t.Fatalf("解析合成样本：%v", err)
	}
	return got
}

func requireArtwork(t *testing.T, got AudioObservation, want []byte, mime string) {
	t.Helper()
	if !bytes.Equal(got.Artwork, want) || got.ArtworkMIME != mime {
		t.Fatalf("封面不符：实际=%d字节/%q，期望=%d字节/%q，字节相同=%t", len(got.Artwork), got.ArtworkMIME, len(want), mime, bytes.Equal(got.Artwork, want))
	}
}
