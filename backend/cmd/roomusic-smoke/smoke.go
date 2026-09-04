package smoke

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

const SnapshotVersion = 2

const V0GeneratedCorrectedImplementation = "v0_release_graph_generated_corrected"

var v0ExcludedEvidence = []string{
	"local_evidence",
	"quality_badges",
	"scan_diagnostics",
	"production_runtime_status",
}

// DifferenceCategory 显式枚举差异类型，防止未映射或不支持的语义被静默报告为回归。
type DifferenceCategory string

const (
	CategoryCurrentRegression         DifferenceCategory = "current_regression"
	CategorySchemaMappingGap          DifferenceCategory = "schema_mapping_gap"
	CategoryCapabilityGap             DifferenceCategory = "capability_gap"
	CategoryHistoricalCorpusDrift     DifferenceCategory = "historical_corpus_drift"
	CategoryIntentionalContractChange DifferenceCategory = "intentional_contract_difference"
)

// TreeSummary 是扫描前后使用的脱敏资产指纹。根目录下的目录与非目录条目都会被计入，
// 但不会把逐项清单返回给调用方。
type TreeSummary struct {
	Files   int    `json:"files"`
	Entries int    `json:"entries"`
	Bytes   int64  `json:"bytes"`
	Digest  string `json:"digest"`
}

// SummarizeTree 只读取 root 下的条目，不跟随符号链接，也不打开符号链接目标。
func SummarizeTree(root string) (TreeSummary, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return TreeSummary{}, fmt.Errorf("规范化资产根失败: %w", err)
	}
	root = filepath.Clean(root)
	var entries []string
	var total int64
	files := 0
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		relative = filepath.ToSlash(relative)
		if relative == "." {
			return nil
		}
		mode := info.Mode()
		contentDigest := ""
		if mode.IsDir() {
			// 目录自身的路径、类型、大小、权限和 mtime 已进入摘要；不读取内容。
			contentDigest = ""
		} else if mode.IsRegular() {
			files++
			file, openErr := os.Open(path)
			if openErr != nil {
				return openErr
			}
			hasher := sha256.New()
			readBytes, copyErr := io.Copy(hasher, file)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			contentDigest = hex.EncodeToString(hasher.Sum(nil))
			total += readBytes
		} else if mode&os.ModeSymlink != 0 {
			// 仅记录链接目标的摘要，绝不打开链接目标。
			target, readlinkErr := os.Readlink(path)
			if readlinkErr != nil {
				return readlinkErr
			}
			contentDigest = digest("symlink\x00" + target)
		} else {
			contentDigest = digest("special\x00" + mode.String())
		}
		entries = append(entries, fmt.Sprintf("%s\x00%s\x00%d\x00%o\x00%d\x00%s", relative, mode.Type().String(), info.Size(), mode.Perm(), info.ModTime().UnixNano(), contentDigest))
		return nil
	})
	if err != nil {
		return TreeSummary{}, fmt.Errorf("读取资产树失败: %w", err)
	}
	sort.Strings(entries)
	hasher := sha256.New()
	for _, entry := range entries {
		_, _ = io.WriteString(hasher, entry)
		_, _ = io.WriteString(hasher, "\n")
	}
	return TreeSummary{Files: files, Entries: len(entries), Bytes: total, Digest: hex.EncodeToString(hasher.Sum(nil))}, nil
}

type Snapshot struct {
	Version          int            `json:"snapshot_version"`
	Implementation   string         `json:"implementation"`
	CorpusDigest     string         `json:"corpus_digest"`
	CodeHash         string         `json:"code_hash,omitempty"`
	SchemaDigest     string         `json:"schema_digest,omitempty"`
	AdapterHash      string         `json:"adapter_hash,omitempty"`
	GenerationMode   string         `json:"generation_mode,omitempty"`
	BaselineScope    string         `json:"baseline_scope,omitempty"`
	Degraded         bool           `json:"degraded"`
	ExcludedEvidence []string       `json:"excluded_evidence,omitempty"`
	Releases         []Release      `json:"releases"`
	Media            []Medium       `json:"media"`
	Tracks           []Track        `json:"tracks"`
	Files            []File         `json:"files"`
	Diagnostics      map[string]int `json:"diagnostics,omitempty"`
	AttentionCount   int            `json:"attention_count,omitempty"`
	// CategoryHints 保存已知语义差异的显式处置。键使用
	// "entity\x00key\x00field" 并要求显式启用；无注解差异仍是 current_regression。
	CategoryHints map[string]DifferenceCategory `json:"category_hints,omitempty"`
}

type Release struct {
	Key         string            `json:"key"`
	Title       string            `json:"title"`
	Artist      string            `json:"artist"`
	AlbumArtist string            `json:"album_artist,omitempty"`
	Year        int               `json:"year,omitempty"`
	SourceType  string            `json:"source_type,omitempty"`
	MediaType   string            `json:"media_type,omitempty"`
	Edition     string            `json:"edition,omitempty"`
	Label       string            `json:"label,omitempty"`
	Catalog     string            `json:"catalog,omitempty"`
	Genre       string            `json:"genre,omitempty"`
	MediumKeys  []string          `json:"medium_keys"`
	Fields      map[string]string `json:"fields,omitempty"`
	Credits     []Credit          `json:"credits,omitempty"`
	Evidence    []Evidence        `json:"evidence,omitempty"`
}

type Medium struct {
	Key        string   `json:"key"`
	ReleaseKey string   `json:"release_key"`
	Position   int      `json:"position"`
	Title      string   `json:"title,omitempty"`
	Format     string   `json:"format,omitempty"`
	TrackKeys  []string `json:"track_keys"`
}

type Track struct {
	Key             string            `json:"key"`
	MediumKey       string            `json:"medium_key"`
	Position        int               `json:"position"`
	SourceKey       string            `json:"source_key"`
	ParentSourceKey string            `json:"parent_source_key"`
	Title           string            `json:"title"`
	Artist          string            `json:"artist"`
	SourceKind      string            `json:"source_kind"`
	Fields          map[string]string `json:"fields,omitempty"`
	Credits         []Credit          `json:"credits,omitempty"`
}

type File struct {
	Key        string `json:"key"`
	ReleaseKey string `json:"release_key"`
	SourceKey  string `json:"source_key"`
	Media      string `json:"media,omitempty"`
	Size       int64  `json:"size"`
}

type Credit struct {
	Role string `json:"role"`
	Name string `json:"name"`
}

