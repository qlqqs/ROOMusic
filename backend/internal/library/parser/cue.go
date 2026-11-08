package parser

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

type CueTrack struct {
	ArtistTagEvidence []metadata.ArtistTagEvidence
	Number            int
	Title, Artist     string
	PerformerPresent  bool
	IndexFrames       int
	IndexPresent      bool
	ReferencedFile    string
	FileType          string
	Indexes           map[int]int
	EndFrames         int
	EndPresent        bool
	DurationFrames    int
	DurationSeconds   float64
	ISRC              string
	SheetTitle        string
	SheetArtist       string
	Genre             string
	Date              string
	Catalog           string
	ReferenceStatus   string
}

type CueDocument struct {
	ArtistTagEvidence []metadata.ArtistTagEvidence
	Tracks            []CueTrack
	Files             []CueFileReference
	Title             string
	Artist            string
	Genre             string
	Date              string
	Catalog           string
	Encoding          string
	Diagnostics       []string
}

func parseCue(path string) ([]CueTrack, string, error) {
	document, err := ParseCueDocument(path, AudioDuration)
	if err != nil {
		return nil, "", err
	}
	if len(document.Files) == 0 || len(document.Tracks) == 0 {
		return nil, "", fmt.Errorf("incomplete CUE")
	}
	for _, track := range document.Tracks {
		if !track.IndexPresent {
			return nil, "", fmt.Errorf("missing INDEX 01")
		}
	}
	return document.Tracks, document.Files[0].Path, nil
}

// CueDurationReader 为已校验的父音频提供时长；扫描器可从解析缓存取得，错误表示时长未知。
type CueDurationReader func(path string) (float64, error)

// AudioDuration 直接解析音频取得时长，供不使用扫描缓存的调用方显式传入。
func AudioDuration(path string) (float64, error) {
	observation, err := ParseAudioFile(path)
	return observation.DurationSeconds, err
}

