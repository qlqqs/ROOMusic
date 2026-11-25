package metadata

import "strings"

type Authenticity string

const (
	Real        Authenticity = "real"
	Inferred    Authenticity = "inferred"
	Placeholder Authenticity = "placeholder"
)

// FieldAuthenticity 按当前取值层和该字段的原始来源判断，保留有明确来源的同名艺人。
func FieldAuthenticity(field, value, source string, inferred, corrected bool) Authenticity {
	value = strings.TrimSpace(value)
	if value == "" {
		return Placeholder
	}
	if corrected {
		return Real
	}
	switch source {
	case "default_fallback", "display_fallback", "compilation":
		return Placeholder
	case "filename_fallback", "folder_fallback", "folder", "album_title", "folder_structure", "path_order_fallback", "automatic":
		return Inferred
	case "":
		if ((field == "release_title" || field == "album") && value == "未知专辑") ||
			((field == "artist" || field == "album_artist") && value == "未知艺术家") {
			return Placeholder
		}
	}
	if inferred {
		return Inferred
	}
	return Real
}

func (a Authenticity) SearchValue(value string) string {
	if a == Placeholder {
		return ""
	}
	return value
}