type Evidence struct {
	Field      string `json:"field"`
	Value      string `json:"value,omitempty"`
	Source     string `json:"source"`
	Confidence string `json:"confidence"`
	Action     string `json:"action"`
	RuleID     string `json:"rule_id"`
}

func SourceKey(root, path string) string {
	return digest("source\x00" + relativePath(root, path))
}

func CueSourceKey(sheet, parent string, track int, index string) string {
	return digest(fmt.Sprintf("cue\x00%s\x00%s\x00%d\x00%s", slash(sheet), slash(parent), track, index))
}

func ReleaseKey(sources []string) string {
	values := append([]string(nil), sources...)
	sort.Strings(values)
	return digest("release\x00" + strings.Join(values, "\x00"))
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func slash(value string) string { return filepath.ToSlash(filepath.Clean(value)) }

func relativePath(root, path string) string {
	cleanPath := filepath.Clean(path)
	if filepath.IsAbs(cleanPath) {
		cleanRoot := filepath.Clean(root)
		if relative, err := filepath.Rel(cleanRoot, cleanPath); err == nil {
			cleanPath = relative
		}
	}
	return filepath.ToSlash(cleanPath)
}

type Difference struct {
	Entity   string `json:"entity"`
	Key      string `json:"key"`
	Field    string `json:"field"`
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
	Category string `json:"category"`
}

// ComparisonMetadata 记录判断报告是否有意义所需的身份，使脱敏报告仍可审计。
type ComparisonMetadata struct {
	ExpectedSnapshotVersion int    `json:"expected_snapshot_version"`
	ActualSnapshotVersion   int    `json:"actual_snapshot_version"`
	ExpectedImplementation  string `json:"expected_implementation,omitempty"`
	ActualImplementation    string `json:"actual_implementation,omitempty"`
	ExpectedCorpusDigest    string `json:"expected_corpus_digest,omitempty"`
	ActualCorpusDigest      string `json:"actual_corpus_digest,omitempty"`
	VersionCompatible       bool   `json:"version_compatible"`
	CorpusCompatible        bool   `json:"corpus_compatible"`
	Comparable              bool   `json:"comparable"`
}

type DifferenceReport struct {
	Metadata    ComparisonMetadata `json:"metadata"`
	Differences []Difference       `json:"differences"`
	Counts      map[string]int     `json:"counts"`
	Errors      []string           `json:"errors,omitempty"`
}

var (
	ErrIncompatibleSnapshots = errors.New("snapshots are not comparable")
	ErrInvalidSnapshot       = errors.New("invalid snapshot")
	ErrNoBaseline            = errors.New("no baseline available")
)

// ValidateSnapshot 检查 canonical snapshot 作为基准或比较操作数所需的最小身份。
func ValidateSnapshot(snapshot Snapshot) error {
	if snapshot.Version != SnapshotVersion {
		return fmt.Errorf("%w: snapshot version %d, want %d", ErrInvalidSnapshot, snapshot.Version, SnapshotVersion)
	}
	if strings.TrimSpace(snapshot.Implementation) == "" {
		return fmt.Errorf("%w: implementation is required", ErrInvalidSnapshot)
	}
	if strings.TrimSpace(snapshot.CorpusDigest) == "" {
		return fmt.Errorf("%w: corpus digest is required", ErrInvalidSnapshot)
	}
	if snapshot.Implementation == V0GeneratedCorrectedImplementation {
		if snapshot.GenerationMode != "standalone_scanner" || snapshot.BaselineScope != "release_graph_only" || snapshot.Degraded {
			return fmt.Errorf("%w: invalid standalone V0 identity", ErrInvalidSnapshot)
		}
		if !isLowerSHA256(snapshot.CodeHash) || !isLowerSHA256(snapshot.AdapterHash) || !isLowerSHA256(snapshot.SchemaDigest) {
			return fmt.Errorf("%w: incomplete standalone V0 hashes", ErrInvalidSnapshot)
		}
		if strings.Join(snapshot.ExcludedEvidence, "\x00") != strings.Join(v0ExcludedEvidence, "\x00") {
			return fmt.Errorf("%w: invalid standalone V0 excluded evidence", ErrInvalidSnapshot)
		}
	}
	for key, category := range snapshot.CategoryHints {
		if !knownCategory(category) {
			return fmt.Errorf("%w: unknown category hint %q for %q", ErrInvalidSnapshot, category, key)
		}
		parts := strings.Split(key, "\x00")
		if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
			return fmt.Errorf("%w: malformed category hint", ErrInvalidSnapshot)
		}
	}
	if err := validateSnapshotGraph(snapshot); err != nil {
		return err
	}
	return nil
}

