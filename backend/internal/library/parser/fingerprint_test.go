package parser

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
)

// fingerprintFormat 描述一种格式的合成夹具：同一份音频负载配不同标签。
type fingerprintFormat struct {
	name        string
	extension   string
	kind        string
	freeAtParse bool // 解析时就带出指纹，不需要额外读盘
	build       func(t *testing.T, audio []byte, tag string) []byte
}

func fingerprintFormats() []fingerprintFormat {
	return []fingerprintFormat{
		{"flac with md5", ".flac", "flac-md5-v1", true, func(t *testing.T, audio []byte, tag string) []byte {
			sum := sha256.Sum256(audio)
			var md5 [16]byte
			copy(md5[:], sum[:16])
			return parsertest.FLACWithAudio(t, md5, audio, "TITLE="+tag, "ARTIST="+tag)
		}},
		{"flac without md5", ".flac", "flac-frames-v1", false, func(t *testing.T, audio []byte, tag string) []byte {
			return parsertest.FLACWithAudio(t, [16]byte{}, audio, "TITLE="+tag, "ARTIST="+tag)
		}},
		{"m4a", ".m4a", "m4a-mdat-v1", false, func(t *testing.T, audio []byte, tag string) []byte {
			return bytes.Join([][]byte{
				testM4AFtyp(),
				repairM4AAtom("mdat", audio),
				repairM4AMoov("alac", 2, 16, 44100, 88200, tag, tag, tag, tag),
			}, nil)
		}},
		{"mp3", ".mp3", "mp3-frames-v1", false, func(t *testing.T, audio []byte, tag string) []byte {
			return bytes.Join([][]byte{
				repairMP3Frames(map[string]string{"TIT2": tag, "TPE1": tag}),
				testMP3Frames(audio),
			}, nil)
		}},
		{"ogg vorbis", ".ogg", "ogg-audio-v1", true, func(t *testing.T, audio []byte, tag string) []byte {
			return testVorbisStream(tag, 255, splitPackets(audio, 500)...)
		}},
		{"opus", ".opus", "ogg-audio-v1", true, func(t *testing.T, audio []byte, tag string) []byte {
			return testOpusStream(tag, 255, splitPackets(audio, 500)...)
		}},
		{"wav", ".wav", "wav-data-v1", false, func(t *testing.T, audio []byte, tag string) []byte {
			return testWAV(audio, tag)
		}},
	}
}

func TestContentFingerprintIgnoresTags(t *testing.T) {
	audio := bytes.Repeat([]byte{0x11, 0x22, 0x33, 0x44, 0x55}, 400)
	for _, format := range fingerprintFormats() {
		t.Run(format.name, func(t *testing.T) {
			short := writeFingerprintFixture(t, "short"+format.extension, format.build(t, audio, "a"))
			long := writeFingerprintFixture(t, "long"+format.extension, format.build(t, audio, strings.Repeat("长标签", 40)))
			first, second := fingerprintOf(t, short), fingerprintOf(t, long)
			if first != second {
				t.Fatalf("同一份音频、不同标签的指纹不同：%s / %s", first, second)
			}
			if !strings.HasPrefix(first, format.kind+":") {
				t.Fatalf("指纹种类不符：%s，期望 %s", first, format.kind)
			}
			for _, path := range []string{short, long} {
				observation, err := ParseAudioFile(path)
				if err != nil {
					t.Fatalf("夹具无法被解析器接受：%v", err)
				}
				want := ""
				if format.freeAtParse {
					want = first
				}
				if observation.ContentFingerprint != want {
					t.Fatalf("解析时带出的指纹 = %q，期望 %q", observation.ContentFingerprint, want)
				}
			}
		})
	}
}

func TestContentFingerprintDetectsAudioChange(t *testing.T) {
	original := bytes.Repeat([]byte{0x11, 0x22, 0x33, 0x44, 0x55}, 400)
	changed := append([]byte(nil), original...)
	changed[len(changed)/2] ^= 0xff
	for _, format := range fingerprintFormats() {
		t.Run(format.name, func(t *testing.T) {
			before := fingerprintOf(t, writeFingerprintFixture(t, "before"+format.extension, format.build(t, original, "same")))
			after := fingerprintOf(t, writeFingerprintFixture(t, "after"+format.extension, format.build(t, changed, "same")))
			if before == after {
				t.Fatalf("音频改变后指纹没有变化：%s", before)
			}
		})
	}
}

