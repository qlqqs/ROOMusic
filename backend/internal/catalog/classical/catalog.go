// Package classical 提供作品编号与作品、乐章标题的纯文本解析。
package classical

import (
	"strconv"
	"strings"
	"unicode"
)

// CatalogEntry 的 Start、Length 是输入原文中的 rune 区间，包含副号。
type CatalogEntry struct {
	System        string
	Number        string
	Partial       bool
	Start, Length int
}

type catalogEnv struct {
	allowStart bool
	entries    []CatalogEntry
}

type catalogParser func(*catalogEnv, []rune) bool

type catalogFlags uint8

const (
	catalogSubnumber catalogFlags = 1 << iota
	catalogRoman
	catalogVolume
	catalogAlpha
	catalogAttached
	catalogSlash
)

// 组合子共享同一 rune 数组；不可用短路逻辑替代顺序执行。
func catalogBoth(a, b catalogParser) catalogParser {
	return func(env *catalogEnv, text []rune) bool {
		x, y := a(env, text), b(env, text)
		return x || y
	}
}

func catalogEither(a, b catalogParser) catalogParser {
	return func(env *catalogEnv, text []rune) bool {
		return a(env, text) || b(env, text)
	}
}

func catalogSequence(parsers ...catalogParser) catalogParser {
	return func(env *catalogEnv, text []rune) bool {
		found := false
		for _, parse := range parsers {
			if parse(env, text) {
				found = true
			}
		}
		return found
	}
}

func catalogFirst(parsers ...catalogParser) catalogParser {
	return func(env *catalogEnv, text []rune) bool {
		for _, parse := range parsers {
			if parse(env, text) {
				return true
			}
		}
		return false
	}
}

func catalogNever(*catalogEnv, []rune) bool { return false }

// ParseCatalogs 按内置解析顺序返回编号；串首不匹配，每个体系只留首条。
func ParseCatalogs(s string) []CatalogEntry {
	if !strings.ContainsFunc(s, asciiDigit) {
		return nil
	}
	parse := catalogSequence(
		catalogFirst(
			catalogAtom("Op", "Op", catalogSubnumber),
			catalogAtom("Op", "Opus", catalogSubnumber),
			catalogAtom("WoO", "WoO", catalogSubnumber),
			catalogAtom("Op", "作品", catalogSubnumber),
		),
		catalogBoth(catalogAtom("Hob", "H", catalogRoman|catalogVolume), catalogAtom("Hob", "Hob", catalogRoman|catalogVolume)),
		catalogAtom("BWV", "BWV", 0),
		catalogAtom("K", "KV", 0),
		catalogAtom("K", "K", 0),
		catalogAtom("D", "D", 0),
		catalogAtom("RV", "RV", 0),
		catalogAtom("HWV", "HWV", 0),
	)
	env := catalogEnv{}
	parse(&env, []rune(s))
	seen := make(map[string]bool, len(env.entries))
	result := env.entries[:0]
	for _, entry := range env.entries {
		if !seen[entry.System] {
			seen[entry.System] = true
			result = append(result, entry)
		}
	}
	return result
}

func catalogAtom(system, prefix string, flags catalogFlags) catalogParser {
	needle := []rune(prefix)
	return func(env *catalogEnv, text []rune) bool {
		cursor := 0
		if env.allowStart {
			cursor = -1
		}
		for {
			pos := catalogFind(text, needle, cursor+1)
			if pos < 0 {
				return false
			}
			cursor = pos
			if flags&catalogAttached == 0 && pos > 0 && asciiAlnum(text[pos-1]) {
				continue
			}
			i := pos + len(needle)
			// 此处有意只比较小写字面量，保留参考对全大写 MINOR 的行为。
			if flags&catalogAlpha != 0 && prefix == "M" && i+3 < len(text) &&
				(string(text[i:i+4]) == "inor" || string(text[i:i+4]) == "ajor") {
				continue
			}
			if i < len(text) && text[i] == '.' {
				i++
			}
			if i < len(text) && flags&catalogSlash != 0 && text[i] == '/' {
				i++
			}
			for i < len(text) && unicode.IsSpace(text[i]) {
				i++
			}
			if i == len(text) || !catalogNumberStart(text, i, flags) {
				continue
			}
			buf, end := collectCatalogNumber(text, i, flags)
			i = end
			sub := ""
			if flags&catalogSubnumber != 0 {
				sub, i = catalogSub(text, i)
			}
			for len(buf) > 0 && !asciiAlnum(buf[len(buf)-1]) {
				buf = buf[:len(buf)-1]
			}
			if len(buf) == 0 {
				continue
			}
			number, partial := string(buf)+sub, false
			if flags&catalogVolume != 0 {
				var ok bool
				number, partial, ok = reorderVolume(number)
				if !ok {
					continue
				}
			}
			env.entries = append(env.entries, CatalogEntry{System: system, Number: number, Partial: partial, Start: pos, Length: i - pos})
			return true
		}
	}
}

