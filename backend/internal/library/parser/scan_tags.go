package parser

import (
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

// 只保留有固定优先级的键；长文本在读取时先分类。
type scanTagInput struct {
	values         map[string]string
	syncedPriority int
	firstGenreSeen bool
	bootlegType    bool
	sylt           [][]byte
}

func (o *AudioObservation) fileTags() *metadata.ScanTags {
	if o.ScanTags == nil {
		o.ScanTags = &metadata.ScanTags{}
	}
	return o.ScanTags
}

var timestampLine = regexp.MustCompile(`^\[[0-9]+:[0-9]+\.[0-9]+\]`)

func (o *AudioObservation) collectSynced(priority int, raw string) {
	if priority < o.scanInput.syncedPriority {
		return
	}
	text, ok := metadata.NormalizeLyrics(raw)
	if !ok {
		return
	}
	total, timed := 0, 0
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			total++
			if timestampLine.MatchString(line) {
				timed++
			}
		}
	}
	if timed*4 > total*3 {
		o.fileTags().SyncedLyrics = text
		o.scanInput.syncedPriority = priority
	}
}

func (o *AudioObservation) collectScanTag(key, value string) {
	key = strings.TrimPrefix(strings.TrimPrefix(strings.ToUpper(key), "TXXX:"), "----:")
	switch key {
	case "COMMENT", "DESCRIPTION", "COMM", "©CMT":
		text := strings.TrimSpace(strings.ReplaceAll(value, "\x00", ""))
		if text != "" && utf8.ValidString(text) {
			tags := o.fileTags()
			tags.Comments = append(tags.Comments, metadata.ScanComment{Key: key, Value: text})
		}
	case "PCST", "ISPODCAST":
		o.fileTags().Podcast = true
	case "PCSP", "ISAUDIOBOOK":
		o.fileTags().Audiobook = true
	case "SYLT":
		o.collectSynced(1, value)
	case "SYNCED LYRICS", "SYNCEDLYRICS":
		o.collectSynced(2, value)
	case "LYRICS":
		o.collectSynced(3, value)
	case "©LYR":
		o.collectSynced(4, value)
	case "UNSYNCED LYRICS", "UNSYNCEDLYRICS":
		o.collectSynced(5, value)
	case "USLT":
		o.collectSynced(6, value)
	case "MEDIATYPE", "MEDIA_TYPE", "RELEASETYPE", "RELEASE_TYPE", "MUSICBRAINZ ALBUM TYPE":
		if strings.EqualFold(strings.TrimSpace(value), "bootleg") {
			o.scanInput.bootlegType = true
		}
	case "TRCK", "TRACK", "TRACKNUMBER", "TRACKTOTAL", "TRACKCOUNT", "TOTALTRACKS",
		"TPOS", "DISCNUMBER", "DISC", "MEDIA", "DISC #", "DISCTOTAL", "DISCCOUNT", "TOTALDISCS", "DISCC", "MEDIACOUNT",
		"TDOR", "TDRC", "TDRL", "RELEASED", "DATE", "YEAR", "TYER", "RELEASEDATE", "ORIGINALDATE", "TORY", "ORIGINALRELEASEDATE",
		"RECORDING DATE", "PERFORMANCE DATE", "RECORDINGDATE", "PERFORMANCEDATE",
		"RECORDING START DATE", "PERFORMANCE START DATE", "RECORDINGSTARTDATE", "PERFORMANCESTARTDATE",
		"RECORDING END DATE", "PERFORMANCE END DATE", "RECORDINGENDDATE", "PERFORMANCEENDDATE",
		"ISSINGLE", "LIVE", "BOOTLEG", "ISEXTENDEDPLAY", "ISSOUNDTRACK", "PRIMARYVERSION", "BESTVERSION",
		"REPLAYGAIN_TRACK_GAIN", "REPLAYGAIN_ALBUM_GAIN", "REPLAYGAIN_TRACK_PEAK", "REPLAYGAIN_ALBUM_PEAK":
		if o.scanInput.values == nil {
			o.scanInput.values = map[string]string{}
		}
		o.scanInput.values[key] = strings.TrimSpace(value)
	}
}

func scanInt(raw string) (int, bool) {
	n, err := strconv.Atoi(strings.ReplaceAll(strings.TrimSpace(raw), ",", ""))
	return n, err == nil
}

func scanTotal(values map[string]string, numberKeys, totalKeys []string, canonical string) int {
	var total int
	for _, key := range numberKeys {
		raw := values[key]
		if raw == "" {
			continue
		}
		n, den, ok := strings.Cut(raw, "/")
		if ok && n != "" {
			total, _ = scanInt(den)
		}
		break
	}
	if total == 0 {
		for _, key := range totalKeys {
			if raw := values[key]; raw != "" {
				total, _ = scanInt(raw)
				break
			}
		}
	}
	if n, ok := scanInt(values[canonical]); ok {
		return n
	}
	return total
}