// 对已打开的句柄取证必须与按路径计算得到同一个指纹：播放在转换前用句柄核对扫描时记录的指纹。
func TestContentFingerprintOfMatchesTheFileVariant(t *testing.T) {
	audio := bytes.Repeat([]byte{0x11, 0x22, 0x33, 0x44, 0x55}, 400)
	for _, format := range fingerprintFormats() {
		t.Run(format.name, func(t *testing.T) {
			path := writeFingerprintFixture(t, "track"+format.extension, format.build(t, audio, "tag"))
			want := fingerprintOf(t, path)
			file, err := os.Open(path)
			if err != nil {
				t.Fatalf("open fixture: %v", err)
			}
			defer file.Close()
			info, err := file.Stat()
			if err != nil {
				t.Fatalf("stat fixture: %v", err)
			}
			for _, extension := range []string{format.extension, strings.ToUpper(format.extension)} {
				got, err := ContentFingerprintOf(context.Background(), extension, file, info.Size())
				if err != nil || got != want {
					t.Fatalf("ContentFingerprintOf(%q) = %q, %v; want %q", extension, got, err, want)
				}
			}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := ContentFingerprintOf(canceled, format.extension, file, info.Size()); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled context: err = %v, want context.Canceled", err)
			}
		})
	}
	if _, err := ContentFingerprintOf(context.Background(), ".txt", strings.NewReader("x"), 1); err == nil {
		t.Fatal("unsupported extension produced a fingerprint")
	}
}

func TestFLACFingerprintFallsBackWhenMD5IsZero(t *testing.T) {
	frames := bytes.Repeat([]byte{0xab}, 512)
	md5 := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	withMD5 := writeFingerprintFixture(t, "md5.flac", parsertest.FLACWithAudio(t, md5, frames, "TITLE=x"))
	withoutMD5 := writeFingerprintFixture(t, "zero.flac", parsertest.FLACWithAudio(t, [16]byte{}, frames, "TITLE=x"))

	wantMD5 := fmt.Sprintf("flac-md5-v1:%x:%d:%d:%d:%d", md5, parsertest.FLACTotalSamples, parsertest.FLACSampleRate, parsertest.FLACChannels, parsertest.FLACBitDepth)
	if got := fingerprintOf(t, withMD5); got != wantMD5 {
		t.Fatalf("MD5 指纹 = %s，期望 %s", got, wantMD5)
	}
	if got, want := fingerprintOf(t, withoutMD5), "flac-frames-v1:"+sha256Hex(frames); got != want {
		t.Fatalf("MD5 为零时的帧指纹 = %s，期望 %s", got, want)
	}

	observation, err := ParseAudioFile(withMD5)
	if err != nil || observation.ContentFingerprint != wantMD5 {
		t.Fatalf("解析时的指纹 = %q（错误 %v），期望 %s", observation.ContentFingerprint, err, wantMD5)
	}
	observation, err = ParseAudioFile(withoutMD5)
	if err != nil || observation.ContentFingerprint != "" {
		t.Fatalf("MD5 为零的 FLAC 不应在解析时得到指纹：%q（错误 %v）", observation.ContentFingerprint, err)
	}

	// 编码器写入的 MD5 是音频内容的标识：帧字节不同（例如换压缩级别）但 MD5 相同，视为同一内容。
	recompressed := writeFingerprintFixture(t, "recompressed.flac", parsertest.FLACWithAudio(t, md5, bytes.Repeat([]byte{0xcd}, 700), "TITLE=y"))
	if got := fingerprintOf(t, recompressed); got != wantMD5 {
		t.Fatalf("MD5 相同的重新编码文件指纹 = %s，期望 %s", got, wantMD5)
	}
}

