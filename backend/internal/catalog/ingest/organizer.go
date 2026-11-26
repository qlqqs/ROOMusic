package ingest

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

// CandidateKind 标识 organizer 如何把一组来源判定为一个 Release 候选。
type CandidateKind string

const (
	CandidateOrdinary     CandidateKind = "ordinary_directory"
	CandidateMultiDisc    CandidateKind = "strict_multidisc"
	CandidateBoxLeaf      CandidateKind = "box_leaf"
	CandidateSameDirSplit CandidateKind = "same_dir_split"
	CandidateFormatSplit  CandidateKind = "format_split"
	CandidateLooseAlbum   CandidateKind = "loose_album"
	CandidateLooseUnknown CandidateKind = "loose_unknown"
)

// CandidateAnchor 是候选在扫描间保持稳定的锚点：判定类型、整理范围与范围内的区分符。
// 它只使用安全的相对来源路径，不含展示标题。
type CandidateAnchor struct {
	Kind             CandidateKind
	Scope, Partition string
}

// FieldDecision 记录一个 Release 字段的选择结果：取值、来源、置信度、动作、规则与候选值。
type FieldDecision struct {
	Field, Value, Source, Confidence, Action, RuleID, Reason string
	Candidates                                               []string
}

// OrganizedTrack 是已归入某个 Medium 的一条曲目来源及其排定的位置与碟号。
type OrganizedTrack struct {
	Observation  SourceObservation
	Position     int
	SortPosition int `json:",omitempty"`
	Disc         int
}

// OrganizedCandidate 是 organizer 的输出：一个 Release 候选，含锚点、按碟分组的曲目、
// 字段决定、需关注原因与分组证据引用。它是纯值，写入数据库由 PersistCandidate 完成。
type OrganizedCandidate struct {
	Anchor                     CandidateAnchor
	ReleaseGroupKey            string
	Title, Artist, AlbumArtist string
	Mediums                    map[int][]OrganizedTrack
	Decisions                  []FieldDecision
	Attention                  []string
	GroupingRefs               []string
	SourceType, MediaType      string
	Genres                     []string
	VersionTerms               []string `json:",omitempty"`
	SharedArtworkDirectory     bool     `json:",omitempty"`
	CatalogNumber              string
	Edition, Label, Barcode    string

	// OriginalKind 仅 format_split 填写切分前种类。
	OriginalKind CandidateKind

	// VersionGroup 相同的候选是同一专辑的格式版本，写入时供调用方选择目标组。
	VersionGroup string
	// JoinGroupID 仅用于新建发行；已有发行的组保持不变。
	JoinGroupID string

	// MediumTitles 只保存有碟名的介质位置。
	MediumTitles map[int]string
}

const unknownCandidateTitle = "未知专辑"

const (
	maxDecisionCandidates = 20
	// MaxGroupingEvidenceRefs 是单个候选保留的分组证据来源引用上限。
	MaxGroupingEvidenceRefs = 100
)

func stableValues(obs []SourceObservation, field func(SourceObservation) string) map[string]int {
	counts := map[string]int{}
	for _, o := range obs {
		if v := metadata.NormalizeValue(field(o)); v != "" {
			counts[v]++
		}
	}
	return counts
}

func chooseValue(obs []SourceObservation, field string, get func(SourceObservation) string) FieldDecision {
	inferenceField := field
	if field == "title" {
		inferenceField = "album"
	}
	authoritative := make([]SourceObservation, 0, len(obs))
	for _, observation := range obs {
		provenanceField := decisionProvenanceField(observation, inferenceField)
		if !observation.InferredFields[provenanceField] && metadata.NormalizeValue(get(observation)) != "" {
			authoritative = append(authoritative, observation)
		}
	}
	decisionObservations := obs
	if len(authoritative) > 0 {
		decisionObservations = authoritative
	}
	counts := stableValues(decisionObservations, get)
	if field == "title" {
		spellings := map[string]string{}
		totals := map[string]int{}
		for v, count := range counts {
			k := textnorm.MergeKey(v)
			if k == "" {
				k = v
			}
			totals[k] += count
			old := spellings[k]
			if old == "" || count > counts[old] || count == counts[old] && v < old {
				spellings[k] = v
			}
		}
		counts = map[string]int{}
		for k, n := range totals {
			counts[spellings[k]] = n
		}
	}
	if len(counts) == 0 {
		return FieldDecision{Field: field, RuleID: "field_missing_v1", Confidence: "low", Action: "uncertain_apply", Reason: "field_missing"}
	}
	values := make([]string, 0, len(counts))
	for value := range counts {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool {
		if counts[values[i]] != counts[values[j]] {
			return counts[values[i]] > counts[values[j]]
		}
		return values[i] < values[j]
	})
	selected := values[0]
	confidence, action, reason := "high", "auto_apply", ""
	if len(values) > 1 {
		confidence, action, reason = "medium", "uncertain_apply", "inconsistent_candidates"
	}
	if allInferred(obs, inferenceField) {
		confidence, action, reason = "low", "uncertain_apply", "inferred_value"
	}
	// source 表示决定级证据来源，不暴露具体解析器实现名称。
	source := "tag"
	for _, o := range decisionObservations {
		provenanceField := decisionProvenanceField(o, inferenceField)
		if metadata.NormalizeValue(get(o)) == selected && !o.InferredFields[provenanceField] {
			if s := o.FieldSources[provenanceField]; s != "" {
				source = s
			}
			break
		}
	}
	if allInferred(obs, inferenceField) {
		for _, o := range obs {
			if s := o.FieldSources[decisionProvenanceField(o, inferenceField)]; s != "" {
				source = s
				break
			}
		}
		if source == "tag" {
			source = "default_fallback"
		}
	}
	if len(values) > 1 && counts[values[0]] == counts[values[1]] {
		selected, confidence, action, reason = "", "low", "uncertain_apply", "tied_candidates"
	}
	if len(values) > maxDecisionCandidates {
		values = values[:maxDecisionCandidates]
	}
	return FieldDecision{Field: field, Value: selected, Source: source, Confidence: confidence, Action: action, RuleID: "majority_v1", Reason: reason, Candidates: values}
}