func validateSnapshotGraph(snapshot Snapshot) error {
	releases := make(map[string]Release, len(snapshot.Releases))
	for _, release := range snapshot.Releases {
		if release.Key == "" {
			return fmt.Errorf("%w: release key is required", ErrInvalidSnapshot)
		}
		if _, exists := releases[release.Key]; exists {
			return fmt.Errorf("%w: duplicate release key", ErrInvalidSnapshot)
		}
		if hasDuplicateStrings(release.MediumKeys) {
			return fmt.Errorf("%w: duplicate release medium relation", ErrInvalidSnapshot)
		}
		if err := validateFieldMap(release.Fields, true); err != nil {
			return err
		}
		if err := validateCredits(release.Credits); err != nil {
			return err
		}
		if err := validateEvidence(release.Evidence); err != nil {
			return err
		}
		releases[release.Key] = release
	}

	media := make(map[string]Medium, len(snapshot.Media))
	mediaByRelease := make(map[string][]Medium)
	mediumPositions := make(map[string]map[int]struct{})
	for _, medium := range snapshot.Media {
		if medium.Key == "" || medium.ReleaseKey == "" || medium.Position < 1 {
			return fmt.Errorf("%w: invalid medium identity", ErrInvalidSnapshot)
		}
		if _, exists := media[medium.Key]; exists {
			return fmt.Errorf("%w: duplicate medium key", ErrInvalidSnapshot)
		}
		if _, exists := releases[medium.ReleaseKey]; !exists {
			return fmt.Errorf("%w: medium references missing release", ErrInvalidSnapshot)
		}
		if hasDuplicateStrings(medium.TrackKeys) {
			return fmt.Errorf("%w: duplicate medium track relation", ErrInvalidSnapshot)
		}
		if mediumPositions[medium.ReleaseKey] == nil {
			mediumPositions[medium.ReleaseKey] = map[int]struct{}{}
		}
		if _, exists := mediumPositions[medium.ReleaseKey][medium.Position]; exists {
			return fmt.Errorf("%w: duplicate medium position", ErrInvalidSnapshot)
		}
		mediumPositions[medium.ReleaseKey][medium.Position] = struct{}{}
		media[medium.Key] = medium
		mediaByRelease[medium.ReleaseKey] = append(mediaByRelease[medium.ReleaseKey], medium)
	}

	tracks := make(map[string]Track, len(snapshot.Tracks))
	tracksByMedium := make(map[string][]Track)
	trackPositions := make(map[string]map[int]struct{})
	trackSources := make(map[string]struct{}, len(snapshot.Tracks))
	for _, track := range snapshot.Tracks {
		if track.Key == "" || track.MediumKey == "" || track.SourceKey == "" || track.ParentSourceKey == "" || track.Position < 1 {
			return fmt.Errorf("%w: invalid track identity", ErrInvalidSnapshot)
		}
		if track.SourceKind != "physical" && track.SourceKind != "cue_virtual" {
			return fmt.Errorf("%w: invalid track source kind", ErrInvalidSnapshot)
		}
		if track.SourceKind == "physical" && track.ParentSourceKey != track.SourceKey {
			return fmt.Errorf("%w: physical track parent identity mismatch", ErrInvalidSnapshot)
		}
		if _, exists := tracks[track.Key]; exists {
			return fmt.Errorf("%w: duplicate track key", ErrInvalidSnapshot)
		}
		if _, exists := trackSources[track.SourceKey]; exists {
			return fmt.Errorf("%w: duplicate track source key", ErrInvalidSnapshot)
		}
		if _, exists := media[track.MediumKey]; !exists {
			return fmt.Errorf("%w: track references missing medium", ErrInvalidSnapshot)
		}
		if trackPositions[track.MediumKey] == nil {
			trackPositions[track.MediumKey] = map[int]struct{}{}
		}
		if _, exists := trackPositions[track.MediumKey][track.Position]; exists {
			return fmt.Errorf("%w: duplicate track position", ErrInvalidSnapshot)
		}
		if err := validateCredits(track.Credits); err != nil {
			return err
		}
		if err := validateFieldMap(track.Fields, false); err != nil {
			return err
		}
		trackPositions[track.MediumKey][track.Position] = struct{}{}
		trackSources[track.SourceKey] = struct{}{}
		tracks[track.Key] = track
		tracksByMedium[track.MediumKey] = append(tracksByMedium[track.MediumKey], track)
	}

	files := make(map[string]File, len(snapshot.Files))
	fileSources := make(map[string]struct{}, len(snapshot.Files))
	fileReleaseBySource := make(map[string]string, len(snapshot.Files))
	for _, file := range snapshot.Files {
		if file.Key == "" || file.ReleaseKey == "" || file.SourceKey == "" || file.Size < 0 {
			return fmt.Errorf("%w: invalid file identity", ErrInvalidSnapshot)
		}
		if _, exists := releases[file.ReleaseKey]; !exists {
			return fmt.Errorf("%w: file references missing release", ErrInvalidSnapshot)
		}
		if _, exists := files[file.Key]; exists {
			return fmt.Errorf("%w: duplicate file key", ErrInvalidSnapshot)
		}
		if _, exists := fileSources[file.SourceKey]; exists {
			return fmt.Errorf("%w: duplicate file source key", ErrInvalidSnapshot)
		}
		files[file.Key] = file
		fileSources[file.SourceKey] = struct{}{}
		fileReleaseBySource[file.SourceKey] = file.ReleaseKey
	}
	for _, track := range snapshot.Tracks {
		fileReleaseKey, exists := fileReleaseBySource[track.ParentSourceKey]
		if !exists {
			return fmt.Errorf("%w: track parent source is missing", ErrInvalidSnapshot)
		}
		if fileReleaseKey != media[track.MediumKey].ReleaseKey {
			return fmt.Errorf("%w: track parent source belongs to another release", ErrInvalidSnapshot)
		}
	}

	for _, release := range snapshot.Releases {
		values := mediaByRelease[release.Key]
		sort.Slice(values, func(i, j int) bool {
			if values[i].Position != values[j].Position {
				return values[i].Position < values[j].Position
			}
			return values[i].Key < values[j].Key
		})
		keys := make([]string, 0, len(values))
		for _, medium := range values {
			keys = append(keys, medium.Key)
		}
		if !slices.Equal(release.MediumKeys, keys) {
			return fmt.Errorf("%w: release medium graph is not closed or ordered", ErrInvalidSnapshot)
		}
	}
	for _, medium := range snapshot.Media {
		values := tracksByMedium[medium.Key]
		sort.Slice(values, func(i, j int) bool {
			if values[i].Position != values[j].Position {
				return values[i].Position < values[j].Position
			}
			return values[i].Key < values[j].Key
		})
		keys := make([]string, 0, len(values))
		for _, track := range values {
			keys = append(keys, track.Key)
		}
		if !slices.Equal(medium.TrackKeys, keys) {
			return fmt.Errorf("%w: medium track graph is not closed or ordered", ErrInvalidSnapshot)
		}
	}
	return nil
}

func hasDuplicateStrings(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			return true
		}
		if _, exists := seen[value]; exists {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}

func validateCredits(values []Credit) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value.Role) == "" || strings.TrimSpace(value.Name) == "" {
			return fmt.Errorf("%w: invalid credit", ErrInvalidSnapshot)
		}
		key := value.Role + "\x00" + value.Name
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: duplicate credit", ErrInvalidSnapshot)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateEvidence(values []Evidence) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value.Field) == "" || strings.TrimSpace(value.Source) == "" {
			return fmt.Errorf("%w: invalid release evidence", ErrInvalidSnapshot)
		}
		field := canonicalReleaseField(value.Field)
		if _, exists := seen[field]; exists {
			return fmt.Errorf("%w: duplicate release evidence field", ErrInvalidSnapshot)
		}
		seen[field] = struct{}{}
	}
	return nil
}

func validateFieldMap(values map[string]string, releaseFields bool) error {
	seen := make(map[string]struct{}, len(values))
	for field := range values {
		if strings.TrimSpace(field) == "" {
			return fmt.Errorf("%w: empty field key", ErrInvalidSnapshot)
		}
		if releaseFields {
			field = canonicalReleaseField(field)
		}
		if _, exists := seen[field]; exists {
			return fmt.Errorf("%w: duplicate canonical field", ErrInvalidSnapshot)
		}
		seen[field] = struct{}{}
	}
	return nil
}

func isLowerSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

