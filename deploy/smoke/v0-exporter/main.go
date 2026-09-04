// Command roomusic-smoke-exporter 只编排固定 V0 scanner 的 Release Graph 核心。
//
// 该文件由 Smoke 入口复制到已校验归档的临时解包副本中构建。它不属于 V0
// scanner，也不得实现或改写任何 parser、grouping、assembler 规则。
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/qlqqs/roomusic/internal/scanner"
)

const rowsFormatVersion = 1

var excludedEvidence = []string{
	"local_evidence",
	"quality_badges",
	"scan_diagnostics",
	"production_runtime_status",
}

type headerRecord struct {
	RecordType       string   `json:"record_type"`
	FormatVersion    int      `json:"format_version"`
	Implementation   string   `json:"implementation"`
	GenerationMode   string   `json:"generation_mode"`
	BaselineScope    string   `json:"baseline_scope"`
	Degraded         bool     `json:"degraded"`
	ExcludedEvidence []string `json:"excluded_evidence"`
}

type releaseRecord struct {
	RecordType          string                `json:"record_type"`
	Reference           string                `json:"reference"`
	Title               string                `json:"title"`
	AlbumArtist         string                `json:"album_artist"`
	Year                int                   `json:"year"`
	Country             string                `json:"country"`
	Catalog             string                `json:"catalog"`
	Barcode             string                `json:"barcode"`
	SourceType          string                `json:"source_type"`
	MediaType           string                `json:"media_type"`
	Provider            string                `json:"provider"`
	Edition             string                `json:"edition"`
	ReleaseType         string                `json:"release_type"`
	Label               string                `json:"label"`
	Genre               string                `json:"genre"`
	CandidateKind       string                `json:"candidate_kind"`
	ParentCollection    string                `json:"parent_collection_path,omitempty"`
	Media               []mediumRecord        `json:"media"`
	Files               []fileRecord          `json:"files"`
	Credits             []creditRecord        `json:"credits"`
	FieldEvidence       []fieldEvidenceRecord `json:"field_evidence"`
	GroupingTrackCount  int                   `json:"grouping_track_count"`
	GroupingMediumCount int                   `json:"grouping_medium_count"`
}

type mediumRecord struct {
	Position int           `json:"position"`
	Format   string        `json:"format"`
	Tracks   []trackRecord `json:"tracks"`
}

type trackRecord struct {
	Position       int            `json:"position"`
	Title          string         `json:"title"`
	Artist         string         `json:"artist"`
	SourceKind     string         `json:"source_kind"`
	RelativePath   string         `json:"relative_path,omitempty"`
	CueSheetPath   string         `json:"cue_sheet_path,omitempty"`
	CueParentPath  string         `json:"cue_parent_relative_path,omitempty"`
	CueIndexFrames int            `json:"cue_index_frames,omitempty"`
	CueEndFrames   *int           `json:"cue_end_frames,omitempty"`
	CueISRC        string         `json:"cue_isrc,omitempty"`
	DurationMS     int            `json:"duration_ms,omitempty"`
	Codec          string         `json:"codec,omitempty"`
	SampleRate     int            `json:"sample_rate,omitempty"`
	Channels       int            `json:"channels,omitempty"`
	Bitrate        int            `json:"bitrate,omitempty"`
	BitDepth       int            `json:"bit_depth,omitempty"`
	Credits        []creditRecord `json:"credits,omitempty"`
}

type fileRecord struct {
	RelativePath string `json:"relative_path"`
	Media        string `json:"media,omitempty"`
	Size         int64  `json:"size"`
}

type creditRecord struct {
	Role string `json:"role"`
	Name string `json:"name"`
}

type fieldEvidenceRecord struct {
	Field      string `json:"field"`
	Value      string `json:"value"`
	Source     string `json:"source"`
	Confidence string `json:"confidence"`
	Action     string `json:"action"`
	RuleID     string `json:"rule_id"`
}