func TestMP3FingerprintStripsLeadingAndTrailingTags(t *testing.T) {
	frames := testMP3Frames(bytes.Repeat([]byte{0x5a, 0xa5, 0x3c}, 300))
	want := "mp3-frames-v1:" + sha256Hex(frames)
	id3v2 := repairMP3Frames(map[string]string{"TIT2": "标题", "TPE1": "艺术家"})
	id3v2WithFooter := append(append([]byte(nil), id3v2...), '3', 'D', 'I', 4, 0, 0x10, 0, 0, 0, 0)
	id3v2WithFooter[5] |= 0x10
	id3v1 := testID3v1()
	id3v1Extended := testID3v1Extended()
	ape := testAPEv2(false)
	apeWithHeader := testAPEv2(true)
	lyrics := testLyrics3v2()

	cases := []struct {
		name string
		data []byte
	}{
		{"bare frames", frames},
		{"id3v2", bytes.Join([][]byte{id3v2, frames}, nil)},
		{"id3v2 with footer", bytes.Join([][]byte{id3v2WithFooter, frames}, nil)},
		{"stacked id3v2", bytes.Join([][]byte{id3v2, id3v2, frames}, nil)},
		{"id3v1", bytes.Join([][]byte{frames, id3v1}, nil)},
		{"id3v1 extended", bytes.Join([][]byte{frames, id3v1Extended, id3v1}, nil)},
		{"apev2", bytes.Join([][]byte{frames, ape}, nil)},
		{"apev2 with header", bytes.Join([][]byte{frames, apeWithHeader}, nil)},
		{"lyrics3v2", bytes.Join([][]byte{frames, lyrics}, nil)},
		{"apev2, lyrics3v2, id3v1", bytes.Join([][]byte{frames, ape, lyrics, id3v1}, nil)},
		{"lyrics3v2, apev2 with header, id3v1", bytes.Join([][]byte{frames, lyrics, apeWithHeader, id3v1}, nil)},
		{"all tags", bytes.Join([][]byte{id3v2, frames, apeWithHeader, lyrics, id3v1}, nil)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := fingerprintOf(t, writeFingerprintFixture(t, "track.mp3", testCase.data)); got != want {
				t.Fatalf("指纹 = %s，期望 %s", got, want)
			}
		})
	}

	t.Run("untagged trailing bytes belong to the audio", func(t *testing.T) {
		data := bytes.Join([][]byte{frames, []byte("trailing bytes that are not a tag")}, nil)
		if got := fingerprintOf(t, writeFingerprintFixture(t, "track.mp3", data)); got == want {
			t.Fatal("不是标签的尾部字节被当作标签剥离")
		}
	})
}

func TestOggFingerprintIgnoresCommentPagination(t *testing.T) {
	packets := splitPackets(bytes.Repeat([]byte{0x31, 0x41, 0x59, 0x26}, 750), 500)
	want := "ogg-audio-v1:" + sha256Hex(packets...)
	codecs := []struct {
		name      string
		extension string
		build     func(comment string, pageSegments int, audio ...[]byte) []byte
	}{
		{"vorbis", ".ogg", testVorbisStream},
		{"opus", ".opus", testOpusStream},
	}
	for _, codec := range codecs {
		t.Run(codec.name, func(t *testing.T) {
			small := codec.build("short", 255, packets...)
			// 100 KB 的注释包会跨页，之后各页的序号后移；音频包本身不变。
			large := codec.build(strings.Repeat("x", 100_000), 255, packets...)
			// 同样的音频包，每页最多两个段：页数大增，包内容不变。
			repaginated := codec.build("short", 2, packets...)
			if countOggPages(t, large) <= countOggPages(t, small) || countOggPages(t, repaginated) <= countOggPages(t, small) {
				t.Fatal("夹具没有改变分页，测试不能证明指纹与分页无关")
			}
			for name, data := range map[string][]byte{"small comment": small, "large comment": large, "repaginated audio": repaginated} {
				if got := fingerprintOf(t, writeFingerprintFixture(t, "track"+codec.extension, data)); got != want {
					t.Fatalf("%s：指纹 = %s，期望 %s", name, got, want)
				}
			}
		})
	}

	t.Run("unsupported codec", func(t *testing.T) {
		page, _ := testOggPages(1, 0, 255, append([]byte("\x7fFLAC"), make([]byte, 20)...))
		if _, err := oggAudioFingerprint(page); err == nil {
			t.Fatal("非 Vorbis/Opus 的 OGG 流被接受")
		}
	})
}