// ValidateComparable 在实体级比较前拒绝混合 snapshot 合同；不同 corpus 身份
// 无法产生有效的回归结论。
func ValidateComparable(expected, actual Snapshot) error {
	if err := ValidateSnapshot(expected); err != nil {
		return fmt.Errorf("expected snapshot: %w", err)
	}
	if err := ValidateSnapshot(actual); err != nil {
		return fmt.Errorf("actual snapshot: %w", err)
	}
	if expected.Version != actual.Version {
		return fmt.Errorf("%w: snapshot versions differ (%d versus %d)", ErrIncompatibleSnapshots, expected.Version, actual.Version)
	}
	if expected.CorpusDigest != actual.CorpusDigest {
		return fmt.Errorf("%w: corpus digests differ", ErrIncompatibleSnapshots)
	}
	for key, expectedCategory := range expected.CategoryHints {
		if actualCategory, ok := actual.CategoryHints[key]; ok && expectedCategory != actualCategory {
			return fmt.Errorf("%w: category hints differ for %q", ErrIncompatibleSnapshots, key)
		}
	}
	return nil
}

type comparisonProfile struct {
	v0                    bool
	expectedReleases      map[string]Release
	actualReleases        map[string]Release
	v0RipLogOnlyFields    map[string]struct{}
	omittedPhysicalTracks map[string]struct{}
	intentionalCredits    map[string]struct{}
	intentionalALACFacts  map[string]struct{}
	intentionalMedia      map[string]struct{}
	intentionalTracks     map[string]struct{}
	intentionalReleases   map[string]struct{}
}

func newComparisonProfile(expected, actual Snapshot) comparisonProfile {
	profile := comparisonProfile{
		v0:                    expected.Implementation == V0GeneratedCorrectedImplementation && expected.BaselineScope == "release_graph_only",
		expectedReleases:      indexReleases(expected.Releases),
		actualReleases:        indexReleases(actual.Releases),
		v0RipLogOnlyFields:    map[string]struct{}{},
		omittedPhysicalTracks: map[string]struct{}{},
		intentionalCredits:    map[string]struct{}{},
		intentionalALACFacts:  map[string]struct{}{},
		intentionalMedia:      map[string]struct{}{},
		intentionalTracks:     map[string]struct{}{},
		intentionalReleases:   map[string]struct{}{},
	}
	if !profile.v0 {
		return profile
	}
	// V0 只要目录中存在抓轨日志就会补 CD；current 只接受 EAC/XLD 签名。
	// 仅当 V0 的精确规则是唯一证据、current 同时缺值和决策时记录这项合同差异。
	for key, want := range profile.expectedReleases {
		got, exists := profile.actualReleases[key]
		if !exists {
			continue
		}
		wantEvidence := indexEvidence(want.Evidence)
		gotEvidence := indexEvidence(got.Evidence)
		fields := []struct {
			name       string
			wantValue  string
			gotValue   string
			wantRuleID string
		}{
			{name: "source_type", wantValue: want.SourceType, gotValue: got.SourceType, wantRuleID: "source.rip_log.cd"},
			{name: "media_type", wantValue: want.MediaType, gotValue: got.MediaType, wantRuleID: "media.rip_log.cd"},
		}
		for _, field := range fields {
			decision, hasWantDecision := wantEvidence[field.name]
			_, hasGotDecision := gotEvidence[field.name]
			if field.wantValue == "cd" && field.gotValue == "" && hasWantDecision && !hasGotDecision &&
				decision.Source == "rule" && canonicalToken(decision.Value) == "cd" && decision.RuleID == field.wantRuleID {
				profile.v0RipLogOnlyFields[key+"\x00"+field.name] = struct{}{}
			}
		}
	}
	expectedTracks := indexTracks(expected.Tracks)
	actualTracks := indexTracks(actual.Tracks)
	for key, want := range expectedTracks {
		if got, exists := actualTracks[key]; exists {
			if currentOnlyALACComposer(want, got) {
				profile.intentionalCredits[key] = struct{}{}
			}
			if strings.EqualFold(want.Fields["codec"], "aac") && strings.EqualFold(got.Fields["codec"], "alac") {
				profile.intentionalALACFacts[key] = struct{}{}
			}
		}
	}
	expectedFileSources := make(map[string]struct{}, len(expected.Files))
	expectedCueParents := make(map[string]struct{})
	for _, file := range expected.Files {
		expectedFileSources[file.SourceKey] = struct{}{}
	}
	for _, track := range expected.Tracks {
		if track.SourceKind == "cue_virtual" {
			expectedCueParents[track.ParentSourceKey] = struct{}{}
		}
	}
	for key, track := range actualTracks {
		if _, exists := expectedTracks[key]; exists || track.SourceKind != "physical" {
			continue
		}
		if _, exists := expectedFileSources[track.SourceKey]; !exists {
			continue
		}
		if _, replacedByCue := expectedCueParents[track.SourceKey]; replacedByCue {
			continue
		}
		// V0 candidate 已解析此物理音频，却没有投影到任何 Track，也没有用 CUE
		// 虚拟 Track 替代；current 的“不得静默丢失”合同会有意保留该物理 Track。
		profile.omittedPhysicalTracks[key] = struct{}{}
	}

	expectedMedia := indexMedia(expected.Media)
	actualMedia := indexMedia(actual.Media)
	for key, want := range expectedMedia {
		got, exists := actualMedia[key]
		if !exists || slices.Equal(want.TrackKeys, got.TrackKeys) {
			continue
		}
		filtered := make([]string, 0, len(got.TrackKeys))
		for _, trackKey := range got.TrackKeys {
			if _, omitted := profile.omittedPhysicalTracks[trackKey]; !omitted {
				filtered = append(filtered, trackKey)
			}
		}
		if !slices.Equal(want.TrackKeys, filtered) {
			continue
		}
		profile.intentionalMedia[key] = struct{}{}
		profile.intentionalReleases[want.ReleaseKey] = struct{}{}
		for _, trackKey := range want.TrackKeys {
			profile.intentionalTracks[trackKey] = struct{}{}
		}
	}
	return profile
}

