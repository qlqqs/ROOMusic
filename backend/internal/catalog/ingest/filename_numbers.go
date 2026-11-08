package ingest

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// filenameNumbers 保留独立的文件名证据；即使标签优先，也可用于连续性检验。
// prefixEnd 是 stem 中可清除的编号前缀末端，按字节计。
type filenameNumbers struct {
	Disc, Track int
	prefixEnd   int
	pattern     string
	firstEnd    int
}

var (
	filenameLeadingNumbers = regexp.MustCompile(`^\s*(?:(\d{1,2})[\s_\-\.]+)?(\d{1,2})(?:\D.*)?$`)
	filenameTrackWord      = regexp.MustCompile(`(?i)(?:^|[^a-z])track[\s_.\-]*(\d{1,2})(?:\D|$)`)
)

func filenameStem(relativePath string) string {
	name := filepath.Base(filepath.FromSlash(relativePath))
	return strings.TrimSpace(strings.TrimSuffix(name, filepath.Ext(name)))
}

func filenamePrefixEnd(stem string, end int) int {
	return len(stem) - len(strings.TrimLeftFunc(stem[end:], func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("._-", r)
	}))
}

func asciiDigit(b byte) bool { return b >= '0' && b <= '9' }

func filenameNumber(text string) int {
	value, _ := strconv.Atoi(text)
	return value
}

// inferFilenameNumbers 按五级规则取第一个非零轨号；目录多数保护由调用方应用。
func inferFilenameNumbers(stem string) filenameNumbers {
	stem = strings.TrimSpace(stem)
	leading := 0
	for leading < len(stem) && asciiDigit(stem[leading]) {
		leading++
	}
	if leading == len(stem) && leading > 0 && leading <= 2 {
		if track := filenameNumber(stem); track > 0 {
			return filenameNumbers{Track: track, prefixEnd: len(stem)}
		}
	}
	if match := filenameLeadingNumbers.FindStringSubmatchIndex(stem); match != nil {
		if track := filenameNumber(stem[match[4]:match[5]]); track > 0 {
			result := filenameNumbers{Track: track, prefixEnd: filenamePrefixEnd(stem, match[5])}
			if match[2] >= 0 {
				result.Disc = filenameNumber(stem[match[2]:match[3]])
				result.pattern = "disc_track"
				result.firstEnd = filenamePrefixEnd(stem, match[3])
			}
			return result
		}
	}
	if leading == 3 || leading == 4 && stem[0] == '0' {
		if track := filenameNumber(stem[leading-2 : leading]); track > 0 {
			pattern := "packed_three"
			if leading == 4 {
				pattern = "packed_four"
			}
			return filenameNumbers{Disc: filenameNumber(stem[:leading-2]), Track: track, prefixEnd: filenamePrefixEnd(stem, leading), pattern: pattern}
		}
	}
	return inferFilenameTrailingNumber(stem)
}

func inferFilenameTrailingNumber(stem string) filenameNumbers {
	start := len(stem)
	for start > 0 && asciiDigit(stem[start-1]) {
		start--
	}
	if digits := len(stem) - start; digits > 0 && digits <= 2 {
		if track := filenameNumber(stem[start:]); track > 0 {
			return filenameNumbers{Track: track}
		}
	}
	for _, match := range filenameTrackWord.FindAllStringSubmatchIndex(stem, -1) {
		if track := filenameNumber(stem[match[2]:match[3]]); track > 0 {
			result := filenameNumbers{Track: track}
			if match[0] == 0 && strings.HasPrefix(strings.ToLower(stem), "track") {
				result.prefixEnd = filenamePrefixEnd(stem, match[3])
			}
			return result
		}
	}
	return filenameNumbers{}
}

// prepareFilenameEvidence 仅修改 organizer 私有的切片和 map 副本，不污染调用方观察。
func prepareFilenameEvidence(items []SourceObservation) map[string]filenameNumbers {
	type directoryEvidence struct {
		paths    map[string]bool
		patterns map[string]int
	}
	directories := map[string]*directoryEvidence{}
	numbers := map[string]filenameNumbers{}
	for _, observation := range items {
		if isCueObservation(observation) {
			continue
		}
		path := NormalizedPath(observation.RelativePath)
		dir := locateObservation(observation).dir
		if directories[dir] == nil {
			directories[dir] = &directoryEvidence{paths: map[string]bool{}, patterns: map[string]int{}}
		}
		evidence := directories[dir]
		if evidence.paths[path] {
			continue
		}
		evidence.paths[path] = true
		parsed := inferFilenameNumbers(filenameStem(path))
		numbers[path] = parsed
		if parsed.pattern != "" {
			evidence.patterns[parsed.pattern]++
		}
	}
	for _, evidence := range directories {
		for path := range evidence.paths {
			parsed := numbers[path]
			count := evidence.patterns[parsed.pattern]
			if parsed.pattern == "" || count >= 2 && count*2 > len(evidence.paths) {
				continue
			}
			if parsed.pattern == "disc_track" && parsed.Disc > 0 {
				parsed = filenameNumbers{Track: parsed.Disc, prefixEnd: parsed.firstEnd}
			} else {
				parsed = inferFilenameTrailingNumber(filenameStem(path))
			}
			numbers[path] = parsed
		}
	}
	for index := range items {
		observation := &items[index]
		if isCueObservation(*observation) {
			continue
		}
		parsed := numbers[NormalizedPath(observation.RelativePath)]
		observation.FieldSources = cloneStringMap(observation.FieldSources)
		if observation.InferredFields["track_number"] && parsed.Track > 0 {
			observation.TrackNumber = parsed.Track
			observation.FieldSources["track_number"] = "filename_fallback"
		}
		if observation.InferredFields["disc_number"] && observation.FieldSources["disc_number"] != "album_title" && parsed.Disc > 0 {
			observation.DiscNumber = parsed.Disc
			observation.FieldSources["disc_number"] = "filename_fallback"
		}
		if !observation.InferredFields["title"] || observation.FieldSources["title"] != "filename_fallback" {
			continue
		}
		stem := filenameStem(observation.RelativePath)
		title := strings.TrimSpace(stem[parsed.prefixEnd:])
		if title == "" {
			title = stem
		}
		observation.Title = title
		if !observation.InferredFields["artist"] || observation.FieldSources["artist"] != "display_fallback" || strings.Count(title, " - ") != 1 {
			continue
		}
		artist, title, _ := strings.Cut(title, " - ")
		artist, title = strings.TrimSpace(artist), strings.TrimSpace(title)
		if artist == "" || title == "" || strings.TrimFunc(artist, unicode.IsDigit) == "" {
			continue
		}
		observation.Artist, observation.Title = artist, title
		observation.FieldSources["artist"] = "filename_fallback"
	}
	return numbers
}

// discardConflictingFilenamePositions 在同介质任一文件名轨号重号时，整批文件名轨号退回自然序。
func discardConflictingFilenamePositions(tracks []OrganizedTrack) {
	counts := map[int]int{}
	for _, track := range tracks {
		if track.Position > 0 {
			counts[track.Position]++
		}
	}
	conflict := false
	for _, track := range tracks {
		if track.Observation.InferredFields["track_number"] && track.Observation.FieldSources["track_number"] == "filename_fallback" && counts[track.Position] > 1 {
			conflict = true
			break
		}
	}
	if !conflict {
		return
	}
	for index := range tracks {
		if tracks[index].Observation.InferredFields["track_number"] && tracks[index].Observation.FieldSources["track_number"] == "filename_fallback" {
			tracks[index].Position = 0
		}
	}
}
