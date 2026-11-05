package parser

import (
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

func ParseYear(value string) int {
	value = strings.TrimSpace(value)
	if len(value) < 4 {
		return 0
	}
	year, err := strconv.Atoi(value[:4])
	if err != nil || year < 1 {
		return 0
	}
	return year
}

func estimatedBitrateKbps(fileSize int64, durationSeconds float64) int {
	if fileSize <= 0 || durationSeconds <= 0 || math.IsNaN(durationSeconds) || math.IsInf(durationSeconds, 0) {
		return 0
	}
	bitrate := float64(fileSize) * 8 / durationSeconds / 1000
	if bitrate <= 0 || math.IsNaN(bitrate) || math.IsInf(bitrate, 0) || bitrate > float64(math.MaxInt32) {
		return 0
	}
	return int(bitrate)
}

func parseNumber(value string) int {
	returnValue, _ := strconv.Atoi(strings.SplitN(value, "/", 2)[0])
	return returnValue
}

func setAudioTag(observation *AudioObservation, key, value string) error {
	if setAudioLyrics(observation, key, value) {
		return nil
	}
	if err := captureArtistTag(observation, key, value); err != nil {
		return err
	}
	value = metadata.BoundedText(value)
	switch strings.ToUpper(key) {
	case "TITLE", "TRACKTITLE":
		observation.Title = value
	case "ALBUMARTIST", "ALBUM ARTIST":
		observation.AlbumArtist = value
	case "ALBUM", "ALBUMTITLE":
		observation.Album = value
	case "TRACKNUMBER":
		observation.TrackNumber = parseNumber(value)
	case "DISCNUMBER":
		observation.DiscNumber = parseNumber(value)
	case "GENRE":
		observation.Genre = value
	case "DATE", "YEAR":
		observation.Year = ParseYear(value)
	case "CATALOG", "CATALOGNUMBER", "CATALOGNO":
		observation.Catalog = value
	case "LABEL", "PUBLISHER", "ORGANIZATION":
		if observation.Label == "" {
			observation.Label = value
		}
	case "BARCODE", "UPC", "EAN":
		if observation.Barcode == "" {
			observation.Barcode = value
		}
	case "EDITION", "VERSION", "SUBTITLE", "RELEASEVERSION":
		if observation.Edition == "" {
			observation.Edition = value
		}
	case "COMPOSER", "TCOM":
		addObservationCredits(observation, "composer", value)
	case "CONDUCTOR", "TPE3":
		addObservationCredits(observation, "conductor", value)
	case "PERFORMER", "PERFORMERS":
		addObservationCredits(observation, "performer", value)
	case "PRODUCER":
		addObservationCredits(observation, "producer", value)
	case "ISRC":
		observation.ISRC = value
	case "SOURCE", "SOURCETYPE":
		observation.SourceType = value
	case "MEDIA", "MEDIATYPE":
		observation.MediaType = value
	}
	return nil
}

func addObservationCredits(observation *AudioObservation, role, value string) {
	if observation == nil {
		return
	}
	role = strings.ToLower(metadata.NormalizeValue(role))
	if role == "" {
		return
	}
	for _, name := range splitCreditNames(value) {
		credit := metadata.Credit{Role: role, Name: name}
		duplicate := false
		for _, existing := range observation.Credits {
			if existing == credit {
				duplicate = true
				break
			}
		}
		if !duplicate {
			observation.Credits = append(observation.Credits, credit)
		}
	}
}

func splitCreditNames(value string) []string {
	return metadata.CanonicalArtistNames(value)
}

func applyFilenameFallback(path string, observation AudioObservation) AudioObservation {
	if observation.InferredFields == nil {
		observation.InferredFields = make(map[string]bool)
	}
	if observation.Title == "" {
		observation.Title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		observation.Inferred = true
		observation.InferredFields["title"] = true
	}
	if observation.Artist == "" {
		observation.Artist = "未知艺术家"
		observation.Inferred = true
		observation.InferredFields["artist"] = true
	}
	if observation.Album == "" {
		observation.Album = filepath.Base(filepath.Dir(path))
		observation.Inferred = true
		observation.InferredFields["album"] = true
	}
	if observation.TrackNumber <= 0 {
		observation.TrackNumber = 1
		observation.Inferred = true
		observation.InferredFields["track_number"] = true
	}
	if observation.DiscNumber <= 0 {
		observation.DiscNumber = 1
		observation.Inferred = true
		observation.InferredFields["disc_number"] = true
	}
	return observation
}
