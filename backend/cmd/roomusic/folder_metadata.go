package main

import (
	"regexp"
	"strconv"
	"strings"
)

// folderMetadata 是目录名解析产生的辅助证据。它只补充候选中缺失的发行字段，
// 不参与来源身份或候选归组。
type folderMetadata struct {
	AlbumArtist string
	Album       string
	Year        int
	SourceType  string
	MediaType   string
	Edition     string
	Label       string
	Catalog     string
	format      string
	provider    string
	bitDepth    string
	pattern     string
}

const folderMetadataMaxBytes = 1024

var (
	folderEACPattern           = regexp.MustCompile(`^\[EAC\]\[([^\]]+)\]\[([^\]]+)\]\[([^\]]+)\]\[([^\]]+)\](?:\[([^\]]*)\])?`)
	folderJPPTPattern          = regexp.MustCompile(`^\[([^\]]+)\]\[([^\]]*)\]\[([^\]]+)\]\[([^\]]+)\](?:\[([^\]]*)\])?(?:\[([^\]]+)\])?`)
	folderJPPTDashPattern      = regexp.MustCompile(`^\[([^\]]+)\]\s*(.+?)\s+-\s+(.+?)\s+((?:\[[^\]]+\])+)$`)
	folderDateTitlePattern     = regexp.MustCompile(`^\[([^\]]+)\]\s*(.+?)\s+((?:\[[^\]]+\]\s*)+)$`)
	folderWebDLPattern         = regexp.MustCompile(`^(.+?)\s+-\s+(.+?)\s+\((\d{4})\)\s+-\s+WEB-DL\s+-\s+(\d+)(?:bit)?\s+(\w+)`)
	folderDICPattern           = regexp.MustCompile(`^(.+?)\s+-\s+(.+?)\s+\((\d{4})\)\s+-\s+(\w+)\s+(\w+)\s+\[([^\]]+)\]$`)
	folderDICVariant2Pattern   = regexp.MustCompile(`^(.+?)\s+-\s+(.+?)\s+\((\d{4})\)\s+\[([^\]]+)\]\s+\{([^}]+)\}$`)
	folderDICVariant3Pattern   = regexp.MustCompile(`^(.+?)\s+-\s+(.+?)\s+\((\d{4})\)\s+\{([^}]+)\}\s+\[([^\]]+)\]$`)
	folderBracketSuffixPattern = regexp.MustCompile(`^(.+?)\s+-\s+(.+?)\s+\((\d{4})\)\s+((?:\[[^\]]+\]\s*)+)$`)
	folderSimplePattern        = regexp.MustCompile(`^(.+?)\s+-\s+(.+?)(?:\s*\((\d{4})\))?$`)
	folderBracketFieldPattern  = regexp.MustCompile(`\[([^\]]+)\]`)
	folderCatalogPattern       = regexp.MustCompile(`(?i)^[A-Z]{2,}[A-Z0-9]*(?:-\d+)+$|^[A-Z]{2,}-?\d+$|^\d{10,}$`)
	folderBitDepthPattern      = regexp.MustCompile(`(?i)^\d+bit$`)
	folderBitDepthTokenPattern = regexp.MustCompile(`(?i)(\d+)\s*bit`)
	folderMediaPattern         = regexp.MustCompile(`(?i)^(CD|WEB|Vinyl|SACD|Blu-ray|DVD)$`)
)