// ParseCueDocument 保留全部 FILE、sheet 元数据和有界逐引用诊断。父音频时长由调用方提供。
// 可选根目录用于扫描级 containment，省略时保守限定在 CUE 目录；缺失或不安全的引用仍携带状态。
func ParseCueDocument(path string, duration CueDurationReader, containmentRoot ...string) (CueDocument, error) {
	if duration == nil {
		return CueDocument{}, errors.New("CUE duration reader is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return CueDocument{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return CueDocument{}, err
	}
	if info.Size() > maxCueBytes {
		return CueDocument{}, fmt.Errorf("CUE metadata too large")
	}
	data := make([]byte, int(info.Size()))
	if _, err := io.ReadFull(file, data); err != nil {
		return CueDocument{}, err
	}
	content, encoding, err := decodeCueText(data)
	if err != nil {
		return CueDocument{}, err
	}
	document := CueDocument{Encoding: encoding}
	boundary := filepath.Dir(path)
	if len(containmentRoot) > 0 && strings.TrimSpace(containmentRoot[0]) != "" {
		boundary = containmentRoot[0]
	}
	var currentFile *CueFileReference
	var currentTrack *CueTrack
	var currentFileID int
	var nextFileID int
	var trackCount int
	trackFileIDs := make([]int, 0)
	flushTrack := func() {
		if currentTrack == nil || currentFile == nil {
			return
		}
		currentTrack.ReferencedFile = currentFile.Path
		currentTrack.FileType = currentFile.Type
		currentTrack.ReferenceStatus = currentFile.Status
		currentTrack.SheetTitle = document.Title
		currentTrack.SheetArtist = document.Artist
		currentTrack.Genre = document.Genre
		currentTrack.Date = document.Date
		currentTrack.Catalog = document.Catalog
		currentFile.TrackNumbers = append(currentFile.TrackNumbers, currentTrack.Number)
		document.Tracks = append(document.Tracks, *currentTrack)
		trackFileIDs = append(trackFileIDs, currentFileID)
		currentTrack = nil
	}
	flushFile := func() {
		flushTrack()
		if currentFile != nil {
			document.Files = append(document.Files, *currentFile)
			currentFile = nil
		}
	}
	for lineNumber, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		if len(line) > maxCueLineBytes {
			return CueDocument{}, fmt.Errorf("CUE line %d exceeds size limit", lineNumber+1)
		}
		tokens, tokenErr := cueTokens(line)
		if tokenErr != nil {
			return CueDocument{}, fmt.Errorf("CUE line %d: %w", lineNumber+1, tokenErr)
		}
		if len(tokens) == 0 {
			continue
		}
		for _, token := range tokens {
			if len(token) > maxCueTokenBytes {
				return CueDocument{}, fmt.Errorf("CUE line %d contains an overlong value", lineNumber+1)
			}
		}
		key := strings.ToUpper(tokens[0])
		switch key {
		case "FILE":
			if len(tokens) < 3 || !supportedCueFileType(tokens[2]) {
				return CueDocument{}, fmt.Errorf("CUE line %d: missing FILE type", lineNumber+1)
			}
			flushFile()
			if nextFileID >= maxCueFiles {
				return CueDocument{}, fmt.Errorf("CUE contains too many FILE declarations")
			}
			nextFileID++
			currentFileID = nextFileID
			ref := strings.TrimSpace(tokens[1])
			status, resolved, diagnostic := validateCueReference(path, boundary, ref)
			referencePath := normalizeCueReference(ref)
			if status == "present" {
				if cueDir, dirErr := filepath.Abs(filepath.Dir(path)); dirErr == nil {
					if relative, relErr := filepath.Rel(cueDir, resolved); relErr == nil {
						referencePath = filepath.ToSlash(filepath.Clean(relative))
					}
				}
			}
			currentFile = &CueFileReference{Path: referencePath, Type: strings.ToUpper(tokens[2]), ResolvedPath: resolved, Status: status}
			if diagnostic != "" {
				appendCueDiagnostic(&document, diagnostic)
			}
		case "TRACK":
			if currentFile == nil {
				return CueDocument{}, fmt.Errorf("CUE line %d: TRACK before FILE", lineNumber+1)
			}
			if len(tokens) < 3 || strings.ToUpper(tokens[2]) != "AUDIO" {
				return CueDocument{}, fmt.Errorf("CUE line %d: unsupported CUE track", lineNumber+1)
			}
			if trackCount >= maxCueTracks {
				return CueDocument{}, fmt.Errorf("CUE contains too many TRACK declarations")
			}
			number, numberErr := strconv.Atoi(tokens[1])
			if numberErr != nil || number < 1 || number > 999 {
				return CueDocument{}, fmt.Errorf("CUE line %d: invalid TRACK number", lineNumber+1)
			}
			flushTrack()
			currentTrack = &CueTrack{Number: number, Indexes: make(map[int]int)}
			trackCount++
		case "TITLE":
			value := cueValue(tokens[1:])
			if currentTrack != nil {
				currentTrack.Title = value
			} else {
				document.Title = value
			}
		case "PERFORMER":
			// 在展示修剪之前保留解码原文；不能用 cueValue 静默截断。
			value := strings.Join(tokens[1:], " ")
			if currentTrack != nil {
				if err := appendArtistEvidence(&currentTrack.ArtistTagEvidence, "track_artist", "PERFORMER", value, "cue_track"); err != nil {
					return CueDocument{}, err
				}
				currentTrack.Artist = artistEvidenceDisplay(currentTrack.ArtistTagEvidence, "track_artist")
				currentTrack.PerformerPresent = true
			} else {
				if err := appendArtistEvidence(&document.ArtistTagEvidence, "album_artist", "PERFORMER", value, "cue_sheet"); err != nil {
					return CueDocument{}, err
				}
				document.Artist = strings.TrimSpace(value)
			}
		case "REM":
			if len(tokens) < 3 {
				continue
			}
			value := cueValue(tokens[2:])
			switch strings.ToUpper(tokens[1]) {
			case "GENRE":
				document.Genre = value
			case "DATE", "YEAR":
				document.Date = value
			}
		case "CATALOG":
			document.Catalog = cueValue(tokens[1:])
		case "ISRC":
			if currentTrack != nil {
				currentTrack.ISRC = cueValue(tokens[1:])
			}
		case "INDEX":
			if currentTrack == nil || len(tokens) < 3 {
				continue
			}
			indexNumber, indexErr := strconv.Atoi(tokens[1])
			if indexErr != nil || indexNumber < 0 || indexNumber > 99 {
				return CueDocument{}, fmt.Errorf("CUE line %d: invalid INDEX number", lineNumber+1)
			}
			frames, frameErr := parseCueTimecode(tokens[2])
			if frameErr != nil {
				return CueDocument{}, fmt.Errorf("CUE line %d: %w", lineNumber+1, frameErr)
			}
			if currentTrack.Indexes == nil {
				currentTrack.Indexes = make(map[int]int)
			}
			if _, duplicate := currentTrack.Indexes[indexNumber]; duplicate {
				return CueDocument{}, fmt.Errorf("CUE line %d: duplicate INDEX number", lineNumber+1)
			}
			currentTrack.Indexes[indexNumber] = frames
			if indexNumber == 1 {
				currentTrack.IndexFrames = frames
				currentTrack.IndexPresent = true
			}
		}
	}
	flushFile()
	if len(document.Files) == 0 || len(document.Tracks) == 0 {
		return CueDocument{}, fmt.Errorf("incomplete CUE")
	}
	for fileIndex := range document.Files {
		fileRef := &document.Files[fileIndex]
		if setCueTrackRanges(document.Tracks, trackFileIDs, fileIndex+1, cueDurationFrames(fileRef, duration)) {
			appendCueDiagnostic(&document, "non-monotonic INDEX 01 in FILE reference: "+fileRef.Path)
		}
	}
	for index := range document.Tracks {
		document.Tracks[index].SheetTitle = document.Title
		document.Tracks[index].SheetArtist = document.Artist
		document.Tracks[index].Genre = document.Genre
		document.Tracks[index].Date = document.Date
		document.Tracks[index].Catalog = document.Catalog
		if len(document.ArtistTagEvidence)+len(document.Tracks[index].ArtistTagEvidence) > metadata.MaxArtistTagEvidenceItems {
			return CueDocument{}, errors.New("artist tag evidence exceeds item limit")
		}
		if document.Tracks[index].Artist == "" {
			document.Tracks[index].Artist = artistEvidenceDisplay(document.ArtistTagEvidence, "album_artist")
		}
		if !document.Tracks[index].IndexPresent {
			appendCueDiagnostic(&document, fmt.Sprintf("missing INDEX 01 for track %d", document.Tracks[index].Number))
		}
	}
	return document, nil
}

