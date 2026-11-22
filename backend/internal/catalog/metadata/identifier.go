package metadata

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// Identifier 是从标签中读出的外部标识，值已按种类规范化。
type Identifier struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

const (
	IdentifierMBRelease      = "mb_release"
	IdentifierMBReleaseGroup = "mb_release_group"
	IdentifierMBRecording    = "mb_recording"
	IdentifierMBTrack        = "mb_track"
	IdentifierMBArtist       = "mb_artist"
	IdentifierMBAlbumArtist  = "mb_album_artist"
	IdentifierMBWork         = "mb_work"
	IdentifierMBDisc         = "mb_disc"
	IdentifierAcoustID       = "acoustid"
	IdentifierISRC           = "isrc"

	maxIdentifiersPerKind = 32
)

var (
	identifierUUID = regexp.MustCompile(`[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}`)
	identifierISRC = regexp.MustCompile(`^[A-Z]{2}[A-Z0-9]{3}[0-9]{7}$`)
	identifierDisc = regexp.MustCompile(`^[A-Za-z0-9._-]{27}-$`)
)

// IdentifierKindForTag 只在标识键对照中忽略大小写和非字母数字字符。
func IdentifierKindForTag(key string) string {
	key = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, key)
	switch key {
	case "musicbrainzalbumid":
		return IdentifierMBRelease
	case "musicbrainzreleasegroupid":
		return IdentifierMBReleaseGroup
	case "musicbrainztrackid", "musicbrainzrecordingid":
		return IdentifierMBRecording
	case "musicbrainzreleasetrackid":
		return IdentifierMBTrack
	case "musicbrainzartistid":
		return IdentifierMBArtist
	case "musicbrainzalbumartistid":
		return IdentifierMBAlbumArtist
	case "musicbrainzworkid":
		return IdentifierMBWork
	case "musicbrainzdiscid":
		return IdentifierMBDisc
	case "acoustidid":
		return IdentifierAcoustID
	case "isrc":
		return IdentifierISRC
	default:
		return ""
	}
}

// AppendIdentifierValues 追加原文里的合法值，每种保留最先出现的 32 个不同值。
func AppendIdentifierValues(ids []Identifier, kind, raw string) []Identifier {
	count := 0
	for _, id := range ids {
		if id.Kind == kind {
			count++
		}
	}
	if count >= maxIdentifiersPerKind {
		return ids
	}
	add := func(value string) {
		id := Identifier{Kind: kind, Value: value}
		if !slices.Contains(ids, id) {
			id.Value = strings.Clone(value)
			ids = append(ids, id)
			count++
		}
	}
	switch kind {
	case IdentifierMBRelease, IdentifierMBReleaseGroup, IdentifierMBRecording, IdentifierMBTrack,
		IdentifierMBArtist, IdentifierMBAlbumArtist, IdentifierMBWork, IdentifierAcoustID:
		for count < maxIdentifiersPerKind {
			match := identifierUUID.FindStringIndex(raw)
			if match == nil {
				break
			}
			value := raw[match[0]:match[1]]
			if value != "00000000-0000-0000-0000-000000000000" {
				add(strings.ToLower(value))
			}
			raw = raw[match[1]:]
		}
	case IdentifierISRC:
		for token := range strings.FieldsFuncSeq(raw, func(r rune) bool {
			return r == '\x00' || r == '/' || r == ';' || r == ',' || unicode.IsSpace(r)
		}) {
			value := strings.ToUpper(strings.ReplaceAll(token, "-", ""))
			if identifierISRC.MatchString(value) {
				add(value)
				if count == maxIdentifiersPerKind {
					break
				}
			}
		}
	case IdentifierMBDisc:
		raw = strings.TrimFunc(raw, func(r rune) bool {
			return r == '\x00' || unicode.IsSpace(r)
		})
		if identifierDisc.MatchString(raw) {
			add(raw)
		}
	}
	return ids
}
