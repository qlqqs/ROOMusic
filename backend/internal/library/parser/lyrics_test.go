package parser

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
)

func TestNormalizeLyrics(t *testing.T) {
	for _, testCase := range []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{"plain lines", "first\nsecond", "first\nsecond", true},
		{"leading byte order mark", "\ufeffhello", "hello", true},
		{"CRLF line endings", "a\r\nb\r\nc", "a\nb\nc", true},
		{"bare CR line endings", "a\rb", "a\nb", true},
		{"mixed line endings keep blank lines", "a\r\n\r\nb\rc\n", "a\n\nb\nc", true},
		{"NUL and C0 controls are dropped, tab is kept", "a\x00b\x01c\x08d\te", "abcd\te", true},
		{"surrounding whitespace is trimmed", "  \n\n [00:01.00]line \n\n", "[00:01.00]line", true},
		{"inner blank lines are kept", "a\n\nb", "a\n\nb", true},
		{"CJK text and LRC tags", "[ti:标题]\n[00:01.50]你好，世界", "[ti:标题]\n[00:01.50]你好，世界", true},
		{"empty", "", "", false},
		{"whitespace only", " \n\t\r\n", "", false},
		{"controls only", "\x00\x01\x02", "", false},
		{"byte order mark only", "\ufeff", "", false},
		{"invalid UTF-8", "ab\xffcd", "", false},
		{"exactly at the limit", strings.Repeat("a", MaxLyricsBytes), strings.Repeat("a", MaxLyricsBytes), true},
		{"one byte over the limit", strings.Repeat("a", MaxLyricsBytes+1), "", false},
		{"the limit applies after normalization", strings.Repeat("a", MaxLyricsBytes) + "\r\n\r\n   \x00", strings.Repeat("a", MaxLyricsBytes), true},
		{"the limit counts bytes, not characters (fits)", strings.Repeat("歌", MaxLyricsBytes/3), strings.Repeat("歌", MaxLyricsBytes/3), true},
		{"the limit counts bytes, not characters (over)", strings.Repeat("歌", MaxLyricsBytes/3+1), "", false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := NormalizeLyrics(testCase.raw)
			if got != testCase.want || ok != testCase.ok {
				t.Fatalf("NormalizeLyrics(%q) = %q, %v; want %q, %v", truncateForLog(testCase.raw), truncateForLog(got), ok, truncateForLog(testCase.want), testCase.ok)
			}
		})
	}
}

func truncateForLog(value string) string {
	if len(value) > 60 {
		return value[:60] + "…"
	}
	return value
}

func parseLyricsFixture(t *testing.T, name string, content []byte) AudioObservation {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	observation, err := ParseAudioFile(path)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return observation
}

func requireLyrics(t *testing.T, observation AudioObservation, wantLyrics, wantSource string) {
	t.Helper()
	if observation.Lyrics != wantLyrics || observation.LyricsSource != wantSource {
		t.Fatalf("lyrics = %q (%s), want %q (%s)", truncateForLog(observation.Lyrics), observation.LyricsSource, truncateForLog(wantLyrics), wantSource)
	}
}

