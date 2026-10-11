package catalog

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

type parsedLine struct {
	start int64 // -1 表示没有时间（plain）
	text  string
}

func describeLyrics(lyrics Lyrics) []parsedLine {
	lines := make([]parsedLine, 0, len(lyrics.Lines))
	for _, line := range lyrics.Lines {
		start := int64(-1)
		if line.StartMS != nil {
			start = *line.StartMS
		}
		lines = append(lines, parsedLine{start: start, text: line.Text})
	}
	return lines
}

func requireLyricsLines(t *testing.T, raw, wantKind string, want []parsedLine) {
	t.Helper()
	lyrics, ok := ParseLyrics(raw)
	if !ok {
		t.Fatalf("ParseLyrics(%q) reported no lyrics, want %s %v", raw, wantKind, want)
	}
	if lyrics.Kind != wantKind || lyrics.Source != LyricsSourceEmbedded {
		t.Fatalf("ParseLyrics(%q) = %s/%s, want %s/%s", raw, lyrics.Kind, lyrics.Source, wantKind, LyricsSourceEmbedded)
	}
	got := describeLyrics(lyrics)
	if len(got) != len(want) {
		t.Fatalf("ParseLyrics(%q) lines = %v, want %v", raw, got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("ParseLyrics(%q) line %d = %v, want %v (all: %v)", raw, index, got[index], want[index], got)
		}
	}
}

func TestParseLyricsSynced(t *testing.T) {
	for _, testCase := range []struct {
		name string
		raw  string
		want []parsedLine
	}{
		{"basic lines", "[00:01.00]one\n[00:02.50]two", []parsedLine{{1000, "one"}, {2500, "two"}}},
		{"every fraction form", "[00:01.5]a\n[00:02.55]b\n[00:03.555]c\n[00:04:55]d\n[00:05]e",
			[]parsedLine{{1500, "a"}, {2550, "b"}, {3555, "c"}, {4550, "d"}, {5000, "e"}}},
		{"one to three minute digits", "[1:05.00]a\n[12:00.00]b\n[100:00.00]c", []parsedLine{{65000, "a"}, {720000, "b"}, {6000000, "c"}}},
		{"four minute digits are not a timestamp", "[1000:00.00]late\n[00:01.00]ok", []parsedLine{{1000, "ok"}}},
		{"seconds of 60 or more are not a timestamp", "[00:61.00]bad\n[00:01.00]ok", []parsedLine{{1000, "ok"}}},
		{"several timestamps expand into several lines", "[00:10.00][01:20.00]chorus\n[00:20.00]verse",
			[]parsedLine{{10000, "chorus"}, {20000, "verse"}, {80000, "chorus"}}},
		{"lines are sorted by time", "[00:30.00]c\n[00:10.00]a\n[00:20.00]b", []parsedLine{{10000, "a"}, {20000, "b"}, {30000, "c"}}},
		{"meta tag lines are removed", "[ti:Title]\n[ar:Artist]\n[al:Album]\n[by:someone]\n[length:03:20]\n[00:01.00]x", []parsedLine{{1000, "x"}}},
		{"lines without a timestamp are dropped", "a title line\n[00:01.00]sung\nplain again", []parsedLine{{1000, "sung"}}},
		{"an empty line with a timestamp is an interlude", "[00:30.00]\n[00:31.00]  \n[00:32.00]after", []parsedLine{{30000, ""}, {31000, ""}, {32000, "after"}}},
		{"text is trimmed", "  [00:01.00]   spaced out \t ", []parsedLine{{1000, "spaced out"}}},
		{"word marks are removed", "[00:01.00]<00:01.00>he<00:01.50>llo <00:02.00>world", []parsedLine{{1000, "hello world"}}},
		{"equal times keep their order", "[00:05.00]English line\n[00:05.00]中文翻译\n[00:01.00]before\n[00:05.00]third",
			[]parsedLine{{1000, "before"}, {5000, "English line"}, {5000, "中文翻译"}, {5000, "third"}}},
		{"CRLF line endings", "[00:01.00]a\r\n[00:02.00]b\r\n", []parsedLine{{1000, "a"}, {2000, "b"}}},
		{"the last minute digit boundary", "[999:59.999]end", []parsedLine{{59999999, "end"}}},
		{"text may contain brackets", "[00:01.00]see [note] and [00:02.00]", []parsedLine{{1000, "see [note] and [00:02.00]"}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			requireLyricsLines(t, testCase.raw, LyricsKindSynced, testCase.want)
		})
	}
}

