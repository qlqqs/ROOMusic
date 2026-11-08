package ingest

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
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
	Observation SourceObservation
	Position    int
	Disc        int
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
	CatalogNumber              string
	Edition, Label, Barcode    string

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
	if marker, ok := parseDiscMarker(filepath.Base(filepath.FromSlash(dir))); ok {
		parent := NormalizedPath(filepath.ToSlash(filepath.Dir(filepath.FromSlash(dir))))
		return observationLocation{dir: dir, parent: parent, disc: marker.Number, isDiscDir: true, marker: marker}
	}
	return observationLocation{dir: dir}
}

// discDirectory 汇集一个物理碟目录的标签证据，避免按曲目重复计算目录编号。
type discDirectory struct {
	location     observationLocation
	observations []SourceObservation
}

type discParentPlan struct {
	positions map[string]int
	titles    map[int]string
	label     string
}

func planDiscParent(parent string, byPath map[string]*discDirectory) discParentPlan {
	directories := make([]*discDirectory, 0, len(byPath))
	exclusive := parent != ""
	for _, directory := range byPath {
		directories = append(directories, directory)
		for _, observation := range directory.observations {
			exclusive = exclusive && observation.DiscParentExclusive
		}
	}
	sort.Slice(directories, func(i, j int) bool { return directories[i].location.dir < directories[j].location.dir })
	if !exclusive {
		// 父目录不独占时，只有纯碟号目录参与原有的严格多碟判断。
		pure := make([]*discDirectory, 0, len(directories))
		for _, directory := range directories {
			marker := directory.location.marker
			if marker.Prefix == "" && marker.Suffix == "" {
				pure = append(pure, directory)
			}
		}
		if len(pure) < 2 || discPartitions(pure) > 1 || !contiguousDiscDirectories(pure) {
			return discParentPlan{}
		}
		return composeDiscParent(pure, nil, "")
	}
	partitionCount := discPartitions(directories)
	if partitionCount > 1 {
		return discParentPlan{}
	}
	var main, bonus []*discDirectory
	prefixes := map[string]bool{}
	label := ""
	for _, directory := range directories {
		marker := directory.location.marker
		if isBonusRemainder(marker.Prefix) || isBonusRemainder(marker.Suffix) {
			bonus = append(bonus, directory)
			continue
		}
		main = append(main, directory)
		prefix := metadata.NormalizeValue(marker.Prefix)
		prefixes[strings.ToLower(prefix)] = true
		if label == "" {
			label = prefix
		}
	}
	if len(prefixes) > 1 {
		return discParentPlan{}
	}
	if len(main) == 0 {
		main, bonus = bonus, nil
	}
	if !uniqueDiscDirectories(main) {
		var plain, titled []*discDirectory
		for _, directory := range main {
			if directory.location.marker.Suffix == "" {
				plain = append(plain, directory)
			} else {
				titled = append(titled, directory)
			}
		}
		if len(plain) == 0 || !uniqueDiscDirectories(plain) {
			return discParentPlan{}
		}
		main = plain
		bonus = append(bonus, titled...)
	}
	canMerge := len(directories) == 1 || len(main) == 1 && len(bonus) > 0 || len(main) >= 2 && contiguousDiscDirectories(main)
	if !canMerge && partitionCount == 1 {
		canMerge = true
		for _, directory := range directories {
			if !hasAuthoritativeAlbum(directory.observations) {
				canMerge = false
				break
			}
		}
	}
	if !canMerge {
		return discParentPlan{}
	}
	return composeDiscParent(main, bonus, label)
}

func discPartitions(directories []*discDirectory) int {
	var observations []SourceObservation
	for _, directory := range directories {
		observations = append(observations, directory.observations...)
	}
	knownArtists := knownAlbumArtists(observations)
	partitions := map[string]bool{}
	for _, observation := range observations {
		if partition := compatibleAlbumPartition(observation, knownArtists); partition != "|" {
			partitions[partition] = true
		}
	}
	return len(partitions)
}