type completeRecord struct {
	RecordType         string `json:"record_type"`
	ReleaseCount       int    `json:"release_count"`
	MediumCount        int    `json:"medium_count"`
	TrackCount         int    `json:"track_count"`
	FileCount          int    `json:"file_count"`
	CreditCount        int    `json:"credit_count"`
	FieldEvidenceCount int    `json:"field_evidence_count"`
	RecordsSHA256      string `json:"records_sha256"`
}

type parsedEvidence struct {
	files map[string]scanner.FileEvidence
	tags  map[string]*scanner.TagResult
	cues  map[string]*scanner.CueSheet
}

type cueTrackIdentity struct {
	sheet       string
	parent      string
	trackNumber int
	indexFrames int
	endFrames   *int
	isrc        string
}

func main() {
	root := os.Getenv("ROOMUSIC_V0_EXPORT_ROOT")
	if root == "" {
		root = "/music"
	}
	output := os.Getenv("ROOMUSIC_V0_EXPORT_OUTPUT")
	if output == "" {
		output = "/output/v0-rows.ndjson"
	}
	if err := runExporter(root, output); err != nil {
		// 真实错误可能含有私有路径或标签，只向容器日志暴露稳定类别。
		fmt.Fprintln(os.Stderr, "v0-standalone-exporter: export_failed")
		os.Exit(1)
	}
}

func runExporter(root, output string) error {
	canonicalRoot, err := validateRoot(root)
	if err != nil {
		return err
	}
	if err := validateOutput(output); err != nil {
		return err
	}

	releases, footer, err := buildRecords(context.Background(), canonicalRoot)
	if err != nil {
		return err
	}

	var body bytes.Buffer
	header := headerRecord{
		RecordType:       "header",
		FormatVersion:    rowsFormatVersion,
		Implementation:   "v0_release_graph_generated_corrected",
		GenerationMode:   "standalone_scanner",
		BaselineScope:    "release_graph_only",
		Degraded:         false,
		ExcludedEvidence: append([]string(nil), excludedEvidence...),
	}
	if err := writeJSONLine(&body, header); err != nil {
		return fmt.Errorf("encode header: %w", err)
	}

	recordHasher := sha256.New()
	for _, release := range releases {
		encoded, err := json.Marshal(release)
		if err != nil {
			return fmt.Errorf("encode release: %w", err)
		}
		if _, err := recordHasher.Write(encoded); err != nil {
			return err
		}
		if _, err := recordHasher.Write([]byte{'\n'}); err != nil {
			return err
		}
		body.Write(encoded)
		body.WriteByte('\n')
	}
	footer.RecordType = "complete"
	footer.RecordsSHA256 = hex.EncodeToString(recordHasher.Sum(nil))
	if err := writeJSONLine(&body, footer); err != nil {
		return fmt.Errorf("encode footer: %w", err)
	}

	return writeAtomic(output, body.Bytes())
}

func validateRoot(root string) (string, error) {
	if root == "" || !filepath.IsAbs(root) {
		return "", errors.New("invalid root")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("invalid root")
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", errors.New("invalid root")
	}
	return filepath.Clean(canonical), nil
}

func validateOutput(output string) error {
	if output == "" || !filepath.IsAbs(output) {
		return errors.New("invalid output")
	}
	if _, err := os.Lstat(output); err == nil {
		return errors.New("output already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("invalid output")
	}
	parent := filepath.Dir(output)
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid output directory")
	}
	return nil
}

