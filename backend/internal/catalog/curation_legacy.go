package catalog

import (
	"slices"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

// LegacyCandidates 只包含从所选扫描字段的原字节还原的读法。
type LegacyCandidates map[string][]string

func (m *LegacyCandidates) add(field string, values []string) {
	if len(values) == 0 {
		return
	}
	if *m == nil {
		*m = LegacyCandidates{}
	}
	combined := append((*m)[field], values...)
	slices.Sort(combined)
	(*m)[field] = slices.Compact(combined)
}

func (e *CurationEvidence) collectLegacyField(t *CurationTrack, name, scan string, values []string) {
	t.Legacy.add(name, values)
	field, selected := "", ""
	switch name {
	case "album":
		field, selected = "title", e.Title
	case "album_artist":
		field, selected = "album_artist", e.AlbumArtist
	case "artist":
		if e.AlbumArtist == "" {
			field, selected = "artist", e.Artist
		}
	}
	if field != "" && e.FieldSources[field] == "garbled" && metadata.NormalizeValue(scan) == selected {
		if field == "artist" {
			field = "album_artist"
		}
		e.Legacy.add(field, values)
	}
}

// LocalValueSource 将程序的唯一解码和模型从片段选取的文字分别标记为推断。
func (m LegacyCandidates) LocalValueSource(field, value string) string {
	if values := m[field]; len(values) == 1 && strings.TrimSpace(value) == values[0] {
		return "legacy_encoding"
	}
	return "model_inferred"
}
