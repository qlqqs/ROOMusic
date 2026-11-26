package metadata

import (
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

type Authenticity string

const (
	Real        Authenticity = "real"
	Inferred    Authenticity = "inferred"
	Placeholder Authenticity = "placeholder"
)

var placeholderName = regexp.MustCompile(`(?i)^(?:unknown(?:[ _-]*(?:artist|album))?|<unknown>|未知(?:歌手|艺人|艺术家|专辑|藝人|藝術家|專輯)?|(?:audio[ _.\-]*)?track[ _.#\-]*[0-9]+|音[轨軌][ _.#\-]*[0-9]+)$`)

// FieldAuthenticity 按字段用途、人工修正与扫描来源判断真实性。
func FieldAuthenticity(field, value, source string, inferred, corrected bool) Authenticity {
	value = strings.TrimSpace(value)
	if value == "" {
		return Placeholder
	}
	if corrected {
		return Real
	}
	switch field {
	case "title", "release_title", "album", "artist", "album_artist":
		if placeholderName.MatchString(NormalizeValue(norm.NFKC.String(value))) {
			return Placeholder
		}
	}
	switch source {
	case "garbled", "default_fallback", "display_fallback", "compilation":
		return Placeholder
	case "legacy_encoding", "artist_distribution", "vinyl_side", "track_sequence", "filename_fallback", "folder_fallback", "folder", "album_title", "folder_structure", "path_order_fallback", "automatic":
		return Inferred
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