func buildRecords(ctx context.Context, root string) ([]releaseRecord, completeRecord, error) {
	walkResults, err := scanner.Walk(ctx, root, scanner.WalkOptions{
		FollowSymlinks: true,
		AllowedRoots:   []string{root},
	})
	if err != nil {
		return nil, completeRecord{}, fmt.Errorf("walk: %w", err)
	}
	if len(walkResults) == 0 {
		return nil, completeRecord{}, errors.New("empty corpus")
	}

	parsed, err := parseEvidence(walkResults)
	if err != nil {
		return nil, completeRecord{}, err
	}
	candidates := scanner.BuildReleaseCandidates(root, walkResults, parsed.files)
	if len(candidates) == 0 {
		return nil, completeRecord{}, errors.New("no release candidates")
	}

	releases := make([]releaseRecord, 0, len(candidates))
	footer := completeRecord{}
	seenReleaseRefs := make(map[string]struct{}, len(candidates))
	seenPhysicalFiles := make(map[string]struct{})
	for index := range candidates {
		candidate := candidates[index]
		hydrateCandidate(&candidate, parsed)
		assembled, err := scanner.AssembleReleaseCandidate(candidate)
		if err != nil {
			return nil, completeRecord{}, fmt.Errorf("assemble: %w", err)
		}
		if assembled == nil {
			return nil, completeRecord{}, errors.New("nil assembled release")
		}
		record, err := makeReleaseRecord(root, candidate, assembled, parsed)
		if err != nil {
			return nil, completeRecord{}, err
		}
		if _, exists := seenReleaseRefs[record.Reference]; exists {
			return nil, completeRecord{}, errors.New("duplicate release reference")
		}
		seenReleaseRefs[record.Reference] = struct{}{}
		for _, file := range record.Files {
			if _, exists := seenPhysicalFiles[file.RelativePath]; exists {
				return nil, completeRecord{}, errors.New("physical file belongs to multiple releases")
			}
			seenPhysicalFiles[file.RelativePath] = struct{}{}
		}
		footer.MediumCount += len(record.Media)
		footer.FileCount += len(record.Files)
		footer.CreditCount += len(record.Credits)
		footer.FieldEvidenceCount += len(record.FieldEvidence)
		for _, medium := range record.Media {
			footer.TrackCount += len(medium.Tracks)
			for _, track := range medium.Tracks {
				footer.CreditCount += len(track.Credits)
			}
		}
		releases = append(releases, record)
	}
	footer.ReleaseCount = len(releases)
	sort.Slice(releases, func(i, j int) bool { return releases[i].Reference < releases[j].Reference })
	return releases, footer, nil
}

func parseEvidence(walkResults []scanner.WalkResult) (parsedEvidence, error) {
	parsed := parsedEvidence{
		files: make(map[string]scanner.FileEvidence),
		tags:  make(map[string]*scanner.TagResult),
		cues:  make(map[string]*scanner.CueSheet),
	}

	for _, directory := range walkResults {
		for _, cuePath := range directory.CueFiles {
			cue, err := scanner.ParseCueSheet(cuePath)
			if err != nil || cue == nil {
				return parsedEvidence{}, errors.New("cue parse failed")
			}
			if err := scanner.ValidateCueFiles(cue, filepath.Dir(cuePath)); err != nil {
				return parsedEvidence{}, errors.New("cue validation failed")
			}
			parsed.cues[cuePath] = cue
		}
	}

	for _, directory := range walkResults {
		folder := scanner.ParseFolderName(filepath.Base(directory.DirPath))
		folderEvidence := scanner.FolderEvidence{}
		if folder != nil {
			folderEvidence = folder.Evidence()
		}
		for _, file := range directory.AudioFiles {
			tag, err := scanner.ParseTags(file.Path)
			if err != nil || tag == nil {
				return parsedEvidence{}, errors.New("tag parse failed")
			}
			parsed.tags[file.Path] = tag
			parsed.files[file.Path] = scanner.FileEvidence{
				Path:      file.Path,
				SizeBytes: file.Size,
				Audio:     tag.AudioEvidence(),
				Tag:       tag.Evidence(),
				CueRefs:   cueRefsForFile(directory, file.Path, parsed.cues),
				LogRefs:   append([]string(nil), directory.LogFiles...),
				Folder:    folderEvidence,
			}
		}
	}
	return parsed, nil
}