func TestM4AFingerprintHashesAllMediaData(t *testing.T) {
	first := bytes.Repeat([]byte{0x01}, 900)
	second := bytes.Repeat([]byte{0x02}, 700)
	ftyp := testM4AFtyp()
	moovOne := repairM4AMoov("alac", 2, 16, 44100, 88200, "one", "", "", "")
	moovTwo := repairM4AMoov("alac", 2, 16, 44100, 88200, "a different title", "artist", "album", "")
	want := "m4a-mdat-v1:" + sha256Hex(first, second)

	layouts := map[string][]byte{
		"moov last":         bytes.Join([][]byte{ftyp, repairM4AAtom("mdat", first), repairM4AAtom64("mdat", second), moovOne}, nil),
		"moov first":        bytes.Join([][]byte{ftyp, moovTwo, repairM4AAtom("mdat", first), repairM4AAtom("mdat", second)}, nil),
		"moov between":      bytes.Join([][]byte{ftyp, repairM4AAtom("mdat", first), moovOne, repairM4AAtom("mdat", second)}, nil),
		"last mdat to EOF":  bytes.Join([][]byte{ftyp, moovOne, repairM4AAtom("mdat", first), repairM4AAtomToEOF("mdat", second)}, nil),
		"padding and moov":  bytes.Join([][]byte{ftyp, repairM4AAtom64("free", []byte("padding")), repairM4AAtom("mdat", first), moovTwo, repairM4AAtom("mdat", second)}, nil),
		"large size header": bytes.Join([][]byte{ftyp, repairM4AAtom64("mdat", first), repairM4AAtom64("mdat", second), moovOne}, nil),
	}
	for name, data := range layouts {
		t.Run(name, func(t *testing.T) {
			if got := fingerprintOf(t, writeFingerprintFixture(t, "track.m4a", data)); got != want {
				t.Fatalf("指纹 = %s，期望 %s", got, want)
			}
		})
	}

	t.Run("every mdat contributes and order matters", func(t *testing.T) {
		tampered := append([]byte(nil), second...)
		tampered[len(tampered)-1] ^= 0xff
		for name, data := range map[string][]byte{
			"second mdat changed": bytes.Join([][]byte{ftyp, repairM4AAtom("mdat", first), repairM4AAtom("mdat", tampered), moovOne}, nil),
			"mdat order swapped":  bytes.Join([][]byte{ftyp, repairM4AAtom("mdat", second), repairM4AAtom("mdat", first), moovOne}, nil),
			"second mdat missing": bytes.Join([][]byte{ftyp, repairM4AAtom("mdat", first), moovOne}, nil),
		} {
			if got := fingerprintOf(t, writeFingerprintFixture(t, "track.m4a", data)); got == want {
				t.Fatalf("%s：指纹没有变化", name)
			}
		}
	})
}

func TestWAVFingerprintHashesFormatAndDataChunks(t *testing.T) {
	format := testWAVFormat()
	firstData := bytes.Repeat([]byte{0x10}, 301) // 奇数长度，带填充字节
	secondData := bytes.Repeat([]byte{0x20}, 200)
	body := bytes.Join([][]byte{
		[]byte("WAVE"),
		testWAVChunk("fmt ", format),
		testWAVChunk("LIST", append([]byte("INFO"), testWAVChunk("INAM", []byte("title"))...)),
		testWAVChunk("data", firstData),
		testWAVChunk("id3 ", []byte("tag chunk after the audio")),
		testWAVChunk("data", secondData),
	}, nil)
	data := append(binary.LittleEndian.AppendUint32([]byte("RIFF"), uint32(len(body))), body...)

	length := binary.LittleEndian.AppendUint32(nil, uint32(len(format)))
	want := "wav-data-v1:" + sha256Hex(length, format, firstData, secondData)
	if got := fingerprintOf(t, writeFingerprintFixture(t, "track.wav", data)); got != want {
		t.Fatalf("指纹 = %s，期望 %s", got, want)
	}
}

