package parser

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestDecodeExternalLyrics(t *testing.T) {
	const raw = " \r\n[00:01.00]你好𠀀\x00\r\n第二行\r "
	const want = "[00:01.00]你好𠀀\n第二行"
	encodeUTF16 := func(order binary.AppendByteOrder, bom []byte) []byte {
		out := append([]byte{}, bom...)
		for _, unit := range utf16.Encode([]rune(raw)) {
			out = order.AppendUint16(out, unit)
		}
		return out
	}
	gb, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		data []byte
		want string
		ok   bool
	}{
		{"UTF-8", []byte(raw), want, true},
		{"UTF-8 BOM", append([]byte{0xef, 0xbb, 0xbf}, []byte(raw)...), want, true},
		{"UTF-16LE BOM", encodeUTF16(binary.LittleEndian, []byte{0xff, 0xfe}), want, true},
		{"UTF-16BE BOM", encodeUTF16(binary.BigEndian, []byte{0xfe, 0xff}), want, true},
		{"GB18030", gb, want, true},
		{"GBK", []byte{0xc4, 0xe3, 0xba, 0xc3}, "你好", true},
		{"UTF-32LE BOM", []byte{0xff, 0xfe, 0, 0, 'a', 0, 0, 0}, "", false},
		{"UTF-32BE BOM", []byte{0, 0, 0xfe, 0xff, 0, 0, 0, 'a'}, "", false},
		{"UTF-8 BOM 后非法字节不转 GB18030", []byte{0xef, 0xbb, 0xbf, 0xc4, 0xe3}, "", false},
		{"UTF-16 奇数长度", []byte{0xff, 0xfe, 'a'}, "", false},
		{"UTF-16LE 孤立高代理", []byte{0xff, 0xfe, 0, 0xd8}, "", false},
		{"UTF-16BE 孤立低代理", []byte{0xfe, 0xff, 0xdc, 0}, "", false},
		{"UTF-16 非法代理对", []byte{0xff, 0xfe, 0, 0xd8, 'a', 0}, "", false},
		{"GB18030 解码产生替换字符", []byte{'a', 0x81}, "", false},
		{"空文件", nil, "", false},
		{"规范化后为空", []byte("\ufeff\r\n\x00 \t"), "", false},
		{"恰好 256 KiB", append(bytes.Repeat([]byte{0}, MaxExternalLyricsBytes-1), 'a'), "a", true},
		{"超过 256 KiB", append(bytes.Repeat([]byte{0}, MaxExternalLyricsBytes), 'a'), "", false},
		{"规范化后恰好 64 KiB", []byte(strings.Repeat("a", MaxLyricsBytes) + "\r\n\x00"), strings.Repeat("a", MaxLyricsBytes), true},
		{"规范化后超过 64 KiB", []byte(strings.Repeat("a", MaxLyricsBytes+1)), "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := DecodeExternalLyrics(tc.data)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("歌词=%q ok=%v，期望 %q %v", truncateForLog(got), ok, truncateForLog(tc.want), tc.ok)
			}
		})
	}
}

func TestApplyExternalLyrics(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		inferred   bool
		tags, want []metadata.Credit
	}{
		{name: "外置署名替换内嵌并保留其他来源", text: "[00:00.00]作词：外置作词\n作曲：外置作曲\n正文", want: []metadata.Credit{{Role: "lyricist", Name: "外置作词", Source: "lyrics"}, {Role: "composer", Name: "外置作曲", Source: "lyrics"}}},
		{name: "标签角色整体优先", text: "作词：外置甲/外置乙\n作曲：外置作曲", tags: []metadata.Credit{{Role: "lyricist", Name: "标签作词", Source: "tag"}}, want: []metadata.Credit{{Role: "lyricist", Name: "标签作词", Source: "tag"}, {Role: "composer", Name: "外置作曲", Source: "lyrics"}}},
		{name: "真实标题可跳题头", text: "Song - Artist\n作词：外置作词", want: []metadata.Credit{{Role: "lyricist", Name: "外置作词", Source: "lyrics"}}},
		{name: "补位标题不跳题头", text: "Song - Artist\n作词：不能读取", inferred: true},
		{name: "没有署名也移除旧署名", text: "[00:01.00]正文"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := AudioObservation{Title: "Song", Lyrics: "作词：旧作词", LyricsSource: LyricsSourceVorbisLyrics, InferredFields: map[string]bool{"title": tc.inferred}}
			// 预留容量，能发现原地过滤或 append 改写缓存共享底层数组。
			original.Credits = make([]metadata.Credit, 0, 16)
			original.Credits = append(original.Credits, metadata.Credit{Role: "lyricist", Name: "旧作词", Source: "lyrics"}, metadata.Credit{Role: "performer", Name: "标签演奏", Source: "tag"})
			original.Credits = append(original.Credits, tc.tags...)
			before := append([]metadata.Credit{}, original.Credits...)
			observation := original
			ApplyExternalLyrics(&observation, tc.text)
			requireLyrics(t, observation, tc.text, LyricsSourceLRCFile)
			expected := append(append([]metadata.Credit{}, tc.want...), metadata.Credit{Role: "performer", Name: "标签演奏", Source: "tag"})
			requireCredits(t, observation.Credits, metadata.CanonicalCredits(expected))
			if !reflect.DeepEqual(original.Credits, before) || original.Lyrics != "作词：旧作词" || original.LyricsSource != LyricsSourceVorbisLyrics {
				t.Fatal("应用外置歌词改写了原观察")
			}
			if observation.Title != original.Title || observation.InferredFields["title"] != tc.inferred {
				t.Fatal("应用外置歌词改写了标题事实")
			}
		})
	}
}
