package ingest

import (
	"path"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

// versionTerms 保留括号叶节点的原始拼写，目录名优先。
func versionTerms(scope string, tracks []SourceObservation) []string {
	var result []string
	seen := map[string]bool{}
	extract := func(name string) {
		type bracket struct {
			start  int
			close  rune
			nested bool
		}
		var stack []bracket
		for i, r := range name {
			var close rune
			switch r {
			case '(':
				close = ')'
			case '[':
				close = ']'
			case '{':
				close = '}'
			case '<':
				close = '>'
			}
			if close != 0 {
				if len(stack) > 0 {
					stack[len(stack)-1].nested = true
				}
				stack = append(stack, bracket{i + 1, close, false})
				continue
			}
			if len(stack) == 0 {
				continue
			}
			top := stack[len(stack)-1]
			if r != top.close {
				continue
			}
			stack = stack[:len(stack)-1]
			if top.nested {
				continue
			}
			value := strings.TrimSpace(name[top.start:i])
			key := textnorm.MergeKey(value)
			switch key {
			case "", "ep", "single", "ost", "sdtk", "soundtrack", "单曲", "原声", "原声带":
				continue
			}
			if len(key) == 4 && strings.Trim(key, "0123456789") == "" {
				continue
			}
			if !seen[key] {
				seen[key] = true
				result = append(result, value)
			}
		}
	}
	extract(path.Base(scope))
	for _, o := range tracks {
		if !o.InferredFields["album"] {
			extract(o.Album)
		}
	}
	return result
}
