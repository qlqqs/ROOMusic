// Package textnorm 提供仅用于搜索与身份匹配的版本化文本键，不改写显示原文。
package textnorm

import (
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// Version 是规范化规则与内置字表的共同版本；修改任一规则均需升级并重建投影。
const Version = 1

// Text 依次进行 NFKC、完整大小写折叠、拉丁附加符清理、假名与繁简转换及空白折叠。
// 调用方负责 UTF-8、长度和控制字符校验；本函数不截断、不音译、不修改显示原文。
// 前置条件：本进程必须已成功调用 LoadEmbedded；违反此前置条件会 panic。
// 字表加载失败应由调用方阻止新投影构建/发布并保留旧搜索，不得调用本函数降级。
func Text(raw string) string {
	return normalizeText(raw, embeddedTables.require())
}

// CatalogNumber 在 Text 规则上额外删除 Unicode 空白、Pd 连字符与 U+2212 减号。
// 其他标点保留；仅用于唱片编号，不能作为姓名/标题的键。
// 与 Text 相同，必须先成功调用 LoadEmbedded，否则 panic。
func CatalogNumber(raw string) string {
	table := embeddedTables.require()
	text := normalizeText(raw, table)
	compact := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.Is(unicode.Pd, r) || r == '\u2212' {
			return -1
		}
		return r
	}, text)
	if compact == text {
		return text
	}
	// 删除分隔符可能使原本分离的字母/组合标记或韩文字母相邻；收敛到相同规范形式，
	// 避免 a-◌́ 首次得到 a◌́、第二次才得到 a，保证编号键同样幂等。
	return normalizeText(compact, table)
}

func normalizeText(raw string, table characterTable) string {
	text := cases.Fold().String(norm.NFKC.String(raw))
	text = stripLatinDiacritics(text)
	text = hiragana(text)
	text = strings.Map(func(r rune) rune {
		if replacement, ok := table[r]; ok {
			return replacement
		}
		return r
	}, text)
	return strings.Join(strings.Fields(text), " ")
}

func stripLatinDiacritics(text string) string {
	var out strings.Builder
	out.Grow(len(text))
	latin := false
	for _, r := range norm.NFD.String(text) {
		if unicode.IsMark(r) {
			// 只去真正的组合变音符，不顺带删除变体选择符/组合字符连接符。
			if latin && unicode.Is(unicode.Diacritic, r) {
				continue
			}
		} else {
			latin = unicode.IsLetter(r) && unicode.Is(unicode.Latin, r)
		}
		out.WriteRune(r)
	}
	return norm.NFC.String(out.String())
}

func hiragana(text string) string {
	var out strings.Builder
	out.Grow(len(text))
	for _, r := range text {
		switch {
		case r >= '\u30a1' && r <= '\u30f6', r == '\u30fd', r == '\u30fe':
			out.WriteRune(r - 0x60)
		case r >= '\u30f7' && r <= '\u30fa':
			// ヷ/ヸ/ヹ/ヺ 没有单字符平假名：先分解，再保留浊点。
			for _, decomposed := range norm.NFD.String(string(r)) {
				if decomposed >= '\u30a1' && decomposed <= '\u30f6' {
					decomposed -= 0x60
				}
				out.WriteRune(decomposed)
			}
		default:
			out.WriteRune(r)
		}
	}
	return norm.NFC.String(out.String())
}