func (profile comparisonProfile) category(entity, key, field, expected, actual string) DifferenceCategory {
	if !profile.v0 {
		return CategoryCurrentRegression
	}
	if profile.isV0RipLogOnlyDifference(entity, key, field) {
		return CategoryIntentionalContractChange
	}
	if profile.isV0GenreMemberMapping(entity, key, field, expected, actual) {
		return CategorySchemaMappingGap
	}
	if profile.isV0StaleGroupingMediumCount(entity, key, field, expected, actual) {
		return CategorySchemaMappingGap
	}
	if entity == "track" && field == "credits" {
		if _, ok := profile.intentionalCredits[key]; ok {
			return CategoryIntentionalContractChange
		}
	}
	if entity == "track" && field == "presence" {
		if _, ok := profile.omittedPhysicalTracks[key]; ok {
			return CategoryIntentionalContractChange
		}
	}
	if entity == "medium" && field == "track_keys" {
		if _, ok := profile.intentionalMedia[key]; ok {
			return CategoryIntentionalContractChange
		}
	}
	if entity == "track" && field == "position" {
		if _, ok := profile.intentionalTracks[key]; ok {
			return CategoryIntentionalContractChange
		}
	}
	if entity == "release" && field == "field.grouping_track_count" {
		if _, ok := profile.intentionalReleases[key]; ok {
			return CategoryIntentionalContractChange
		}
	}
	if entity == "release" && (field == "album_artist" || strings.HasPrefix(field, "evidence.album_artist.")) {
		want, wantOK := profile.expectedReleases[key]
		got, gotOK := profile.actualReleases[key]
		if wantOK && gotOK && got.AlbumArtist == "" && got.Artist != "" && got.Artist == want.Artist {
			return CategoryIntentionalContractChange
		}
	}
	if (field == "media" || field == "format" || field == "field.codec") && strings.EqualFold(expected, "aac") && strings.EqualFold(actual, "alac") {
		return CategoryIntentionalContractChange
	}
	if entity == "track" && isALACAudioFact(field) {
		if _, ok := profile.intentionalALACFacts[key]; ok && absentCanonicalValue(field, expected) && !absentCanonicalValue(field, actual) {
			return CategoryIntentionalContractChange
		}
	}
	if entity == "release" {
		switch field {
		case "field.provider", "field.country", "field.release_type", "field.parent_collection_key":
			return CategoryCapabilityGap
		}
	}
	return CategoryCurrentRegression
}

func (profile comparisonProfile) isV0RipLogOnlyDifference(entity, key, field string) bool {
	if entity != "release" {
		return false
	}
	for _, metadataField := range []string{"source_type", "media_type"} {
		if field != metadataField && field != "evidence."+metadataField+".presence" {
			continue
		}
		_, exists := profile.v0RipLogOnlyFields[key+"\x00"+metadataField]
		return exists
	}
	return false
}

func (profile comparisonProfile) isV0GenreMemberMapping(entity, key, field, expected, actual string) bool {
	if entity != "release" || (field != "genre" && field != "evidence.genre.value") {
		return false
	}
	want, wantExists := profile.expectedReleases[key]
	got, gotExists := profile.actualReleases[key]
	if !wantExists || !gotExists || expected != want.Genre || actual != got.Genre {
		return false
	}
	// V0 adapter 将多个 genre 用固定分隔符压成一个字符串；current 模型只保存单值。
	return joinedValueContainsExactMember(want.Genre, got.Genre)
}

func (profile comparisonProfile) isV0StaleGroupingMediumCount(entity, key, field, expected, actual string) bool {
	if entity != "release" || field != "field.grouping_medium_count" || expected != "1" {
		return false
	}
	want, wantExists := profile.expectedReleases[key]
	got, gotExists := profile.actualReleases[key]
	return wantExists && gotExists && len(got.MediumKeys) > 1 && slices.Equal(want.MediumKeys, got.MediumKeys) && actual == fmt.Sprint(len(got.MediumKeys))
}

func currentOnlyALACComposer(expected, actual Track) bool {
	if !strings.EqualFold(expected.Fields["codec"], "aac") || !strings.EqualFold(actual.Fields["codec"], "alac") {
		return false
	}
	want := withoutCreditRole(expected.Credits, "performer")
	got := withoutCreditRole(actual.Credits, "performer")
	actualCredits := make(map[Credit]struct{}, len(got))
	for _, credit := range got {
		actualCredits[credit] = struct{}{}
	}
	for _, credit := range want {
		if _, exists := actualCredits[credit]; !exists {
			return false
		}
	}
	expectedCredits := make(map[Credit]struct{}, len(want))
	for _, credit := range want {
		expectedCredits[credit] = struct{}{}
	}
	addedComposer := false
	for _, credit := range got {
		if _, exists := expectedCredits[credit]; exists {
			continue
		}
		if credit.Role != "composer" {
			return false
		}
		addedComposer = true
	}
	return addedComposer
}

func joinedValueContainsExactMember(joined, member string) bool {
	if member == "" {
		return false
	}
	values := strings.Split(joined, " / ")
	if len(values) < 2 {
		return false
	}
	found := false
	for _, value := range values {
		if value == "" {
			return false
		}
		if value == member {
			found = true
		}
	}
	return found
}

func isALACAudioFact(field string) bool {
	switch field {
	case "field.duration_ms", "field.sample_rate", "field.channels", "field.bitrate", "field.bit_depth":
		return true
	default:
		return false
	}
}

func absentCanonicalValue(field, value string) bool {
	if value == "" {
		return true
	}
	switch field {
	case "year", "size", "field.duration_ms", "field.sample_rate", "field.channels", "field.bitrate", "field.bit_depth", "field.cue_end_frames":
		return value == "0"
	default:
		return false
	}
}

// Compare 比较两个 canonical snapshot。不兼容输入会得到 Comparable=false 且无实体级
// 差异的报告；调用方需要错误来执行失败关闭编排时应使用 CompareStrict。
func Compare(expected, actual Snapshot) DifferenceReport {
	report, err := CompareStrict(expected, actual)
	if err != nil {
		report.Errors = []string{err.Error()}
	}
	return report
}

