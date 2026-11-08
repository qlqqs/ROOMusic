package ingest

import (
	"path/filepath"
	"sort"
	"strings"
)

// discTrack 是连续性证据中的碟轨位置，Track 为零表示没有可用轨号。
type discTrack struct{ Disc, Track int }

// continuousAlbum 要求至少两首、存在第一碟、每碟恰好是 1..n；允许缺碟。
func continuousAlbum(pairs []discTrack) bool {
	return continuousTracks(pairs, 2)
}

func continuousTracks(pairs []discTrack, minimum int) bool {
	if len(pairs) < minimum {
		return false
	}
	discs := map[int]map[int]bool{}
	for _, pair := range pairs {
		if pair.Track < 1 {
			return false
		}
		disc := max(pair.Disc, 1)
		if discs[disc] == nil {
			discs[disc] = map[int]bool{}
		}
		if discs[disc][pair.Track] {
			return false
		}
		discs[disc][pair.Track] = true
	}
	if discs[1] == nil {
		return false
	}
	for _, tracks := range discs {
		for track := range tracks {
			if track > len(tracks) {
				return false
			}
		}
	}
	return true
}

// observationDiscNumber 是非合成候选的碟号优先级，与混合连续性证据共用。
func observationDiscNumber(o SourceObservation) int {
	if o.DiscNumber > 0 && (!o.InferredFields["disc_number"] || o.FieldSources["disc_number"] == "album_title") {
		return o.DiscNumber
	}
	if location := locateObservation(o); location.isDiscDir {
		return location.disc
	}
	if o.DiscNumber > 0 && o.FieldSources["disc_number"] == "filename_fallback" {
		return o.DiscNumber
	}
	return 1
}

func continuityPairs(observations []SourceObservation, filenames map[string]filenameNumbers) (mixed, filenameOnly []discTrack) {
	for _, observation := range observations {
		numbers := filenames[NormalizedPath(observation.RelativePath)]
		track := observation.TrackNumber
		if track < 1 || observation.InferredFields["track_number"] {
			track = numbers.Track
		}
		mixed = append(mixed, discTrack{Disc: observationDiscNumber(observation), Track: track})
		disc := numbers.Disc
		if disc < 1 {
			disc = locateObservation(observation).disc
		}
		filenameOnly = append(filenameOnly, discTrack{Disc: max(disc, 1), Track: numbers.Track})
	}
	return mixed, filenameOnly
}

func continuousObservations(observations []SourceObservation, filenames map[string]filenameNumbers) bool {
	mixed, filenameOnly := continuityPairs(observations, filenames)
	return continuousAlbum(mixed) || continuousAlbum(filenameOnly)
}

// splitContinuousFormats 先验证各格式完整，再要求所采用的连续性证据存在跨格式重号。
func splitContinuousFormats(observations []SourceObservation, filenames map[string]filenameNumbers) map[string][]SourceObservation {
	formats := map[string][]SourceObservation{}
	for _, observation := range observations {
		format := strings.ToLower(observation.Codec)
		if format == "" {
			format = strings.TrimPrefix(strings.ToLower(filepath.Ext(observation.RelativePath)), ".")
		}
		formats[format] = append(formats[format], observation)
	}
	if len(formats) < 2 {
		return nil
	}
	keys := make([]string, 0, len(formats))
	for format := range formats {
		keys = append(keys, format)
	}
	sort.Strings(keys)
	seen, duplicate := map[discTrack]bool{}, false
	for _, format := range keys {
		mixed, filenameOnly := continuityPairs(formats[format], filenames)
		pairs := mixed
		if !continuousTracks(pairs, 1) {
			pairs = filenameOnly
			if !continuousTracks(pairs, 1) {
				return nil
			}
		}
		for _, pair := range pairs {
			if seen[pair] {
				duplicate = true
			}
			seen[pair] = true
		}
	}
	if !duplicate {
		return nil
	}
	return formats
}
