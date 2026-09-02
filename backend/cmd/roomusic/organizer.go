package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// sourceObservation is the scanner-to-organizer contract. It intentionally
// contains only normalized metadata and safe relative source identifiers.
type sourceObservation struct {
	RelativePath                      string
	Directory                         string
	Title, Album, AlbumArtist, Artist string
	TrackNumber, DiscNumber           int
	SourceKind                        string
	InferredFields                    map[string]bool
	DurationSeconds                   float64
	Codec                             string
	SampleRate, Channels, Bitrate     int
}

func (o sourceObservation) fieldObservations() []fieldObservation {
	fields := []fieldObservation{{Name: "title", Value: o.Title}, {Name: "artist", Value: o.Artist}, {Name: "album", Value: o.Album}, {Name: "track_number", Value: strconv.Itoa(o.TrackNumber)}, {Name: "disc_number", Value: strconv.Itoa(o.DiscNumber)}}
	for i := range fields {
		fields[i].Inferred = o.InferredFields[fields[i].Name]
		fields[i].SourceKind = o.SourceKind
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
}

func normalizeValue(v string) string { return strings.Join(strings.Fields(strings.TrimSpace(v)), " ") }
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
	counts := stableValues(obs, get)
	if len(counts) == 0 {
		return fieldDecision{Field: field, RuleID: "field_missing", Confidence: "low", Action: "uncertain_apply"}
	}
	vals := make([]string, 0, len(counts))
	for v := range counts {
		vals = append(vals, v)
	}
	sort.Strings(vals)
	sort.SliceStable(vals, func(i, j int) bool {
		if counts[vals[i]] != counts[vals[j]] {
			return counts[vals[i]] > counts[vals[j]]
		}
		return vals[i] < vals[j]
	})
	selected := vals[0]
	confidence, action := "high", "auto_apply"
	reason := ""
	if len(vals) > 1 {
		confidence, action, reason = "medium", "uncertain_apply", "inconsistent_candidates"
	}
	source := "tag"
	if allInferred(obs, field) {
		source = "filename_fallback"
		confidence = "low"
		action = "uncertain_apply"
		reason = "inferred_value"
	}
	return fieldDecision{Field: field, Value: selected, Source: source, Confidence: confidence, Action: action, RuleID: "majority_v1", Reason: reason, Candidates: vals}
}
func allInferred(obs []sourceObservation, field string) bool {
	if len(obs) == 0 {
		return true
	}
	for _, o := range obs {
		if o.InferredFields == nil || !o.InferredFields[field] {
			return false
		}
	}
	return true
}

// organizeObservations deterministically groups observations into Release candidates.
func organizeObservations(observations []sourceObservation) []organizedCandidate {
	items := append([]sourceObservation(nil), observations...)
	sort.Slice(items, func(i, j int) bool { return items[i].RelativePath < items[j].RelativePath })
	groups := map[string][]sourceObservation{}
	for _, o := range items {
		dir := o.Directory
		if dir == "" {
			dir = filepath.ToSlash(filepath.Dir(o.RelativePath))
			if dir == "." {
				dir = ""
			}
		}
		base := strings.ToLower(filepath.Base(filepath.FromSlash(dir)))
		discLayout := false
		if strings.HasPrefix(base, "disc ") || strings.HasPrefix(base, "cd ") || strings.HasPrefix(base, "disk ") {
			discLayout = true
			parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(dir)))
			if parent == "." {
				parent = ""
			}
			dir = parent
		}
		album := normalizeValue(o.Album)
		kind := candidateOrdinary
		partition := ""
		if dir == "" && !discLayout {
			if album != "" {
				kind = candidateLooseAlbum
				partition = album + "|" + normalizeValue(o.AlbumArtist)
			} else {
				kind = candidateLooseUnknown
				partition = o.RelativePath
			}
		}
		key := string(kind) + "|" + dir + "|" + partition
		groups[key] = append(groups[key], o)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	result := make([]organizedCandidate, 0, len(keys))
	for _, k := range keys {
		obs := groups[k]
		parts := strings.SplitN(k, "|", 3)
		kind := candidateKind(parts[0])
		scope := parts[1]
		partition := ""
		if len(parts) > 2 {
			partition = parts[2]
		}
		if kind == candidateOrdinary {
			kind, scope = refineDirectoryKind(obs, scope)
		}
		c := organizedCandidate{Anchor: candidateAnchor{Kind: kind, Scope: scope, Partition: partition}, ReleaseGroupKey: k, Mediums: map[int][]organizedTrack{}}
		c.Decisions = []fieldDecision{chooseValue(obs, "title", func(o sourceObservation) string { return o.Album }), chooseValue(obs, "artist", func(o sourceObservation) string {
			if o.AlbumArtist != "" {
				return o.AlbumArtist
			}
			return o.Artist
		})}
		for _, d := range c.Decisions {
			if d.Action == "uncertain_apply" {
				c.Attention = append(c.Attention, d.Field+":"+d.Reason)
			}
			if d.Field == "title" {
				c.Title = d.Value
			}
			if d.Field == "artist" {
				c.Artist = d.Value
			}
		}
		for _, o := range obs {
			disc := o.DiscNumber
			if disc < 1 {
				disc = 1
			}
			pos := o.TrackNumber
			if pos < 1 {
				pos = 1
			}
			c.Mediums[disc] = append(c.Mediums[disc], organizedTrack{Observation: o, Position: pos, Disc: disc})
		}
		for disc := range c.Mediums {
			sort.SliceStable(c.Mediums[disc], func(i, j int) bool {
				return c.Mediums[disc][i].Position < c.Mediums[disc][j].Position || (c.Mediums[disc][i].Position == c.Mediums[disc][j].Position && c.Mediums[disc][i].Observation.RelativePath < c.Mediums[disc][j].Observation.RelativePath)
			})
		}
		result = append(result, c)
	}
	return result
}
func refineDirectoryKind(obs []sourceObservation, scope string) (candidateKind, string) {
	set := map[string]bool{}
	discs := map[int]bool{}
	for _, o := range obs {
		if a := normalizeValue(o.Album); a != "" {
			set[a] = true
		}
		if o.DiscNumber > 0 {
			discs[o.DiscNumber] = true
		}
	}
	if len(set) > 1 {
		return candidateSameDirSplit, scope
	}
	if len(discs) > 1 {
		ok := true
		for i := 1; i <= len(discs); i++ {
			if !discs[i] {
				ok = false
			}
		}
		if ok {
			return candidateMultiDisc, scope
		}
	}
	base := strings.ToLower(filepath.Base(filepath.FromSlash(scope)))
	if strings.Contains(base, "disc ") || strings.Contains(base, "cd ") {
		return candidateBoxLeaf, scope
	}
	return candidateOrdinary, scope
}

func candidateIdentity(root string, a candidateAnchor) string {
	return fmt.Sprintf("%s:v1:%s:%s:%s", root, a.Kind, a.Scope, a.Partition)
}
func decisionValue(d fieldDecision) string {
	if d.Value != "" {
		return d.Value
	}
	return strconv.Itoa(len(d.Candidates))
}
