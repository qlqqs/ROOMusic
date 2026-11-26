package parser

import (
	"bytes"
	"io"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

// rememberID3Text 在有界展示值去除控制字符之前保存疑似旧编码的原字节。
func rememberID3Text(o *AudioObservation, field string, raw []byte) {
	delete(o.LegacyText, field)
	if len(raw) > metadata.MaxTextBytes || !metadata.SuspectLegacyText(raw) {
		return
	}
	if o.LegacyText == nil {
		o.LegacyText = map[string][]byte{}
	}
	o.LegacyText[field] = bytes.Clone(raw)
}

func readID3v1(reader io.ReaderAt, size int64, o *AudioObservation) error {
	var tag [128]byte
	if size < int64(len(tag)) {
		return nil
	}
	if _, err := reader.ReadAt(tag[:], size-int64(len(tag))); err != nil {
		return err
	}
	if string(tag[:3]) != "TAG" {
		return nil
	}
	if _, _, present, err := locateAPEv2(reader, size); err != nil {
		return err
	} else if present {
		return nil
	}
	o.SourceKind = "mp3_id3v1"
	text := func(field string, data []byte) string {
		raw, _, _ := bytes.Cut(data, []byte{0})
		raw = bytes.TrimRight(raw, " ")
		rememberID3Text(o, field, raw)
		var value strings.Builder
		for _, b := range raw {
			value.WriteRune(rune(b))
		}
		if o.LegacyText[field] != nil {
			return value.String()
		}
		return metadata.BoundedText(value.String())
	}
	o.Title = text("title", tag[3:33])
	artist := text("artist", tag[33:63])
	o.markLatinArtist("ARTIST", artist)
	if err := captureArtistTag(o, "ARTIST", artist); err != nil {
		return err
	}
	o.Album = text("album", tag[63:93])
	o.Year = ParseYear(string(tag[93:97]))
	o.collectScanTag("YEAR", string(tag[93:97]))
	if tag[125] == 0 {
		o.TrackNumber = int(tag[126])
	}
	if genre := int(tag[127]); genre < len(id3v1Genres) {
		o.Genres = []string{id3v1Genres[genre]}
	}
	return nil
}

// ID3v1 标准的流派编号为 0 至 79。
var id3v1Genres = []string{
	"Blues", "Classic Rock", "Country", "Dance", "Disco", "Funk", "Grunge", "Hip-Hop", "Jazz", "Metal",
	"New Age", "Oldies", "Other", "Pop", "R&B", "Rap", "Reggae", "Rock", "Techno", "Industrial",
	"Alternative", "Ska", "Death Metal", "Pranks", "Soundtrack", "Euro-Techno", "Ambient", "Trip-Hop", "Vocal", "Jazz+Funk",
	"Fusion", "Trance", "Classical", "Instrumental", "Acid", "House", "Game", "Sound Clip", "Gospel", "Noise",
	"Alternative Rock", "Bass", "Soul", "Punk", "Space", "Meditative", "Instrumental Pop", "Instrumental Rock", "Ethnic", "Gothic",
	"Darkwave", "Techno-Industrial", "Electronic", "Pop-Folk", "Eurodance", "Dream", "Southern Rock", "Comedy", "Cult", "Gangsta",
	"Top 40", "Christian Rap", "Pop/Funk", "Jungle", "Native American", "Cabaret", "New Wave", "Psychedelic", "Rave", "Showtunes",
	"Trailer", "Lo-Fi", "Tribal", "Acid Punk", "Acid Jazz", "Polka", "Retro", "Musical", "Rock & Roll", "Hard Rock",
}

func (o *AudioObservation) markLatinArtist(key, raw string) {
	role := artistTagRole(key)
	if raw == "" || (role != "track_artist" && role != "album_artist") {
		return
	}
	if o.latinArtistTags == nil {
		o.latinArtistTags = map[int]bool{}
	}
	o.latinArtistTags[len(o.ArtistTagEvidence)] = true
}

// 同一展示字段可由多条署名组成；只采用实际选中的标签及其编码。
func (o *AudioObservation) collectLegacyArtists() {
	if len(o.latinArtistTags) == 0 {
		return
	}
	for _, field := range []string{"artist", "album_artist"} {
		role, priority := "track_artist", 5
		if field == "album_artist" {
			role = field
			for _, e := range o.ArtistTagEvidence {
				if p := albumArtistTagPriority(e.TagKey); e.Role == role && p > 0 && p < priority {
					priority = p
				}
			}
		}
		var parts []metadata.LegacyArtistPart
		var combined []string
		hasLegacy := false
		allLatin := true
		for i, e := range o.ArtistTagEvidence {
			if e.Role != role || field == "album_artist" && albumArtistTagPriority(e.TagKey) != priority {
				continue
			}
			part := metadata.LegacyArtistPart{Text: e.RawValue}
			if o.latinArtistTags[i] {
				raw := latinBytes(e.RawValue)
				if metadata.SuspectLegacyText(raw) {
					part = metadata.LegacyArtistPart{Raw: raw, TagKey: e.TagKey, Occurrence: e.OccurrenceIndex}
					hasLegacy = true
				}
			} else if strings.IndexFunc(e.RawValue, func(r rune) bool { return r >= 128 }) >= 0 {
				allLatin = false
			}
			parts = append(parts, part)
			combined = append(combined, e.RawValue)
		}
		if hasLegacy {
			tags := o.fileTags()
			if tags.LegacyArtistParts == nil {
				tags.LegacyArtistParts = map[string][]metadata.LegacyArtistPart{}
			}
			tags.LegacyArtistParts[field] = parts
			if allLatin {
				rememberID3Text(o, field, latinBytes(strings.Join(combined, " / ")))
			}
		}
	}
}

func latinBytes(value string) []byte {
	raw := make([]byte, 0, len(value))
	for _, r := range value {
		raw = append(raw, byte(r))
	}
	return raw
}