// chooseGenres 按包含流派的曲目数合并列表，同票时按展示名称排序；多流派不产生字段问题。
func chooseGenres(observations []SourceObservation) FieldDecision {
	type genreVotes struct {
		tracks    int
		spellings map[string]int
	}
	votes := make(map[string]*genreVotes)
	for _, observation := range observations {
		for _, genre := range metadata.SplitGenres(observation.Genres...) {
			key := metadata.GenreKey(genre)
			if votes[key] == nil {
				votes[key] = &genreVotes{spellings: make(map[string]int)}
			}
			votes[key].tracks++
			votes[key].spellings[genre]++
		}
	}
	decision := FieldDecision{Field: "genre", Source: "tag", Confidence: "high", Action: "auto_apply", RuleID: "genre_union_v1", Candidates: []string{}}
	counts := make(map[string]int, len(votes))
	for _, vote := range votes {
		display, most := "", 0
		for spelling, count := range vote.spellings {
			if count > most || count == most && spelling < display {
				display, most = spelling, count
			}
		}
		counts[display] = vote.tracks
		decision.Candidates = append(decision.Candidates, display)
	}
	sort.Slice(decision.Candidates, func(i, j int) bool {
		a, b := decision.Candidates[i], decision.Candidates[j]
		if counts[a] != counts[b] {
			return counts[a] > counts[b]
		}
		return a < b
	})
	if len(decision.Candidates) == 0 {
		return decision
	}
	if len(decision.Candidates) > maxDecisionCandidates {
		decision.Candidates = decision.Candidates[:maxDecisionCandidates]
	}
	decision.Value = decision.Candidates[0]
source:
	for _, observation := range observations {
		if observation.InferredFields["genre"] {
			continue
		}
		for _, genre := range metadata.SplitGenres(observation.Genres...) {
			if genre == decision.Value {
				if source := observation.FieldSources["genre"]; source != "" {
					decision.Source = source
				}
				break source
			}
		}
	}
	return decision
}

func allInferred(obs []SourceObservation, field string) bool {
	if len(obs) == 0 {
		return true
	}
	seen := false
	for _, o := range obs {
		provenanceField := decisionProvenanceField(o, field)
		if metadata.NormalizeValue(fieldValue(o, provenanceField)) == "" {
			continue
		}
		seen = true
		if o.InferredFields == nil || !o.InferredFields[provenanceField] {
			return false
		}
	}
	return seen
}

func decisionProvenanceField(observation SourceObservation, field string) string {
	if field == "artist" && metadata.NormalizeValue(observation.AlbumArtist) != "" {
		return "album_artist"
	}
	return field
}

func fieldValue(o SourceObservation, field string) string {
	switch field {
	case "title":
		return o.Title
	case "album":
		return o.Album
	case "album_artist":
		return o.AlbumArtist
	case "artist":
		if o.AlbumArtist != "" {
			return o.AlbumArtist
		}
		return o.Artist
	case "year":
		if o.Year > 0 {
			return strconv.Itoa(o.Year)
		}
	case "catalog_number":
		return o.CatalogNumber
	case "edition":
		return o.Edition
	case "label":
		return o.Label
	case "barcode":
		return o.Barcode
	case "source_type":
		return o.SourceType
	case "media_type":
		return o.MediaType
	default:
		return ""
	}
	return ""
}

type observationLocation struct {
	dir       string
	parent    string
	disc      int
	isDiscDir bool
	marker    discMarker
}

func locateObservation(o SourceObservation) observationLocation {
	dir := NormalizedPath(o.Directory)
	if dir == "" {
		dir = NormalizedPath(filepath.ToSlash(filepath.Dir(filepath.FromSlash(o.RelativePath))))
	}
	name := filepath.Base(filepath.FromSlash(dir))
	marker, ok := parseDiscMarker(name)
	if !ok && standaloneBonusDir(name) {
		marker, ok = discMarker{Suffix: name}, true
	}
	if ok {
		parent := NormalizedPath(filepath.ToSlash(filepath.Dir(filepath.FromSlash(dir))))
		return observationLocation{dir: dir, parent: parent, disc: marker.Number, isDiscDir: true, marker: marker}
	}
	return observationLocation{dir: dir}
}

type discParentPlan struct {
	positions map[string]int
	titles    map[int]string
	label     string
}

func planDiscParent(directories map[string]observationLocation, maxDisc int) discParentPlan {
	plan := discParentPlan{positions: map[string]int{}, titles: map[int]string{}}
	prefixes := map[string]bool{}
	var bonus []observationLocation
	paths := make([]string, 0, len(directories))
	for path := range directories {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		loc := directories[path]
		m := loc.marker
		if isBonusRemainder(m.Prefix) || isBonusRemainder(m.Suffix) {
			bonus = append(bonus, loc)
			continue
		}
		if m.Prefix != "" {
			prefixes[m.Prefix] = true
		}
		plan.positions[path] = loc.disc
		if title := discMediumTitle(m.Suffix); title != "" {
			if previous, exists := plan.titles[loc.disc]; exists && previous != title {
				title = ""
			}
			plan.titles[loc.disc] = title
		}
		maxDisc = max(maxDisc, loc.disc)
	}
	if len(prefixes) == 1 {
		for prefix := range prefixes {
			plan.label = prefix
		}
	}
	titleOf := func(loc observationLocation) string {
		if loc.marker.Suffix != "" {
			return discMediumTitle(loc.marker.Suffix)
		}
		return discMediumTitle(loc.marker.Prefix)
	}
	sort.SliceStable(bonus, func(i, j int) bool {
		a, b := strings.ToLower(titleOf(bonus[i])), strings.ToLower(titleOf(bonus[j]))
		if a != b {
			return a < b
		}
		return bonus[i].disc < bonus[j].disc
	})
	for _, loc := range bonus {
		maxDisc++
		plan.positions[loc.dir] = maxDisc
		plan.titles[maxDisc] = titleOf(loc)
	}
	return plan
}

