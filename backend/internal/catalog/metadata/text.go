package metadata

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxTextBytes 是单个标签文本保留的最大字节数；解析器读取文本原子时也用它限定预算。
const MaxTextBytes = 4096

// Credit 是一条逐轨或 Release 级署名：角色与规范化后的姓名。
type Credit struct {
	Role string `json:"role"`
	Name string `json:"name"`
}

// NormalizeValue 去掉首尾空白并把内部连续空白折叠为单个空格。
func NormalizeValue(v string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(v)), " ")
}

// BoundedText 丢弃控制字符并把文本截断到 MaxTextBytes 以内（不切断多字节字符）。
func BoundedText(value string) string {
	value = strings.TrimSpace(value)
	var result strings.Builder
	for _, character := range value {
		if unicode.IsControl(character) {
			continue
		}
		width := utf8.RuneLen(character)
		if width < 0 || result.Len()+width > MaxTextBytes {
			break
		}
		result.WriteRune(character)
	}
	return strings.TrimSpace(result.String())
}

// CanonicalCredits 规范化署名：角色转小写、姓名折叠空白，丢弃空项与重复项，并按角色、姓名排序。
func CanonicalCredits(values []Credit) []Credit {
	seen := make(map[string]bool, len(values))
	result := make([]Credit, 0, len(values))
	for _, value := range values {
		value.Role = strings.ToLower(NormalizeValue(value.Role))
		value.Name = NormalizeValue(value.Name)
		if value.Role == "" || value.Name == "" {
			continue
		}
		key := value.Role + "\x00" + value.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Role != result[j].Role {
			return result[i].Role < result[j].Role
		}
		return result[i].Name < result[j].Name
	})
	return result
}
