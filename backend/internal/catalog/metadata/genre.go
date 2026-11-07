package metadata

import (
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// GenreKey 只合并大小写与全半角差异，不合并繁简或改写展示名称。
func GenreKey(value string) string {
	return cases.Fold().String(norm.NFKC.String(NormalizeValue(value)))
}

// SplitGenres 拆分分号、全角分号与顿号，保留斜杠和逗号；同键流派保留首次写法。
func SplitGenres(values ...string) []string {
	genres := make([]string, 0)
	seen := make(map[string]bool)
	for _, value := range values {
		for _, genre := range strings.FieldsFunc(value, func(r rune) bool { return r == ';' || r == '；' || r == '、' }) {
			genre = NormalizeValue(BoundedText(genre))
			key := GenreKey(genre)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			genres = append(genres, genre)
		}
	}
	return genres
}