func TestFLACParserReadsVorbisLyrics(t *testing.T) {
	synced := "[00:01.00]第一行\n[00:02.00]第二行"
	for _, testCase := range []struct {
		name       string
		comments   []string
		wantLyrics string
		wantSource string
	}{
		{"LYRICS", []string{"LYRICS=" + synced}, synced, LyricsSourceVorbisLyrics},
		{"UNSYNCEDLYRICS", []string{"UNSYNCEDLYRICS=plain"}, "plain", LyricsSourceVorbisUnsynced},
		{"UNSYNCED LYRICS", []string{"UNSYNCED LYRICS=plain"}, "plain", LyricsSourceVorbisUnsynced},
		{"keys are case insensitive", []string{"lyrics=lower"}, "lower", LyricsSourceVorbisLyrics},
		{"LYRICS beats UNSYNCEDLYRICS when it comes second", []string{"UNSYNCEDLYRICS=plain", "LYRICS=synced"}, "synced", LyricsSourceVorbisLyrics},
		{"LYRICS beats UNSYNCEDLYRICS when it comes first", []string{"LYRICS=synced", "UNSYNCEDLYRICS=plain"}, "synced", LyricsSourceVorbisLyrics},
		{"the first valid LYRICS wins", []string{"LYRICS=first", "LYRICS=second"}, "first", LyricsSourceVorbisLyrics},
		{"the first valid UNSYNCEDLYRICS wins", []string{"UNSYNCEDLYRICS=first", "UNSYNCED LYRICS=second"}, "first", LyricsSourceVorbisUnsynced},
		{"a blank LYRICS is skipped", []string{"LYRICS=   ", "LYRICS=real"}, "real", LyricsSourceVorbisLyrics},
		{"a blank LYRICS falls back to UNSYNCEDLYRICS", []string{"LYRICS=  \r\n", "UNSYNCEDLYRICS=plain"}, "plain", LyricsSourceVorbisUnsynced},
		{"invalid UTF-8 is skipped", []string{"LYRICS=\xff\xfe\xfd", "UNSYNCEDLYRICS=plain"}, "plain", LyricsSourceVorbisUnsynced},
		{"CRLF is normalized", []string{"LYRICS=a\r\nb\r\n"}, "a\nb", LyricsSourceVorbisLyrics},
		{"exactly at the limit", []string{"LYRICS=" + strings.Repeat("a", MaxLyricsBytes)}, strings.Repeat("a", MaxLyricsBytes), LyricsSourceVorbisLyrics},
		{"over the limit", []string{"LYRICS=" + strings.Repeat("a", MaxLyricsBytes+1)}, "", ""},
		{"over the limit falls back to UNSYNCEDLYRICS", []string{"LYRICS=" + strings.Repeat("a", MaxLyricsBytes+1), "UNSYNCEDLYRICS=short"}, "short", LyricsSourceVorbisUnsynced},
		{"no lyrics", []string{"ALBUM=Only an album"}, "", ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			comments := append([]string{"TITLE=Title", "ARTIST=Artist"}, testCase.comments...)
			observation := parseLyricsFixture(t, "track.flac", parsertest.FLACWithComments(t, comments))
			requireLyrics(t, observation, testCase.wantLyrics, testCase.wantSource)
			// 歌词键不属于普通标签：其余字段照常解析，歌词也不会出现在别的字段里。
			if observation.Title != "Title" || observation.Artist != "Artist" {
				t.Fatalf("other fields changed: %+v", observation)
			}
		})
	}
}

func lyricsVorbisStream(entries ...string) []byte {
	identification := make([]byte, 30)
	copy(identification, "\x01vorbis")
	identification[11] = 2
	binary.LittleEndian.PutUint32(identification[12:16], 44100)
	identification[29] = 1
	comments := append(append([]byte("\x03vorbis"), testVorbisComments(entries...)...), 1)
	setup := append([]byte("\x05vorbis"), bytes.Repeat([]byte{0x42}, 64)...)
	return testOggStream(identification, [][]byte{comments, setup}, 255, [][]byte{bytes.Repeat([]byte{0x11}, 64)})
}

func lyricsOpusStream(entries ...string) []byte {
	head := make([]byte, 19)
	copy(head, "OpusHead")
	head[8] = 1
	head[9] = 2
	tags := append([]byte("OpusTags"), testVorbisComments(entries...)...)
	return testOggStream(head, [][]byte{tags}, 255, [][]byte{bytes.Repeat([]byte{0x11}, 64)})
}