func hydrateCandidate(candidate *scanner.ReleaseCandidate, parsed parsedEvidence) {
	candidate.Tags = make(map[string]*scanner.TagResult, len(candidate.InputFiles))
	candidate.FileEvidence = make(map[string]scanner.FileEvidence, len(candidate.InputFiles))
	for _, file := range candidate.InputFiles {
		if tag := parsed.tags[file.Path]; tag != nil {
			candidate.Tags[file.Path] = tag
		}
		if evidence, ok := parsed.files[file.Path]; ok {
			candidate.FileEvidence[file.Path] = evidence
		}
	}
	candidate.CueSheets = make(map[string]*scanner.CueSheet)
	for _, directory := range candidate.InputDirs {
		for _, cuePath := range directory.CueFiles {
			if cue := parsed.cues[cuePath]; cue != nil {
				candidate.CueSheets[cuePath] = cue
			}
		}
	}
}

func cueRefsForFile(directory scanner.WalkResult, filePath string, cues map[string]*scanner.CueSheet) []string {
	refs := make([]string, 0)
	for _, cuePath := range directory.CueFiles {
		cue := cues[cuePath]
		if cue == nil {
			continue
		}
		for _, cueFile := range cue.Files {
			if filepath.Clean(filepath.Join(filepath.Dir(cuePath), cueFile.FileName)) == filepath.Clean(filePath) {
				refs = append(refs, cuePath)
				break
			}
		}
	}
	sort.Strings(refs)
	return refs
}

func makeReleaseRecord(root string, candidate scanner.ReleaseCandidate, assembled *scanner.AssembleResult, parsed parsedEvidence) (releaseRecord, error) {
	primary, err := relativePath(root, candidate.PrimaryPath, true)
	if err != nil {
		return releaseRecord{}, err
	}
	filePaths := make([]string, 0, len(candidate.InputFiles))
	files := make([]fileRecord, 0, len(candidate.InputFiles))
	for _, file := range candidate.InputFiles {
		relative, err := relativePath(root, file.Path, false)
		if err != nil {
			return releaseRecord{}, err
		}
		filePaths = append(filePaths, relative)
		media := ""
		if tag := parsed.tags[file.Path]; tag != nil {
			media = tag.Codec
		}
		files = append(files, fileRecord{RelativePath: relative, Media: media, Size: file.Size})
	}
	sort.Strings(filePaths)
	sort.Slice(files, func(i, j int) bool { return files[i].RelativePath < files[j].RelativePath })
	reference := stableDigest("release-reference\x00" + candidate.CandidateKind + "\x00" + primary + "\x00" + strings.Join(filePaths, "\x00"))

	parent := ""
	if candidate.ParentCollectionPath != "" {
		parent, err = relativePath(root, candidate.ParentCollectionPath, true)
		if err != nil {
			return releaseRecord{}, err
		}
	}

	media := make([]mediumRecord, 0, len(assembled.Media))
	for _, assembledMedium := range assembled.Media {
		if assembledMedium.Position <= 0 || len(assembledMedium.Tracks) == 0 {
			return releaseRecord{}, errors.New("invalid medium")
		}
		tracks := make([]trackRecord, 0, len(assembledMedium.Tracks))
		for _, assembledTrack := range assembledMedium.Tracks {
			track, err := makeTrackRecord(root, candidate, assembledTrack)
			if err != nil {
				return releaseRecord{}, err
			}
			tracks = append(tracks, track)
		}
		sort.Slice(tracks, func(i, j int) bool {
			if tracks[i].Position != tracks[j].Position {
				return tracks[i].Position < tracks[j].Position
			}
			return trackSortKey(tracks[i]) < trackSortKey(tracks[j])
		})
		media = append(media, mediumRecord{Position: assembledMedium.Position, Format: assembledMedium.Format, Tracks: tracks})
	}
	sort.Slice(media, func(i, j int) bool { return media[i].Position < media[j].Position })

	labels := make([]string, 0, len(assembled.Labels))
	for _, label := range assembled.Labels {
		if strings.TrimSpace(label.Name) != "" {
			labels = append(labels, label.Name)
		}
	}
	labels = uniqueSorted(labels)
	genres := uniqueSorted(assembled.Genres)

	credits := make([]creditRecord, 0)
	for _, artist := range assembled.Artists {
		if artist.Role == "album_artist" && strings.TrimSpace(artist.Name) != "" {
			credits = append(credits, creditRecord{Role: artist.Role, Name: artist.Name})
		}
	}
	credits = canonicalCredits(credits)

	fieldEvidence := make([]fieldEvidenceRecord, 0, len(assembled.GroupingEvidence.FieldDecisions))
	for field, evidence := range assembled.GroupingEvidence.FieldDecisions {
		fieldEvidence = append(fieldEvidence, fieldEvidenceRecord{
			Field:      field,
			Value:      evidence.Value,
			Source:     string(evidence.Source),
			Confidence: string(evidence.ConfidenceLevel),
			Action:     string(evidence.Action),
			RuleID:     evidence.RuleID,
		})
	}
	sort.Slice(fieldEvidence, func(i, j int) bool { return fieldEvidence[i].Field < fieldEvidence[j].Field })

	return releaseRecord{
		RecordType:          "release",
		Reference:           reference,
		Title:               assembled.Release.Name,
		AlbumArtist:         assembled.Release.AlbumArtist,
		Year:                assembled.Release.Year,
		Country:             assembled.Release.Country,
		Catalog:             assembled.Release.CatalogNumber,
		Barcode:             assembled.Release.Barcode,
		SourceType:          assembled.Release.ReleaseSourceType,
		MediaType:           assembled.Release.MediaType,
		Provider:            assembled.Release.Provider,
		Edition:             assembled.Release.EditionVersion,
		ReleaseType:         assembled.Release.ReleaseType,
		Label:               strings.Join(labels, " / "),
		Genre:               strings.Join(genres, " / "),
		CandidateKind:       assembled.CandidateKind,
		ParentCollection:    parent,
		Media:               media,
		Files:               files,
		Credits:             credits,
		FieldEvidence:       fieldEvidence,
		GroupingTrackCount:  assembled.GroupingEvidence.TrackCount,
		GroupingMediumCount: assembled.GroupingEvidence.MediumCount,
	}, nil
}

