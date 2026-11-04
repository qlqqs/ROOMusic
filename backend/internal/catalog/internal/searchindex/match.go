package searchindex

import (
	"sort"
	"strings"
)

// EscapeLike 保留用户输入的字面含义，SQL 必须配合 ESCAPE '~'。
func EscapeLike(value string) string {
	return strings.NewReplacer("~", "~~", "%", "~%", "_", "~_").Replace(value)
}

// QueryGram 从完整查询取连续的至多两个 scalar，不能按字节切割。
func QueryGram(value string) string {
	runes := []rune(value)
	if len(runes) > 2 {
		runes = runes[:2]
	}
	return string(runes)
}

// ShortGrams 保留全部一元、二元连续片段；排序让持久化和测试可重现。
func ShortGrams(value string) []string {
	runes := []rune(value)
	seen := make(map[string]struct{}, len(runes)*2)
	for i := range runes {
		seen[string(runes[i:i+1])] = struct{}{}
		if i+1 < len(runes) {
			seen[string(runes[i:i+2])] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for gram := range seen {
		out = append(out, gram)
	}
	sort.Strings(out)
	return out
}