func TestOggAndOpusParsersReadVorbisLyrics(t *testing.T) {
	// 注释包占多个 lacing 段，覆盖包不止 255 字节的情形。
	synced := "[00:01.00]" + strings.Repeat("词", 120) + "\n[00:02.00]second"
	for _, testCase := range []struct {
		name    string
		file    string
		content func(entries ...string) []byte
	}{
		{"Ogg Vorbis", "track.ogg", lyricsVorbisStream},
		{"Ogg Opus", "track.opus", lyricsOpusStream},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			observation := parseLyricsFixture(t, testCase.file, testCase.content("TITLE=Title", "UNSYNCEDLYRICS=plain", "LYRICS="+synced))
			requireLyrics(t, observation, synced, LyricsSourceVorbisLyrics)
			if observation.Title != "Title" {
				t.Fatalf("title = %q", observation.Title)
			}
			unsynced := parseLyricsFixture(t, testCase.file, testCase.content("TITLE=Title", "UNSYNCED LYRICS=plain"))
			requireLyrics(t, unsynced, "plain", LyricsSourceVorbisUnsynced)
			none := parseLyricsFixture(t, testCase.file, testCase.content("TITLE=Title"))
			requireLyrics(t, none, "", "")
		})
	}
}

const m4aLyricsItem = "\xa9lyr"

func lyricsM4A(codec string, extraItems []byte) []byte {
	return bytes.Join([][]byte{
		repairM4AAtom("ftyp", append([]byte("M4A "), []byte{0, 0, 0, 0}...)),
		repairM4AMoovWithItems(codec, 2, 16, 44100, 88200, "Title", "Artist", "Album", "", extraItems),
	}, nil)
}

func TestM4AParserReadsLyrics(t *testing.T) {
	lyrics := "[00:01.00]第一行\r\n[00:02.00]第二行"
	want := "[00:01.00]第一行\n[00:02.00]第二行"
	item := func(dataAtoms ...[]byte) []byte {
		return repairM4AAtom(m4aLyricsItem, bytes.Join(dataAtoms, nil))
	}
	for _, testCase := range []struct {
		name       string
		item       []byte
		wantLyrics string
		wantSource string
	}{
		{"UTF-8 text", item(repairM4AData(1, []byte(lyrics))), want, LyricsSourceMP4},
		{"binary data is ignored", item(repairM4AData(0, []byte(lyrics))), "", ""},
		{"UTF-16 data is ignored", item(repairM4AData(2, []byte(lyrics))), "", ""},
		{"JPEG data is ignored", item(repairM4AData(13, []byte(lyrics))), "", ""},
		{"blank text is ignored", item(repairM4AData(1, []byte(" \r\n "))), "", ""},
		{"invalid UTF-8 is ignored", item(repairM4AData(1, []byte("ab\xffcd"))), "", ""},
		{"the first valid data atom wins", item(repairM4AData(1, []byte("first")), repairM4AData(1, []byte("second"))), "first", LyricsSourceMP4},
		{"a blank data atom is skipped", item(repairM4AData(1, []byte("  ")), repairM4AData(1, []byte("second"))), "second", LyricsSourceMP4},
		{"over the limit", item(repairM4AData(1, []byte(strings.Repeat("a", MaxLyricsBytes+1)))), "", ""},
		{"no lyrics item", nil, "", ""},
	} {
		for _, codec := range []string{"mp4a", "alac"} {
			t.Run(testCase.name+" ("+codec+")", func(t *testing.T) {
				observation := parseLyricsFixture(t, "track.m4a", lyricsM4A(codec, testCase.item))
				requireLyrics(t, observation, testCase.wantLyrics, testCase.wantSource)
				if observation.Title != "Title" || observation.Artist != "Artist" {
					t.Fatalf("other fields changed: %+v", observation)
				}
				// 歌词可达 64 KiB 且保留换行：它不进入给诊断用的原始 atom 文本。
				if _, present := observation.RawAtoms[m4aLyricsItem]; present {
					t.Fatalf("the lyrics item leaked into RawAtoms")
				}
			})
		}
	}
}