func TestParseLyricsOffset(t *testing.T) {
	for _, testCase := range []struct {
		name string
		raw  string
		want []parsedLine
	}{
		// 正的 offset 让歌词提前出现：开始时间 = 时间戳 − offset，小于 0 取 0。
		{"positive offset shifts lines earlier and clamps at zero", "[offset:+500]\n[00:01.00]a\n[00:00.20]b", []parsedLine{{0, "b"}, {500, "a"}}},
		{"negative offset shifts lines later", "[offset:-1000]\n[00:01.00]a", []parsedLine{{2000, "a"}}},
		{"the key is case insensitive and the value may have spaces", "[Offset: 300 ]\n[00:01.00]a", []parsedLine{{700, "a"}}},
		{"an offset after the lines still applies", "[00:01.00]a\n[offset:200]", []parsedLine{{800, "a"}}},
		{"the first valid offset wins", "[offset:100]\n[offset:900]\n[00:01.00]a", []parsedLine{{900, "a"}}},
		{"an invalid offset is ignored and the next valid one applies", "[offset:abc]\n[offset:100]\n[00:01.00]a", []parsedLine{{900, "a"}}},
		{"a fractional offset is invalid", "[offset:1.5]\n[00:01.00]a", []parsedLine{{1000, "a"}}},
		{"an offset beyond 24 hours is ignored", "[offset:999999999]\n[00:01.00]a", []parsedLine{{1000, "a"}}},
		{"lines whose time exceeds 24 hours after the offset are dropped", "[offset:-80000000]\n[999:00.00]x\n[00:01.00]y", []parsedLine{{80001000, "y"}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			requireLyricsLines(t, testCase.raw, LyricsKindSynced, testCase.want)
		})
	}
}

func TestParseLyricsPlain(t *testing.T) {
	for _, testCase := range []struct {
		name string
		raw  string
		want []parsedLine
	}{
		{"paragraphs are kept as single blank lines", "first line\n\nsecond paragraph\nstill second\n\n\n\nthird",
			[]parsedLine{{-1, "first line"}, {-1, ""}, {-1, "second paragraph"}, {-1, "still second"}, {-1, ""}, {-1, "third"}}},
		{"leading and trailing blank lines are removed", "\n\n  \nonly\n\n \n", []parsedLine{{-1, "only"}}},
		{"trailing whitespace is trimmed and indentation is kept", "  indented  \t\nplain", []parsedLine{{-1, "  indented"}, {-1, "plain"}}},
		{"meta tag lines are removed without leaving blank lines", "[ti:x]\nreal\n[ar:y]\nlines", []parsedLine{{-1, "real"}, {-1, "lines"}}},
		{"section labels are text, not meta tags", "[Chorus]\nla la\n[Verse 1: Someone]", []parsedLine{{-1, "[Chorus]"}, {-1, "la la"}, {-1, "[Verse 1: Someone]"}}},
		{"CRLF line endings", "a\r\nb\r\n\r\nc", []parsedLine{{-1, "a"}, {-1, "b"}, {-1, ""}, {-1, "c"}}},
		{"unrecognised timestamps make the whole text plain", "[00:99.00]a\n[xx:yy]b\n[1000:00.00]c", []parsedLine{{-1, "[00:99.00]a"}, {-1, "[xx:yy]b"}, {-1, "[1000:00.00]c"}}},
		{"word marks are left alone in plain text", "a <3:45> b", []parsedLine{{-1, "a <3:45> b"}}},
		// 过滤后一行不剩时按纯文本显示，并去掉行首的合法时间戳，让文本可读。
		{"synced lines that are all dropped fall back to plain text", "[offset:-86400000]\n[00:30.00]late\n[00:31.00][00:32.00]later", []parsedLine{{-1, "late"}, {-1, "later"}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			requireLyricsLines(t, testCase.raw, LyricsKindPlain, testCase.want)
		})
	}
}

func TestParseLyricsWithNothingToShow(t *testing.T) {
	for name, raw := range map[string]string{
		"empty":           "",
		"blank":           " \n\t\n",
		"only meta tags":  "[ti:Title]\n[ar:Artist]\n[offset:100]",
		"meta and blanks": "\n[ti:Title]\n\n[by:me]\n",
	} {
		t.Run(name, func(t *testing.T) {
			if lyrics, ok := ParseLyrics(raw); ok {
				t.Fatalf("ParseLyrics(%q) = %+v, want no lyrics", raw, lyrics)
			}
		})
	}
}

