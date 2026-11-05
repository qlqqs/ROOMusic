package artistgraph

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
	"golang.org/x/text/unicode/norm"
)

// LoadInitials 首次严格校验内置拼音表、许可证及复用的 textnorm 字表，缓存成功或失败。
// 不联网、不读取外部字典、不在 init 中 panic；调用方可在构建艺人投影前预检。
func LoadInitials() error {
	return embeddedInitials.load(pinyinFiles)
}

// InitialGroup 取 NFKC 后首个非空白字符，返回 A–Z、kana 或 #。
// 首字母只用于浏览分组，不音译全名、不生成搜索词；开头标点不会被跳过。
// 汉字先查本字，缺表才尝试 textnorm 的明确繁简映射；多音字不猜姓氏读法。
func InitialGroup(name string) (string, error) {
	if !utf8.ValidString(name) {
		return "", fmt.Errorf("artistgraph: 首字母姓名不是有效 UTF-8")
	}
	if err := LoadInitials(); err != nil {
		return "", err
	}
	return initialGroup(name, embeddedInitials.table), nil
}

func initialGroup(name string, table initialsTable) string {
	name = strings.TrimLeftFunc(norm.NFKC.String(name), unicode.IsSpace)
	if name == "" {
		return "#"
	}
	first, _ := utf8.DecodeRuneInString(name)
	// 先查固定 Unicode 17 表，不受 Go 标准库 Unicode 版本的汉字范围限制。
	if initial, ok := table[first]; ok {
		return string(initial)
	}
	switch {
	case unicode.Is(unicode.Latin, first):
		folded := textnorm.Text(string(first))
		if len(folded) > 0 && folded[0] >= 'a' && folded[0] <= 'z' {
			return string(folded[0] - 'a' + 'A')
		}
	case unicode.Is(unicode.Han, first):
		folded := textnorm.Text(string(first))
		replacement, size := utf8.DecodeRuneInString(folded)
		if size == len(folded) {
			if initial, ok := table[replacement]; ok {
				return string(initial)
			}
		}
	case unicode.Is(unicode.Hiragana, first), unicode.Is(unicode.Katakana, first):
		return "kana"
	}
	return "#"
}