// id3Tag 组装一个 ID3v2 标签：标签头的大小总是 synchsafe，帧由调用方按版本编码。
func id3Tag(major, tagFlags byte, frames ...[]byte) []byte {
	body := bytes.Join(frames, nil)
	size := len(body)
	header := []byte{'I', 'D', '3', major, 0, tagFlags, byte(size>>21) & 0x7f, byte(size>>14) & 0x7f, byte(size>>7) & 0x7f, byte(size) & 0x7f}
	return append(header, body...)
}

// id3Frame 编码一个帧：v2.3 的大小是普通大端整数，v2.4 是 synchsafe 整数。
func id3Frame(major byte, id string, flags [2]byte, payload []byte) []byte {
	size := len(payload)
	frame := []byte(id)
	if major >= 4 {
		frame = append(frame, byte(size>>21)&0x7f, byte(size>>14)&0x7f, byte(size>>7)&0x7f, byte(size)&0x7f)
	} else {
		frame = binary.BigEndian.AppendUint32(frame, uint32(size))
	}
	frame = append(frame, flags[0], flags[1])
	return append(frame, payload...)
}

// id3PlainSizeFrame 按普通整数写大小，不论版本：模拟在 v2.4 标签里写普通大小的写入器。
func id3PlainSizeFrame(id string, payload []byte) []byte {
	frame := binary.BigEndian.AppendUint32([]byte(id), uint32(len(payload)))
	frame = append(frame, 0, 0)
	return append(frame, payload...)
}

// usltPayload 组装 USLT 帧内容：编码字节、语言、以编码对应的终止符结尾的内容描述、歌词文本。
func usltPayload(encoding byte, descriptor, text []byte) []byte {
	payload := append([]byte{encoding, 'e', 'n', 'g'}, descriptor...)
	if encoding == 1 || encoding == 2 {
		payload = append(payload, 0, 0)
	} else {
		payload = append(payload, 0)
	}
	return append(payload, text...)
}

func titleFrame(major byte, title string) []byte {
	return id3Frame(major, "TIT2", [2]byte{}, append([]byte{3}, title...))
}

// addUnsynchronisation 按 ID3v2 的同步消除规则编码：在每个后随 0xE0 以上字节、0x00 或位于末尾的 0xFF 后插入 0x00。
func addUnsynchronisation(data []byte) []byte {
	var encoded []byte
	for index, value := range data {
		encoded = append(encoded, value)
		if value == 0xff && (index+1 == len(data) || data[index+1] >= 0xe0 || data[index+1] == 0) {
			encoded = append(encoded, 0)
		}
	}
	return encoded
}