func cueTokens(line string) ([]string, error) {
	var tokens []string
	var value strings.Builder
	inQuotes := false
	flush := func() {
		if value.Len() > 0 {
			tokens = append(tokens, value.String())
			value.Reset()
		}
	}
	for _, character := range line {
		if character == '"' {
			inQuotes = !inQuotes
			continue
		}
		if !inQuotes && (character == ' ' || character == '\t') {
			flush()
			continue
		}
		value.WriteRune(character)
	}
	if inQuotes {
		return nil, errors.New("unterminated quote")
	}
	flush()
	return tokens, nil
}

func cueValue(tokens []string) string {
	return truncateUTF8Bytes(strings.TrimSpace(strings.Join(tokens, " ")), maxCueTokenBytes)
}

func parseCueTimecode(value string) (int, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return 0, errors.New("invalid INDEX timecode")
	}
	minutes, minutesErr := strconv.ParseInt(parts[0], 10, 32)
	seconds, secondsErr := strconv.ParseInt(parts[1], 10, 8)
	frames, framesErr := strconv.ParseInt(parts[2], 10, 8)
	if minutesErr != nil || secondsErr != nil || framesErr != nil || minutes < 0 || seconds < 0 || frames < 0 || seconds >= 60 || frames >= 75 {
		return 0, errors.New("invalid INDEX timecode")
	}
	totalFrames := (minutes*60+seconds)*75 + frames
	if totalFrames > math.MaxInt32 {
		return 0, errors.New("INDEX timecode exceeds supported range")
	}
	return int(totalFrames), nil
}

func appendCueDiagnostic(document *CueDocument, diagnostic string) {
	if document == nil || len(document.Diagnostics) >= maxCueDiagnostics || diagnostic == "" {
		return
	}
	if len(diagnostic) > maxCueDiagnosticBytes {
		diagnostic = truncateUTF8Bytes(diagnostic, maxCueDiagnosticBytes)
	}
	document.Diagnostics = append(document.Diagnostics, diagnostic)
}

func cueDurationFrames(fileRef *CueFileReference, readDuration CueDurationReader) int {
	if fileRef == nil || fileRef.Status != "present" || fileRef.ResolvedPath == "" {
		return 0
	}
	duration, err := readDuration(fileRef.ResolvedPath)
	if err != nil || duration <= 0 {
		return 0
	}
	if duration > float64(math.MaxInt32)/75 {
		return 0
	}
	return int(math.Round(duration * 75))
}

func setCueTrackRanges(tracks []CueTrack, trackFileIDs []int, fileID, parentDuration int) bool {
	indices := make([]int, 0)
	for index := range tracks {
		if index < len(trackFileIDs) && trackFileIDs[index] == fileID {
			indices = append(indices, index)
		}
	}
	nonMonotonic := false
	for index, trackIndex := range indices {
		track := &tracks[trackIndex]
		if !track.IndexPresent {
			continue
		}
		if index+1 < len(indices) && tracks[indices[index+1]].IndexPresent {
			track.EndFrames = tracks[indices[index+1]].IndexFrames
			track.EndPresent = track.EndFrames >= track.IndexFrames
			if !track.EndPresent {
				nonMonotonic = true
			}
		} else if parentDuration > track.IndexFrames {
			track.EndFrames = parentDuration
			track.EndPresent = true
		} else if parentDuration > 0 && parentDuration <= track.IndexFrames {
			nonMonotonic = true
		}
		if track.EndPresent {
			track.DurationFrames = track.EndFrames - track.IndexFrames
			if track.DurationFrames >= 0 {
				track.DurationSeconds = float64(track.DurationFrames) / 75
			}
		}
	}
	return nonMonotonic
}

func supportedCueFileType(value string) bool {
	switch strings.ToUpper(value) {
	case "WAVE", "BINARY", "AIFF", "MP3", "OGG", "OPUS", "FLAC":
		return true
	default:
		return false
	}
}