func parseFolderMetadata(name string) (folderMetadata, bool) {
	if len(name) == 0 || len(name) > folderMetadataMaxBytes {
		return folderMetadata{}, false
	}
	if match := folderEACPattern.FindStringSubmatch(name); match != nil {
		result := folderMetadata{AlbumArtist: match[3], Album: match[4], Catalog: match[2], MediaType: "cd", pattern: "EAC"}
		applyFolderDate(match[1], &result)
		result.Edition = match[5]
		return finishFolderMetadata(result), true
	}
	if match := folderJPPTPattern.FindStringSubmatch(name); match != nil {
		result := folderMetadata{AlbumArtist: match[3], Album: match[4], pattern: "JP-PT"}
		if isUpperFolderReleaseKind(match[2]) {
			result.Album, result.AlbumArtist = match[3], match[4]
		}
		applyFolderDate(match[1], &result)
		applyFolderBracketField(match[5], &result)
		applyFolderBracketField(match[6], &result)
		return finishFolderMetadata(result), true
	}
	if match := folderJPPTDashPattern.FindStringSubmatch(name); match != nil {
		result := folderMetadata{AlbumArtist: match[2], Album: match[3], pattern: "JP-PT-Dash"}
		applyFolderDate(match[1], &result)
		for _, field := range folderBracketFields(match[4]) {
			applyFolderBracketField(field, &result)
		}
		return finishFolderMetadata(result), true
	}
	if match := folderDateTitlePattern.FindStringSubmatch(name); match != nil {
		result := folderMetadata{Album: match[2], pattern: "Date-Title-Bracket"}
		applyFolderDate(match[1], &result)
		for _, field := range folderBracketFields(match[3]) {
			applyFolderBracketField(field, &result)
		}
		return finishFolderMetadata(result), true
	}
	if match := folderWebDLPattern.FindStringSubmatch(name); match != nil {
		year, _ := strconv.Atoi(match[3])
		return finishFolderMetadata(folderMetadata{AlbumArtist: match[1], Album: match[2], Year: year, MediaType: "web", bitDepth: match[4] + "bit", format: match[5], pattern: "WEB-DL"}), true
	}
	if match := folderDICPattern.FindStringSubmatch(name); match != nil {
		year, _ := strconv.Atoi(match[3])
		result := folderMetadata{AlbumArtist: match[1], Album: match[2], Year: year, MediaType: match[4], format: match[5], pattern: "DIC"}
		parseFolderExtraFields(match[6], &result)
		return finishFolderMetadata(result), true
	}
	if match := folderDICVariant2Pattern.FindStringSubmatch(name); match != nil {
		year, _ := strconv.Atoi(match[3])
		result := folderMetadata{AlbumArtist: match[1], Album: match[2], Year: year, pattern: "DIC-Variant"}
		applyFolderBracketField(match[4], &result)
		parseFolderExtraFields(match[5], &result)
		return finishFolderMetadata(result), true
	}
	if match := folderDICVariant3Pattern.FindStringSubmatch(name); match != nil {
		year, _ := strconv.Atoi(match[3])
		result := folderMetadata{AlbumArtist: match[1], Album: match[2], Year: year, pattern: "DIC-Variant"}
		parseFolderExtraFields(match[4], &result)
		applyFolderBracketField(match[5], &result)
		return finishFolderMetadata(result), true
	}
	if match := folderBracketSuffixPattern.FindStringSubmatch(name); match != nil {
		year, _ := strconv.Atoi(match[3])
		result := folderMetadata{AlbumArtist: match[1], Album: match[2], Year: year, pattern: "Bracket-Suffix"}
		for _, field := range folderBracketFields(match[4]) {
			applyFolderBracketField(field, &result)
		}
		return finishFolderMetadata(result), true
	}
	if match := folderSimplePattern.FindStringSubmatch(name); match != nil {
		year, _ := strconv.Atoi(match[3])
		return finishFolderMetadata(folderMetadata{AlbumArtist: match[1], Album: match[2], Year: year, pattern: "Simple"}), true
	}
	return folderMetadata{}, false
}

func finishFolderMetadata(result folderMetadata) folderMetadata {
	result.AlbumArtist = normalizeValue(result.AlbumArtist)
	result.Album = normalizeValue(result.Album)
	result.MediaType = normalizeFolderToken(result.MediaType)
	result.SourceType = folderSourceType(result.MediaType)
	result.Edition = normalizeValue(result.Edition)
	result.Label = normalizeValue(result.Label)
	result.Catalog = normalizeValue(result.Catalog)
	result.format = strings.ToLower(normalizeValue(result.format))
	return result
}

func normalizeFolderToken(value string) string {
	value = strings.ToLower(normalizeValue(value))
	value = strings.ReplaceAll(value, "_", "-")
	switch value {
	case "web-dl", "webdl", "download", "digital":
		return "web"
	case "compact disc":
		return "cd"
	default:
		return value
	}
}

func folderSourceType(media string) string {
	switch media {
	case "web", "cd", "sacd", "vinyl":
		return media
	default:
		return ""
	}
}

func applyFolderDate(value string, result *folderMetadata) {
	if result == nil {
		return
	}
	value = strings.TrimSpace(value)
	if len(value) >= 6 && value[:2] >= "00" && value[:2] <= "49" {
		result.Year, _ = strconv.Atoi("20" + value[:2])
	} else if len(value) >= 6 && value[:2] >= "50" && value[:2] <= "99" {
		result.Year, _ = strconv.Atoi("19" + value[:2])
	}
	if len(value) >= 8 && value[:4] >= "1900" && value[:4] <= "2099" {
		result.Year, _ = strconv.Atoi(value[:4])
	}
}

func isUpperFolderReleaseKind(value string) bool {
	trimmed := strings.TrimSpace(value)
	switch strings.ToLower(trimmed) {
	case "album", "single", "ep", "ost", "soundtrack", "hi-res", "hires":
		return trimmed == strings.ToUpper(trimmed)
	default:
		return false
	}
}