// CompareStrict 是 runner 使用的失败关闭比较入口。
func CompareStrict(expected, actual Snapshot) (DifferenceReport, error) {
	report := DifferenceReport{Metadata: ComparisonMetadata{
		ExpectedSnapshotVersion: expected.Version,
		ActualSnapshotVersion:   actual.Version,
		ExpectedImplementation:  expected.Implementation,
		ActualImplementation:    actual.Implementation,
		ExpectedCorpusDigest:    expected.CorpusDigest,
		ActualCorpusDigest:      actual.CorpusDigest,
		VersionCompatible:       expected.Version == actual.Version && expected.Version == SnapshotVersion,
		CorpusCompatible:        expected.CorpusDigest != "" && expected.CorpusDigest == actual.CorpusDigest,
	}}
	report.Metadata.Comparable = report.Metadata.VersionCompatible && report.Metadata.CorpusCompatible
	if err := ValidateComparable(expected, actual); err != nil {
		return report, err
	}

	expected = canonicalSnapshot(expected)
	actual = canonicalSnapshot(actual)
	profile := newComparisonProfile(expected, actual)
	differences := make([]Difference, 0)
	categoryFor := func(entity, key, field, want, got string) string {
		hintKey := entity + "\x00" + key + "\x00" + field
		if category, ok := expected.CategoryHints[hintKey]; ok {
			return string(category)
		}
		if category, ok := actual.CategoryHints[hintKey]; ok {
			return string(category)
		}
		return string(profile.category(entity, key, field, want, got))
	}
	compare := func(entity, key, field, want, got string) {
		if want != got {
			differences = append(differences, Difference{Entity: entity, Key: key, Field: field, Expected: want, Actual: got, Category: categoryFor(entity, key, field, want, got)})
		}
	}
	comparePresence := func(entity, key string, expectedPresent, actualPresent bool) {
		if expectedPresent != actualPresent {
			compare(entity, key, "presence", fmt.Sprint(expectedPresent), fmt.Sprint(actualPresent))
		}
	}

	expectedReleases := indexReleases(expected.Releases)
	actualReleases := indexReleases(actual.Releases)
	for _, key := range unionKeys(expectedReleases, actualReleases) {
		want, wantOK := expectedReleases[key]
		got, gotOK := actualReleases[key]
		comparePresence("release", key, wantOK, gotOK)
		if wantOK && gotOK {
			compareRelease(compare, want, got, profile.v0)
		}
	}

	expectedMedia := indexMedia(expected.Media)
	actualMedia := indexMedia(actual.Media)
	for _, key := range unionKeys(expectedMedia, actualMedia) {
		want, wantOK := expectedMedia[key]
		got, gotOK := actualMedia[key]
		comparePresence("medium", key, wantOK, gotOK)
		if !wantOK || !gotOK {
			continue
		}
		compare("medium", key, "release_key", want.ReleaseKey, got.ReleaseKey)
		compare("medium", key, "position", fmt.Sprint(want.Position), fmt.Sprint(got.Position))
		compare("medium", key, "title", want.Title, got.Title)
		compare("medium", key, "format", want.Format, got.Format)
		compare("medium", key, "track_keys", strings.Join(want.TrackKeys, "\x00"), strings.Join(got.TrackKeys, "\x00"))
	}

	expectedTracks := indexTracks(expected.Tracks)
	actualTracks := indexTracks(actual.Tracks)
	for _, key := range unionKeys(expectedTracks, actualTracks) {
		want, wantOK := expectedTracks[key]
		got, gotOK := actualTracks[key]
		comparePresence("track", key, wantOK, gotOK)
		if !wantOK || !gotOK {
			continue
		}
		compare("track", key, "medium_key", want.MediumKey, got.MediumKey)
		compare("track", key, "position", fmt.Sprint(want.Position), fmt.Sprint(got.Position))
		compare("track", key, "source_key", want.SourceKey, got.SourceKey)
		compare("track", key, "parent_source_key", want.ParentSourceKey, got.ParentSourceKey)
		compare("track", key, "title", want.Title, got.Title)
		compare("track", key, "artist", want.Artist, got.Artist)
		compare("track", key, "source_kind", want.SourceKind, got.SourceKind)
		compareFields(compare, "track", key, want.Fields, got.Fields)
		if profile.v0 {
			compare("track", key, "credits", encodeSorted(withoutCreditRole(want.Credits, "performer")), encodeSorted(withoutCreditRole(got.Credits, "performer")))
		} else {
			compare("track", key, "credits", encodeSorted(want.Credits), encodeSorted(got.Credits))
		}
	}

	expectedFiles := indexFiles(expected.Files)
	actualFiles := indexFiles(actual.Files)
	for _, key := range unionKeys(expectedFiles, actualFiles) {
		want, wantOK := expectedFiles[key]
		got, gotOK := actualFiles[key]
		comparePresence("file", key, wantOK, gotOK)
		if !wantOK || !gotOK {
			continue
		}
		compare("file", key, "source_key", want.SourceKey, got.SourceKey)
		compare("file", key, "release_key", want.ReleaseKey, got.ReleaseKey)
		compare("file", key, "media", want.Media, got.Media)
		compare("file", key, "size", fmt.Sprint(want.Size), fmt.Sprint(got.Size))
	}

	if expected.CorpusDigest != actual.CorpusDigest {
		compare("snapshot", "corpus", "corpus_digest", expected.CorpusDigest, actual.CorpusDigest)
	}
	// standalone V0 有意排除 production diagnostics、attention、quality 和 local-evidence
	// 状态。current/current 仍比较这些字段；Release Graph-only V0 基准不得把明确缺席的
	// 子系统误报为 current regression。
	if expected.Implementation != V0GeneratedCorrectedImplementation || expected.BaselineScope != "release_graph_only" {
		compare("snapshot", "attention", "count", fmt.Sprint(expected.AttentionCount), fmt.Sprint(actual.AttentionCount))
		if expected.Diagnostics != nil || actual.Diagnostics != nil {
			compare("snapshot", "diagnostics", "counts", mapStringInt(expected.Diagnostics), mapStringInt(actual.Diagnostics))
		}
	}

	sort.SliceStable(differences, func(i, j int) bool {
		left, right := differences[i], differences[j]
		return left.Entity+"\x00"+left.Key+"\x00"+left.Field < right.Entity+"\x00"+right.Key+"\x00"+right.Field
	})
	counts := map[string]int{}
	for _, difference := range differences {
		counts[difference.Category]++
	}
	report.Differences = differences
	report.Counts = counts
	return report, nil
}

func compareRelease(compare func(string, string, string, string, string), expected, actual Release, v0Comparison bool) {
	key := expected.Key
	compare("release", key, "title", expected.Title, actual.Title)
	compare("release", key, "artist", expected.Artist, actual.Artist)
	compare("release", key, "album_artist", expected.AlbumArtist, actual.AlbumArtist)
	compare("release", key, "year", fmt.Sprint(expected.Year), fmt.Sprint(actual.Year))
	compare("release", key, "source_type", expected.SourceType, actual.SourceType)
	compare("release", key, "media_type", expected.MediaType, actual.MediaType)
	compare("release", key, "edition", expected.Edition, actual.Edition)
	compare("release", key, "label", expected.Label, actual.Label)
	compare("release", key, "catalog", expected.Catalog, actual.Catalog)
	compare("release", key, "genre", expected.Genre, actual.Genre)
	compare("release", key, "medium_keys", strings.Join(expected.MediumKeys, "\x00"), strings.Join(actual.MediumKeys, "\x00"))
	if v0Comparison {
		compareReleaseFieldsV0(compare, key, expected.Fields, actual.Fields)
		compare("release", key, "credits", encodeSorted(withoutCreditRole(expected.Credits, "album_artist")), encodeSorted(withoutCreditRole(actual.Credits, "album_artist")))
		compareReleaseEvidenceV0(compare, key, expected.Evidence, actual.Evidence)
		return
	}
	compareFields(compare, "release", key, expected.Fields, actual.Fields)
	compare("release", key, "credits", encodeSorted(expected.Credits), encodeSorted(actual.Credits))
	compare("release", key, "evidence", encodeSorted(expected.Evidence), encodeSorted(actual.Evidence))
}

