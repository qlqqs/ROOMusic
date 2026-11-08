package ingest

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// discMarker 保留碟号标记两侧的文字，供目录标题与专辑标题分别使用。
type discMarker struct {
	Number         int
	Prefix, Suffix string
	AtStart        bool
}

var discMarkerPattern = regexp.MustCompile(`(?i)(?:^|[^0-9A-Za-z_])(([\(\[\{<]\s*)?(disc|disk|cd)[\s_.\-]*(?:\(\s*)?([0-9]{1,3}|[A-Za-z]+)(\s*(?:of|/)\s*[0-9]{1,3})?(\s*\))?(\s*[)\]}>])?)(?:$|[^0-9A-Za-z_])`)
var romanDiscPattern = regexp.MustCompile(`^(xc|xl|l?x{0,3})(ix|iv|v?i{0,3})$`)

var discNumberWords = []string{
	"one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten",
	"eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen", "seventeen", "eighteen", "nineteen", "twenty",
}

// parseDiscMarker 从左到右寻找有效碟号，允许无效编号文字中包含下一处标记。
func parseDiscMarker(name string) (discMarker, bool) {
	name = strings.TrimSpace(name)
	for offset := 0; offset < len(name); {
		match := discMarkerPattern.FindStringSubmatchIndex(name[offset:])
		if match == nil {
			break
		}
		start, end := offset+match[2], offset+match[3]
		numberText := name[offset+match[8] : offset+match[9]]
		number := parseDiscNumber(numberText)
		if number < 1 || number > 99 {
			// 从关键字末字母继续，既不跳过编号中的下一处标记（CD Disc Two），
			// 也保留左边界上下文，避免把连写的 CDDisc II 误认成 Disc II。
			offset += match[7] - 1
			continue
		}
		prefix, suffix := trimDiscRemainder(name[:start]), trimDiscRemainder(name[end:])
		return discMarker{Number: number, Prefix: prefix, Suffix: suffix, AtStart: prefix == ""}, true
	}
	return discMarker{}, false
}

func parseDiscNumber(text string) int {
	if number, err := strconv.Atoi(text); err == nil {
		return number
	}
	text = strings.ToLower(text)
	for index, word := range discNumberWords {
		if text == word {
			return index + 1
		}
	}
	if text == "" || !romanDiscPattern.MatchString(text) {
		return 0
	}
	values := map[byte]int{'i': 1, 'v': 5, 'x': 10, 'l': 50, 'c': 100}
	number, previous := 0, 0
	for index := len(text) - 1; index >= 0; index-- {
		value := values[text[index]]
		if value < previous {
			number -= value
		} else {
			number += value
		}
		previous = value
	}
	return number
}

func trimDiscRemainder(text string) string {
	for {
		trimmed := strings.TrimFunc(text, func(r rune) bool {
			return unicode.IsSpace(r) || strings.ContainsRune("-_.,:;~·–—", r)
		})
		for _, pair := range []string{"()", "[]", "{}", "<>"} {
			if strings.Count(trimmed, pair[:1]) > strings.Count(trimmed, pair[1:]) {
				trimmed = strings.Trim(trimmed, pair[:1])
			} else if strings.Count(trimmed, pair[1:]) > strings.Count(trimmed, pair[:1]) {
				trimmed = strings.Trim(trimmed, pair[1:])
			}
		}
		if text == trimmed {
			return trimmed
		}
		text = trimmed
	}
}

// stripAlbumDiscMarker 保留开头标记的后缀，否则从标记处截断；不产生空专辑名。
func stripAlbumDiscMarker(album string) (stripped string, disc int, ok bool) {
	marker, found := parseDiscMarker(album)
	if !found {
		return album, 0, false
	}
	stripped = marker.Prefix
	if marker.AtStart {
		stripped = marker.Suffix
	}
	if stripped == "" {
		return album, 0, false
	}
	return stripped, marker.Number, true
}

func isBonusRemainder(text string) bool {
	text = strings.ToLower(text)
	for _, word := range []string{"特典", "初回特典", "附赠", "附录", "赠品", "おまけ"} {
		if strings.Contains(text, word) {
			return true
		}
	}
	words := strings.FieldsFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("-_.,:;~·–—()[]{}<>", r)
	})
	for _, word := range words {
		for _, bonus := range []string{"bonus", "extra", "special"} {
			if strings.HasPrefix(word, bonus) {
				return true
			}
		}
	}
	return false
}