func uniqueDiscDirectories(directories []*discDirectory) bool {
	numbers := map[int]bool{}
	for _, directory := range directories {
		number := directory.location.disc
		if numbers[number] {
			return false
		}
		numbers[number] = true
	}
	return true
}

func contiguousDiscDirectories(directories []*discDirectory) bool {
	if !uniqueDiscDirectories(directories) {
		return false
	}
	for _, directory := range directories {
		if directory.location.disc < 1 || directory.location.disc > len(directories) {
			return false
		}
	}
	return len(directories) > 0
}

func composeDiscParent(main, bonus []*discDirectory, label string) discParentPlan {
	plan := discParentPlan{positions: map[string]int{}, titles: map[int]string{}, label: label}
	maxDisc := 0
	for _, directory := range main {
		number := directory.location.disc
		plan.positions[directory.location.dir] = number
		if title := discMediumTitle(directory.location.marker.Suffix); title != "" {
			plan.titles[number] = title
		}
		if number > maxDisc {
			maxDisc = number
		}
	}
	remainder := func(directory *discDirectory) string {
		marker := directory.location.marker
		if marker.Suffix != "" {
			return marker.Suffix
		}
		return marker.Prefix
	}
	sort.Slice(bonus, func(i, j int) bool {
		left, right := strings.ToLower(metadata.NormalizeValue(remainder(bonus[i]))), strings.ToLower(metadata.NormalizeValue(remainder(bonus[j])))
		if left != right {
			return left < right
		}
		if bonus[i].location.disc != bonus[j].location.disc {
			return bonus[i].location.disc < bonus[j].location.disc
		}
		return bonus[i].location.dir < bonus[j].location.dir
	})
	for index, directory := range bonus {
		number := maxDisc + index + 1
		plan.positions[directory.location.dir] = number
		if title := discMediumTitle(remainder(directory)); title != "" {
			plan.titles[number] = title
		}
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
	album := metadata.NormalizeValue(albumWithoutDiscMarker(o))
	if o.InferredFields["album"] {
		album = ""
	}
	if album == "" {
		return "|"
	}
	artist := metadata.NormalizeValue(o.AlbumArtist)
	if o.InferredFields["album_artist"] {
		artist = ""
	}
	return album + "|" + artist
}

func albumEvidence(o SourceObservation) string {
	album, _, _ := strings.Cut(albumPartition(o), "|")
	return album
}

func knownAlbumArtists(observations []SourceObservation) map[string]map[string]bool {
	result := map[string]map[string]bool{}
	for _, observation := range observations {
		partition := albumPartition(observation)
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

func compatibleAlbumPartition(observation SourceObservation, knownArtists map[string]map[string]bool) string {
	partition := albumPartition(observation)
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
	parentDirectories := map[string]map[string]*discDirectory{}
	for i, o := range items {
		loc := locateObservation(o)
		locations[i] = loc
		if !loc.isDiscDir {
			continue
		}
		if parentDirectories[loc.parent] == nil {
			parentDirectories[loc.parent] = map[string]*discDirectory{}
		}
		if parentDirectories[loc.parent][loc.dir] == nil {
			parentDirectories[loc.parent][loc.dir] = &discDirectory{location: loc}
		}
		directory := parentDirectories[loc.parent][loc.dir]
		directory.observations = append(directory.observations, o)
	}
	parentPlans := map[string]discParentPlan{}
	for parent, directories := range parentDirectories {
		parentPlans[parent] = planDiscParent(parent, directories)
	}

	type bucket struct {
		originalKind CandidateKind
		kind         CandidateKind
		scope        string
		partition    string
		obs          []SourceObservation
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

	// 先建立目录与 Box 边界，再在边界内应用 album 多数决；严格多碟父目录是唯一允许的
	// 跨目录合并。
	baseGroups := map[string][]SourceObservation{}
	baseMeta := map[string]struct {
		kind  CandidateKind
		scope string
	}{}
	for i, o := range items {
		loc := locations[i]
		kind, scope := CandidateOrdinary, loc.dir
		if loc.isDiscDir {
			if _, merged := parentPlans[loc.parent].positions[loc.dir]; merged {
				kind, scope = CandidateMultiDisc, loc.parent
			} else {
				kind, scope = CandidateBoxLeaf, loc.dir
			}
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
		if meta.kind == CandidateMultiDisc {
			addBucket(meta.kind, meta.scope, "", group...)
			continue
		}
		// 根目录文件没有可信的专辑文件夹身份：存在 album tag 时可以归组，否则每个文件
		// 都是独立的未知专辑候选。
		if meta.scope == "" {
			withAlbum := map[string][]SourceObservation{}
			knownArtists := knownAlbumArtists(group)
			for _, o := range group {
				if part := compatibleAlbumPartition(o, knownArtists); part != "|" {
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
		if formats := splitContinuousFormats(group, filenameEvidence); len(formats) > 0 {
			for format, observations := range formats {
				b := addBucket(CandidateFormatSplit, meta.scope, "format:"+format, observations...)
				b.originalKind = meta.kind
			}
			continue
		}
		// 同目录 album 冲突仅在严格多数（>50%）时使用稳定多数决；平票或无明显多数时
		// 按 album 事实拆分。
		partCounts := map[string]int{}
		knownArtists := knownAlbumArtists(group)
		for _, o := range group {
			if part := compatibleAlbumPartition(o, knownArtists); part != "|" {
				partCounts[part]++
			}
		}
		winner, winnerCount, authoritativeCount := "", 0, 0
		for part, count := range partCounts {
			authoritativeCount += count
			if count > winnerCount || (count == winnerCount && (winner == "" || part < winner)) {
				winner, winnerCount = part, count
			}
		}
		if len(partCounts) <= 1 || (winner != "" && winnerCount*2 > authoritativeCount) || continuousObservations(group, filenameEvidence) {
			addBucket(meta.kind, meta.scope, "", group...)
			continue
		}
		parts := splitByPartition(group, knownArtists)
		partKeys := make([]string, 0, len(parts))
		for part := range parts {
			partKeys = append(partKeys, part)
		}
		sort.Strings(partKeys)
		for _, part := range partKeys {
			obs := parts[part]
			if part == "|" {
				for _, unknown := range obs {
					addBucket(CandidateSameDirSplit, meta.scope, "|@"+NormalizedPath(unknown.RelativePath), unknown)
				}
				continue
			}
			addBucket(CandidateSameDirSplit, meta.scope, part, obs...)
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
		if b.kind == CandidateFormatSplit {
			c.VersionGroup = "format:" + b.scope
		}
		folderDecisionScope := b.scope
		if b.kind == CandidateMultiDisc && len(obs) > 0 {
			// V0 的严格多碟候选使用首个物理碟目录的 folder evidence，父目录只负责
			// 归组和缺失标题 fallback，不能额外制造 catalog 等高置信度字段。
			folderDecisionScope = DirectoryOf(obs[0].RelativePath)
		}
		decisionObservations := appendFolderDecisionObservation(obs, folderDecisionScope)
		albumDecision := chooseValue(decisionObservations, "title", albumWithoutDiscMarker)
		if !hasAuthoritativeAlbum(obs) {
			fallback, rule := "", ""
			if b.kind == CandidateMultiDisc {
				fallback = parentPlans[b.scope].label
				if fallback == "" {
					fallback = metadata.NormalizeValue(filepath.Base(filepath.FromSlash(b.scope)))
				}
				rule = "multidisc_parent_fallback_v1"
			} else if b.originalKind == CandidateBoxLeaf && len(obs) > 0 {
				loc := locateObservation(obs[0])
				if len(parentDirectories[loc.parent]) == 1 {
					fallback = loc.marker.Prefix
					rule = "disc_dir_prefix_fallback_v1"
				}
			}
			if fallback != "" && fallback != "." {
				albumDecision = FieldDecision{Field: "title", Value: fallback, Source: "folder_fallback", Confidence: "low", Action: "uncertain_apply", RuleID: rule, Reason: "inferred_value", Candidates: []string{fallback}}
			}
		}
		artistDecision := chooseReleaseArtist(decisionObservations)
		albumArtistDecision := chooseValue(decisionObservations, "album_artist", func(o SourceObservation) string { return o.AlbumArtist })
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
			if optionalDecision.Value != "" {
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
		for _, o := range resolveCuePhysicalCoexistence(obs) {
			loc := locateObservation(o)
			disc := observationDiscNumber(o)
			folderDisc := 0
			if b.kind == CandidateMultiDisc {
				plan := parentPlans[b.scope]
				folderDisc = plan.positions[loc.dir]
				disc = folderDisc
				if title := plan.titles[disc]; title != "" {
					c.MediumTitles[disc] = title
				}
			} else if loc.isDiscDir && o.FieldSources["disc_number"] != "album_title" {
				folderDisc = loc.disc
			}
			if folderDisc > 0 && (o.DiscNumber < 1 || o.InferredFields["disc_number"]) {
				disc = folderDisc
				o.DiscNumber = disc
				o.FieldSources = cloneStringMap(o.FieldSources)
				o.FieldSources["disc_number"] = "folder_structure"
			}
			if disc < 1 {
				disc = 1
			}
			position := o.TrackNumber
			if position < 1 || o.InferredFields["track_number"] && o.FieldSources["track_number"] != "filename_fallback" {
				position = 0
			}
			c.Mediums[disc] = append(c.Mediums[disc], OrganizedTrack{Observation: o, Position: position, Disc: disc})
		}
		for disc := range c.Mediums {
			discardConflictingFilenamePositions(c.Mediums[disc])
			assignFallbackTrackPositions(c.Mediums[disc])
			sort.SliceStable(c.Mediums[disc], func(i, j int) bool {
				if c.Mediums[disc][i].Position != c.Mediums[disc][j].Position {
					return c.Mediums[disc][i].Position < c.Mediums[disc][j].Position
				}
				return observationSortKey(c.Mediums[disc][i].Observation) < observationSortKey(c.Mediums[disc][j].Observation)
			})
		}
		result = append(result, c)
	}
	return result
}

func assignFallbackTrackPositions(tracks []OrganizedTrack) {
	used := map[int]bool{}
	for _, track := range tracks {
		if track.Position > 0 {
			used[track.Position] = true
		}
	}
	indices := make([]int, 0)
	for index := range tracks {
		if tracks[index].Position <= 0 {
			indices = append(indices, index)
		}
	}
	sort.Slice(indices, func(i, j int) bool {
		left, right := tracks[indices[i]].Observation, tracks[indices[j]].Observation
		if left.RelativePath != right.RelativePath {
			return naturalLess(left.RelativePath, right.RelativePath)
		}
		return observationSortKey(left) < observationSortKey(right)
	})
	next := 1
	for _, index := range indices {
		for used[next] {
			next++
		}
		tracks[index].Position = next
		tracks[index].Observation.TrackNumber = next
		tracks[index].Observation.InferredFields = cloneBoolMap(tracks[index].Observation.InferredFields)
		tracks[index].Observation.InferredFields["track_number"] = true
		tracks[index].Observation.FieldSources = cloneStringMap(tracks[index].Observation.FieldSources)
		tracks[index].Observation.FieldSources["track_number"] = "path_order_fallback"
		used[next] = true
		next++
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
			if index < 0 || candidate.Decisions[index].Source != "folder" {
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
		if len(survivors) > 0 && len(survivors[0].Artwork) == 0 && !survivors[0].ArtworkDeferred {
			for _, member := range members {
				if len(member.Artwork) > 0 || member.ArtworkDeferred {
					survivors[0].Artwork = append([]byte(nil), member.Artwork...)
					survivors[0].ArtworkMIME = member.ArtworkMIME
					survivors[0].ArtworkDeferred = member.ArtworkDeferred
					survivors[0].ArtworkRelativePath = member.ArtworkRelativePath
					break
				}
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

func splitByPartition(obs []SourceObservation, knownArtists map[string]map[string]bool) map[string][]SourceObservation {
	result := map[string][]SourceObservation{}
	for _, o := range obs {
		partition := compatibleAlbumPartition(o, knownArtists)
		result[partition] = append(result[partition], o)
	}
	return result
}

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