func folderBracketFields(value string) []string {
	matches := folderBracketFieldPattern.FindAllStringSubmatch(value, -1)
	result := make([]string, 0, len(matches))
	for _, match := range matches {
		if field := normalizeValue(match[1]); field != "" {
			result = append(result, field)
		}
	}
	return result
}

func applyFolderBracketField(field string, result *folderMetadata) {
	if result == nil {
		return
	}
	field = normalizeValue(field)
	bundleMedia := folderMediaFromBundle(field)
	if bundleMedia != "" {
		result.MediaType = bundleMedia
	}
	switch {
	case field == "":
		return
	case folderMediaPattern.MatchString(field):
		result.MediaType = field
	case isFolderProvider(field):
		result.provider = field
	case folderCatalogPattern.MatchString(field):
		result.Catalog = field
	case folderBitDepthPattern.MatchString(field):
		result.bitDepth = strings.ToLower(field)
	default:
		if format := folderFormat(field); format != "" {
			result.format = format
			if bitDepth := folderBitDepth(field); bitDepth != "" {
				result.bitDepth = bitDepth
			}
			if result.MediaType == "" && folderBundleHasToken(field, "LOG") {
				result.MediaType = "cd"
			}
			return
		}
		if bundleMedia != "" {
			return
		}
		if result.Edition == "" {
			result.Edition = field
		} else {
			result.Edition += ", " + field
		}
	}
}

func folderMediaFromBundle(value string) string {
	for _, token := range folderBundleTokens(value) {
		switch token {
		case "CD":
			return "cd"
		case "WEB":
			return "web"
		case "VINYL", "LP":
			return "vinyl"
		case "SACD":
			return "sacd"
		}
	}
	return ""
}

func folderBundleHasToken(value, expected string) bool {
	for _, token := range folderBundleTokens(value) {
		if token == expected {
			return true
		}
	}
	return false
}

func folderBundleTokens(value string) []string {
	return strings.FieldsFunc(strings.ToUpper(value), func(character rune) bool {
		return !((character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9'))
	})
}

func folderFormat(value string) string {
	upper := strings.ToUpper(value)
	for _, candidate := range []string{"FLAC", "MP3", "WAV", "AAC", "DSD"} {
		if strings.Contains(upper, candidate) {
			return strings.ToLower(candidate)
		}
	}
	return ""
}

func folderBitDepth(value string) string {
	match := folderBitDepthTokenPattern.FindStringSubmatch(value)
	if match == nil {
		return ""
	}
	return strings.ToLower(match[1] + "bit")
}

func isFolderProvider(value string) bool {
	token := strings.NewReplacer(" ", "", "-", "", "_", "", ".", "", "/", "", "　", "").Replace(strings.ToLower(strings.TrimSpace(value)))
	switch token {
	case "netease", "neteasecloud", "neteasecloudmusic", "网易云", "网易云音乐", "qqmusic", "qq音乐", "applemusic", "itunes", "mora", "qobuz", "tidal", "bandcamp", "spotify", "deezer", "amazonmusic", "amazon", "ototoy", "booth", "hdtracks", "xiami", "虾米", "虾米音乐", "kugou", "酷狗", "酷狗音乐", "kuwo", "酷我", "酷我音乐", "linemusic":
		return true
	default:
		return false
	}
}

func parseFolderExtraFields(extra string, result *folderMetadata) {
	if result == nil {
		return
	}
	fields := make([]string, 0)
	for _, field := range strings.Split(extra, ",") {
		if field = normalizeValue(field); field != "" {
			fields = append(fields, field)
		}
	}
	catalogIndex := -1
	unknown := make([]int, 0)
	for index, field := range fields {
		switch {
		case folderBitDepthPattern.MatchString(field):
			result.bitDepth = strings.ToLower(field)
		case folderCatalogPattern.MatchString(field):
			result.Catalog = field
			catalogIndex = index
		case folderMediaPattern.MatchString(field):
			result.MediaType = field
		case isFolderProvider(field):
			result.provider = field
		default:
			unknown = append(unknown, index)
		}
	}
	edition := make([]string, 0)
	for _, index := range unknown {
		switch {
		case catalogIndex > 0 && index == catalogIndex-1:
			result.Label = fields[index]
		case len(unknown) == 1 && result.Catalog != "":
			result.Label = fields[index]
		default:
			edition = append(edition, fields[index])
		}
	}
	if result.Label == "" && len(edition) > 1 {
		result.Label = edition[len(edition)-1]
		edition = edition[:len(edition)-1]
	}
	result.Edition = strings.Join(edition, ", ")
}
