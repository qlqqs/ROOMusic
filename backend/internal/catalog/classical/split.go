package classical

import (
	"strconv"
	"strings"
	"unicode"
)

// WorkSplit 中 Group 从 1 起；0 表示未产生作品信息。
type WorkSplit struct {
	Work, Movement string
	Group          int
}

const (
	largeSeparators    = "),-/:;\\：，；）／＼－—"
	joinedSeparators   = ",.;:-/\\)：，；）／＼－—"
	fullSeparators     = "：，；）／＼－—"
	workTrim           = " ,.;:-/\\：，；）／＼－—"
	movementSeparators = ",;:-，；：－—"
)

// Segments 保留段尾分隔符；没有打开空格闸门时不产出段。
func Segments(title string) []string {
	text := []rune(title)
	if len(text) == 0 {
		return []string{""}
	}
	var result []string
	i, start := 1, 0
	gate, abbreviation := false, false
	for i < len(text) {
		c := text[i]
		switch {
		case strings.ContainsRune(largeSeparators, c):
			if strings.ContainsRune(fullSeparators, c) {
				gate = true
			}
			i++
			if c == '-' {
				if runePrefixFold(text[i:], "flat") {
					i += 4
					continue
				}
				if runePrefixFold(text[i:], "sharp") {
					i += 5
					continue
				}
			}
			if c == ':' && i == 2 && unicode.IsDigit(text[0]) {
				continue
			}
			for i < len(text) && strings.ContainsRune(joinedSeparators, text[i]) {
				if strings.ContainsRune(fullSeparators, text[i]) {
					gate = true
				}
				i++
			}
			if gate && !(text[i-1] == ':' && text[i-2] == ':') {
				result = append(result, string(text[start:i]))
				start = i
			}
		case c == '.':
			skip := i < 4
			if i >= 2 && asciiLetter(text[i-2]) && asciiLetter(text[i-1]) {
				switch strings.ToUpper(string(text[i-2 : i])) {
				case "NO", "NR", "OP", "PT":
					skip = true
				}
			}
			if i == 1 && i+2 < len(text) && unicode.IsDigit(text[0]) && unicode.IsDigit(text[2]) && unicode.IsDigit(text[3]) {
				skip = true
			}
			if i+1 < len(text) && unicode.IsLetter(text[i+1]) {
				abbreviation = true
			}
			i++
			if gate && !skip && !abbreviation {
				for i < len(text) && strings.ContainsRune(joinedSeparators, text[i]) {
					i++
				}
				result = append(result, string(text[start:i]))
				start = i
			}
		case c == ' ':
			i++
			abbreviation = false
			if i < len(text) && (unicode.IsLetter(text[i]) || unicode.IsDigit(text[i])) {
				gate = true
			}
		default:
			i++
		}
	}
	if gate && start < len(text) && (i != start || start == 0) {
		result = append(result, string(text[start:]))
	}
	return result
}

func runePrefixFold(text []rune, prefix string) bool {
	needle := []rune(prefix)
	if len(text) < len(needle) {
		return false
	}
	for i, c := range needle {
		if unicode.ToLower(text[i]) != unicode.ToLower(c) {
			return false
		}
	}
	return true
}

// 两类括号独立计数；不修正失配括号，也不用归一化后的串取名。
func normalizeTitle(title string) string {
	var out strings.Builder
	round, square := 0, 0
	for _, c := range title {
		switch c {
		case '(':
			round++
		case ')':
			round--
		case '[':
			square++
		case ']':
			square--
		default:
			if round == 0 && square == 0 {
				out.WriteRune(c)
			}
		}
	}
	return strings.TrimSpace(out.String())
}

func needsSplit(titles []string) bool {
	first := ""
	for i, title := range titles {
		n := normalizeTitle(title)
		if titleEndsColon(n) {
			return true
		}
		if i == 0 {
			first = n
		} else if first != n {
			return true
		}
	}
	return false
}

func titleEndsColon(title string) bool {
	return strings.HasSuffix(title, ":") || strings.HasSuffix(title, "：")
}

func segmentCount(title string) int {
	n := len(Segments(title))
	if n <= 1 {
		return 0
	}
	return n
}

func firstSegments(title string, n int) string {
	parts := Segments(title)
	if n > len(parts) {
		n = len(parts)
	}
	return strings.TrimRight(strings.Join(parts[:n], ""), workTrim)
}

func afterSegments(title string, n int) string {
	if n == 0 {
		return title
	}
	parts := Segments(title)
	if n >= len(parts) {
		return ""
	}
	return strings.TrimSpace(strings.Join(parts[n:], ""))
}

func commonSegments(a, b string) int {
	if a == b {
		return 0
	}
	x, y := Segments(a), Segments(b)
	if len(x) <= 1 || len(y) <= 1 {
		return 0
	}
	for i := 0; ; i++ {
		if i == len(x) || i == len(y) {
			return 0
		}
		if !strings.EqualFold(strings.TrimSpace(x[i]), strings.TrimSpace(y[i])) {
			return i
		}
	}
}

func compatibleCatalogs(first, current []CatalogEntry) bool {
	if len(first) != 0 && len(current) == 0 {
		return true
	}
	if len(first) != len(current) {
		return false
	}
	for i, entry := range first {
		if entry.System != current[i].System || entry.Number != current[i].Number {
			return false
		}
	}
	return true
}