func TestParseLyricsLimits(t *testing.T) {
	t.Run("synced lines are capped after sorting", func(t *testing.T) {
		var raw strings.Builder
		// 倒序写入：保留的是时间最早的 2,000 行。
		for index := 2499; index >= 0; index-- {
			fmt.Fprintf(&raw, "[%02d:%02d.00]line %d\n", index/60, index%60, index)
		}
		lyrics, ok := ParseLyrics(raw.String())
		if !ok || lyrics.Kind != LyricsKindSynced || len(lyrics.Lines) != maxLyricsLines {
			t.Fatalf("lines = %d (%s), want %d synced", len(lyrics.Lines), lyrics.Kind, maxLyricsLines)
		}
		if first, last := lyrics.Lines[0], lyrics.Lines[len(lyrics.Lines)-1]; *first.StartMS != 0 || first.Text != "line 0" || *last.StartMS != 1999000 || last.Text != "line 1999" {
			t.Fatalf("first = %+v, last = %+v", first, last)
		}
	})

	t.Run("plain lines are capped and a trailing blank line is trimmed", func(t *testing.T) {
		var raw strings.Builder
		for index := 0; index < 1999; index++ {
			fmt.Fprintf(&raw, "line %d\n", index)
		}
		// 第 2,000 行是空行，其后还有内容：截到 2,000 行后，结尾的空行不保留。
		raw.WriteString("\nbeyond the cap\nand more\n")
		lyrics, ok := ParseLyrics(raw.String())
		if !ok || lyrics.Kind != LyricsKindPlain || len(lyrics.Lines) != 1999 {
			t.Fatalf("lines = %d (%s), want 1999 plain", len(lyrics.Lines), lyrics.Kind)
		}
		if last := lyrics.Lines[len(lyrics.Lines)-1]; last.Text != "line 1998" {
			t.Fatalf("last line = %q", last.Text)
		}
	})

	t.Run("a long line is cut to 1,000 characters without splitting a character", func(t *testing.T) {
		for name, unit := range map[string]string{"ASCII": "a", "CJK": "歌", "emoji": "🎵"} {
			t.Run(name, func(t *testing.T) {
				for _, raw := range []string{"[00:01.00]" + strings.Repeat(unit, 1500), strings.Repeat(unit, 1500)} {
					lyrics, ok := ParseLyrics(raw)
					if !ok || len(lyrics.Lines) != 1 {
						t.Fatalf("ParseLyrics = %+v, %v", lyrics, ok)
					}
					text := lyrics.Lines[0].Text
					if utf8.RuneCountInString(text) != maxLyricsLineRunes || !utf8.ValidString(text) || text != strings.Repeat(unit, maxLyricsLineRunes) {
						t.Fatalf("text has %d characters (valid UTF-8: %v)", utf8.RuneCountInString(text), utf8.ValidString(text))
					}
				}
			})
		}
	})

	t.Run("a line of exactly 1,000 characters is kept whole", func(t *testing.T) {
		lyrics, _ := ParseLyrics(strings.Repeat("歌", maxLyricsLineRunes))
		if utf8.RuneCountInString(lyrics.Lines[0].Text) != maxLyricsLineRunes {
			t.Fatalf("text has %d characters", utf8.RuneCountInString(lyrics.Lines[0].Text))
		}
	})
}

func TestLyricsJSONShape(t *testing.T) {
	synced, _ := ParseLyrics("[00:12.34]hello")
	plain, _ := ParseLyrics("hello\n\nworld")
	for name, testCase := range map[string]struct {
		value any
		want  string
	}{
		"synced":    {TrackLyrics{TrackID: "t", Lyrics: &synced}, `{"track_id":"t","lyrics":{"kind":"synced","source":"embedded","lines":[{"start_ms":12340,"text":"hello"}]}}`},
		"plain":     {TrackLyrics{TrackID: "t", Lyrics: &plain}, `{"track_id":"t","lyrics":{"kind":"plain","source":"embedded","lines":[{"start_ms":null,"text":"hello"},{"start_ms":null,"text":""},{"start_ms":null,"text":"world"}]}}`},
		"no lyrics": {TrackLyrics{TrackID: "t"}, `{"track_id":"t","lyrics":null}`},
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(testCase.value)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(encoded) != testCase.want {
				t.Fatalf("JSON = %s, want %s", encoded, testCase.want)
			}
		})
	}
}
