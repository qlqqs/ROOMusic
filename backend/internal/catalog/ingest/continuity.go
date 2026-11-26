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
	if o.structureDisc > 0 {
		return o.structureDisc
	}
	if o.DiscNumber > 0 && (!o.InferredFields["disc_number"] || o.FieldSources["disc_number"] == "album_title" || o.FieldSources["disc_number"] == "vinyl_side" || o.FieldSources["disc_number"] == "folder_structure") {
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

func continuousEvidence(observations []SourceObservation, filenames map[string]filenameNumbers, minimum int) []discTrack {
	firstSide := vinylSideLengths(observations)
	var modes [4][]discTrack
	for _, o := range observations {
		f := filenames[NormalizedPath(o.RelativePath)]
		tag := discTrack{o.DiscNumber, vinylPosition(o, firstSide)}
		folder := o.structureDisc > 0
		if folder {
			tag.Disc = o.structureDisc
		}
		if o.InferredFields["disc_number"] && !folder && o.VinylSide == 0 && o.FieldSources["disc_number"] != "album_title" {
			tag.Disc = 0
		}
		if o.InferredFields["track_number"] && o.VinylSide == 0 {
			tag.Track = 0
		}
		file := discTrack{f.Disc, f.Track}
		if file.Disc == 0 && folder {
			file.Disc = tag.Disc
		}
		mixed := tag
		if mixed.Track == 0 {
			mixed.Track = file.Track
		}
		if mixed.Disc == 0 {
			mixed.Disc = file.Disc
		}
		for i, v := range []discTrack{tag, file, mixed, {1, f.Track}} {
			v.Disc = max(v.Disc, 1)
			modes[i] = append(modes[i], v)
		}
	}
	for _, pairs := range modes {
		if continuousTracks(pairs, minimum) {
			return pairs
		}
	}
	return nil
}

func continuousObservations(observations []SourceObservation, filenames map[string]filenameNumbers) bool {
	return continuousEvidence(observations, filenames, 2) != nil
}

// observationFormat 返回格式拆分与内嵌封面去重共用的格式键。
func observationFormat(observation SourceObservation) string {
	format := strings.ToLower(observation.Codec)
	if format == "" {
		format = strings.TrimPrefix(strings.ToLower(filepath.Ext(observation.RelativePath)), ".")
	}
	return format
}

// splitContinuousFormats 先验证各格式完整，再要求所采用的连续性证据存在跨格式重号。
func splitContinuousFormats(observations []SourceObservation, filenames map[string]filenameNumbers) map[string][]SourceObservation {
	formats := groupByFormat(observations)

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
		pairs := continuousEvidence(formats[format], filenames, 1)
		if pairs == nil {
			return nil
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

func groupByFormat(observations []SourceObservation) map[string][]SourceObservation {
	formats := map[string][]SourceObservation{}
	for _, observation := range observations {
		format := observationFormat(observation)
		formats[format] = append(formats[format], observation)
	}
	return formats
}

// 后一面从前一面的最大面内曲号继续，同一碟中的曲位才可与外部曲序比较。
func vinylSideLengths(tracks []SourceObservation) map[int]int {
	firstSide := map[int]int{}
	for _, t := range tracks {
		if t.VinylSide%2 == 1 {
			firstSide[t.VinylSide] = max(firstSide[t.VinylSide], t.TrackNumber)
		}
	}
	return firstSide
}

func vinylPosition(track SourceObservation, firstSide map[int]int) int {
	if track.VinylSide > 0 && track.VinylSide%2 == 0 {
		return track.TrackNumber + firstSide[track.VinylSide-1]
	}
	return track.TrackNumber
}

func applyVinylPositions(tracks []SourceObservation) {
	firstSide := vinylSideLengths(tracks)
	for i := range tracks {
		tracks[i].TrackNumber = vinylPosition(tracks[i], firstSide)
	}
}