func TestContentFingerprintHonorsCancellation(t *testing.T) {
	audio := bytes.Repeat([]byte{0x11, 0x22, 0x33}, 200)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, format := range fingerprintFormats() {
		t.Run(format.name, func(t *testing.T) {
			path := writeFingerprintFixture(t, "track"+format.extension, format.build(t, audio, "tag"))
			if _, err := ContentFingerprint(canceled, path); !errors.Is(err, context.Canceled) {
				t.Fatalf("已取消的 context 返回 %v，期望 context.Canceled", err)
			}
		})
	}

	t.Run("cancel between reads", func(t *testing.T) {
		const total = 4 * fingerprintReadChunk
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		reader := &cancelAfterFirstRead{ReaderAt: bytes.NewReader(make([]byte, total)), cancel: cancel}
		err := hashSection(ctx, sha256.New(), reader, 0, total)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("读取中途取消返回 %v，期望 context.Canceled", err)
		}
		if reader.reads != 1 {
			t.Fatalf("取消后仍继续读取：读取 %d 次", reader.reads)
		}
	})
}

func TestContentFingerprintIsEmptyWhenThereIsNoAudioPayload(t *testing.T) {
	cases := []struct {
		name string
		file string
		data []byte
	}{
		{"FLAC without frames and without md5", "track.flac", parsertest.FLACWithAudio(t, [16]byte{}, nil, "TITLE=x")},
		{"M4A without mdat", "track.m4a", bytes.Join([][]byte{testM4AFtyp(), repairM4AMoov("alac", 2, 16, 44100, 88200, "t", "", "", "")}, nil)},
		{"M4A with an empty mdat", "track.m4a", bytes.Join([][]byte{testM4AFtyp(), repairM4AAtom("mdat", nil), repairM4AMoov("alac", 2, 16, 44100, 88200, "t", "", "", "")}, nil)},
		{"MP3 with tags only", "track.mp3", bytes.Join([][]byte{repairMP3Frames(map[string]string{"TIT2": "title"}), testID3v1()}, nil)},
		{"Vorbis with headers only", "track.ogg", testVorbisStream("title", 255)},
		{"Opus with headers only", "track.opus", testOpusStream("title", 255)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := writeFingerprintFixture(t, testCase.file, testCase.data)
			fingerprint, err := ContentFingerprint(context.Background(), path)
			if err != nil || fingerprint != "" {
				t.Fatalf("没有音频负载的文件指纹 = %q（错误 %v），期望空串", fingerprint, err)
			}
		})
	}
}

func TestContentFingerprintRejectsUnsupportedAndDamagedFiles(t *testing.T) {
	streamInfoBlock := append([]byte{0, 0, 0, 34}, make([]byte, 34)...)
	cases := []struct {
		name string
		file string
		data []byte
	}{
		{"unsupported extension", "track.txt", []byte("plain text")},
		{"not a FLAC stream", "track.flac", []byte("not-flac")},
		{"FLAC metadata block beyond the end of file", "track.flac", bytes.Join([][]byte{[]byte("fLaC"), streamInfoBlock, {0x84, 0, 0x03, 0xe8}}, nil)},
		{"truncated M4A atom", "track.m4a", append(binary.BigEndian.AppendUint32(nil, 4096), "mdat"...)},
		{"M4A too short", "track.m4a", []byte("tiny")},
		{"ID3 tag larger than the file", "track.mp3", []byte{'I', 'D', '3', 4, 0, 0, 0, 0, 0x7f, 0x7f, 1, 2, 3}},
		{"WAV without audio chunks", "track.wav", append(binary.LittleEndian.AppendUint32([]byte("RIFF"), 4), "WAVE"...)},
		{"truncated OGG page", "track.ogg", append([]byte("OggS"), make([]byte, 23)...)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := writeFingerprintFixture(t, testCase.file, testCase.data)
			if fingerprint, err := ContentFingerprint(context.Background(), path); err == nil {
				t.Fatalf("损坏或不支持的输入得到了指纹 %q", fingerprint)
			}
		})
	}
	if _, err := ContentFingerprint(context.Background(), filepath.Join(t.TempDir(), "missing.flac")); err == nil {
		t.Fatal("不存在的文件得到了指纹")
	}
}

