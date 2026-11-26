package textnorm

import (
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// MergeKey 比较专辑与艺人的原名，保留括号内的文字。
func MergeKey(raw string) string {
	folded := norm.NFD.String(cases.Fold().String(raw))
	return norm.NFC.String(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) || unicode.IsMark(r) && unicode.Is(unicode.Diacritic, r) {
			return -1
		}
		return r
	}, folded))
}