func compareReleaseFieldsV0(compare func(string, string, string, string, string), key string, expected, actual map[string]string) {
	skipped := map[string]struct{}{
		"title": {}, "artist": {}, "album_artist": {}, "year": {}, "source_type": {},
		"media_type": {}, "edition": {}, "label": {}, "catalog": {}, "catalog_number": {},
		"genre": {}, "candidate_kind": {},
	}
	keys := make(map[string]struct{}, len(expected)+len(actual))
	for field := range expected {
		keys[field] = struct{}{}
	}
	for field := range actual {
		keys[field] = struct{}{}
	}
	fields := make([]string, 0, len(keys))
	for field := range keys {
		if _, skip := skipped[field]; !skip {
			fields = append(fields, field)
		}
	}
	sort.Strings(fields)
	for _, field := range fields {
		compare("release", key, "field."+field, expected[field], actual[field])
	}
}

func compareReleaseEvidenceV0(compare func(string, string, string, string, string), key string, expected, actual []Evidence) {
	comparableFields := map[string]struct{}{
		"title": {}, "album_artist": {}, "year": {}, "source_type": {}, "media_type": {},
		"genre": {}, "catalog_number": {},
	}
	expectedByField := indexEvidence(expected)
	actualByField := indexEvidence(actual)
	fields := make([]string, 0, len(expectedByField))
	for field := range expectedByField {
		if _, comparable := comparableFields[field]; comparable {
			fields = append(fields, field)
		}
	}
	sort.Strings(fields)
	for _, field := range fields {
		want := expectedByField[field]
		got, exists := actualByField[field]
		if !exists {
			compare("release", key, "evidence."+field+".presence", "true", "false")
			continue
		}
		compare("release", key, "evidence."+field+".value", want.Value, got.Value)
		actualSource := got.Source
		if v0RipLogEvidenceSourceEquivalent(field, want, got) {
			actualSource = want.Source
		}
		compare("release", key, "evidence."+field+".source", want.Source, actualSource)
	}
}

func v0RipLogEvidenceSourceEquivalent(field string, expected, actual Evidence) bool {
	expectedRule := map[string]string{
		"source_type": "source.rip_log.cd",
		"media_type":  "media.rip_log.cd",
	}[field]
	return expectedRule != "" &&
		expected.Source == "rule" && expected.RuleID == expectedRule &&
		actual.Source == "rip_log" && actual.RuleID == "rip_log_cd_v1" &&
		canonicalToken(expected.Value) == "cd" && canonicalToken(actual.Value) == "cd" &&
		expected.Confidence == "high" && actual.Confidence == "high" &&
		expected.Action == "auto_apply" && actual.Action == "auto_apply"
}

func indexEvidence(values []Evidence) map[string]Evidence {
	result := make(map[string]Evidence, len(values))
	for _, value := range values {
		field := canonicalReleaseField(value.Field)
		value.Field = field
		result[field] = value
	}
	return result
}

func withoutCreditRole(values []Credit, ignoredRole string) []Credit {
	result := make([]Credit, 0, len(values))
	for _, value := range values {
		if value.Role != ignoredRole {
			result = append(result, value)
		}
	}
	return result
}

func compareFields(compare func(string, string, string, string, string), entity, key string, expected, actual map[string]string) {
	keys := make(map[string]struct{}, len(expected)+len(actual))
	for field := range expected {
		keys[field] = struct{}{}
	}
	for field := range actual {
		keys[field] = struct{}{}
	}
	fields := make([]string, 0, len(keys))
	for field := range keys {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		compare(entity, key, "field."+field, expected[field], actual[field])
	}
}

func encodeSorted(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "<encode-error>"
	}
	return string(encoded)
}

func mapStringInt(value map[string]int) string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+fmt.Sprint(value[key]))
	}
	return strings.Join(parts, "\x00")
}