func fingerprintOf(t *testing.T, path string) string {
	t.Helper()
	fingerprint, err := ContentFingerprint(context.Background(), path)
	if err != nil {
		t.Fatalf("计算 %s 的指纹：%v", filepath.Base(path), err)
	}
	return fingerprint
}

func writeFingerprintFixture(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("写入夹具 %s：%v", name, err)
	}
	return path
}

func sha256Hex(parts ...[]byte) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write(part)
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

// splitPackets 把音频负载切成 size 字节一个的包；每个包在 Ogg 流里独立成包。
func splitPackets(audio []byte, size int) [][]byte {
	var packets [][]byte
	for len(audio) > 0 {
		next := min(size, len(audio))
		packets = append(packets, audio[:next])
		audio = audio[next:]
	}
	return packets
}

func testM4AFtyp() []byte {
	return repairM4AAtom("ftyp", append([]byte("M4A "), 0, 0, 0, 0))
}

func testMP3Frames(audio []byte) []byte {
	return append([]byte{0xff, 0xfb, 0x90, 0x64}, audio...)
}

func testID3v1() []byte {
	tag := make([]byte, 128)
	copy(tag, "TAG")
	copy(tag[3:], "title")
	return tag
}

// testID3v1Extended 是 ID3v1 之前的 227 字节扩展块。
func testID3v1Extended() []byte {
	tag := make([]byte, 227)
	copy(tag, "TAG+")
	return tag
}

func testAPEv2(withHeader bool) []byte {
	items := binary.LittleEndian.AppendUint32(nil, 5) // 值长度
	items = binary.LittleEndian.AppendUint32(items, 0)
	items = append(items, "Title\x00hello"...)
	block := func(flags uint32) []byte {
		out := []byte("APETAGEX")
		out = binary.LittleEndian.AppendUint32(out, 2000)
		out = binary.LittleEndian.AppendUint32(out, uint32(len(items)+32)) // 含 footer，不含 header
		out = binary.LittleEndian.AppendUint32(out, 1)
		out = binary.LittleEndian.AppendUint32(out, flags)
		return append(out, make([]byte, 8)...)
	}
	if !withHeader {
		return append(items, block(0)...)
	}
	tag := block(1<<31 | 1<<29)
	tag = append(tag, items...)
	return append(tag, block(1<<31)...)
}

func testLyrics3v2() []byte {
	block := []byte("LYRICSBEGIN" + "LYR00005hello")
	return append(append(block, fmt.Sprintf("%06d", len(block))...), "LYRICS200"...)
}

func testWAVFormat() []byte {
	format := make([]byte, 16)
	binary.LittleEndian.PutUint16(format[0:2], 1)
	binary.LittleEndian.PutUint16(format[2:4], 2)
	binary.LittleEndian.PutUint32(format[4:8], 44100)
	binary.LittleEndian.PutUint32(format[8:12], 176400)
	binary.LittleEndian.PutUint16(format[12:14], 4)
	binary.LittleEndian.PutUint16(format[14:16], 16)
	return format
}

func testWAVChunk(id string, payload []byte) []byte {
	chunk := binary.LittleEndian.AppendUint32([]byte(id), uint32(len(payload)))
	chunk = append(chunk, payload...)
	if len(payload)%2 == 1 {
		chunk = append(chunk, 0)
	}
	return chunk
}

func testWAV(audio []byte, title string) []byte {
	body := bytes.Join([][]byte{
		[]byte("WAVE"),
		testWAVChunk("fmt ", testWAVFormat()),
		testWAVChunk("LIST", append([]byte("INFO"), testWAVChunk("INAM", []byte(title))...)),
		testWAVChunk("data", audio),
	}, nil)
	return append(binary.LittleEndian.AppendUint32([]byte("RIFF"), uint32(len(body))), body...)
}