// discMediumTitle 去掉包裹整个碟名的括号，保留名称内部的括号。
func discMediumTitle(text string) string {
	text = metadata.NormalizeValue(text)
	for len(text) >= 2 {
		pair := string(text[0]) + string(text[len(text)-1])
		if pair != "()" && pair != "[]" && pair != "{}" && pair != "<>" {
			break
		}
		depth, enclosed := 0, true
		for index := 0; index < len(text)-1; index++ {
			if text[index] == pair[0] {
				depth++
			} else if text[index] == pair[1] {
				depth--
			}
			if depth == 0 {
				enclosed = false
				break
			}
		}
		if !enclosed || depth != 1 {
			break
		}
		text = strings.TrimSpace(text[1 : len(text)-1])
	}
	return text
}

func albumWithoutDiscMarker(o SourceObservation) string {
	album, _, _ := stripAlbumDiscMarker(o.Album)
	return album
}

func albumPartition(o SourceObservation) string {
	album := textnorm.MergeKey(albumWithoutDiscMarker(o))
	if o.InferredFields["album"] {
		album = ""
	}
	if album == "" {
		return "|"
	}
	artist := textnorm.MergeKey(o.AlbumArtist)
	if o.InferredFields["album_artist"] {
		artist = ""
	}
	return album + "|" + artist
}

func albumEvidence(o SourceObservation) string {
	album, _, _ := strings.Cut(albumPartition(o), "|")
	return album
}

func hasTaggedTitleAndArtist(o SourceObservation) bool {
	return !isCueObservation(o) &&
		!o.InferredFields["title"] && metadata.NormalizeValue(o.Title) != "" &&
		!o.InferredFields["artist"] && metadata.NormalizeValue(o.Artist) != ""
}

// directoryLooseSingles 在普通目录完成格式切分后检查整组。每首的标签号与文件名号
// 必须相容，整组要么都无编号，要么每首都有相同的编号。
func directoryLooseSingles(scope string, observations []SourceObservation, filenames map[string]filenameNumbers) bool {
	if hasAuthoritativeAlbum(observations) {
		return false
	}
	commonTrack := 0
	for i, o := range observations {
		if !hasTaggedTitleAndArtist(o) || o.CueSheetPath != "" {
			return false
		}
		track := filenames[NormalizedPath(o.RelativePath)].Track
		if o.TrackNumber > 0 && !o.InferredFields["track_number"] {
			if track > 0 && track != o.TrackNumber {
				return false
			}
			track = o.TrackNumber
		}
		if i > 0 && track != commonTrack {
			return false
		}
		commonTrack = track
	}
	_, albumFolder := parseFolderMetadata(filepath.Base(filepath.FromSlash(scope)))
	return !albumFolder
}

func knownAlbumArtists(observations []SourceObservation, partitionOf func(SourceObservation) string) map[string]map[string]bool {
	result := map[string]map[string]bool{}
	for _, observation := range observations {
		partition := partitionOf(observation)
		album, artist, found := strings.Cut(partition, "|")
		if !found || album == "" || artist == "" {
			continue
		}
		if result[album] == nil {
			result[album] = map[string]bool{}
		}
		result[album][artist] = true
	}
	return result
}

func compatibleAlbumPartition(partition string, knownArtists map[string]map[string]bool) string {
	album, artist, found := strings.Cut(partition, "|")
	if !found || album == "" || artist != "" || len(knownArtists[album]) != 1 {
		return partition
	}
	for knownArtist := range knownArtists[album] {
		return album + "|" + knownArtist
	}
	return partition
}

func chooseReleaseArtist(observations []SourceObservation) FieldDecision {
	withAlbumArtist := make([]SourceObservation, 0, len(observations))
	for _, observation := range observations {
		if metadata.NormalizeValue(observation.AlbumArtist) != "" && !observation.InferredFields["album_artist"] {
			withAlbumArtist = append(withAlbumArtist, observation)
		}
	}
	if len(withAlbumArtist) > 0 {
		return chooseValue(withAlbumArtist, "artist", func(observation SourceObservation) string { return observation.AlbumArtist })
	}
	return chooseValue(observations, "artist", func(observation SourceObservation) string { return observation.Artist })
}

// appendFolderDecisionObservation 在候选边界已经确定后补充目录名证据，避免目录名
// 改变来源身份或把本应分开的候选重新归组。每个字段只有在标签/CUE 没有权威值时才补充。
func appendFolderDecisionObservation(observations []SourceObservation, scope string) []SourceObservation {
	scope = NormalizedPath(scope)
	if scope == "" {
		return observations
	}
	folderName := filepath.Base(filepath.FromSlash(scope))
	if marker, ok := parseDiscMarker(folderName); ok {
		folderName = marker.Prefix
	}
	parsed, matched := parseFolderMetadata(folderName)
	if !matched {
		return observations
	}
	folder := SourceObservation{
		SourceKind:     "folder",
		InferredFields: map[string]bool{},
		FieldSources:   map[string]string{},
	}
	setText := func(field, value string, target *string) {
		if metadata.NormalizeValue(value) == "" || hasAuthoritativeObservationValue(observations, field) {
			return
		}
		*target = value
		folder.FieldSources[field] = "folder"
	}
	setText("album", parsed.Album, &folder.Album)
	setText("album_artist", parsed.AlbumArtist, &folder.AlbumArtist)
	setText("source_type", parsed.SourceType, &folder.SourceType)
	setText("media_type", parsed.MediaType, &folder.MediaType)
	setText("catalog_number", parsed.Catalog, &folder.CatalogNumber)
	setText("edition", parsed.Edition, &folder.Edition)
	setText("label", parsed.Label, &folder.Label)
	if parsed.Year > 0 && !hasAuthoritativeObservationValue(observations, "year") {
		folder.Year = parsed.Year
		folder.FieldSources["year"] = "folder"
	}
	if len(folder.FieldSources) == 0 {
		return observations
	}
	result := append([]SourceObservation(nil), observations...)
	return append(result, folder)
}