type workGroup struct {
	start    int
	titles   []string
	catalogs []CatalogEntry
	common   int
	prefix   string
	lastNorm string
}

// SplitWorks 只拆相邻的多曲组，调用方负责古典流派与标签优先级。
func SplitWorks(titles []string) []WorkSplit {
	result := make([]WorkSplit, len(titles))
	if !needsSplit(titles) {
		return result
	}
	var group workGroup
	nextGroup := 0
	for index, original := range titles {
		catalogs := ParseCatalogs(original)
		title, prefix := original, ""
		if len(group.titles) > 0 {
			prefix = movementPrefix(group.titles[0], original, len(group.titles)+1)
			if prefix != "" {
				title = prefix + original
			}
		}
		normalized := normalizeTitle(title)
		if len(group.titles) == 0 {
			group = newWorkGroup(index, title, normalized, catalogs)
			continue
		}
		common := commonSegments(group.titles[len(group.titles)-1], title)
		forcedPrefix := group.prefix
		// 单词作品名的冒号可能先于空格闸门；反向定位已确认其边界，
		// 此时仅为本组使用该前导，不改变 Segments 的参考状态机。
		if forcedPrefix == "" && common == 0 && prefix != "" {
			forcedPrefix = prefix
		}
		if forcedPrefix != "" {
			if strings.HasPrefix(title, forcedPrefix) && strings.HasPrefix(group.titles[0], forcedPrefix) {
				common = 1
			} else {
				common = 0
			}
		}
		if !compatibleCatalogs(group.catalogs, catalogs) || common == 0 || normalized == group.lastNorm && !titleEndsColon(normalized) {
			group.finish(result, &nextGroup)
			group = newWorkGroup(index, title, normalized, catalogs)
			continue
		}
		group.prefix = forcedPrefix
		if forcedPrefix != "" {
			group.common = 1
		} else {
			group.common = min(group.common, common)
		}
		group.titles = append(group.titles, title)
		group.lastNorm = normalized
	}
	group.finish(result, &nextGroup)
	return result
}

func newWorkGroup(start int, title, normalized string, catalogs []CatalogEntry) workGroup {
	return workGroup{start: start, titles: []string{title}, catalogs: catalogs, common: segmentCount(title), lastNorm: normalized}
}

func (g workGroup) finish(result []WorkSplit, nextGroup *int) {
	if len(g.titles) < 2 {
		return
	}
	work := firstSegments(g.titles[0], g.common)
	if g.prefix != "" {
		work = strings.TrimRight(g.prefix, workTrim)
	}
	if work == "" {
		return
	}
	*nextGroup += 1
	last := ""
	for i, title := range g.titles {
		movement := afterSegments(title, g.common)
		if g.prefix != "" {
			movement = strings.TrimSpace(strings.TrimPrefix(title, g.prefix))
		}
		// 与上一首比较，而不是参考伪代码中只更新一次的首曲乐章。
		if i > 0 && movement == last && !strings.EqualFold(movement, "promenade") {
			*nextGroup += 1
		}
		result[g.start+i] = WorkSplit{Work: work, Movement: movement, Group: *nextGroup}
		last = movement
	}
}

// movementPrefix 只在当前标题以期望序号开头时回看组首，原文前导完整保留。
func movementPrefix(first, current string, expected int) string {
	var markers []string
	switch {
	case romanNumeral(expected) != "" && strings.HasPrefix(current, romanNumeral(expected)+"."):
		markers = []string{"I."}
	case strings.HasPrefix(current, strconv.Itoa(expected)+"."):
		markers = []string{"1."}
	case strings.HasPrefix(current, "第"+strconv.Itoa(expected)+"乐章") || chineseNumber(expected) != "" && strings.HasPrefix(current, "第"+chineseNumber(expected)+"乐章"):
		markers = []string{"第一乐章", "第1乐章"}
	default:
		return ""
	}
	text := []rune(first)
	for _, marker := range markers {
		needle := []rune(marker)
		for i := 1; i+len(needle) <= len(text); i++ {
			if string(text[i:i+len(needle)]) != marker {
				continue
			}
			j := i - 1
			for j >= 0 && unicode.IsSpace(text[j]) {
				j--
			}
			if j >= 0 && strings.ContainsRune(movementSeparators, text[j]) {
				return string(text[:i])
			}
		}
	}
	return ""
}

func chineseNumber(n int) string {
	if n < 1 || n > 99 {
		return ""
	}
	digits := []rune("零一二三四五六七八九")
	if n < 10 {
		return string(digits[n])
	}
	result := ""
	if n >= 20 {
		result = string(digits[n/10])
	}
	result += "十"
	if n%10 != 0 {
		result += string(digits[n%10])
	}
	return result
}

func romanNumeral(n int) string {
	if n < 1 || n > 3999 {
		return ""
	}
	var out strings.Builder
	for _, part := range []struct {
		value int
		text  string
	}{{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"}, {100, "C"}, {90, "XC"}, {50, "L"}, {40, "XL"}, {10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"}} {
		for n >= part.value {
			out.WriteString(part.text)
			n -= part.value
		}
	}
	return out.String()
}