func TestMP3ParserReadsUSLTLyrics(t *testing.T) {
	lyrics := "[00:01.00]第一行\r\n[00:02.00]第二行"
	want := "[00:01.00]第一行\n[00:02.00]第二行"
	utf16LE, utf16BE := repairUTF16(lyrics, true), repairUTF16(lyrics, false)
	utf16BENoBOM := utf16BE[2:]

	for _, testCase := range []struct {
		name  string
		major byte
		frame []byte
		want  string
	}{
		{"ISO-8859-1 in v2.3", 3, usltPayload(0, nil, []byte("caf\xe9\r\nline")), "café\nline"},
		{"UTF-16 little endian with BOM in v2.3", 3, usltPayload(1, nil, utf16LE), want},
		{"UTF-16 big endian with BOM in v2.3", 3, usltPayload(1, nil, utf16BE), want},
		{"UTF-16 without BOM defaults to little endian", 3, usltPayload(1, nil, utf16LE[2:]), want},
		{"UTF-16 big endian without BOM in v2.4", 4, usltPayload(2, nil, utf16BENoBOM), want},
		{"UTF-8 in v2.4", 4, usltPayload(3, nil, []byte(lyrics)), want},
		{"UTF-8 in v2.3", 3, usltPayload(3, nil, []byte(lyrics)), want},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			content := id3Tag(testCase.major, 0, id3Frame(testCase.major, "USLT", [2]byte{}, testCase.frame), titleFrame(testCase.major, "Title"))
			observation := parseLyricsFixture(t, "track.mp3", content)
			requireLyrics(t, observation, testCase.want, LyricsSourceID3USLT)
			if observation.Title != "Title" {
				t.Fatalf("the frame after USLT was not parsed: %+v", observation)
			}
		})
	}

	t.Run("a long USLT frame in a v2.4 tag uses synchsafe sizes", func(t *testing.T) {
		long := strings.Repeat("la la la\n", 400)
		content := id3Tag(4, 0, id3Frame(4, "USLT", [2]byte{}, usltPayload(3, nil, []byte(long))), titleFrame(4, "Title"))
		observation := parseLyricsFixture(t, "track.mp3", content)
		requireLyrics(t, observation, strings.TrimSpace(long), LyricsSourceID3USLT)
		if observation.Title != "Title" {
			t.Fatalf("title = %q", observation.Title)
		}
	})

	t.Run("a v2.4 writer that stores plain sizes is still read", func(t *testing.T) {
		// 帧内容正好 16384 字节：普通大小的字节是 00 00 40 00，按 synchsafe 读成 8192，会落在歌词中间。
		text := strings.Repeat("a", 16384-5)
		content := id3Tag(4, 0, id3PlainSizeFrame("USLT", usltPayload(3, nil, []byte(text))), id3PlainSizeFrame("TIT2", append([]byte{3}, "Title"...)))
		observation := parseLyricsFixture(t, "track.mp3", content)
		requireLyrics(t, observation, text, LyricsSourceID3USLT)
		if observation.Title != "Title" {
			t.Fatalf("the frame after USLT was not parsed: %+v", observation)
		}
	})

	t.Run("whole-tag unsynchronisation in v2.3", func(t *testing.T) {
		body := bytes.Join([][]byte{id3Frame(3, "USLT", [2]byte{}, usltPayload(1, nil, utf16LE)), titleFrame(3, "Title")}, nil)
		encoded := addUnsynchronisation(body)
		if len(encoded) == len(body) {
			t.Fatal("the fixture contains nothing to unsynchronise")
		}
		header := []byte{'I', 'D', '3', 3, 0, 0x80, byte(len(encoded)>>21) & 0x7f, byte(len(encoded)>>14) & 0x7f, byte(len(encoded)>>7) & 0x7f, byte(len(encoded)) & 0x7f}
		observation := parseLyricsFixture(t, "track.mp3", append(header, encoded...))
		requireLyrics(t, observation, want, LyricsSourceID3USLT)
		if observation.Title != "Title" {
			t.Fatalf("the frame after USLT was not parsed: %+v", observation)
		}
	})

	t.Run("per-frame unsynchronisation in v2.4", func(t *testing.T) {
		payload := addUnsynchronisation(usltPayload(1, nil, utf16LE))
		if len(payload) == len(usltPayload(1, nil, utf16LE)) {
			t.Fatal("the fixture contains nothing to unsynchronise")
		}
		content := id3Tag(4, 0, id3Frame(4, "USLT", [2]byte{0, 0x02}, payload), titleFrame(4, "Title"))
		observation := parseLyricsFixture(t, "track.mp3", content)
		requireLyrics(t, observation, want, LyricsSourceID3USLT)
	})

	t.Run("data length indicator and grouping identity", func(t *testing.T) {
		payload := usltPayload(3, nil, []byte(lyrics))
		length := []byte{byte(len(payload)>>21) & 0x7f, byte(len(payload)>>14) & 0x7f, byte(len(payload)>>7) & 0x7f, byte(len(payload)) & 0x7f}
		for name, content := range map[string][]byte{
			"v2.4 data length indicator": id3Tag(4, 0, id3Frame(4, "USLT", [2]byte{0, 0x01}, append(append([]byte(nil), length...), payload...))),
			"v2.4 grouping identity":     id3Tag(4, 0, id3Frame(4, "USLT", [2]byte{0, 0x40}, append([]byte{7}, payload...))),
			"v2.4 grouping and length":   id3Tag(4, 0, id3Frame(4, "USLT", [2]byte{0, 0x41}, append(append([]byte{7}, length...), payload...))),
			"v2.3 grouping identity":     id3Tag(3, 0, id3Frame(3, "USLT", [2]byte{0, 0x20}, append([]byte{7}, payload...))),
		} {
			t.Run(name, func(t *testing.T) {
				requireLyrics(t, parseLyricsFixture(t, "track.mp3", content), want, LyricsSourceID3USLT)
			})
		}
	})

	t.Run("compressed and encrypted frames are skipped", func(t *testing.T) {
		payload := usltPayload(3, nil, []byte(lyrics))
		for name, content := range map[string][]byte{
			"v2.3 compressed": id3Tag(3, 0, id3Frame(3, "USLT", [2]byte{0, 0x80}, payload), titleFrame(3, "Title")),
			"v2.3 encrypted":  id3Tag(3, 0, id3Frame(3, "USLT", [2]byte{0, 0x40}, payload), titleFrame(3, "Title")),
			"v2.4 compressed": id3Tag(4, 0, id3Frame(4, "USLT", [2]byte{0, 0x08}, payload), titleFrame(4, "Title")),
			"v2.4 encrypted":  id3Tag(4, 0, id3Frame(4, "USLT", [2]byte{0, 0x04}, payload), titleFrame(4, "Title")),
		} {
			t.Run(name, func(t *testing.T) {
				observation := parseLyricsFixture(t, "track.mp3", content)
				requireLyrics(t, observation, "", "")
				if observation.Title != "Title" {
					t.Fatalf("the frame after the skipped one was not parsed: %+v", observation)
				}
			})
		}
	})
}

