package catalog

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

// searchKeyBranch 只持有服务端列名、参数位置和条件；用户文字始终通过参数绑定。
// 同一套 N/L1/L2 分支用于完整匹配、逐层探测及最佳说明，避免计数与分页集合分叉。
type searchKeyBranch struct {
	value, grams, key, escaped, gram, guard string
	loose                                   bool
	trigram                                 bool
}

func looseSearchEnabled(key, loose string) bool {
	return loose != "" && (loose == key || utf8.RuneCountInString(strings.ReplaceAll(loose, " ", "")) >= 2)
}

func searchKeyArgs(key string) []any {
	return []any{key, searchindex.EscapeLike(key), searchindex.QueryGram(key)}
}

func (b searchKeyBranch) match(rank int) string {
	value := b.value
	if !b.trigram {
		value = "(" + value + " || '')"
	}
	condition := b.guard + " AND " + b.grams + " @> ARRAY[" + b.gram + "]::text[]"
	if b.loose {
		condition += " AND " + b.value + " IS NOT NULL"
	}
	if rank == 0 {
		return condition + " AND md5(" + b.value + ")=md5(" + b.key + ") AND " + b.value + ` COLLATE "C"=` + b.key + ` COLLATE "C"`
	}
	condition += " AND " + value + " LIKE '%' || " + b.escaped + " || '%' ESCAPE '~'"
	prefix := b.value + " LIKE " + b.escaped + " || '%' ESCAPE '~'"
	if rank == 1 {
		condition += " AND " + prefix + " AND " + b.value + "<>" + b.key
	} else if rank == 2 {
		condition += " AND NOT (" + prefix + ")"
	}
	return condition
}

func searchBranchesMatch(branches []searchKeyBranch, rank int) string {
	parts := make([]string, 0, len(branches))
	for _, b := range branches {
		if rank >= 0 && b.loose != (rank%2 == 1) {
			continue
		}
		level := -1
		if rank >= 0 {
			level = rank / 2
		}
		parts = append(parts, "("+b.match(level)+")")
	}
	if len(parts) == 0 {
		return "FALSE"
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}

func searchBranchesRank(branches []searchKeyBranch) string {
	parts := make([]string, 0, len(branches))
	for _, b := range branches {
		bias := 0
		if b.loose {
			bias = 1
		}
		parts = append(parts, "CASE WHEN "+b.match(-1)+" THEN CASE WHEN "+b.value+"="+b.key+fmt.Sprintf(" THEN %d WHEN ", bias)+b.value+" LIKE "+b.escaped+fmt.Sprintf(" || '%%' ESCAPE '~' THEN %d ELSE %d END ELSE 6 END", 2+bias, 4+bias))
	}
	return "LEAST(" + strings.Join(parts, ",") + ")"
}

// 去标点键独立启用；L2 仅用于查询键确实发生变化的反向匹配。
func keyBranches(alias string, first, looseFirst int, scope string) []searchKeyBranch {
	key, loose := fmt.Sprintf("$%d", first), fmt.Sprintf("$%d", looseFirst)
	base := searchKeyBranch{value: alias + ".normalized_value", grams: alias + ".short_grams", key: key,
		escaped: fmt.Sprintf("$%d", first+1), gram: fmt.Sprintf("$%d", first+2), guard: scope + " AND " + key + "<>''", trigram: true}
	enabled := scope + " AND " + loose + "<>'' AND (" + loose + "=" + key + " OR char_length(replace(" + loose + ",' ',''))>=2)"
	l1 := searchKeyBranch{value: alias + ".loose_value", grams: alias + ".loose_grams", key: loose,
		escaped: fmt.Sprintf("$%d", looseFirst+1), gram: fmt.Sprintf("$%d", looseFirst+2), guard: enabled, loose: true, trigram: true}
	l2 := l1
	l2.value, l2.grams, l2.guard = base.value, base.grams, enabled+" AND "+loose+"<>"+key
	return []searchKeyBranch{base, l1, l2}
}

func catalogSearchBranches() []searchKeyBranch {
	return append(keyBranches("v", 1, 8, "v.field NOT IN ('catalog_number','work_catalog')"),
		keyBranches("v", 4, 11, "v.field IN ('catalog_number','work_catalog')")...)
}

// 即使某个分支被裁去，也为完整绑定参数声明类型；分页参数紧随这十三项。
const normalizedSearchGuard = `$1::text<>'' AND $2::text IS NOT NULL AND $3::text IS NOT NULL
 AND $4::text IS NOT NULL AND $5::text IS NOT NULL AND $6::text IS NOT NULL
 AND $8::text IS NOT NULL AND $9::text IS NOT NULL AND $10::text IS NOT NULL
 AND $11::text IS NOT NULL AND $12::text IS NOT NULL AND $13::text IS NOT NULL AND v.projection_generation=$7`

var normalizedSearchRank = searchBranchesRank(catalogSearchBranches())

func (p searchPlan) searchBranches() []searchKeyBranch {
	branches := catalogSearchBranches()
	result := make([]searchKeyBranch, 0, len(branches))
	for i, b := range branches {
		group, part := i/3, i%3
		if group == 1 && p.args[0] == p.args[3] {
			continue
		}
		if p.args[0] == p.args[3] {
			b.guard = strings.Replace(b.guard, "v.field NOT IN ('catalog_number','work_catalog')", "TRUE", 1)
		}
		original, loose := p.args[group*3].(string), p.args[7+group*3].(string)
		if part > 0 && (!looseSearchEnabled(original, loose) || (part == 2 && original == loose)) {
			continue
		}
		trigramIndex := group
		if part > 0 {
			trigramIndex += 2
		}
		b.trigram = p.trigrams[trigramIndex]
		result = append(result, b)
	}
	return result
}

func (p searchPlan) normalizedMatch(rank int) string {
	return normalizedSearchGuard + " AND " + searchBranchesMatch(p.searchBranches(), rank)
}

func artistSearchBranches(key, loose string, trigrams [2]bool) []searchKeyBranch {
	branches := keyBranches("n", 1, 4, "TRUE")
	result := branches[:0]
	for i, b := range branches {
		if i > 0 && (!looseSearchEnabled(key, loose) || (i == 2 && key == loose)) {
			continue
		}
		b.trigram = trigrams[0]
		if i > 0 {
			b.trigram = trigrams[1]
		}
		result = append(result, b)
	}
	return result
}

func artistNamePredicate(branches []searchKeyBranch) string {
	return `$1::text<>'' AND $2::text IS NOT NULL AND $3::text IS NOT NULL
 AND $4::text IS NOT NULL AND $5::text IS NOT NULL AND $6::text IS NOT NULL
 AND n.projection_generation=(SELECT published_generation FROM catalog_projection_state WHERE capability='artists') AND ` + searchBranchesMatch(branches, -1)
}

func looseQueryArgs(key string) []any {
	return searchKeyArgs(textnorm.Loose(key))
}