func hasAuthoritativeObservationValue(observations []SourceObservation, field string) bool {
	provenanceField := field
	if field == "title" {
		provenanceField = "album"
	}
	for _, observation := range observations {
		if metadata.NormalizeValue(fieldValue(observation, provenanceField)) != "" && !observation.InferredFields[provenanceField] {
			return true
		}
	}
	return false
}

// OrganizeObservations 以确定性纯规则把 observations 归为 Release candidates；
// 数据库、文件系统和遍历顺序都不会进入规则输入。
// OrganizeObservations 把同一整理范围内的来源观察确定性地归成 Release 候选；结果不依赖输入顺序。
func OrganizeObservations(observations []SourceObservation) []OrganizedCandidate {
	items := append([]SourceObservation(nil), observations...)
	sort.Slice(items, func(i, j int) bool {
		left, right := NormalizedPath(items[i].RelativePath), NormalizedPath(items[j].RelativePath)
		if left != right {
			return left < right
		}
		return observationSortKey(items[i]) < observationSortKey(items[j])
	})
	items = resolveCueCandidateGroups(items)

	// 在 CUE 合并之后补物理轨的专辑名碟号，标签与 CUE 的权威碟号不被覆盖。
	for index := range items {
		o := &items[index]
		if isCueObservation(*o) || !o.InferredFields["disc_number"] {
			continue
		}
		if _, disc, ok := stripAlbumDiscMarker(o.Album); ok {
			o.DiscNumber = disc
			o.FieldSources = cloneStringMap(o.FieldSources)
			o.FieldSources["disc_number"] = "album_title"
		}
	}
	filenameEvidence := prepareFilenameEvidence(items)
	locations := make([]observationLocation, len(items))
	parentDirectories := map[string]map[string]observationLocation{}
	directMax := map[string]int{}
	for i, o := range items {
		loc := locateObservation(o)
		locations[i] = loc
		if !loc.isDiscDir {
			directMax[loc.dir] = max(directMax[loc.dir], observationDiscNumber(o))
			continue
		}
		if parentDirectories[loc.parent] == nil {
			parentDirectories[loc.parent] = map[string]observationLocation{}
		}
		parentDirectories[loc.parent][loc.dir] = loc
	}
	parentPlans := map[string]discParentPlan{}
	for parent, dirs := range parentDirectories {
		parentPlans[parent] = planDiscParent(dirs, directMax[parent])
	}

	type bucket struct {
		originalKind  CandidateKind
		versionGroup  string
		sharedArtwork bool
		kind          CandidateKind
		scope         string
		partition     string
		obs           []SourceObservation
	}
	buckets := map[string]*bucket{}
	addBucket := func(kind CandidateKind, scope, partition string, observations ...SourceObservation) *bucket {
		key := string(kind) + "\x00" + scope + "\x00" + partition
		if buckets[key] == nil {
			buckets[key] = &bucket{originalKind: kind, kind: kind, scope: scope, partition: partition}
		}
		buckets[key].obs = append(buckets[key].obs, observations...)
		return buckets[key]
	}

	// 碟目录与其父目录的直属曲目共用一个范围。
	baseGroups := map[string][]SourceObservation{}
	baseMeta := map[string]struct {
		kind  CandidateKind
		scope string
	}{}
	for i, o := range items {
		loc := locations[i]
		kind, scope := CandidateOrdinary, loc.dir
		if loc.isDiscDir {
			kind, scope = CandidateMultiDisc, loc.parent
			o.structureDisc = parentPlans[scope].positions[loc.dir]
			if o.DiscNumber <= 0 || o.InferredFields["disc_number"] && o.FieldSources["disc_number"] != "album_title" {
				o.DiscNumber = o.structureDisc
				o.FieldSources = cloneStringMap(o.FieldSources)
				o.FieldSources["disc_number"] = "folder_structure"
			}
		} else if loc.dir != "" && parentDirectories[loc.dir] != nil {
			kind = CandidateMultiDisc
		}
		key := string(kind) + "\x00" + scope
		baseGroups[key] = append(baseGroups[key], o)
		baseMeta[key] = struct {
			kind  CandidateKind
			scope string
		}{kind, scope}
	}
	baseKeys := make([]string, 0, len(baseGroups))
	for key := range baseGroups {
		baseKeys = append(baseKeys, key)
	}
	sort.Strings(baseKeys)
	for _, key := range baseKeys {
		group := baseGroups[key]
		meta := baseMeta[key]
		// 根目录文件没有可信的专辑文件夹身份：存在 album tag 时可以归组，否则每个文件
		// 都是独立的未知专辑候选。
		if meta.scope == "" && meta.kind != CandidateMultiDisc {
			withAlbum := map[string][]SourceObservation{}
			knownArtists := knownAlbumArtists(group, albumPartition)
			for _, o := range group {
				if part := compatibleAlbumPartition(albumPartition(o), knownArtists); part != "|" {
					withAlbum[part] = append(withAlbum[part], o)
				} else {
					addBucket(CandidateLooseUnknown, "", NormalizedPath(o.RelativePath), o)
				}
			}
			parts := make([]string, 0, len(withAlbum))
			for part := range withAlbum {
				parts = append(parts, part)
			}
			sort.Strings(parts)
			for _, part := range parts {
				addBucket(CandidateLooseAlbum, "", part, withAlbum[part]...)
			}
			continue
		}
		addDirectoryBucket := func(kind CandidateKind, partition string, observations []SourceObservation) *bucket {
			if meta.kind == CandidateOrdinary && directoryLooseSingles(meta.scope, observations, filenameEvidence) {
				for _, o := range observations {
					// 与根目录散曲一致，推断的目录名不进入发行标题决定。
					o.Album = ""
					addBucket(CandidateLooseUnknown, meta.scope, NormalizedPath(o.RelativePath), o)
				}
				return nil
			}
			b := addBucket(kind, meta.scope, partition, observations...)
			b.originalKind = meta.kind
			return b
		}
		albums := map[string]bool{}
		for _, o := range group {
			albums[albumEvidence(o)] = true
		}
		if len(albums) <= 1 {
			if formats := splitContinuousFormats(group, filenameEvidence); len(formats) > 0 {
				for format, tracks := range formats {
					if b := addDirectoryBucket(CandidateFormatSplit, "format:"+format, tracks); b != nil {
						b.versionGroup = "format:" + meta.scope
					}
				}
			} else {
				partitions := map[string][]SourceObservation{}
				known := knownAlbumArtists(group, albumPartition)
				majority := false
				for _, o := range group {
					key := compatibleAlbumPartition(albumPartition(o), known)
					partitions[key] = append(partitions[key], o)
					majority = majority || len(partitions[key])*2 > len(group)
				}
				if meta.kind == CandidateMultiDisc || majority || continuousObservations(group, filenameEvidence) {
					addDirectoryBucket(meta.kind, "", group)
				} else {
					for part, tracks := range partitions {
						addDirectoryBucket(CandidateSameDirSplit, part, tracks)
					}
				}
			}
			continue
		}
		if continuousObservations(group, filenameEvidence) {
			addDirectoryBucket(meta.kind, "", group)
			continue
		}
		formats := groupByFormat(group)
		// 相同标题组合可关联为格式版本。批次标识最终使用成员路径，避免展示名进入身份。
		versionBatches := map[string]map[*bucket]bool{}
		recordBatch := func(name string, b *bucket) {
			if b == nil || len(formats) < 2 {
				return
			}
			if versionBatches[name] == nil {
				versionBatches[name] = map[*bucket]bool{}
			}
			versionBatches[name][b] = true
		}
		for format, tracks := range formats {
			if len(formats) > 1 && continuousEvidence(tracks, filenameEvidence, 1) != nil {
				b := addDirectoryBucket(CandidateFormatSplit, "format:"+format, tracks)
				names := map[string]bool{}
				for _, track := range tracks {
					names[albumEvidence(track)] = true
				}
				keys := make([]string, 0, len(names))
				for name := range names {
					keys = append(keys, name)
				}
				sort.Strings(keys)
				recordBatch(strings.Join(keys, "\x00"), b)
				continue
			}
			missing := false
			for _, o := range tracks {
				if albumEvidence(o) == "" {
					missing = true
					break
				}
			}
			for _, o := range tracks {
				album := albumEvidence(o)
				part := album
				if missing {
					artist := o.AlbumArtist
					if metadata.FieldAuthenticity("album_artist", artist, o.FieldSources["album_artist"], o.InferredFields["album_artist"], false) != metadata.Real {
						artist = o.Artist
						if metadata.FieldAuthenticity("artist", artist, o.FieldSources["artist"], o.InferredFields["artist"], false) != metadata.Real {
							artist = ""
						}
					}
					part = textnorm.MergeKey(artist) + "|" + album
					if album == "" {
						o.Album = ""
						if part == "|" {
							part += "@" + NormalizedPath(o.RelativePath)
						}
					}
				}
				b := addBucket(CandidateSameDirSplit, meta.scope, format+"|"+part, o)
				b.originalKind = meta.kind
				batch := album
				if missing {
					batch = part
				}
				recordBatch(batch, b)
			}
		}
		for _, versions := range versionBatches {
			for b := range versions {
				b.sharedArtwork = len(versionBatches) > 1
			}
			if len(versions) < 2 {
				continue
			}
			first := ""
			for b := range versions {
				for _, o := range b.obs {
					path := NormalizedPath(o.RelativePath)
					if first == "" || path < first {
						first = path
					}
				}
			}
			for b := range versions {
				b.versionGroup = "format:" + meta.scope + ":source:" + first
			}
		}
	}

	keys := make([]string, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]OrganizedCandidate, 0, len(keys))
	for _, key := range keys {
		b := buckets[key]
		obs := b.obs
		sort.Slice(obs, func(i, j int) bool { return observationSortKey(obs[i]) < observationSortKey(obs[j]) })
		c := OrganizedCandidate{Anchor: CandidateAnchor{Kind: b.kind, Scope: b.scope, Partition: stableAnchorPartition(b.kind, b.partition, obs)}, ReleaseGroupKey: key, Mediums: map[int][]OrganizedTrack{}, MediumTitles: map[int]string{}}
		obs = decodeCandidateLegacy(obs)
		if b.kind == CandidateFormatSplit {
			c.OriginalKind = b.originalKind
		}
		c.VersionGroup = b.versionGroup
		c.SharedArtworkDirectory = b.sharedArtwork
		folderDecisionScope := b.scope
		if b.originalKind == CandidateMultiDisc && len(obs) > 0 {
			// V0 的严格多碟候选使用首个物理碟目录的 folder evidence，父目录只负责
			// 归组和缺失标题 fallback，不能额外制造 catalog 等高置信度字段。
			folderDecisionScope = DirectoryOf(obs[0].RelativePath)
		}
		c.VersionTerms = versionTerms(b.scope, obs)
		decisionObservations := appendFolderDecisionObservation(obs, folderDecisionScope)
		albumDecision := chooseValue(decisionObservations, "title", albumWithoutDiscMarker)
		if !hasAuthoritativeAlbum(obs) {
			fallback, rule := "", ""
			if b.originalKind == CandidateMultiDisc {
				fallback = parentPlans[b.scope].label
				if fallback == "" {
					fallback = metadata.NormalizeValue(filepath.Base(filepath.FromSlash(b.scope)))
				}
				rule = "multidisc_parent_fallback_v1"
			}
			fallback = metadata.StripFormatNoise(fallback)
			if fallback != "" && fallback != "." {
				albumDecision = FieldDecision{Field: "title", Value: fallback, Source: "folder_fallback", Confidence: "low", Action: "uncertain_apply", RuleID: rule, Reason: "inferred_value", Candidates: []string{fallback}}
			}
		}
		artistDecision := chooseReleaseArtist(decisionObservations)
		albumArtistDecision := chooseValue(decisionObservations, "album_artist", func(o SourceObservation) string { return o.AlbumArtist })
		tracks := resolveCuePhysicalCoexistence(obs)
		applyVinylPositions(tracks)
		applyCompilation(tracks, &artistDecision, &albumArtistDecision)
		yearDecision := chooseValue(decisionObservations, "year", func(o SourceObservation) string { return fieldValue(o, "year") })
		sourceDecision := chooseValue(decisionObservations, "source_type", func(o SourceObservation) string { return o.SourceType })
		mediaDecision := chooseValue(decisionObservations, "media_type", func(o SourceObservation) string { return o.MediaType })
		genreDecision := chooseGenres(decisionObservations)
		catalogDecision := chooseValue(decisionObservations, "catalog_number", func(o SourceObservation) string { return o.CatalogNumber })
		editionDecision := chooseValue(decisionObservations, "edition", func(o SourceObservation) string { return o.Edition })
		labelDecision := chooseValue(decisionObservations, "label", func(o SourceObservation) string { return o.Label })
		barcodeDecision := chooseValue(decisionObservations, "barcode", func(o SourceObservation) string { return o.Barcode })
		c.Decisions = []FieldDecision{albumDecision, artistDecision}
		for _, optionalDecision := range []FieldDecision{albumArtistDecision, yearDecision, sourceDecision, mediaDecision, genreDecision, catalogDecision, editionDecision, labelDecision, barcodeDecision} {
			if optionalDecision.Value != "" || optionalDecision.Reason == "tied_candidates" {
				c.Decisions = append(c.Decisions, optionalDecision)
			}
		}
		c.Title = albumDecision.Value
		if c.Title == "" {
			c.Title = unknownCandidateTitle
		}
		c.Artist = artistDecision.Value
		c.AlbumArtist = albumArtistDecision.Value
		c.SourceType = sourceDecision.Value
		c.MediaType = mediaDecision.Value
		c.Genres = genreDecision.Candidates
		c.CatalogNumber = catalogDecision.Value
		c.Edition = editionDecision.Value
		c.Label = labelDecision.Value
		c.Barcode = barcodeDecision.Value
		// Attention 只保存独立的归组问题；字段不确定性已由 release_field_decisions 表达，
		// 不应再写入 grouping reason 导致 REST 重复计数。
		if b.kind == CandidateSameDirSplit {
			c.Attention = append(c.Attention, "same_directory_conflict")
		}
		c.GroupingRefs = observationRefs(obs)
		for _, o := range tracks {
			disc := observationDiscNumber(o)
			if title := parentPlans[b.scope].titles[disc]; b.originalKind == CandidateMultiDisc && title != "" {
				c.MediumTitles[disc] = title
			}
			position := o.TrackNumber
			if position < 1 || o.InferredFields["track_number"] && o.FieldSources["track_number"] != "filename_fallback" && o.FieldSources["track_number"] != "vinyl_side" {
				position = 0
			}
			c.Mediums[disc] = append(c.Mediums[disc], OrganizedTrack{Observation: o, Position: position, Disc: disc})
		}
		for disc := range c.Mediums {
			discardConflictingFilenamePositions(c.Mediums[disc])
			assignFallbackTrackPositions(c.Mediums[disc])
		}
		result = append(result, c)
	}
	return result
}

