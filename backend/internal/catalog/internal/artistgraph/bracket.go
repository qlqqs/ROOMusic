package artistgraph

import (
	"strings"
	"unicode"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

type alias struct {
	text string
	part string
}

// 括号里的合作、版本和演出标记不是别名；乐器写法复用 metadata 的映射表。
var bracketAliasExclusions = struct {
	prefixes   []string
	words      []string
	substrings []string
}{
	prefixes: []string{"feat", "ft.", "ft ", "featuring", "with ", "vs", "x ", "prod", "cv"},
	words: []string{
		"live", "remix", "mix", "edit", "version", "ver", "demo", "acoustic", "instrumental",
		"inst", "remaster", "remastered", "cover", "karaoke", "bonus",
	},
	substrings: []string{"现场", "版", "伴奏", "演唱会", "合唱", "对唱", "翻唱", "原唱", "纯音乐", "混音", "演奏"},
}

// bracketAliases 只解析规范化名字键，返回括号外和括号内的单一别名写法。
func bracketAliases(key string) []alias {
	if !strings.HasSuffix(key, ")") || strings.Count(key, "(") != 1 || strings.Count(key, ")") != 1 {
		return nil
	}
	open := strings.IndexByte(key, '(')
	if open == 0 {
		return nil
	}
	outer := strings.TrimSpace(key[:open])
	inner := strings.TrimSpace(key[open+1 : len(key)-1])
	if outer == "" || inner == "" || outer == inner || len(metadata.MapInstrument(inner)) > 0 {
		return nil
	}
	for _, prefix := range bracketAliasExclusions.prefixes {
		if strings.HasPrefix(inner, prefix) {
			return nil
		}
	}
	words := strings.FieldsFunc(inner, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_'
	})
	for _, word := range words {
		if len(metadata.MapInstrument(word)) > 0 {
			return nil
		}
		for _, excluded := range bracketAliasExclusions.words {
			if word == excluded {
				return nil
			}
		}
	}
	for _, fragment := range bracketAliasExclusions.substrings {
		if strings.Contains(inner, fragment) {
			return nil
		}
	}
	if strings.IndexFunc(inner, func(r rune) bool {
		return !unicode.IsNumber(r) && !unicode.IsPunct(r) && !unicode.IsSpace(r)
	}) == -1 || len(metadata.CanonicalArtistNames(inner)) != 1 {
		return nil
	}
	return []alias{{text: outer, part: "outer"}, {text: inner, part: "inner"}}
}