func TestMP3ParserPicksTheUSLTFrameWithAnEmptyDescriptor(t *testing.T) {
	frame := func(descriptor, text string) []byte {
		return id3Frame(3, "USLT", [2]byte{}, usltPayload(3, []byte(descriptor), []byte(text)))
	}
	for _, testCase := range []struct {
		name   string
		frames [][]byte
		want   string
	}{
		{"an empty descriptor wins when it comes second", [][]byte{frame("translation", "named"), frame("", "unnamed")}, "unnamed"},
		{"an empty descriptor wins when it comes first", [][]byte{frame("", "unnamed"), frame("translation", "named")}, "unnamed"},
		{"otherwise the first frame wins", [][]byte{frame("one", "first"), frame("two", "second")}, "first"},
		{"the first empty descriptor wins", [][]byte{frame("", "first"), frame("", "second")}, "first"},
		{"a BOM-only UTF-16 descriptor counts as empty", [][]byte{frame("named", "named"), id3Frame(3, "USLT", [2]byte{}, usltPayload(1, []byte{0xff, 0xfe}, repairUTF16("unnamed", true)))}, "unnamed"},
		{"an invalid frame does not hide a later valid one", [][]byte{frame("", ""), frame("named", "named")}, "named"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			observation := parseLyricsFixture(t, "track.mp3", id3Tag(3, 0, testCase.frames...))
			requireLyrics(t, observation, testCase.want, LyricsSourceID3USLT)
		})
	}
}

