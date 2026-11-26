// Package metadata 收纳元数据值的规则：空白规范化、有界文本、署名，以及艺人名的保守拆分。
// 它是叶子包，只依赖标准库，供解析器与 catalog 共同使用。
package metadata

import (
	"sort"
	"strings"
	"unicode"
)

var artistSeparatorRules = []string{";", "；", " / ", " feat. ", " ft. ", " with ", "、"}

// SplitExplicitArtistValues 只在半角或全角括号外按换行、NUL 与明确分隔符切段。
// 括号深度仅决定切段位置，不解释括号语义，也不处理紧凑分隔符或姓名保护。
func SplitExplicitArtistValues(raw string) []string {
	result := make([]string, 0)
	start, depth := 0, 0
	for offset, character := range raw {
		if offset < start {
			continue
		}
		switch character {
		case '(', '（':
			depth++
		case ')', '）':
			if depth > 0 {
				depth--
			}
		}
		if depth > 0 {
			continue
		}
		separatorLength := 0
		if character == '\r' || character == '\n' || character == '\x00' {
			separatorLength = 1
		} else {
			for _, separator := range artistSeparatorRules {
				if strings.HasPrefix(raw[offset:], separator) {
					separatorLength = len(separator)
					break
				}
			}
		}
		if separatorLength > 0 {
			if part := strings.TrimSpace(raw[start:offset]); part != "" {
				result = append(result, part)
			}
			start = offset + separatorLength
		}
	}
	if part := strings.TrimSpace(raw[start:]); part != "" {
		result = append(result, part)
	}
	return result
}

func splitArtistNames(raw string) []string {
	raw = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\r' && r != '\n' && r != 0 {
			return -1
		}
		return r
	}, raw)
	return SplitExplicitArtistValues(raw)
}

// CanonicalArtistNames 只保守拆分并按原写法精确去重，不解析艺人身份或别名。
// 调用方须先校验原始单值预算；合成展示串可包含多个有界姓名，不按整串截断。
func CanonicalArtistNames(raw string) []string {
	parts := splitArtistNames(raw)
	result := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		name := BoundedText(part)
		key := name
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, name)
	}
	return result
}

// JoinArtistValues 展开换行和 NUL 多值，逐值清理、按原写法去重，保留首次顺序。
func JoinArtistValues(values ...string) string {
	var result []string
	seen := make(map[string]bool)
	for _, value := range values {
		for _, part := range splitArtistValues(value) {
			name := BoundedText(part)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			result = append(result, name)
		}
	}
	return strings.Join(result, " / ")
}

func splitArtistValues(raw string) []string {
	return strings.FieldsFunc(raw, func(character rune) bool {
		return character == '\r' || character == '\n' || character == '\x00'
	})
}

func CanonicalTrackArtist(raw string) string {
	names := CanonicalArtistNames(raw)
	sort.Strings(names)
	return strings.Join(names, " / ")
}