func makeTrackRecord(root string, candidate scanner.ReleaseCandidate, track scanner.TrackData) (trackRecord, error) {
	if track.Position <= 0 {
		return trackRecord{}, errors.New("invalid track position")
	}
	record := trackRecord{
		Position:   track.Position,
		Title:      track.Title,
		Artist:     trackArtist(track.Artists),
		DurationMS: track.DurationMs,
		Codec:      track.Codec,
		SampleRate: track.SampleRate,
		Channels:   track.Channels,
		Bitrate:    track.Bitrate,
		BitDepth:   track.BitDepth,
		Credits:    trackCredits(track.Artists),
	}
	if track.TrackSourceType == scanner.TrackIdentitySourceCueVirtual {
		identity, err := findCueTrack(candidate, track)
		if err != nil {
			return trackRecord{}, err
		}
		record.SourceKind = "cue_virtual"
		record.CueSheetPath, err = relativePath(root, identity.sheet, false)
		if err != nil {
			return trackRecord{}, err
		}
		record.CueParentPath, err = relativePath(root, identity.parent, false)
		if err != nil {
			return trackRecord{}, err
		}
		record.CueIndexFrames = identity.indexFrames
		record.CueEndFrames = identity.endFrames
		record.CueISRC = identity.isrc
		return record, nil
	}
	if track.TrackSourceType != "" && track.TrackSourceType != scanner.TrackIdentitySourceFile {
		return trackRecord{}, errors.New("unknown track source kind")
	}
	record.SourceKind = "physical"
	var err error
	record.RelativePath, err = relativePath(root, track.FilePath, false)
	return record, err
}

