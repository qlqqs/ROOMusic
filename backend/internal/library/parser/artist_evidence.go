package parser

import (
	"errors"
	"sort"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

func artistTagRole(key string) string {
	key = strings.TrimPrefix(strings.TrimPrefix(strings.ToUpper(key), "TXXX:"), "----:")
	switch key {
	case "ARTIST", "TPE1", "IART", "©ART", "FEATURING":
		return "track_artist"
	case "ALBUMARTIST", "ALBUM_ARTIST", "ALBUM ARTIST", "TPE2", "AART", "ALBUMPERFORMER":
		return "album_artist"
	}
	if assignments := creditTagRoles(key); len(assignments) > 0 {
		return assignments[0].Role
	}
	return ""
}

func appendArtistEvidence(values *[]metadata.ArtistTagEvidence, role, key, raw, source string) error {
	if raw == "" {
		return nil
	}
	entry := metadata.ArtistTagEvidence{Role: role, TagKey: key, RawValue: raw, SourceKind: source}
	for _, previous := range *values {
		if previous.Role == role && previous.TagKey == key && previous.SourceKind == source {
			entry.OccurrenceIndex++
		}
	}
	// 先检查单项与总数，失败时不返回貌似完整的截断集合。
	if err := metadata.ValidateArtistTagEvidence([]metadata.ArtistTagEvidence{entry}); err != nil {
		return err
	}
	if len(*values) >= metadata.MaxArtistTagEvidenceItems {
		return errors.New("artist tag evidence exceeds item limit")
	}
	*values = append(*values, entry)
	return nil
}

func captureArtistTag(observation *AudioObservation, key, raw string) error {
	return captureArtistTagRole(observation, artistTagRole(key), key, raw)
}

func captureArtistTagRole(observation *AudioObservation, role, key, raw string) error {
	if role == "" {
		return nil
	}
	if err := appendArtistEvidence(&observation.ArtistTagEvidence, role, key, raw, observation.SourceKind); err != nil {
		return err
	}
	if role == "track_artist" {
		observation.Artist = artistEvidenceDisplay(observation.ArtistTagEvidence, role)
	}
	return nil
}

func artistEvidenceDisplay(evidence []metadata.ArtistTagEvidence, role string) string {
	names := make(map[string]bool)
	for _, entry := range evidence {
		if entry.Role != role {
			continue
		}
		for _, name := range metadata.CanonicalArtistNames(entry.RawValue) {
			names[name] = true
		}
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	return strings.Join(ordered, " / ")
}