func scanDate(raw string) *metadata.ScanDate {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ' ' || r == '/' || r == ':' })
	if len(parts) == 2 && len(parts[1]) == 4 && strings.Trim(parts[1], "0123456789") == "" {
		if year, err := strconv.Atoi(parts[1]); err == nil && year > 0 {
			month, err := strconv.Atoi(parts[0])
			if err == nil && strings.Trim(parts[0], "0123456789") == "" && month <= 255 {
				return &metadata.ScanDate{Year: year, Month: month}
			}
			for m := time.January; m <= time.December; m++ {
				if strings.EqualFold(parts[0], m.String()) || strings.EqualFold(parts[0], m.String()[:3]) {
					return &metadata.ScanDate{Year: year, Month: int(m)}
				}
			}
		}
	}
	for _, layout := range []string{"2006", "2006-01", "2006-01-02", time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006/1/2", "1/2/2006", "January 2, 2006", "January 2 2006", "2 January 2006", "Jan 2, 2006", "2 Jan 2006"} {
		d, err := time.Parse(layout, raw)
		if err != nil || d.Year() == 0 {
			continue
		}
		date := &metadata.ScanDate{Year: d.Year()}
		if layout != "2006" {
			date.Month = int(d.Month())
		}
		if layout != "2006" && layout != "2006-01" {
			date.Day = d.Day()
		}
		return date
	}
	return &metadata.ScanDate{}
}

func finalScanTags(o *AudioObservation) {
	input := o.scanInput
	values := input.values
	for _, frame := range input.sylt {
		o.collectSYLT(frame)
	}
	o.scanInput = scanTagInput{}
	if len(values) == 0 && !input.bootlegType {
		return
	}
	tags := o.fileTags()
	tags.TrackTotal = scanTotal(values, []string{"TRCK", "TRACK", "TRACKNUMBER"}, []string{"TRACKTOTAL", "TRACKCOUNT", "TOTALTRACKS"}, "TRACKCOUNT")
	discKeys := []string{"TPOS", "DISCNUMBER", "DISC", "MEDIA", "DISC #"}
	tags.DiscTotal = scanTotal(values, discKeys, []string{"DISCTOTAL", "DISCCOUNT", "TOTALDISCS", "DISCC"}, "MEDIACOUNT")
	if tags.DiscTotal != 0 {
		disc := o.DiscNumber
		for _, key := range discKeys {
			if value := values[key]; value != "" {
				disc = max(disc, parseNumber(value))
				break
			}
		}
		tags.DiscTotal = max(tags.DiscTotal, max(1, disc))
	}
	for _, key := range []string{"TDOR", "TDRC", "TDRL", "RELEASED", "DATE", "YEAR", "TYER"} {
		if raw, ok := values[key]; ok {
			tags.ReleaseDate = scanDate(raw)
			break
		}
	}
	if raw, ok := values["RELEASEDATE"]; ok {
		tags.ReleaseDate = scanDate(raw)
	}
	for _, spec := range []struct {
		dst  **metadata.ScanDate
		keys []string
	}{
		{&tags.OriginalReleaseDate, []string{"ORIGINALDATE", "TDOR", "TORY", "ORIGINALRELEASEDATE"}},
		{&tags.RecordingDate, []string{"RECORDING DATE", "PERFORMANCE DATE", "RECORDINGDATE", "PERFORMANCEDATE"}},
		{&tags.RecordingStartDate, []string{"RECORDING START DATE", "PERFORMANCE START DATE", "RECORDINGSTARTDATE", "PERFORMANCESTARTDATE"}},
		{&tags.RecordingEndDate, []string{"RECORDING END DATE", "PERFORMANCE END DATE", "RECORDINGENDDATE", "PERFORMANCEENDDATE"}},
	} {
		for _, key := range spec.keys {
			if raw, ok := values[key]; ok {
				*spec.dst = scanDate(raw)
			}
		}
	}
	if input.bootlegType {
		v := true
		tags.Bootleg = &v
	}
	for _, spec := range []struct {
		key string
		dst **bool
	}{
		{"ISSINGLE", &tags.Single}, {"LIVE", &tags.Live}, {"BOOTLEG", &tags.Bootleg}, {"ISEXTENDEDPLAY", &tags.EP}, {"ISSOUNDTRACK", &tags.Soundtrack}, {"BESTVERSION", &tags.PrimaryVersion}, {"PRIMARYVERSION", &tags.PrimaryVersion},
	} {
		if raw, ok := values[spec.key]; ok {
			v := strings.ToLower(raw)
			truth := v == "1" || v == "yes" || v == "true"
			*spec.dst = &truth
		}
	}
	for _, spec := range []struct {
		key string
		dst **float64
	}{
		{"REPLAYGAIN_TRACK_GAIN", &tags.TrackGain}, {"REPLAYGAIN_ALBUM_GAIN", &tags.AlbumGain}, {"REPLAYGAIN_TRACK_PEAK", &tags.TrackPeak}, {"REPLAYGAIN_ALBUM_PEAK", &tags.AlbumPeak},
	} {
		raw := values[spec.key]
		if strings.HasSuffix(spec.key, "_GAIN") {
			raw = strings.TrimSpace(strings.TrimSuffix(strings.ToLower(raw), "db"))
		}
		if n, err := strconv.ParseFloat(strings.ReplaceAll(raw, ",", ""), 64); err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) {
			*spec.dst = &n
		}
	}
	if reflect.ValueOf(*tags).IsZero() {
		o.ScanTags = nil
	}

}
