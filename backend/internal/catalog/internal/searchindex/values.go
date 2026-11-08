package searchindex

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

// 单对象与批量双写共用造值和校验，不在 graph 复制搜索字段语义。
func appendValue(values []value, field, role, raw string, position int) []value {
	if field == "release_artist" || field == "album_artist" || field == "track_artist" {
		for i, name := range metadata.CanonicalArtistNames(raw) {
			values = append(values, value{Field: field, Role: role, Position: i + 1, Original: name})
		}
	} else if strings.TrimSpace(raw) != "" {
		values = append(values, value{Field: field, Role: role, Position: position, Original: raw})
	}
	return values
}
func appendCredit(values []value, role, name string, position int) ([]value, error) {
	switch role {
	case "lyricist", "composer", "arranger", "conductor", "performer", "producer":
	default:
		return nil, errors.New("unsupported search credit role")
	}
	return appendValue(values, "credit", role, name, position), nil
}
func normalizeValues(values []value) (string, error) {
	sortTitle := ""
	for i := range values {
		v := &values[i]
		if !utf8.ValidString(v.Original) || len(v.Original) > 4096 {
			return "", errors.New("search source text exceeds budget or is invalid")
		}
		v.Normalized = textnorm.Text(v.Original)
		if v.Field == "catalog_number" || v.Field == "work_catalog" {
			v.Normalized = textnorm.CatalogNumber(v.Original)
		}
		if len(v.Normalized) > 65536 {
			return "", errors.New("normalized search text exceeds budget")
		}
		if v.Field == "release_title" || v.Field == "track_title" {
			sortTitle = v.Normalized
		}
		v.Grams = ShortGrams(v.Normalized)
		v.Loose, v.LooseGrams = "", nil
		if loose := textnorm.Loose(v.Normalized); loose != "" && loose != v.Normalized {
			v.Loose = loose
			v.LooseGrams = ShortGrams(loose)
		}
	}
	return sortTitle, nil
}
