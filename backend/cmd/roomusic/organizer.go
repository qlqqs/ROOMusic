package main

import (
	"fmt"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// sourceObservation 是 scanner 到 organizer 的领域合同，只包含规范化元数据与安全的
// 相对来源标识。CUE offset 等解析器专属值保持为事实，由 organizer 负责归组和选择
// Release 字段。
type sourceObservation struct {
	RelativePath    string            `json:"relative_path"`
	Directory       string            `json:"directory"`
	Title           string            `json:"title,omitempty"`
	Album           string            `json:"album,omitempty"`
	AlbumArtist     string            `json:"album_artist,omitempty"`
	Artist          string            `json:"artist,omitempty"`
	SourceType      string            `json:"source_type,omitempty"`
	MediaType       string            `json:"media_type,omitempty"`
	Genre           string            `json:"genre,omitempty"`
	CatalogNumber   string            `json:"catalog_number,omitempty"`
	Year            int               `json:"year,omitempty"`
	TrackNumber     int               `json:"track_number,omitempty"`
	DiscNumber      int               `json:"disc_number,omitempty"`
	SourceKind      string            `json:"source_kind"`
	InferredFields  map[string]bool   `json:"inferred_fields,omitempty"`
	FieldSources    map[string]string `json:"field_sources,omitempty"`
	DurationSeconds float64           `json:"duration_seconds,omitempty"`
	Codec           string            `json:"codec,omitempty"`
	BitDepth        int               `json:"bit_depth,omitempty"`
	SampleRate      int               `json:"sample_rate,omitempty"`
	Channels        int               `json:"channels,omitempty"`
	Bitrate         int               `json:"bitrate,omitempty"`

	// CUE 事实只保存安全的相对引用；虚拟轨身份包含 sheet、父来源、轨号与 INDEX 01。
	CueSheetPath          string   `json:"cue_sheet_path,omitempty"`
	CueParentRelativePath string   `json:"cue_parent_relative_path,omitempty"`
	CueReferencedFile     string   `json:"cue_referenced_file,omitempty"`
	CueIndexFrames        int      `json:"cue_index_frames,omitempty"`
	CueIndexPresent       bool     `json:"cue_index_present,omitempty"`
	CueEndFrames          int      `json:"cue_end_frames,omitempty"`
	CueEndPresent         bool     `json:"cue_end_present,omitempty"`
	CueISRC               string   `json:"cue_isrc,omitempty"`
	Artwork               []byte   `json:"artwork,omitempty"`
	ArtworkMIME           string   `json:"artwork_mime,omitempty"`
	RelatedSourceRefs     []string `json:"related_source_refs,omitempty"`
}

func (o sourceObservation) fieldObservations() []fieldObservation {
	fields := []fieldObservation{
		{Name: "title", Value: o.Title},
		{Name: "artist", Value: o.Artist},
		{Name: "album", Value: o.Album},
		{Name: "album_artist", Value: o.AlbumArtist},
		{Name: "track_number", Value: strconv.Itoa(o.TrackNumber)},
		{Name: "disc_number", Value: strconv.Itoa(o.DiscNumber)},
	}
	if o.Year > 0 {
		fields = append(fields, fieldObservation{Name: "year", Value: strconv.Itoa(o.Year)})
	}
	for _, optional := range []fieldObservation{{Name: "source_type", Value: o.SourceType}, {Name: "media_type", Value: o.MediaType}, {Name: "genre", Value: o.Genre}, {Name: "catalog_number", Value: o.CatalogNumber}, {Name: "isrc", Value: o.CueISRC}} {
		if optional.Value != "" {
			fields = append(fields, optional)
		}
	}
	for i := range fields {
		fields[i].Inferred = o.InferredFields[fields[i].Name]
		fields[i].SourceKind = o.FieldSources[fields[i].Name]
		if fields[i].SourceKind == "" {
			fields[i].SourceKind = o.SourceKind
		}
		if fields[i].Inferred {
			if o.FieldSources[fields[i].Name] != "" {
				fields[i].SourceKind = o.FieldSources[fields[i].Name]
			} else {
				fields[i].SourceKind = "default_fallback"
			}
		}
	}
	return fields
}

type candidateKind string

const (
	candidateOrdinary     candidateKind = "ordinary_directory"
	candidateMultiDisc    candidateKind = "strict_multidisc"
	candidateBoxLeaf      candidateKind = "box_leaf"
	candidateSameDirSplit candidateKind = "same_dir_split"
	candidateLooseAlbum   candidateKind = "loose_album"
	candidateLooseUnknown candidateKind = "loose_unknown"
)

type candidateAnchor struct {
	Kind             candidateKind
	Scope, Partition string
}

type fieldDecision struct {
	Field, Value, Source, Confidence, Action, RuleID, Reason string
	Candidates                                               []string
}

type organizedTrack struct {
	Observation sourceObservation
	Position    int
	Disc        int
}

type organizedCandidate struct {
	Anchor                     candidateAnchor
	ReleaseGroupKey            string
	Title, Artist, AlbumArtist string
	Mediums                    map[int][]organizedTrack
	Decisions                  []fieldDecision
	Attention                  []string
	GroupingRefs               []string
	SourceType, MediaType      string
	Genre, CatalogNumber       string
}

const unknownCandidateTitle = "未知专辑"

const (
	maxDecisionCandidates   = 20
	maxGroupingEvidenceRefs = 100
)

func normalizeValue(v string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(v)), " ")
}

