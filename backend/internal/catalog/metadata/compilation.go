package metadata

import "strings"

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