func findCueTrack(candidate scanner.ReleaseCandidate, track scanner.TrackData) (cueTrackIdentity, error) {
	matches := make([]cueTrackIdentity, 0, 1)
	for cuePath, cue := range candidate.CueSheets {
		if cue == nil {
			continue
		}
		for _, cueFile := range cue.Files {
			parent := filepath.Clean(filepath.Join(filepath.Dir(cuePath), cueFile.FileName))
			if parent != filepath.Clean(track.ParentFilePath) {
				continue
			}
			for _, cueTrack := range cueFile.Tracks {
				if cueTrack.Number != track.Position || cueTrack.StartTime.ToMilliseconds() != track.StartOffsetMs {
					continue
				}
				var endFrames *int
				if cueTrack.EndTime != nil {
					value := cueFrames(*cueTrack.EndTime)
					endFrames = &value
				}
				matches = append(matches, cueTrackIdentity{
					sheet:       cuePath,
					parent:      parent,
					trackNumber: cueTrack.Number,
					indexFrames: cueFrames(cueTrack.StartTime),
					endFrames:   endFrames,
					isrc:        cueTrack.ISRC,
				})
			}
		}
	}
	if len(matches) != 1 {
		return cueTrackIdentity{}, errors.New("cue track identity is not unique")
	}
	return matches[0], nil
}

func cueFrames(value scanner.CueTime) int {
	return value.Minutes*60*75 + value.Seconds*75 + value.Frames
}

func trackArtist(values []scanner.ArtistRef) string {
	names := make([]string, 0, len(values))
	for _, value := range values {
		if value.Role == "performer" && strings.TrimSpace(value.Name) != "" {
			names = append(names, value.Name)
		}
	}
	return strings.Join(uniqueSorted(names), " / ")
}

func trackCredits(values []scanner.ArtistRef) []creditRecord {
	credits := make([]creditRecord, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value.Role) != "" && strings.TrimSpace(value.Name) != "" {
			credits = append(credits, creditRecord{Role: value.Role, Name: value.Name})
		}
	}
	return canonicalCredits(credits)
}

func canonicalCredits(values []creditRecord) []creditRecord {
	sort.Slice(values, func(i, j int) bool {
		return values[i].Role+"\x00"+values[i].Name < values[j].Role+"\x00"+values[j].Name
	})
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func uniqueSorted(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	deduplicated := result[:0]
	for _, value := range result {
		if len(deduplicated) == 0 || deduplicated[len(deduplicated)-1] != value {
			deduplicated = append(deduplicated, value)
		}
	}
	return deduplicated
}

func relativePath(root, path string, allowRoot bool) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", errors.New("invalid source path")
	}
	relative, err := filepath.Rel(root, filepath.Clean(path))
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("source path escaped root")
	}
	relative = filepath.ToSlash(filepath.Clean(relative))
	if relative == "." {
		if allowRoot {
			return "", nil
		}
		return "", errors.New("source path is root")
	}
	if relative == "" || strings.ContainsRune(relative, '\x00') {
		return "", errors.New("invalid relative source path")
	}
	return relative, nil
}

func trackSortKey(track trackRecord) string {
	if track.SourceKind == "cue_virtual" {
		return track.CueSheetPath + "\x00" + track.CueParentPath + "\x00" + fmt.Sprint(track.CueIndexFrames)
	}
	return track.RelativePath
}

func stableDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func writeJSONLine(writer io.Writer, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := writer.Write(encoded); err != nil {
		return err
	}
	_, err = writer.Write([]byte{'\n'})
	return err
}

func writeAtomic(path string, content []byte) (returnErr error) {
	parent := filepath.Dir(path)
	temporary, err := os.CreateTemp(parent, ".v0-rows-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if returnErr != nil {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	// 临时文件与目标位于同一输出目录，因此 Link 提供原子且不替换的发布；
	// 并发创建的目标绝不会被覆盖。
	if err := os.Link(temporaryPath, path); err != nil {
		return err
	}
	if err := os.Remove(temporaryPath); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}