func assignFallbackTrackPositions(tracks []OrganizedTrack) {
	sort.SliceStable(tracks, func(i, j int) bool {
		a, b := tracks[i], tracks[j]
		if a.Position != b.Position {
			return b.Position == 0 || a.Position > 0 && a.Position < b.Position
		}
		left, right := filepath.Base(a.Observation.RelativePath), filepath.Base(b.Observation.RelativePath)
		if left != right {
			return naturalLess(left, right)
		}
		return observationSortKey(a.Observation) < observationSortKey(b.Observation)
	})
	for i := range tracks {
		tracks[i].SortPosition = i + 1
		tracks[i].Observation.TrackNumber = tracks[i].Position
	}
}

func hasAuthoritativeAlbum(observations []SourceObservation) bool {
	for _, observation := range observations {
		if metadata.NormalizeValue(observation.Album) != "" && !observation.InferredFields["album"] {
			return true
		}
	}
	return false
}

func observationRefs(observations []SourceObservation) []string {
	seen := map[string]bool{}
	refs := make([]string, 0, len(observations))
	for _, observation := range observations {
		candidates := append([]string{observation.RelativePath}, observation.RelatedSourceRefs...)
		for _, ref := range candidates {
			ref = NormalizedPath(ref)
			if ref == "" || seen[ref] {
				continue
			}
			seen[ref] = true
			refs = append(refs, ref)
		}
	}
	sort.Strings(refs)
	if len(refs) > MaxGroupingEvidenceRefs {
		refs = refs[:MaxGroupingEvidenceRefs]
	}
	return refs
}