// testVorbisComments 返回没有 vendor 字符串的 Vorbis 注释块（不含 Vorbis 的 framing 位）。
func testVorbisComments(entries ...string) []byte {
	block := []byte{0, 0, 0, 0}
	block = binary.LittleEndian.AppendUint32(block, uint32(len(entries)))
	for _, entry := range entries {
		block = binary.LittleEndian.AppendUint32(block, uint32(len(entry)))
		block = append(block, entry...)
	}
	return block
}

// testOggPages 把 packets 分页写成 Ogg 页序列：每页最多 pageSegments 个 lacing 值，放不下的包在下一页续写。
// 页头的 CRC 不被解析器校验，写零即可。返回页字节和下一个页序号。
func testOggPages(serial, sequence uint32, pageSegments int, packets ...[]byte) ([]byte, uint32) {
	var out bytes.Buffer
	var lacing, body []byte
	continued := false
	flush := func() {
		header := make([]byte, 27)
		copy(header, "OggS")
		if continued {
			header[5] = 0x01
		}
		binary.LittleEndian.PutUint32(header[14:18], serial)
		binary.LittleEndian.PutUint32(header[18:22], sequence)
		header[26] = byte(len(lacing))
		out.Write(header)
		out.Write(lacing)
		out.Write(body)
		sequence++
		continued = lacing[len(lacing)-1] == 255
		lacing, body = nil, nil
	}
	for _, packet := range packets {
		for offset := 0; ; {
			if len(lacing) == pageSegments {
				flush()
			}
			segment := min(len(packet)-offset, 255)
			lacing = append(lacing, byte(segment))
			body = append(body, packet[offset:offset+segment]...)
			offset += segment
			if segment < 255 {
				break
			}
		}
	}
	if len(lacing) > 0 {
		flush()
	}
	return out.Bytes(), sequence
}

// testOggStream 按规范排列一个 Ogg 流：标识包独占首页，其余头部包共用后续页，音频包从新页开始。
func testOggStream(identification []byte, headers [][]byte, pageSegments int, audio [][]byte) []byte {
	first, next := testOggPages(7, 0, 255, identification)
	rest, next := testOggPages(7, next, 255, headers...)
	body, _ := testOggPages(7, next, pageSegments, audio...)
	return bytes.Join([][]byte{first, rest, body}, nil)
}

func testVorbisStream(comment string, pageSegments int, audio ...[]byte) []byte {
	identification := make([]byte, 30)
	copy(identification, "\x01vorbis")
	identification[11] = 2
	binary.LittleEndian.PutUint32(identification[12:16], 44100)
	identification[29] = 1
	comments := append(append([]byte("\x03vorbis"), testVorbisComments("TITLE="+comment)...), 1)
	setup := append([]byte("\x05vorbis"), bytes.Repeat([]byte{0x42}, 300)...)
	return testOggStream(identification, [][]byte{comments, setup}, pageSegments, audio)
}

func testOpusStream(comment string, pageSegments int, audio ...[]byte) []byte {
	head := make([]byte, 19)
	copy(head, "OpusHead")
	head[8] = 1
	head[9] = 2
	tags := append([]byte("OpusTags"), testVorbisComments("TITLE="+comment)...)
	return testOggStream(head, [][]byte{tags}, pageSegments, audio)
}

func countOggPages(t *testing.T, data []byte) int {
	t.Helper()
	pages := 0
	for pos := 0; pos < len(data); pages++ {
		if pos+27 > len(data) || string(data[pos:pos+4]) != "OggS" {
			t.Fatalf("夹具在偏移 %d 处不是有效的 Ogg 页", pos)
		}
		segments := int(data[pos+26])
		pos += 27 + segments
		for _, length := range data[pos-segments : pos] {
			pos += int(length)
		}
	}
	return pages
}

// cancelAfterFirstRead 在第一次读取后取消 context，用来证明长读取会在块之间响应取消。
type cancelAfterFirstRead struct {
	io.ReaderAt
	cancel context.CancelFunc
	reads  int
}

func (r *cancelAfterFirstRead) ReadAt(p []byte, offset int64) (int, error) {
	r.reads++
	n, err := r.ReaderAt.ReadAt(p, offset)
	r.cancel()
	return n, err
}
