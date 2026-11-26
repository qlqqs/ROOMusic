package metadata

import (
	"sort"
	"strings"
)

// CompilationDisplayName 是合辑发行统一使用的艺人展示名。
const CompilationDisplayName = "群星"

// IsCompilationPlaceholder 按折叠后的整串判断合辑占位词，不用于过滤曲目艺人。
func IsCompilationPlaceholder(raw string) bool {
	switch foldCompilationPlaceholder(raw) {
	case "various", "various artists", "various artist", "v.a.", "v.a", "va", "v/a", "compilation",
		"various composers", "various recording", "various recordings", "various soundtrack",
		"multiple artists", "diverse artists", "artisti vari", "artistes divers", "artistas variados",
		"blandade artister", "verschillende artiesten",
		"群星", "羣星", "华语群星", "華語群星", "多位艺人", "多位藝人", "多位艺术家", "多位藝術家",
		"オムニバス", "ヴァリアス・アーティスト":
		return true
	default:
		return false
	}
}

func foldCompilationPlaceholder(raw string) string {
	raw = strings.Map(func(character rune) rune {
		switch {
		case character >= '\uFF01' && character <= '\uFF5E':
			return character - 0xFEE0
		case character == '\u3000':
			return ' '
		default:
			return character
		}
	}, raw)
	return NormalizeValue(strings.ToLower(raw))
}

// PrimaryArtistCounts 按每首的第一位真实艺人统计合辑分布。
type PrimaryArtistCounts map[string]artistVotes

type artistVotes struct {
	name  string
	count int
}

func (counts PrimaryArtistCounts) Add(credit string) {
	names := CanonicalArtistNames(credit)
	if len(names) == 0 || IsCompilationPlaceholder(credit) || IsCompilationPlaceholder(names[0]) || FieldAuthenticity("artist", names[0], "", false, false) != Real {
		return
	}
	name := names[0]
	key := strings.ToLower(name)
	v := counts[key]
	if v.name == "" || name < v.name {
		v.name = name
	}
	v.count++
	counts[key] = v
}

func (counts PrimaryArtistCounts) IsCompilation(tracks int) bool {
	if len(counts) < 5 {
		return false
	}
	for _, v := range counts {
		if v.count*2 > tracks {
			return false
		}
	}
	return true
}

// ReleaseArtist 按保留曲目中的首位真实艺人决定展示署名。
func (counts PrimaryArtistCounts) ReleaseArtist(tracks int) string {
	if counts.IsCompilation(tracks) {
		return "Various Artists"
	}
	var names []string
	for _, v := range counts {
		if v.count*2 > tracks {
			return v.name
		}
		names = append(names, v.name)
	}
	if len(names) < 2 {
		return ""
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := counts[strings.ToLower(names[i])], counts[strings.ToLower(names[j])]
		if a.count != b.count {
			return a.count > b.count
		}
		return a.name < b.name
	})
	return strings.Join(names, " / ")
}