func normalizedPath(v string) string {
	v = pathpkg.Clean(strings.ReplaceAll(strings.TrimSpace(v), "\\", "/"))
	if v == "." {
		return ""
	}
	return strings.TrimPrefix(v, "./")
}

func stableValues(obs []sourceObservation, field func(sourceObservation) string) map[string]int {
	counts := map[string]int{}
	for _, o := range obs {
		if v := normalizeValue(field(o)); v != "" {
			counts[v]++
		}
	}
	return counts
}

func chooseValue(obs []sourceObservation, field string, get func(sourceObservation) string) fieldDecision {
	inferenceField := field
	if field == "title" {
		inferenceField = "album"
	}
	authoritative := make([]sourceObservation, 0, len(obs))
	for _, observation := range obs {
		provenanceField := decisionProvenanceField(observation, inferenceField)
		if !observation.InferredFields[provenanceField] && normalizeValue(get(observation)) != "" {
			authoritative = append(authoritative, observation)
		}
	}
	decisionObservations := obs
	if len(authoritative) > 0 {
		decisionObservations = authoritative
	}
	counts := stableValues(decisionObservations, get)
	if len(counts) == 0 {
		return fieldDecision{Field: field, RuleID: "field_missing_v1", Confidence: "low", Action: "uncertain_apply", Reason: "field_missing"}
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
		if normalizeValue(get(o)) == selected && !o.InferredFields[provenanceField] {
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
	return fieldDecision{Field: field, Value: selected, Source: source, Confidence: confidence, Action: action, RuleID: "majority_v1", Reason: reason, Candidates: values}
}

func allInferred(obs []sourceObservation, field string) bool {
	if len(obs) == 0 {
		return true
	}
	seen := false
	for _, o := range obs {
		provenanceField := decisionProvenanceField(o, field)
		if normalizeValue(fieldValue(o, provenanceField)) == "" {
			continue
		}
		seen = true
		if o.InferredFields == nil || !o.InferredFields[provenanceField] {
			return false
		}
	}
	return seen
}

func decisionProvenanceField(observation sourceObservation, field string) string {
	if field == "artist" && normalizeValue(observation.AlbumArtist) != "" {
		return "album_artist"
	}
	return field
}

func fieldValue(o sourceObservation, field string) string {
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
	case "genre":
		return o.Genre
	case "catalog_number":
		return o.CatalogNumber
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
}

func locateObservation(o sourceObservation) observationLocation {
	dir := normalizedPath(o.Directory)
	if dir == "" {
		dir = normalizedPath(filepath.ToSlash(filepath.Dir(filepath.FromSlash(o.RelativePath))))
	}
	base := strings.ToLower(filepath.Base(filepath.FromSlash(dir)))
	for _, prefix := range []string{"disc", "cd", "disk"} {
		if strings.HasPrefix(base, prefix) {
			remainder := strings.TrimLeft(strings.TrimPrefix(base, prefix), " _-.([")
			digitEnd := 0
			for digitEnd < len(remainder) && remainder[digitEnd] >= '0' && remainder[digitEnd] <= '9' {
				digitEnd++
			}
			n, err := strconv.Atoi(remainder[:digitEnd])
			if err == nil && n > 0 && strings.Trim(strings.TrimSpace(remainder[digitEnd:]), "_-.()[]") == "" {
				parent := normalizedPath(filepath.ToSlash(filepath.Dir(filepath.FromSlash(dir))))
				return observationLocation{dir: dir, parent: parent, disc: n, isDiscDir: true}
			}
		}
	}
	return observationLocation{dir: dir}
}

func albumPartition(o sourceObservation) string {
	album := normalizeValue(o.Album)
	if o.InferredFields["album"] {
		album = ""
	}
	if album == "" {
		return "|"
	}
	artist := normalizeValue(o.AlbumArtist)
	if o.InferredFields["album_artist"] {
		artist = ""
	}
	return album + "|" + artist
}

func albumEvidence(o sourceObservation) string {
	album, _, _ := strings.Cut(albumPartition(o), "|")
	return album
}

func knownAlbumArtists(observations []sourceObservation) map[string]map[string]bool {
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

func compatibleAlbumPartition(observation sourceObservation, knownArtists map[string]map[string]bool) string {
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

func chooseReleaseArtist(observations []sourceObservation) fieldDecision {
	withAlbumArtist := make([]sourceObservation, 0, len(observations))
	for _, observation := range observations {
		if normalizeValue(observation.AlbumArtist) != "" && !observation.InferredFields["album_artist"] {
			withAlbumArtist = append(withAlbumArtist, observation)
		}
	}
	if len(withAlbumArtist) > 0 {
		return chooseValue(withAlbumArtist, "artist", func(observation sourceObservation) string { return observation.AlbumArtist })
	}
	return chooseValue(observations, "artist", func(observation sourceObservation) string { return observation.Artist })
}

// organizeObservations 以确定性纯规则把 observations 归为 Release candidates；
// 数据库、文件系统和遍历顺序都不会进入规则输入。
func organizeObservations(observations []sourceObservation) []organizedCandidate {
	items := append([]sourceObservation(nil), observations...)
	sort.Slice(items, func(i, j int) bool {
		left, right := normalizedPath(items[i].RelativePath), normalizedPath(items[j].RelativePath)
		if left != right {
			return left < right
		}
		return observationSortKey(items[i]) < observationSortKey(items[j])
	})
	items = resolveCueCandidateGroups(items)

	locations := make([]observationLocation, len(items))
	parentDiscs := map[string]map[int]map[string]bool{}
	parentAlbums := map[string]map[string]int{}
	parentObservations := map[string][]sourceObservation{}
	for i, o := range items {
		locations[i] = locateObservation(o)
		if !locations[i].isDiscDir {
			continue
		}
		parent := locations[i].parent
		if parentDiscs[parent] == nil {
			parentDiscs[parent] = map[int]map[string]bool{}
		}
		if parentDiscs[parent][locations[i].disc] == nil {
			parentDiscs[parent][locations[i].disc] = map[string]bool{}
		}
		parentDiscs[parent][locations[i].disc][locations[i].dir] = true
		parentObservations[parent] = append(parentObservations[parent], o)
	}
	for parent, observations := range parentObservations {
		knownArtists := knownAlbumArtists(observations)
		for _, observation := range observations {
			if key := compatibleAlbumPartition(observation, knownArtists); key != "|" {
				if parentAlbums[parent] == nil {
					parentAlbums[parent] = map[string]int{}
				}
				parentAlbums[parent][key]++
			}
		}
	}

	strictParents := map[string]bool{}
	for parent, discs := range parentDiscs {
		if len(discs) < 2 || len(parentAlbums[parent]) > 1 {
			continue
		}
		max := 0
		for number, dirs := range discs {
			if number < 1 || len(dirs) != 1 {
				max = -1
				break
			}
			if number > max {
				max = number
			}
		}
		if max < 2 {
			continue
		}
		contiguous := true
		for number := 1; number <= max; number++ {
			if len(discs[number]) != 1 {
				contiguous = false
				break
			}
		}
		if contiguous {
			strictParents[parent] = true
		}
	}

	type bucket struct {
		kind      candidateKind
		scope     string
		partition string
		obs       []sourceObservation
	}
	buckets := map[string]*bucket{}
	addBucket := func(kind candidateKind, scope, partition string, observations ...sourceObservation) {
		key := string(kind) + "\x00" + scope + "\x00" + partition
		if buckets[key] == nil {
			buckets[key] = &bucket{kind: kind, scope: scope, partition: partition}
		}
		buckets[key].obs = append(buckets[key].obs, observations...)
	}

	// 先建立目录与 Box 边界，再在边界内应用 album 多数决；严格多碟父目录是唯一允许的
	// 跨目录合并。
	baseGroups := map[string][]sourceObservation{}
	baseMeta := map[string]struct {
		kind  candidateKind
		scope string
	}{}
	for i, o := range items {
		loc := locations[i]
		kind, scope := candidateOrdinary, loc.dir
		if loc.isDiscDir {
			if strictParents[loc.parent] {
				kind, scope = candidateMultiDisc, loc.parent
			} else {
				kind, scope = candidateBoxLeaf, loc.dir
			}
		}
		key := string(kind) + "\x00" + scope
		baseGroups[key] = append(baseGroups[key], o)
		baseMeta[key] = struct {
			kind  candidateKind
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
		if meta.kind == candidateMultiDisc {
			addBucket(meta.kind, meta.scope, "", group...)
			continue
		}
		// 根目录文件没有可信的专辑文件夹身份：存在 album tag 时可以归组，否则每个文件
		// 都是独立的未知专辑候选。
		if meta.scope == "" {
			withAlbum := map[string][]sourceObservation{}
			knownArtists := knownAlbumArtists(group)
			for _, o := range group {
				if part := compatibleAlbumPartition(o, knownArtists); part != "|" {
					withAlbum[part] = append(withAlbum[part], o)
				} else {
					addBucket(candidateLooseUnknown, "", normalizedPath(o.RelativePath), o)
				}
			}
			parts := make([]string, 0, len(withAlbum))
			for part := range withAlbum {
				parts = append(parts, part)
			}
			sort.Strings(parts)
			for _, part := range parts {
				addBucket(candidateLooseAlbum, "", part, withAlbum[part]...)
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
		if len(partCounts) <= 1 || (winner != "" && winnerCount*2 > authoritativeCount) {
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
					addBucket(candidateSameDirSplit, meta.scope, "|@"+normalizedPath(unknown.RelativePath), unknown)
				}
				continue
			}
			addBucket(candidateSameDirSplit, meta.scope, part, obs...)
		}
	}

	keys := make([]string, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]organizedCandidate, 0, len(keys))
	for _, key := range keys {
		b := buckets[key]
		obs := b.obs
		sort.Slice(obs, func(i, j int) bool { return observationSortKey(obs[i]) < observationSortKey(obs[j]) })
		c := organizedCandidate{Anchor: candidateAnchor{Kind: b.kind, Scope: b.scope, Partition: stableAnchorPartition(b.kind, b.partition, obs)}, ReleaseGroupKey: key, Mediums: map[int][]organizedTrack{}}
		albumDecision := chooseValue(obs, "title", func(o sourceObservation) string { return o.Album })
		if b.kind == candidateMultiDisc && !hasAuthoritativeAlbum(obs) {
			if fallback := normalizeValue(filepath.Base(filepath.FromSlash(b.scope))); fallback != "" && fallback != "." {
				albumDecision = fieldDecision{Field: "title", Value: fallback, Source: "folder_fallback", Confidence: "low", Action: "uncertain_apply", RuleID: "multidisc_parent_fallback_v1", Reason: "inferred_value", Candidates: []string{fallback}}
			}
		}
		artistDecision := chooseReleaseArtist(obs)
		albumArtistDecision := chooseValue(obs, "album_artist", func(o sourceObservation) string { return o.AlbumArtist })
		yearDecision := chooseValue(obs, "year", func(o sourceObservation) string { return fieldValue(o, "year") })
		sourceDecision := chooseValue(obs, "source_type", func(o sourceObservation) string { return o.SourceType })
		mediaDecision := chooseValue(obs, "media_type", func(o sourceObservation) string { return o.MediaType })
		genreDecision := chooseValue(obs, "genre", func(o sourceObservation) string { return o.Genre })
		catalogDecision := chooseValue(obs, "catalog_number", func(o sourceObservation) string { return o.CatalogNumber })
		c.Decisions = []fieldDecision{albumDecision, artistDecision}
		for _, optionalDecision := range []fieldDecision{albumArtistDecision, yearDecision, sourceDecision, mediaDecision, genreDecision, catalogDecision} {
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
		c.Genre = genreDecision.Value
		c.CatalogNumber = catalogDecision.Value
		// Attention 只保存独立的归组问题；字段不确定性已由 release_field_decisions 表达，
		// 不应再写入 grouping reason 导致 REST 重复计数。
		if b.kind == candidateSameDirSplit {
			c.Attention = append(c.Attention, "same_directory_conflict")
		}
		c.GroupingRefs = observationRefs(obs)
		for _, o := range resolveCuePhysicalCoexistence(obs) {
			loc := locateObservation(o)
			disc := o.DiscNumber
			if loc.isDiscDir && (disc < 1 || o.InferredFields["disc_number"]) {
				disc = loc.disc
				o.DiscNumber = disc
				if o.FieldSources == nil {
					o.FieldSources = map[string]string{}
				} else {
					o.FieldSources = cloneStringMap(o.FieldSources)
				}
				o.FieldSources["disc_number"] = "folder_structure"
			}
			if disc < 1 {
				disc = 1
			}
			position := o.TrackNumber
			if position < 1 || o.InferredFields["track_number"] {
				position = 0
			}
			c.Mediums[disc] = append(c.Mediums[disc], organizedTrack{Observation: o, Position: position, Disc: disc})
		}
		for disc := range c.Mediums {
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

func assignFallbackTrackPositions(tracks []organizedTrack) {
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
		return observationSortKey(tracks[indices[i]].Observation) < observationSortKey(tracks[indices[j]].Observation)
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

func hasAuthoritativeAlbum(observations []sourceObservation) bool {
	for _, observation := range observations {
		if normalizeValue(observation.Album) != "" && !observation.InferredFields["album"] {
			return true
		}
	}
	return false
}

func observationRefs(observations []sourceObservation) []string {
	seen := map[string]bool{}
	refs := make([]string, 0, len(observations))
	for _, observation := range observations {
		candidates := append([]string{observation.RelativePath}, observation.RelatedSourceRefs...)
		for _, ref := range candidates {
			ref = normalizedPath(ref)
			if ref == "" || seen[ref] {
				continue
			}
			seen[ref] = true
			refs = append(refs, ref)
		}
	}
	sort.Strings(refs)
	if len(refs) > maxGroupingEvidenceRefs {
		refs = refs[:maxGroupingEvidenceRefs]
	}
	return refs
}

func applyRipLogEvidence(candidate *organizedCandidate, sourceRefs []string) {
	if candidate == nil || len(sourceRefs) == 0 {
		return
	}
	candidate.GroupingRefs = mergeEvidenceRefs(sourceRefs, candidate.GroupingRefs)
	decision := func(field string) fieldDecision {
		return fieldDecision{Field: field, Value: "CD", Source: "rip_log", Confidence: "high", Action: "auto_apply", RuleID: "rip_log_cd_v1", Candidates: []string{"CD"}}
	}
	if candidate.SourceType == "" {
		candidate.SourceType = "CD"
		candidate.Decisions = append(candidate.Decisions, decision("source_type"))
	}
	if candidate.MediaType == "" {
		candidate.MediaType = "CD"
		candidate.Decisions = append(candidate.Decisions, decision("media_type"))
	}
}

func mergeEvidenceRefs(groups ...[]string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, maxGroupingEvidenceRefs)
	for _, group := range groups {
		for _, value := range group {
			value = normalizedPath(value)
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			result = append(result, value)
			if len(result) == maxGroupingEvidenceRefs {
				return result
			}
		}
	}
	return result
}

func resolveCueCandidateGroups(observations []sourceObservation) []sourceObservation {
	type cueGroup struct {
		directory string
		partition string
		cues      []sourceObservation
	}
	groups := map[string]*cueGroup{}
	for _, observation := range observations {
		if !isCueObservation(observation) {
			continue
		}
		key := normalizedPath(observation.Directory) + "\x00" + albumPartition(observation)
		if groups[key] == nil {
			groups[key] = &cueGroup{directory: normalizedPath(observation.Directory), partition: albumPartition(observation)}
		}
		groups[key].cues = append(groups[key].cues, observation)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	processed := map[string]bool{}
	result := make([]sourceObservation, 0, len(observations))
	for _, key := range keys {
		group := groups[key]
		members := append([]sourceObservation(nil), group.cues...)
		parents := map[string]bool{}
		for _, cue := range group.cues {
			parents[cueParentPath(cue)] = true
		}
		for _, observation := range observations {
			if isCueObservation(observation) {
				continue
			}
			path := normalizedPath(observation.RelativePath)
			if parents[path] {
				members = append(members, observation)
				continue
			}
			if normalizedPath(observation.Directory) == group.directory && group.partition != "|" && albumPartition(observation) == group.partition {
				members = append(members, observation)
			}
		}
		refs := observationRefs(members)
		for _, member := range members {
			processed[normalizedPath(member.RelativePath)] = true
		}
		survivors := resolveCuePhysicalCoexistence(members)
		for index := range survivors {
			if !isCueObservation(survivors[index]) {
				survivors[index] = mergeSingleCueEvidence(survivors[index], group.cues)
			}
		}
		if len(survivors) > 0 && len(survivors[0].Artwork) == 0 {
			for _, member := range members {
				if len(member.Artwork) > 0 {
					survivors[0].Artwork = append([]byte(nil), member.Artwork...)
					survivors[0].ArtworkMIME = member.ArtworkMIME
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
		if !processed[normalizedPath(observation.RelativePath)] {
			result = append(result, observation)
		}
	}
	sort.Slice(result, func(i, j int) bool { return observationSortKey(result[i]) < observationSortKey(result[j]) })
	return result
}

func mergeSingleCueEvidence(physical sourceObservation, cues []sourceObservation) sourceObservation {
	matching := make([]sourceObservation, 0, 1)
	for _, cue := range cues {
		if cueParentPath(cue) == normalizedPath(physical.RelativePath) {
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
	mergeText := func(field string, target *string, value string) {
		if value == "" || (*target != "" && !physical.InferredFields[field]) {
			return
		}
		*target = value
		delete(physical.InferredFields, field)
		if source := cue.FieldSources[field]; source != "" {
			physical.FieldSources[field] = source
		} else {
			physical.FieldSources[field] = "cue_sheet"
		}
	}
	mergeText("title", &physical.Title, cue.Title)
	mergeText("artist", &physical.Artist, cue.Artist)
	mergeText("album", &physical.Album, cue.Album)
	mergeText("album_artist", &physical.AlbumArtist, cue.AlbumArtist)
	mergeText("genre", &physical.Genre, cue.Genre)
	mergeText("catalog_number", &physical.CatalogNumber, cue.CatalogNumber)
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

func observationSortKey(o sourceObservation) string {
	return normalizedPath(o.RelativePath) + "\x00" + normalizeValue(o.Title) + "\x00" + normalizeValue(o.Album) + "\x00" + normalizeValue(o.Artist)
}

func splitByPartition(obs []sourceObservation, knownArtists map[string]map[string]bool) map[string][]sourceObservation {
	result := map[string][]sourceObservation{}
	for _, o := range obs {
		partition := compatibleAlbumPartition(o, knownArtists)
		result[partition] = append(result[partition], o)
	}
	return result
}

func stableAnchorPartition(kind candidateKind, groupingPartition string, obs []sourceObservation) string {
	if kind == candidateOrdinary || kind == candidateMultiDisc || kind == candidateBoxLeaf {
		return ""
	}
	paths := make([]string, 0, len(obs))
	for _, observation := range obs {
		paths = append(paths, normalizedPath(observation.RelativePath))
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
func resolveCuePhysicalCoexistence(obs []sourceObservation) []sourceObservation {
	physicalByPath := map[string]bool{}
	virtualCountByParent := map[string]int{}
	for _, o := range obs {
		if isCueObservation(o) {
			if parent := cueParentPath(o); parent != "" {
				virtualCountByParent[parent]++
			}
			continue
		}
		physicalByPath[normalizedPath(o.RelativePath)] = true
	}
	result := make([]sourceObservation, 0, len(obs))
	for _, o := range obs {
		if isCueObservation(o) {
			parent := cueParentPath(o)
			if physicalByPath[parent] && virtualCountByParent[parent] <= 1 {
				continue
			}
		} else if virtualCountByParent[normalizedPath(o.RelativePath)] > 1 {
			continue
		}
		result = append(result, o)
	}
	return result
}

func isCueObservation(observation sourceObservation) bool {
	return strings.EqualFold(observation.SourceKind, "cue_virtual") || strings.Contains(strings.ToLower(observation.SourceKind), "cue")
}

func cueParentPath(observation sourceObservation) string {
	if parent := normalizedPath(observation.CueParentRelativePath); parent != "" {
		return parent
	}
	return normalizedPath(observation.CueReferencedFile)
}

func refineDirectoryKind(obs []sourceObservation, scope string) (candidateKind, string) {
	// 保留此兼容 helper，供上一纵向切片的调用方过渡。
	if len(obs) == 0 {
		return candidateOrdinary, scope
	}
	loc := locateObservation(obs[0])
	if loc.isDiscDir {
		return candidateBoxLeaf, scope
	}
	return candidateOrdinary, scope
}

func candidateIdentity(root string, a candidateAnchor) string {
	// Partition 只使用安全的相对来源区分符，不包含展示标题。
	return fmt.Sprintf("%s:v2:%s:%s:%s", normalizeValue(root), a.Kind, normalizedPath(a.Scope), normalizedPath(a.Partition))
}

func decisionValue(d fieldDecision) string {
	if d.Value != "" {
		return d.Value
	}
	return strconv.Itoa(len(d.Candidates))
}
