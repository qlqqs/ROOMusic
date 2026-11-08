package parser

import (
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

// finalizeTags 在文件名补位前按键优先级决定专辑艺人，仅在本版年份缺失时采用原始年份。
func finalizeTags(observation *AudioObservation) {
	var albumArtists [3][]string
	for _, entry := range observation.ArtistTagEvidence {
		if entry.Role != "album_artist" {
			continue
		}
		if priority := albumArtistTagPriority(entry.TagKey); priority > 0 {
			albumArtists[priority-1] = append(albumArtists[priority-1], entry.RawValue)
		}
	}
	for _, values := range albumArtists {
		if value := metadata.JoinArtistValues(values...); value != "" {
			observation.AlbumArtist = value
			break
		}
	}
	if observation.Year == 0 {
		for _, year := range observation.originalYears {
			if year > 0 {
				observation.Year = year
				break
			}
		}
	}
}

func albumArtistTagPriority(key string) int {
	switch strings.ToUpper(key) {
	case "ALBUMARTIST", "TPE2", "AART":
		return 1
	case "ALBUM_ARTIST", "TXXX:ALBUMARTIST", "----:ALBUMARTIST":
		return 2
	case "ALBUM ARTIST", "TXXX:ALBUM_ARTIST", "TXXX:ALBUM ARTIST", "----:ALBUM_ARTIST", "----:ALBUM ARTIST":
		return 3
	}
	return 0
}

func collectOriginalYear(observation *AudioObservation, key, value string) {
	for i, candidate := range [...]string{"ORIGINALDATE", "ORIGINALYEAR", "TDOR", "TORY", "TXXX:ORIGINALDATE", "TXXX:ORIGINALYEAR", "----:ORIGINALDATE", "----:ORIGINALYEAR"} {
		if key == candidate && observation.originalYears[i] == 0 {
			observation.originalYears[i] = ParseYear(value)
			return
		}
	}
}
