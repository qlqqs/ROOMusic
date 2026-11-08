// Package metadata 收纳元数据值的规则：空白规范化、有界文本、署名，以及艺人名的保守拆分。
// 它是叶子包，只依赖标准库，供解析器与 catalog 共同使用。
package metadata

import (
	"sort"
	"strings"
	"unicode"
)

// artistSeparatorRules 与已确认的 V0 scanner 保持同一组保守分隔语义。
// 它只解释明确的 credit 分隔符；已知固定组合名会在紧凑分隔规则之前保留整体。
var artistSeparatorRules = []string{"; ", "；", " / ", " feat. ", " ft. ", " with ", "、"}

var knownFixedArtistGroups = map[string]bool{
	"simon & garfunkel":  true,
	"earth, wind & fire": true,
	"ac/dc":              true,
}

func splitArtistNames(raw string) []string {
	// 换行和 NUL 先分隔多值，再逐段完整拆分，避免清理控制字符时粘连姓名。
	if strings.ContainsAny(raw, "\r\n\x00") {
		var names []string
		for _, value := range splitArtistValues(raw) {
			names = append(names, splitArtistNames(value)...)
		}
		return names
	}
	// 清除其余控制字符，但多值聚合串不能套单值截断预算。
	raw = strings.TrimSpace(strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return -1
		}
		return character
	}, raw))
	if raw == "" {
		return nil
	}
	for _, separator := range artistSeparatorRules {
		if parts := splitArtistBySeparator(raw, separator); len(parts) > 1 {
			return flattenArtistNames(parts)
		}
	}

	if index := strings.Index(raw, " & "); index > 0 {
		left := strings.TrimSpace(raw[:index])
		right := strings.TrimSpace(raw[index+3:])
		if len(left) >= 2 && len(right) >= 2 && strings.ContainsRune(left, ' ') && strings.ContainsRune(right, ' ') {
			return flattenArtistNames([]string{left, right})
		}
	}
	if isKnownFixedArtistGroup(raw) || isLatinSurnameFirstArtist(raw) {
		return []string{raw}
	}
	for _, separator := range []string{"/", ",", "&"} {
		parts := splitArtistBySeparator(raw, separator)
		if len(parts) <= 1 {
			continue
		}
		if separator == "&" && isLikelyFixedAmpersandGroup(parts) {
			return []string{raw}
		}
		return flattenArtistNames(parts)
	}
	return []string{raw}
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

func flattenArtistNames(parts []string) []string {
	flattened := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if isKnownFixedArtistGroup(part) {
			flattened = append(flattened, part)
			continue
		}
		nested := splitNestedArtistName(part)
		if len(nested) <= 1 {
			flattened = append(flattened, part)
			continue
		}
		flattened = append(flattened, flattenArtistNames(nested)...)
	}
	if len(flattened) == 0 {
		return parts
	}
	return flattened
}

func splitNestedArtistName(raw string) []string {
	if isKnownFixedArtistGroup(raw) {
		return []string{raw}
	}
	for _, separator := range artistSeparatorRules {
		if parts := splitArtistBySeparator(raw, separator); len(parts) > 1 {
			return parts
		}
	}
	if isLatinSurnameFirstArtist(raw) {
		return []string{raw}
	}
	for _, separator := range []string{"/", ",", "&"} {
		parts := splitArtistBySeparator(raw, separator)
		if len(parts) <= 1 {
			continue
		}
		if separator == "&" && isLikelyFixedAmpersandGroup(parts) {
			return []string{raw}
		}
		return parts
	}
	return []string{raw}
}

// isLatinSurnameFirstArtist 只保护满足字母、词数和非团体词条件的单逗号姓名。
func isLatinSurnameFirstArtist(raw string) bool {
	if strings.Count(raw, ",") != 1 {
		return false
	}
	left, right, _ := strings.Cut(raw, ",")
	return isLatinArtistNamePart(strings.TrimSpace(left), 3) && isLatinArtistNamePart(strings.TrimSpace(right), 4)
}

func isLatinArtistNamePart(raw string, maxWords int) bool {
	words := strings.Fields(raw)
	if len(words) == 0 || len(words) > maxWords {
		return false
	}
	hasLatinLetter := false
	for _, character := range raw {
		switch {
		case unicode.Is(unicode.Latin, character):
			hasLatinLetter = true
		case unicode.Is(unicode.Mn, character), unicode.IsSpace(character):
		case character == '.', character == '-', character == '\'', character == '’':
		default:
			return false
		}
	}
	if !hasLatinLetter {
		return false
	}
	for _, word := range words {
		word = strings.ToLower(word)
		for _, prefix := range []string{
			"orchest", "philharm", "symphon", "sinfon", "ensemble", "quartet", "quatuor",
			"trio", "choir", "chorus", "chor", "consort", "singers", "players", "band",
		} {
			if strings.HasPrefix(word, prefix) {
				return false
			}
		}
	}
	return true
}

func splitArtistBySeparator(raw, separator string) []string {
	if !strings.Contains(raw, separator) {
		return nil
	}
	parts := strings.Split(raw, separator)
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	if len(result) <= 1 {
		return nil
	}
	return result
}

func isLikelyFixedAmpersandGroup(parts []string) bool {
	if len(parts) != 2 {
		return false
	}
	left, right := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	return left != "" && right != "" && isKnownFixedArtistGroup(left+" & "+right)
}

func isKnownFixedArtistGroup(raw string) bool {
	return knownFixedArtistGroups[strings.ToLower(strings.TrimSpace(raw))]
}