func catalogFind(text, prefix []rune, start int) int {
	if len(prefix) == 0 {
		return -1
	}
	for i := start; i+len(prefix) < len(text); i++ {
		match := true
		for j, c := range prefix {
			if unicode.ToLower(text[i+j]) != unicode.ToLower(c) {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func catalogNumberStart(text []rune, i int, flags catalogFlags) bool {
	if flags&catalogAlpha != 0 {
		return i+1 < len(text) && (asciiLetter(text[i]) || asciiDigit(text[i+1]))
	}
	return asciiDigit(text[i]) || flags&catalogRoman != 0 && romanValue(text[i]) != 0
}

func collectCatalogNumber(text []rune, i int, flags catalogFlags) ([]rune, int) {
	var buf []rune
	var last rune
	separator := false
	for i < len(text) {
		c := text[i]
		if !asciiAlnum(c) && c != '-' && c != '/' && c != ':' && !(flags&catalogVolume != 0 && last == ':' && c == ' ') {
			break
		}
		if flags&catalogVolume != 0 && (c == ':' || c == '/') {
			if separator {
				break
			}
			separator = true
		}
		if c != ' ' {
			buf = append(buf, c)
		}
		last, text[i] = c, ' '
		i++
	}
	return buf, i
}

func catalogSub(text []rune, start int) (string, int) {
	i := start
	for i < len(text) && (unicode.IsSpace(text[i]) || text[i] == ',' || text[i] == '-') {
		i++
	}
	if i+1 >= len(text) || unicode.ToLower(text[i]) != 'n' || (unicode.ToLower(text[i+1]) != 'o' && unicode.ToLower(text[i+1]) != 'r') {
		return "", start
	}
	i += 2
	for i < len(text) && (unicode.IsSpace(text[i]) || text[i] == '.') {
		i++
	}
	startNumber := i
	if i < len(text) && asciiDigit(text[i]) {
		i++
		for i < len(text) && asciiAlnum(text[i]) {
			i++
		}
	}
	if startNumber == i {
		return "", i
	}
	return "/" + string(text[startNumber:i]), i
}

// reorderVolume 只消费一个卷号分隔标点，后缀保留并转小写。
func reorderVolume(number string) (string, bool, bool) {
	text := []rune(number)
	if len(text) == 0 {
		return "", false, false
	}
	i := 0
	roman := romanValue(text[0]) != 0
	for i < len(text) && (roman && romanValue(text[i]) != 0 || !roman && asciiDigit(text[i])) {
		i++
	}
	if i == 0 {
		return "", false, false
	}
	volume := ""
	if roman {
		// 卷号只认 I/V/X；非规范写法同样按暂存减法求值。
		acc, hold := 0, 0
		for j := 0; j < i; j++ {
			value := romanValue(text[j]) - hold
			if j == i-1 || value+hold >= romanValue(text[j+1]) {
				acc, hold = acc+value, 0
			} else {
				hold = value
			}
		}
		volume = romanNumeral(acc)
		if volume == "" {
			volume = strings.ToUpper(string(text[:i]))
		}
	} else {
		n, err := strconv.Atoi(string(text[:i]))
		if err != nil {
			return "", false, false
		}
		volume = strconv.Itoa(n)
	}
	startSuffix := i
	for i < len(text) && !unicode.IsPunct(text[i]) {
		i++
	}
	volume += strings.ToLower(string(text[startSuffix:i]))
	if i == len(text) {
		return volume, true, true
	}
	i++
	startNumber := i
	for i < len(text) && asciiDigit(text[i]) {
		i++
	}
	if startNumber == i {
		return volume, true, true
	}
	n, err := strconv.Atoi(string(text[startNumber:i]))
	if err != nil {
		return volume, true, true
	}
	return volume + ":" + strconv.Itoa(n) + strings.ToLower(string(text[i:])), false, true
}

func romanValue(c rune) int {
	switch unicode.ToUpper(c) {
	case 'I':
		return 1
	case 'V':
		return 5
	case 'X':
		return 10
	}
	return 0
}

func asciiDigit(c rune) bool  { return c >= '0' && c <= '9' }
func asciiLetter(c rune) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func asciiAlnum(c rune) bool  { return asciiDigit(c) || asciiLetter(c) }

// CatalogSortKey 仅替换冒号、斜线与连字符并折叠 ASCII 空格，不裁剪两端。
func CatalogSortKey(text string) string {
	var out strings.Builder
	space := false
	for _, c := range text {
		if c == ':' || c == '/' || c == '-' {
			c = ' '
		}
		if c != ' ' || !space {
			out.WriteRune(c)
		}
		space = c == ' '
	}
	return out.String()
}
