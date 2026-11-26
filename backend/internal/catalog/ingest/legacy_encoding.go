package ingest

import (
	"maps"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

// 只处理本候选实际采用的原字节，文件解析缓存仍保存逐文件的证据。
func decodeCandidateLegacy(observations []SourceObservation) []SourceObservation {
	var candidates []string
	readings := map[string][]metadata.LegacyReading{}
	for _, o := range observations {
		collect := func(raw []byte) {
			if len(raw) == 0 {
				return
			}
			key := string(raw)
			if _, ok := readings[key]; ok {
				return
			}
			choices := metadata.LegacyReadings(raw)
			if len(readings) == 0 {
				for _, c := range choices {
					candidates = append(candidates, c.Encoding)
				}
			} else {
				candidates = slices.DeleteFunc(candidates, func(name string) bool {
					return !slices.ContainsFunc(choices, func(c metadata.LegacyReading) bool { return c.Encoding == name })
				})
			}
			readings[key] = choices
		}
		for field, raw := range o.LegacyText {
			if o.FieldSources[field] == "garbled" && (o.ScanTags == nil || len(o.ScanTags.LegacyArtistParts[field]) == 0) {
				collect(raw)
			}
		}
		if o.ScanTags != nil {
			for field, parts := range o.ScanTags.LegacyArtistParts {
				if o.FieldSources[field] == "garbled" {
					for _, p := range parts {
						collect(p.Raw)
					}
				}
			}
		}
	}
	if len(readings) == 0 {
		return observations
	}
	ordered := slices.Clone(observations)
	for i := range ordered {
		o := &ordered[i]
		if len(o.LegacyText) == 0 && (o.ScanTags == nil || len(o.ScanTags.LegacyArtistParts) == 0) {
			continue
		}
		if len(candidates) != 1 {
			var tags metadata.ScanTags
			if o.ScanTags != nil {
				tags = *o.ScanTags
			}
			names := append([]string{}, candidates...)
			tags.LegacyEncodings = &names
			o.ScanTags = &tags
			continue
		}
		decode := func(raw []byte) string {
			for _, c := range readings[string(raw)] {
				if c.Encoding == candidates[0] {
					return c.Value
				}
			}
			return ""
		}
		o.FieldSources = maps.Clone(o.FieldSources)
		o.ArtistTagEvidence = slices.Clone(o.ArtistTagEvidence)
		for _, field := range []string{"title", "album", "artist", "album_artist"} {
			if o.FieldSources[field] != "garbled" {
				continue
			}
			text := decode(o.LegacyText[field])
			if o.ScanTags != nil && len(o.ScanTags.LegacyArtistParts[field]) > 0 {
				var names []string
				for _, p := range o.ScanTags.LegacyArtistParts[field] {
					name := p.Text
					if len(p.Raw) > 0 {
						name = decode(p.Raw)
						// 艺人目录从标签原文派生展示名，须使用同一次选定编码解出的原文。
						for i, e := range o.ArtistTagEvidence {
							if e.TagKey == p.TagKey && e.OccurrenceIndex == p.Occurrence {
								o.ArtistTagEvidence[i].RawValue = name
								break
							}
						}
					}
					names = append(names, name)
				}
				text = metadata.JoinArtistValues(names...)
				if field == "artist" {
					text = metadata.CanonicalTrackArtist(text)
				}
			}
			if text == "" {
				continue
			}
			switch field {
			case "album":
				o.Album = text
			case "artist":
				o.Artist = text
			case "album_artist":
				o.AlbumArtist = text
			case "title":
				o.Title = text
			}
			o.FieldSources[field] = "legacy_encoding"
		}
	}
	return ordered
}