func canonicalSnapshot(snapshot Snapshot) Snapshot {
	snapshot.Releases = append([]Release(nil), snapshot.Releases...)
	snapshot.Media = append([]Medium(nil), snapshot.Media...)
	snapshot.Tracks = append([]Track(nil), snapshot.Tracks...)
	snapshot.Files = append([]File(nil), snapshot.Files...)
	if snapshot.CategoryHints != nil {
		snapshot.CategoryHints = mapsClone(snapshot.CategoryHints)
	}
	for index := range snapshot.Releases {
		snapshot.Releases[index].SourceType = canonicalToken(snapshot.Releases[index].SourceType)
		snapshot.Releases[index].MediaType = canonicalToken(snapshot.Releases[index].MediaType)
		snapshot.Releases[index].Fields = canonicalReleaseFields(snapshot.Releases[index].Fields)
		// MediumKeys 按 medium position 排序；保留该语义顺序，避免隐藏碟片重排。
		snapshot.Releases[index].MediumKeys = append([]string(nil), snapshot.Releases[index].MediumKeys...)
		snapshot.Releases[index].Credits = append([]Credit(nil), snapshot.Releases[index].Credits...)
		snapshot.Releases[index].Evidence = append([]Evidence(nil), snapshot.Releases[index].Evidence...)
		for evidenceIndex := range snapshot.Releases[index].Evidence {
			field := canonicalReleaseField(snapshot.Releases[index].Evidence[evidenceIndex].Field)
			if field == "source_type" || field == "media_type" {
				snapshot.Releases[index].Evidence[evidenceIndex].Value = canonicalToken(snapshot.Releases[index].Evidence[evidenceIndex].Value)
			}
		}
		sort.Slice(snapshot.Releases[index].Credits, func(i, j int) bool {
			left, right := snapshot.Releases[index].Credits[i], snapshot.Releases[index].Credits[j]
			return left.Role+"\x00"+left.Name < right.Role+"\x00"+right.Name
		})
		sort.Slice(snapshot.Releases[index].Evidence, func(i, j int) bool {
			left, right := snapshot.Releases[index].Evidence[i], snapshot.Releases[index].Evidence[j]
			return left.Field+"\x00"+left.RuleID < right.Field+"\x00"+right.RuleID
		})
	}
	for index := range snapshot.Media {
		snapshot.Media[index].Format = canonicalToken(snapshot.Media[index].Format)
		// TrackKeys 按 track position 排序；再次排序会掩盖真实曲序回归。
		snapshot.Media[index].TrackKeys = append([]string(nil), snapshot.Media[index].TrackKeys...)
	}
	for index := range snapshot.Tracks {
		snapshot.Tracks[index].Fields = canonicalTrackFields(snapshot.Tracks[index].Fields)
		snapshot.Tracks[index].Credits = append([]Credit(nil), snapshot.Tracks[index].Credits...)
		sort.Slice(snapshot.Tracks[index].Credits, func(i, j int) bool {
			left, right := snapshot.Tracks[index].Credits[i], snapshot.Tracks[index].Credits[j]
			return left.Role+"\x00"+left.Name < right.Role+"\x00"+right.Name
		})
	}
	for index := range snapshot.Files {
		snapshot.Files[index].Media = canonicalToken(snapshot.Files[index].Media)
	}
	snapshot.ExcludedEvidence = append([]string(nil), snapshot.ExcludedEvidence...)
	sort.Slice(snapshot.Releases, func(i, j int) bool { return snapshot.Releases[i].Key < snapshot.Releases[j].Key })
	sort.Slice(snapshot.Media, func(i, j int) bool {
		left, right := snapshot.Media[i], snapshot.Media[j]
		if left.ReleaseKey != right.ReleaseKey {
			return left.ReleaseKey < right.ReleaseKey
		}
		if left.Position != right.Position {
			return left.Position < right.Position
		}
		return left.Key < right.Key
	})
	sort.Slice(snapshot.Tracks, func(i, j int) bool {
		left, right := snapshot.Tracks[i], snapshot.Tracks[j]
		if left.MediumKey != right.MediumKey {
			return left.MediumKey < right.MediumKey
		}
		if left.Position != right.Position {
			return left.Position < right.Position
		}
		return left.Key < right.Key
	})
	sort.Slice(snapshot.Files, func(i, j int) bool { return snapshot.Files[i].Key < snapshot.Files[j].Key })
	return snapshot
}

func canonicalToken(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func canonicalReleaseField(field string) string {
	switch field {
	case "album_title":
		return "title"
	case "edition_version":
		return "edition"
	default:
		return field
	}
}

func canonicalReleaseFields(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for field, value := range values {
		field = canonicalReleaseField(field)
		if existing, exists := result[field]; exists && existing != value {
			// 校验会阻止重复 evidence 字段，但旧 field map 仍可能携带别名。
			// 保留这个不可能的冲突，使比较失败，而不是静默选择任一值。
			result[field] = existing + "\x00<alias-conflict>\x00" + value
			continue
		}
		result[field] = value
	}
	if value, exists := result["source_type"]; exists {
		result["source_type"] = canonicalToken(value)
	}
	if value, exists := result["media_type"]; exists {
		result["media_type"] = canonicalToken(value)
	}
	return result
}

func canonicalTrackFields(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := cloneStringValues(values)
	if codec, exists := result["codec"]; exists {
		result["codec"] = canonicalToken(codec)
	}
	// current 将未知数值音频事实保存为零，而 V0 canonical writer 会省略它们。
	// CUE INDEX 01 位于零帧有实际语义，不得折叠为缺失。
	for _, field := range []string{"duration_ms", "sample_rate", "channels", "bitrate", "bit_depth", "cue_end_frames"} {
		if result[field] == "0" {
			delete(result, field)
		}
	}
	return result
}

func cloneStringValues(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func mapsClone(value map[string]DifferenceCategory) map[string]DifferenceCategory {
	clone := make(map[string]DifferenceCategory, len(value))
	for key, category := range value {
		clone[key] = category
	}
	return clone
}

func knownCategory(category DifferenceCategory) bool {
	switch category {
	case CategoryCurrentRegression, CategorySchemaMappingGap, CategoryCapabilityGap,
		CategoryHistoricalCorpusDrift, CategoryIntentionalContractChange:
		return true
	default:
		return false
	}
}

func sortedCopy(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func indexReleases(values []Release) map[string]Release {
	result := make(map[string]Release, len(values))
	for _, value := range values {
		result[value.Key] = value
	}
	return result
}

func indexMedia(values []Medium) map[string]Medium {
	result := make(map[string]Medium, len(values))
	for _, value := range values {
		result[value.Key] = value
	}
	return result
}

func indexTracks(values []Track) map[string]Track {
	result := make(map[string]Track, len(values))
	for _, value := range values {
		result[value.Key] = value
	}
	return result
}

func indexFiles(values []File) map[string]File {
	result := make(map[string]File, len(values))
	for _, value := range values {
		result[value.Key] = value
	}
	return result
}

func unionKeys[T any](left, right map[string]T) []string {
	keys := make(map[string]struct{}, len(left)+len(right))
	for key := range left {
		keys[key] = struct{}{}
	}
	for key := range right {
		keys[key] = struct{}{}
	}
	result := make([]string, 0, len(keys))
	for key := range keys {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func (report DifferenceReport) JSON() ([]byte, error) {
	copyReport := report
	copyReport.Differences = append([]Difference(nil), report.Differences...)
	for index := range copyReport.Differences {
		copyReport.Differences[index].Expected = redactValue(copyReport.Differences[index].Expected)
		copyReport.Differences[index].Actual = redactValue(copyReport.Differences[index].Actual)
	}
	if copyReport.Counts == nil {
		copyReport.Counts = map[string]int{}
	}
	return json.Marshal(copyReport)
}

func redactValue(value string) string {
	if value == "" {
		return value
	}
	return "<redacted:" + digest("report\x00" + value)[:16] + ">"
}

var ErrMultipleBaselines = errors.New("multiple baselines require explicit selection")

func SelectBaseline(candidates []Snapshot) (Snapshot, error) {
	if len(candidates) == 0 {
		return Snapshot{}, ErrNoBaseline
	}
	if len(candidates) > 1 {
		return Snapshot{}, ErrMultipleBaselines
	}
	if err := ValidateSnapshot(candidates[0]); err != nil {
		return Snapshot{}, err
	}
	return canonicalSnapshot(candidates[0]), nil
}
