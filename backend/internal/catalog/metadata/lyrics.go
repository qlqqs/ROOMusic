package metadata

import (
	"strings"
	"unicode/utf8"
)

// MaxLyricsBytes 是保存的歌词原文（规范化之后）的上限。
const MaxLyricsBytes = 64 << 10

// NormalizeLyrics 规范化歌词原文：去掉 UTF-8 BOM；`\r\n` 与 `\r` 改为 `\n`；删除 NUL 与除 `\n`、`\t` 外的 C0 控制字符；
// 去掉首尾空白。不是合法 UTF-8、规范化后为空或超过 MaxLyricsBytes 时返回 false：歌词缺失不影响曲目可用性，所以不记诊断。
// 它保留换行与行内空白（与曲目标题等字段使用的 BoundedText 不同），LRC 的行结构要靠它。
func NormalizeLyrics(raw string) (string, bool) {
	if !utf8.ValidString(raw) {
		return "", false
	}
	raw = strings.TrimPrefix(raw, "\ufeff")
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	var builder strings.Builder
	builder.Grow(len(raw))
	for _, character := range raw {
		if character < 0x20 && character != '\n' && character != '\t' {
			continue
		}
		builder.WriteRune(character)
	}
	normalized := strings.TrimSpace(builder.String())
	if normalized == "" || len(normalized) > MaxLyricsBytes {
		return "", false
	}
	return normalized, true
}