func TestMP3ParserIgnoresMalformedUSLTFrames(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		payload []byte
	}{
		{"shorter than the fixed header", []byte{3, 'e', 'n'}},
		{"unknown encoding", usltPayload(9, nil, []byte("text"))},
		{"no descriptor terminator", append([]byte{3, 'e', 'n', 'g'}, []byte("never terminated")...)},
		{"UTF-16 without a terminator", append([]byte{1, 'e', 'n', 'g'}, 0xff, 0xfe, 'a', 0)},
		{"odd-length UTF-16 text", usltPayload(1, nil, append(repairUTF16("text", true), 'x'))},
		{"unpaired UTF-16 surrogate", usltPayload(2, nil, []byte{0xd8, 0x00, 0x00, 0x41})},
		{"invalid UTF-8 text", usltPayload(3, nil, []byte("ab\xffcd"))},
		{"blank text", usltPayload(3, nil, []byte(" \r\n\t"))},
		{"text over the limit", usltPayload(3, nil, bytes.Repeat([]byte("a"), MaxLyricsBytes+1))},
		{"text far over the limit", usltPayload(3, nil, bytes.Repeat([]byte("a"), maxLyricsFrameTextBytes+1))},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			content := id3Tag(3, 0, id3Frame(3, "USLT", [2]byte{}, testCase.payload), titleFrame(3, "Title"))
			observation := parseLyricsFixture(t, "track.mp3", content)
			requireLyrics(t, observation, "", "")
			if observation.Title != "Title" {
				t.Fatalf("parsing stopped at the malformed frame: %+v", observation)
			}
		})
	}
}

func TestID3FrameSizeFollowsTheTagVersion(t *testing.T) {
	filler := func(count int) []byte { return bytes.Repeat([]byte("a"), count) }
	nextFrame := append([]byte("TALB"), make([]byte, 6)...)
	// 大小字节 00 00 01 00：按普通整数是 256，按 synchsafe 整数是 128。
	frame := func(size []byte, parts ...[]byte) []byte {
		return append(append([]byte("TIT2"), size...), append([]byte{0, 0}, bytes.Join(parts, nil)...)...)
	}
	ambiguous := []byte{0, 0, 1, 0}
	for _, testCase := range []struct {
		name  string
		major byte
		data  []byte
		want  int
	}{
		{"v2.3 sizes are plain", 3, frame(ambiguous, filler(300)), 256},
		{"v2.4 sizes are synchsafe when the boundary after them is valid", 4, frame(ambiguous, filler(128), nextFrame), 128},
		{"v2.4 reads a plain size when only its boundary is valid", 4, frame(ambiguous, filler(256), nextFrame), 256},
		{"v2.4 follows the specification when neither boundary is valid", 4, frame(ambiguous, filler(300)), 128},
		{"the end of the tag is a valid boundary", 4, frame(ambiguous, filler(128)), 128},
		{"padding is a valid boundary", 4, frame(ambiguous, filler(128), make([]byte, 10)), 128},
		{"a v2.4 size byte of 0x80 or more can only be a plain size", 4, frame([]byte{0, 0, 1, 0xe0}, filler(480)), 0x1e0},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := id3FrameSize(testCase.data, 0, testCase.major); got != testCase.want {
				t.Fatalf("id3FrameSize = %d, want %d", got, testCase.want)
			}
		})
	}
}

func TestRemoveUnsynchronisation(t *testing.T) {
	for _, testCase := range []struct {
		name string
		data []byte
		want []byte
	}{
		{"nothing to remove", []byte{1, 2, 0xff, 0xfe}, []byte{1, 2, 0xff, 0xfe}},
		{"a zero after 0xFF is removed", []byte{0xff, 0, 0xfe, 1}, []byte{0xff, 0xfe, 1}},
		{"0xFF 0x00 0x00 keeps the real zero", []byte{0xff, 0, 0}, []byte{0xff, 0}},
		{"consecutive pairs", []byte{0xff, 0, 0xff, 0, 0x41}, []byte{0xff, 0xff, 0x41}},
		{"a trailing 0xFF 0x00", []byte{1, 0xff, 0}, []byte{1, 0xff}},
		{"a trailing lone 0xFF", []byte{1, 0xff}, []byte{1, 0xff}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := removeUnsynchronisation(append([]byte(nil), testCase.data...))
			if !bytes.Equal(got, testCase.want) {
				t.Fatalf("removeUnsynchronisation(% x) = % x, want % x", testCase.data, got, testCase.want)
			}
		})
	}
}