// ApplyRipLogEvidence 用抓轨日志证据补全候选缺失的光盘语义（来源类型与媒体类型）；
// 已由 tag、CUE 或目录名明确给出的字段不会被覆盖。
func ApplyRipLogEvidence(candidate *OrganizedCandidate, sourceRefs []string) {
	if candidate == nil || len(sourceRefs) == 0 {
		return
	}
	candidate.GroupingRefs = mergeEvidenceRefs(sourceRefs, candidate.GroupingRefs)
	decision := func(field string) FieldDecision {
		return FieldDecision{Field: field, Value: "CD", Source: "rip_log", Confidence: "high", Action: "auto_apply", RuleID: "rip_log_cd_v1", Candidates: []string{"CD"}}
	}
	apply := func(field string, target *string) {
		index := -1
		for i := range candidate.Decisions {
			if candidate.Decisions[i].Field == field {
				index = i
				break
			}
		}
		if *target != "" || index >= 0 {
			if index < 0 || candidate.Decisions[index].Source != "folder" || candidate.Decisions[index].Reason == "tied_candidates" {
				return
			}
		}
		*target = "CD"
		next := decision(field)
		updated := make([]FieldDecision, 0, len(candidate.Decisions)+1)
		replaced := false
		for _, existing := range candidate.Decisions {
			if existing.Field != field {
				updated = append(updated, existing)
				continue
			}
			if !replaced {
				updated = append(updated, next)
				replaced = true
			}
		}
		if !replaced {
			updated = append(updated, next)
		}
		candidate.Decisions = updated
	}
	apply("source_type", &candidate.SourceType)
	apply("media_type", &candidate.MediaType)
}

func mergeEvidenceRefs(groups ...[]string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, MaxGroupingEvidenceRefs)
	for _, group := range groups {
		for _, value := range group {
			value = NormalizedPath(value)
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			result = append(result, value)
			if len(result) == MaxGroupingEvidenceRefs {
				return result
			}
		}
	}
	return result
}

func resolveCueCandidateGroups(observations []SourceObservation) []SourceObservation {
	type cueGroup struct {
		directory string
		partition string
		cues      []SourceObservation
	}
	groups := map[string]*cueGroup{}
	for _, observation := range observations {
		if !isCueObservation(observation) {
			continue
		}
		key := NormalizedPath(observation.Directory) + "\x00" + albumPartition(observation)
		if groups[key] == nil {
			groups[key] = &cueGroup{directory: NormalizedPath(observation.Directory), partition: albumPartition(observation)}
		}
		groups[key].cues = append(groups[key].cues, observation)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	processed := map[string]bool{}
	result := make([]SourceObservation, 0, len(observations))
	for _, key := range keys {
		group := groups[key]
		members := append([]SourceObservation(nil), group.cues...)
		parents := map[string]bool{}
		for _, cue := range group.cues {
			parents[cueParentPath(cue)] = true
		}
		for _, observation := range observations {
			if isCueObservation(observation) {
				continue
			}
			path := NormalizedPath(observation.RelativePath)
			if parents[path] {
				members = append(members, observation)
				continue
			}
			if NormalizedPath(observation.Directory) == group.directory && group.partition != "|" && albumPartition(observation) == group.partition {
				members = append(members, observation)
			}
		}
		refs := observationRefs(members)
		for _, member := range members {
			processed[NormalizedPath(member.RelativePath)] = true
		}
		survivors := resolveCuePhysicalCoexistence(members)
		for index := range survivors {
			if !isCueObservation(survivors[index]) {
				survivors[index] = mergeSingleCueEvidence(survivors[index], group.cues)
			}
		}
		// 按原暂存键保留封面；被 CUE 淘汰的来源迁移到尚无封面的幸存曲目。
		artworkScopes := map[string]bool{}
		for _, survivor := range survivors {
			if len(survivor.Artwork) > 0 || survivor.ArtworkDeferred {
				artworkScopes[observationArtworkScope(survivor)] = true
			}
		}
		for _, member := range members {
			if len(member.Artwork) == 0 && !member.ArtworkDeferred {
				continue
			}
			scope := observationArtworkScope(member)
			if artworkScopes[scope] {
				continue
			}
			for index := range survivors {
				if len(survivors[index].Artwork) > 0 || survivors[index].ArtworkDeferred {
					continue
				}
				survivors[index].Artwork = append([]byte(nil), member.Artwork...)
				survivors[index].ArtworkMIME = member.ArtworkMIME
				survivors[index].ArtworkDeferred = member.ArtworkDeferred
				survivors[index].ArtworkRelativePath = member.ArtworkRelativePath
				survivors[index].ArtworkScope = scope
				artworkScopes[scope] = true
				break
			}
		}
		for _, survivor := range survivors {
			survivor.RelatedSourceRefs = refs
			result = append(result, survivor)
		}
	}
	for _, observation := range observations {
		if !processed[NormalizedPath(observation.RelativePath)] {
			result = append(result, observation)
		}
	}
	sort.Slice(result, func(i, j int) bool { return observationSortKey(result[i]) < observationSortKey(result[j]) })
	return result
}

func mergeSingleCueEvidence(physical SourceObservation, cues []SourceObservation) SourceObservation {
	matching := make([]SourceObservation, 0, 1)
	for _, cue := range cues {
		if cueParentPath(cue) == NormalizedPath(physical.RelativePath) {
			matching = append(matching, cue)
		}
	}
	if len(matching) != 1 {
		return physical
	}
	cue := matching[0]
	if physical.InferredFields == nil {
		physical.InferredFields = map[string]bool{}
	} else {
		physical.InferredFields = cloneBoolMap(physical.InferredFields)
	}
	if physical.FieldSources == nil {
		physical.FieldSources = map[string]string{}
	} else {
		physical.FieldSources = cloneStringMap(physical.FieldSources)
	}
	mergeText := func(field string, target *string, value string) bool {
		if value == "" || (*target != "" && !physical.InferredFields[field]) {
			return false
		}
		*target = value
		delete(physical.InferredFields, field)
		if source := cue.FieldSources[field]; source != "" {
			physical.FieldSources[field] = source
		} else {
			physical.FieldSources[field] = "cue_sheet"
		}
		return true
	}
	mergeText("title", &physical.Title, cue.Title)
	artistAdopted := mergeText("artist", &physical.Artist, cue.Artist)
	mergeText("album", &physical.Album, cue.Album)
	if cue.TrackNumber > 0 && !cue.InferredFields["track_number"] && (physical.TrackNumber < 1 || physical.InferredFields["track_number"]) {
		physical.TrackNumber = cue.TrackNumber
		delete(physical.InferredFields, "track_number")
		physical.FieldSources["track_number"] = "cue_track"
	}
	if cue.DiscNumber > 0 && !cue.InferredFields["disc_number"] && (physical.DiscNumber < 1 || physical.InferredFields["disc_number"]) {
		physical.DiscNumber = cue.DiscNumber
		delete(physical.InferredFields, "disc_number")
		physical.FieldSources["disc_number"] = cue.FieldSources["disc_number"]
		if physical.FieldSources["disc_number"] == "" {
			physical.FieldSources["disc_number"] = "cue_sheet"
		}
	}
	albumArtistAdopted := mergeText("album_artist", &physical.AlbumArtist, cue.AlbumArtist)
	if len(cue.Genres) > 0 {
		physical.Genres = append([]string(nil), cue.Genres...)
		delete(physical.InferredFields, "genre")
		physical.FieldSources["genre"] = cue.FieldSources["genre"]
		if physical.FieldSources["genre"] == "" {
			physical.FieldSources["genre"] = "cue_sheet"
		}
	}
	mergeText("catalog_number", &physical.CatalogNumber, cue.CatalogNumber)
	if artistAdopted || albumArtistAdopted {
		adoptedSources := map[string]string{}
		if artistAdopted {
			adoptedSources["track_artist"] = physical.FieldSources["artist"]
		}
		if albumArtistAdopted {
			adoptedSources["album_artist"] = physical.FieldSources["album_artist"]
		}
		var evidence []metadata.ArtistTagEvidence
		for _, entry := range physical.ArtistTagEvidence {
			if _, replaced := adoptedSources[entry.Role]; !replaced {
				evidence = append(evidence, entry)
			}
		}
		for _, entry := range cue.ArtistTagEvidence {
			if source, adopted := adoptedSources[entry.Role]; adopted && entry.SourceKind == source {
				evidence = append(evidence, entry)
			}
		}
		// 两个有界输入合并后仍须满足每轨 100 项。不得截断或去重来掩盖溢出；
		// 原样交给 PersistCandidate 的 ValidateArtistTagEvidence，使整个候选失败并回滚。
		physical.ArtistTagEvidence = evidence
	}
	if cue.Year > 0 && physical.Year == 0 {
		physical.Year = cue.Year
		physical.FieldSources["year"] = cue.FieldSources["year"]
	}
	physical.CueSheetPath = cue.CueSheetPath
	physical.CueParentRelativePath = cue.CueParentRelativePath
	physical.CueReferencedFile = cue.CueReferencedFile
	physical.CueIndexFrames = cue.CueIndexFrames
	physical.CueIndexPresent = cue.CueIndexPresent
	physical.CueEndFrames = cue.CueEndFrames
	physical.CueEndPresent = cue.CueEndPresent
	physical.CueISRC = cue.CueISRC
	if cue.CueISRC != "" {
		physical.FieldSources["isrc"] = "cue_track"
	}
	return physical
}

func cloneStringMap(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func cloneBoolMap(input map[string]bool) map[string]bool {
	result := make(map[string]bool, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func observationSortKey(o SourceObservation) string {
	return NormalizedPath(o.RelativePath) + "\x00" + metadata.NormalizeValue(o.Title) + "\x00" + metadata.NormalizeValue(o.Album) + "\x00" + metadata.NormalizeValue(o.Artist)
}

// 稳定锚只使用格式或来源路径。
func stableAnchorPartition(kind CandidateKind, groupingPartition string, obs []SourceObservation) string {
	if kind == CandidateFormatSplit {
		return groupingPartition
	}
	if kind == CandidateOrdinary || kind == CandidateMultiDisc || kind == CandidateBoxLeaf {
		return ""
	}
	paths := make([]string, 0, len(obs))
	for _, observation := range obs {
		paths = append(paths, NormalizedPath(observation.RelativePath))
	}
	sort.Strings(paths)
	if len(paths) > 0 && paths[0] != "" {
		return "source:" + paths[0]
	}
	return "group:" + groupingPartition
}

// resolveCuePhysicalCoexistence 只使用明确的 CUE 父来源关系。每个父来源独立决策：
// 多个虚拟分轨保留虚拟轨并隐藏整轨；只有一个虚拟轨时保留物理轨；无关物理文件
// 永远不参与该决策。GroupingRefs 仍保留被隐藏来源的证据。
func resolveCuePhysicalCoexistence(obs []SourceObservation) []SourceObservation {
	physicalByPath := map[string]bool{}
	virtualCountByParent := map[string]int{}
	for _, o := range obs {
		if isCueObservation(o) {
			if parent := cueParentPath(o); parent != "" {
				virtualCountByParent[parent]++
			}
			continue
		}
		physicalByPath[NormalizedPath(o.RelativePath)] = true
	}
	result := make([]SourceObservation, 0, len(obs))
	for _, o := range obs {
		if isCueObservation(o) {
			parent := cueParentPath(o)
			if physicalByPath[parent] && virtualCountByParent[parent] <= 1 {
				continue
			}
		} else if virtualCountByParent[NormalizedPath(o.RelativePath)] > 1 {
			continue
		}
		result = append(result, o)
	}
	return result
}

func isCueObservation(observation SourceObservation) bool {
	return strings.EqualFold(observation.SourceKind, "cue_virtual") || strings.Contains(strings.ToLower(observation.SourceKind), "cue")
}

func cueParentPath(observation SourceObservation) string {
	if parent := NormalizedPath(observation.CueParentRelativePath); parent != "" {
		return parent
	}
	return NormalizedPath(observation.CueReferencedFile)
}

// CandidateIdentity 由根目录 ID 与锚点生成写入 releases.candidate_anchor 的稳定标识。
func CandidateIdentity(root string, a CandidateAnchor) string {
	// Partition 只使用安全的相对来源区分符，不包含展示标题。
	return fmt.Sprintf("%s:v2:%s:%s:%s", metadata.NormalizeValue(root), a.Kind, NormalizedPath(a.Scope), NormalizedPath(a.Partition))
}

func observationArtworkScope(observation SourceObservation) string {
	if observation.ArtworkScope != "" {
		return observation.ArtworkScope
	}
	return artworkScope(observation)
}
