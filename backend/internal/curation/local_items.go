package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

const LocalRuleVersion int16 = 4

type localCredit struct {
	Target    string `json:"target_id"`
	Role      string `json:"role"`
	Ref       string `json:"source_ref"`
	Raw       string `json:"raw"`
	Requested bool   `json:"requested,omitempty"`
}
type localTrack struct {
	catalog.LocalFile
	ID          string              `json:"track_id"`
	FileID      string              `json:"file_id"`
	Path        string              `json:"path"`
	Root        string              `json:"root_id"`
	Cue         string              `json:"cue,omitempty"`
	Title       string              `json:"title"`
	Artist      string              `json:"artist"`
	Fields      []string            `json:"fields,omitempty"`
	Disc        int                 `json:"disc"`
	Track       int                 `json:"track"`
	CueStart    *int                `json:"cue_start,omitempty"`
	CueEnd      *int                `json:"cue_end,omitempty"`
	Seconds     int                 `json:"seconds"`
	Identifiers map[string][]string `json:"identifiers,omitempty"`
}
type localRelease struct {
	displayTitle   string
	protection     []string
	stewardGrouped bool
	ID             string          `json:"release_id"`
	Title          string          `json:"title"`
	Artist         string          `json:"artist"`
	Kind           string          `json:"scan_grouping"`
	Fields         []string        `json:"fields,omitempty"`
	Encodings      []string        `json:"encodings,omitempty"`
	Credits        []localCredit   `json:"credits,omitempty"`
	Tracks         []localTrack    `json:"tracks"`
	Reasons        []string        `json:"reasons,omitempty"`
	Protected      bool            `json:"protected_structure,omitempty"`
	Conflicts      json.RawMessage `json:"conflicts,omitempty"`
}
type localAlbum struct {
	DirectoryKey string         `json:"directory_key"`
	ReleaseIDs   []string       `json:"release_ids"`
	Directories  []string       `json:"directories"`
	Releases     []localRelease `json:"releases"`
	Group        bool           `json:"grouping_requested,omitempty"`
}

func appendOnce[T comparable](xs []T, x T) []T {
	if !slices.Contains(xs, x) {
		return append(xs, x)
	}
	return xs
}
func plainLocalField(e catalog.CurationEvidence, field string) bool {
	return !e.Corrected[field] && (!e.Automatic[field] || field == "title" && e.LocalTitle != "" || field == "album_artist" && e.LocalArtist != "")
}
func noisyLocalName(value string) bool {
	return strings.ContainsAny(value, "[]【】") || strings.IndexFunc(value, unicode.IsLetter) < 0
}
func makeLocalRelease(e catalog.CurationEvidence, files map[string]catalog.LocalFile, extra []localCredit) localRelease {
	artist := e.AlbumArtist
	if artist == "" {
		artist = e.Artist
	}
	r := localRelease{ID: e.ID, Title: e.Title, Artist: artist, Kind: e.CandidateKind, Protected: e.ExternalID != "" || e.ReleaseGroupMatchID != ""}
	titleOK := plainLocalField(e, "title")
	artistOK := plainLocalField(e, "album_artist") && !e.Compilation && !CompilationEvidence(e)
	if e.Corrected["title"] {
		r.Title = ""
	}
	if e.Corrected["album_artist"] {
		r.Artist = ""
	}
	compound := false
	if left, right, ok := strings.Cut(r.Title, " - "); ok {
		compound = e.Authenticity["title"] == metadata.Inferred || textnorm.Text(left) == textnorm.Text(artist) || textnorm.Text(right) == textnorm.Text(artist)
	}
	if compound && titleOK {
		r.Reasons = append(r.Reasons, "compound_title")
		r.Fields = appendOnce(r.Fields, "title")
	}
	missingArtist := e.SelectedArtistAuth() == metadata.Placeholder || artist == "" || e.SelectedArtistAuth() == metadata.Inferred && noisyLocalName(artist)
	hasName := !e.TitlePlaceholder && e.Title != "" || slices.ContainsFunc(e.Tracks, func(t catalog.CurationTrack) bool { return t.Title != "" && !t.TitlePlaceholder })
	if artistOK && hasName && missingArtist && !e.HasSearchID() {
		r.Fields = appendOnce(r.Fields, "album_artist")
		r.Reasons = appendOnce(r.Reasons, "missing_artist")
	}
	if titleOK && (e.TitlePlaceholder || e.Authenticity["title"] == metadata.Inferred && noisyLocalName(e.Title)) {
		r.Fields = appendOnce(r.Fields, "title")
	}
	positions := map[[2]int]bool{}
	repeated := false
	unknownDisc := false
	var choices []string
	occurred := false
	for _, t := range e.Tracks {
		f := files[t.ID]
		tr := localTrack{LocalFile: f, ID: t.ID, FileID: operations.Fingerprint([]string{t.SourceRootID, t.RelativePath}), Path: t.RelativePath, Root: t.SourceRootID, Cue: t.CueSheetPath, CueStart: t.CueIndexFrames, CueEnd: t.CueEndFrames, Title: t.Title, Artist: t.Artist, Disc: t.Disc, Track: t.Position, Seconds: t.DurationSeconds, Identifiers: t.Identifiers}
		if t.TitleCorrected {
			tr.Title = ""
		}
		if t.ArtistCorrected {
			tr.Artist = ""
		}
		titleWritable := !t.TitleCorrected && (!t.TitleAutomatic || t.LocalTitle != "")
		artistWritable := !t.ArtistCorrected && (!t.ArtistAutomatic || t.LocalArtist != "")
		noTags := t.TitleObservation.Source == "filename_fallback" && t.ArtistObservation.Inferred
		if noTags {
			r.Reasons = appendOnce(r.Reasons, "filename_only")
		}
		if titleWritable && (noTags || t.TitlePlaceholder || t.TitleInferred && noisyLocalName(t.Title) || t.TitleObservation.Source == "garbled") {
			tr.Fields = append(tr.Fields, "title")
		}
		if artistWritable && (noTags || t.ArtistObservation.Source == "garbled" || t.ArtistPlaceholder) {
			tr.Fields = append(tr.Fields, "artist")
		}
		if t.Position == 0 {
			r.Reasons = appendOnce(r.Reasons, "missing_position")
		}
		pair := [2]int{t.Disc, t.Position}
		repeated = repeated || t.Position > 0 && positions[pair]
		positions[pair] = true
		disc := f.Observations["disc_number"]
		noDisc := disc.Value == "" || disc.Source == "default_fallback"
		unknownDisc = unknownDisc || noDisc
		if noDisc && (f.ScanTags.DiscTotal > 1 || len(e.Media) > 1) {
			r.Reasons = appendOnce(r.Reasons, "missing_disc")
		}
		hasLegacy := false
		for _, field := range []string{"album", "title", "album_artist", "artist"} {
			hasLegacy = hasLegacy || f.Observations[field].Source == "garbled"
		}
		if hasLegacy {
			if !occurred {
				choices = slices.Sorted(maps.Keys(f.Readings))
				occurred = true
			} else {
				choices = slices.DeleteFunc(choices, func(c string) bool { return f.Readings[c] == nil })
			}
		}
		for _, credit := range f.Credits {
			if credit.Role == "track_artist" && f.Observations["artist"].Source == "garbled" || credit.Role == "album_artist" && f.Observations["album_artist"].Source == "garbled" {
				continue
			}
			ref := t.ID + "/" + credit.TagKey + "/" + strconv.Itoa(credit.OccurrenceIndex)
			add := func(target, role string) {
				c := localCredit{Target: target, Role: role, Ref: ref, Raw: credit.RawValue}
				c.Requested = slices.ContainsFunc(extra, func(x localCredit) bool {
					return x.Target == target && x.Role == role && x.Ref == ref && x.Raw == c.Raw
				})
				if c.Requested || !r.Protected && strings.ContainsAny(c.Raw, "&,，＆") {
					r.Credits = appendOnce(r.Credits, c)
					r.Reasons = appendOnce(r.Reasons, "artist_split")
				}
			}
			if credit.Role == "track_artist" {
				if artistWritable {
					add(t.ID, credit.Role)
				}
				if artistOK && credit.RawValue == e.Artist && (e.AlbumArtist == "" || e.FieldSources["album_artist"] == "artist_distribution" || slices.ContainsFunc(extra, func(c localCredit) bool { return c.Role == "release_artist" && c.Ref == ref && c.Target == e.ID })) {
					add(e.ID, "release_artist")
				}
			} else if credit.Role == "album_artist" && artistOK {
				add(e.ID, credit.Role)
			}
		}
		r.Tracks = append(r.Tracks, tr)
	}
	if len(choices) > 1 {
		r.Encodings = choices
		r.Reasons = append(r.Reasons, "legacy_encoding")
		if titleOK && e.FieldSources["title"] == "garbled" {
			r.Fields = appendOnce(r.Fields, "title")
		}
		if artistOK && (e.FieldSources["album_artist"] == "garbled" || e.FieldSources["artist"] == "garbled") {
			r.Fields = appendOnce(r.Fields, "album_artist")
		}
	}
	if repeated && unknownDisc {
		r.Reasons = appendOnce(r.Reasons, "missing_disc")
	}
	// 已确认匹配的确定性拆分可能引用发行采用的组合署名或旧扫描的曲目原文。
	for _, c := range extra {
		if c.Ref != c.Target+"/scan/"+c.Role {
			continue
		}
		found := false
		switch c.Role {
		case "release_artist":
			found = c.Target == e.ID && artistOK && c.Raw == e.Artist && e.FieldSources["artist"] != "garbled"
		case "album_artist":
			found = c.Target == e.ID && artistOK && c.Raw == e.AlbumArtist && e.FieldSources["album_artist"] != "garbled"
		case "track_artist":
			found = slices.ContainsFunc(e.Tracks, func(t catalog.CurationTrack) bool {
				return t.ID == c.Target && !t.ArtistCorrected && (!t.ArtistAutomatic || t.LocalArtist != "") && t.Artist == c.Raw && t.ArtistObservation.Source != "garbled"
			})
		}
		if found && c.Raw != "" {
			r.Credits = appendOnce(r.Credits, c)
			r.Reasons = appendOnce(r.Reasons, "artist_split")
		}
	}
	if r.Protected {
		r.Fields = nil
		r.Encodings = nil
		r.Reasons = nil
		for i := range r.Tracks {
			r.Tracks[i].Fields = nil
		}
		if len(r.Credits) > 0 {
			r.Reasons = []string{"artist_split"}
		}
	}
	return r
}
func directoryKeys(r localRelease) []string {
	var dirs []string
	for _, t := range r.Tracks {
		dirs = appendOnce(dirs, path.Dir(t.Path))
	}
	slices.Sort(dirs)
	return dirs
}

// 以目录、父目录内专辑标签和从 1 开始的共同艺人建索引，一次连通所有有关发行。
func localScopeKeys(r localRelease) []string {
	if len(r.Tracks) == 0 {
		return nil
	}
	root := r.Tracks[0].Root
	dirs := directoryKeys(r)
	var keys []string
	for _, dir := range dirs {
		if dir != "." {
			keys = append(keys, "dir/"+root+"/"+dir)
		}
	}
	if len(dirs) != 1 || path.Dir(dirs[0]) == "." {
		return keys
	}
	prefix := root + "/" + path.Dir(dirs[0]) + "/"
	for _, t := range r.Tracks {
		tag := t.Observations["album"]
		if !tag.Inferred && tag.Value != "" && tag.Source != "garbled" {
			keys = appendOnce(keys, "album/"+prefix+textnorm.MergeKey(tag.Value))
		}
		if t.Track == 1 && r.Artist != "" && !metadata.IsCompilationPlaceholder(r.Artist) && metadata.FieldAuthenticity("artist", r.Artist, "", false, false) != metadata.Placeholder {
			keys = appendOnce(keys, "artist/"+prefix+textnorm.Text(r.Artist))
		}
	}
	return keys
}
func localScopes(ctx context.Context, tx *sql.Tx, ids []string, entries map[string]catalog.CurationEvidence, request *localCredit) ([]localAlbum, error) {
	return loadLocalScopes(ctx, tx, ids, entries, request, false)
}

func loadLocalScopes(ctx context.Context, tx *sql.Tx, ids []string, entries map[string]catalog.CurationEvidence, request *localCredit, explicit bool) ([]localAlbum, error) {
	peers, err := catalog.LocalDirectoryPeers(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	missing := []string{}
	for _, id := range peers {
		if _, ok := entries[id]; !ok {
			missing = append(missing, id)
		}
	}
	full, err := catalog.LoadCurationEvidence(ctx, tx, missing)
	if err != nil {
		return nil, err
	}
	maps.Copy(full, entries)
	files, err := catalog.LoadLocalFiles(ctx, tx, peers)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id::text, EXISTS(SELECT FROM release_merges WHERE source_release_id=r.id OR target_release_id=r.id) OR EXISTS(SELECT FROM operations o WHERE o.target_id=r.id AND o.operation_type IN ('release_merge','release_group_change') AND ((o.origin->>'source') IS DISTINCT FROM 'curation' OR o.origin->>'review_decision'='human') AND NOT EXISTS(SELECT FROM operations undo WHERE undo.reverts_operation_id=o.id)),
 EXISTS(SELECT FROM operations op WHERE op.operation_type='local_model_grouping'
 AND (op.origin->>'source') IS DISTINCT FROM 'curation' AND NOT EXISTS(SELECT FROM operations undo WHERE undo.reverts_operation_id=op.id)
 AND (op.target_id=r.id OR EXISTS(SELECT FROM local_model_groupings g CROSS JOIN LATERAL jsonb_array_elements(g.assignments) f
 JOIN tracks t ON t.source_root_id=g.root_id AND t.relative_path=f->>'path' JOIN media m ON m.id=t.medium_id
 WHERE g.operation_id=op.id AND t.source_status='present' AND m.release_id=r.id))),
 (SELECT jsonb_object_agg(field_key,candidates) FROM release_field_decisions WHERE release_id=r.id AND reason='tied_candidates'),
 (SELECT jsonb_agg(DISTINCT c) FROM curation_pending_items p CROSS JOIN LATERAL jsonb_array_elements(p.evidence->'releases') rel
 CROSS JOIN LATERAL jsonb_array_elements(rel->'credits') c WHERE p.kind='local_cleanup' AND (p.evidence->'release_ids') ? r.id::text AND rel->>'release_id'=r.id::text AND c->>'requested'='true')
 FROM releases r WHERE id=ANY($1::uuid[]) ORDER BY id`, peers)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var releases []localRelease
	for rows.Next() {
		var id string
		var protected, grouped bool
		var conflicts, requests []byte
		if err := rows.Scan(&id, &protected, &grouped, &conflicts, &requests); err != nil {
			return nil, err
		}
		var extra []localCredit
		if len(requests) > 0 {
			if err := json.Unmarshal(requests, &extra); err != nil {
				return nil, err
			}
		}
		if request != nil {
			extra = append(extra, *request)
		}
		r := makeLocalRelease(full[id], files, extra)
		r.displayTitle = full[id].Title
		if full[id].AutomaticTitle != "" {
			r.displayTitle = full[id].AutomaticTitle
		}
		if explicit && slices.ContainsFunc(full[id].Tracks, func(t catalog.CurationTrack) bool { return t.ExternalID != "" }) {
			r.Protected = true
		}
		if r.Protected {
			r.protection = append(r.protection, "已有外部对应")
		}
		if protected {
			r.protection = append(r.protection, "人工合并或调整专辑组")
		}
		if grouped {
			r.protection = append(r.protection, "管家已人工重新归组")
			r.stewardGrouped = true
			r.Reasons, r.Fields, r.Encodings, r.Credits = nil, nil, nil, nil
			for i := range r.Tracks {
				r.Tracks[i].Fields = nil
			}
		}
		r.Protected = r.Protected || protected || grouped
		r.Conflicts = conflicts
		releases = append(releases, r)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	var scopes []localAlbum
	seen := make([]bool, len(releases))
	keys := make([][]string, len(releases))
	byKey := map[string][]int{}
	for i, r := range releases {
		keys[i] = localScopeKeys(r)
		for _, key := range keys[i] {
			byKey[key] = append(byKey[key], i)
		}
	}
	for i := range releases {
		if seen[i] {
			continue
		}
		members := []int{i}
		seen[i] = true
		for n := 0; n < len(members); n++ {
			for _, key := range keys[members[n]] {
				for _, j := range byKey[key] {
					if !seen[j] {
						members = append(members, j)
						seen[j] = true
					}
				}
				delete(byKey, key)
			}
		}
		slices.Sort(members)
		var a localAlbum
		included, needed := false, false
		var source []string
		tags := map[string]bool{}
		split := false
		for _, j := range members {
			r := releases[j]
			if len(r.Tracks) == 0 {
				continue
			}
			included = included || slices.Contains(ids, r.ID)
			a.ReleaseIDs = append(a.ReleaseIDs, r.ID)
			a.Releases = append(a.Releases, r)
			needed = needed || len(r.Reasons) > 0
			for _, dir := range directoryKeys(r) {
				a.Directories = appendOnce(a.Directories, dir)
			}
			split = split || r.Kind == "same_dir_split"
			for _, t := range r.Tracks {
				source = append(source, t.Root)
				if v := t.Observations["album"]; v.Value != "" && !v.Inferred {
					tags[textnorm.MergeKey(v.Value)] = true
				}
			}
			a.Group = a.Group || slices.Contains(r.Reasons, "missing_disc") || slices.Contains(r.Reasons, "missing_position")
		}
		if !included {
			continue
		}
		modifiable := slices.ContainsFunc(a.Releases, func(r localRelease) bool { return !r.Protected })
		unknown := modifiable && len(members) > 1 && len(a.Directories) > 1
		mixed := modifiable && split && len(tags) > 1
		a.Group = a.Group || unknown || mixed
		if explicit {
			a.Group = true
		} else if slices.ContainsFunc(a.Releases, func(r localRelease) bool { return r.stewardGrouped }) {
			// 替换同一目录的分配会连带覆盖人工归组。
			a.Group = false
			unknown, mixed = false, false
		}
		if !needed && !a.Group {
			continue
		}
		for j := range a.Releases {
			if unknown {
				a.Releases[j].Reasons = appendOnce(a.Releases[j].Reasons, "unknown_disc_directory")
			}
			if mixed {
				a.Releases[j].Reasons = appendOnce(a.Releases[j].Reasons, "mixed_directory")
			}
		}
		slices.Sort(a.Directories)
		slices.Sort(source)
		scope := a.Directories
		if len(scope) == 1 && scope[0] == "." {
			scope = a.ReleaseIDs
		}
		a.DirectoryKey = operations.Fingerprint([]any{slices.Compact(source), scope})
		scopes = append(scopes, a)
	}
	return scopes, nil
}

// RequestLocalCreditSplit 将已确认不能按外部署名拆开的本地原文留给 B1。
func RequestLocalCreditSplit(ctx context.Context, tx *sql.Tx, releaseID, trackID, role, sourceRef, raw string) error {
	target := trackID
	if target == "" {
		target = releaseID
	}
	if sourceRef == "" {
		sourceRef = target + "/scan/" + role
	}
	c := localCredit{Target: target, Role: role, Ref: sourceRef, Raw: raw, Requested: true}
	return projectLocalItems(ctx, tx, []string{releaseID}, nil, &c)
}

func ProjectLocalItems(ctx context.Context, tx *sql.Tx, ids []string, entries map[string]catalog.CurationEvidence) error {
	return projectLocalItems(ctx, tx, ids, entries, nil)
}
func projectLocalItems(ctx context.Context, tx *sql.Tx, ids []string, entries map[string]catalog.CurationEvidence, request *localCredit) error {
	scopes, err := localScopes(ctx, tx, ids, entries, request)
	if err != nil {
		return err
	}
	current := make([]string, 0, len(scopes))
	for _, a := range scopes {
		current = append(current, a.itemID())
	}
	_, err = tx.ExecContext(ctx, `UPDATE curation_pending_items SET status='resolved',resolution='inputs_changed',resolved_at=now(),updated_at=now() WHERE kind='local_cleanup' AND (target_id=ANY($1::uuid[]) OR (evidence->'release_ids') ?| $1::uuid[]::text[]) AND status<>'resolved' AND NOT(id=ANY($2::uuid[]))`, ids, current)
	if err != nil {
		return err
	}
	for _, a := range scopes {
		covered, err := existingLocalDecision(ctx, tx, a)
		if err != nil {
			return err
		}
		if covered {
			continue
		}
		digest := operations.Fingerprint(a)
		id := a.itemID()
		raw, err := json.Marshal(a)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO curation_pending_items(id,target_type,target_id,kind,status,rule_version,input_digest,identity_digest,evidence)
 VALUES($1,'release',$2,'local_cleanup','waiting_model',$3,$4,$5,$6) ON CONFLICT(id) DO UPDATE SET status='waiting_model',resolution=NULL,resolved_at=NULL,conversation_id=NULL,claimed_at=NULL,next_attempt_at=NULL,attempts=0,empty_count=0,model_stop_code=NULL,single_request=false,updated_at=now() WHERE curation_pending_items.resolution='inputs_changed'`, id, a.ReleaseIDs[0], LocalRuleVersion, digest, a.fileDigest(), raw)
		if err != nil {
			return err
		}
	}
	return nil
}

// 同代已决定的字段不会因归组后的发行 ID 或调序再问。新角色或新目标仍单独入队。
func (a localAlbum) requests() map[string]bool {
	keys := map[string]bool{}
	for _, r := range a.Releases {
		for _, t := range r.Tracks {
			source := t.Root + "/" + t.SourcePath
			for _, field := range r.Fields {
				keys["release/"+source+"/"+field] = true
			}
			for _, field := range t.Fields {
				keys["track/"+source+"/"+field] = true
			}
			if len(r.Encodings) > 1 {
				keys["encoding/"+source] = true
			}
			if a.Group && !r.Protected {
				keys["group/"+source] = true
			}
		}
		for _, c := range r.Credits {
			keys["split/"+strconv.FormatBool(c.Requested)+"/"+c.Ref+"/"+c.Role+"/"+c.Raw] = true
		}
	}
	return keys
}
func existingLocalDecision(ctx context.Context, tx *sql.Tx, a localAlbum) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT evidence,rule_version,extract(epoch FROM resolved_at)*1000000 FROM curation_pending_items
      WHERE kind='local_cleanup' AND status='resolved' AND resolution IN ('local_cleanup','undetermined') AND
       ((rule_version=$1 AND identity_digest=$2) OR (rule_version<$1 AND target_id=ANY($3::uuid[])))`, LocalRuleVersion, a.fileDigest(), a.ReleaseIDs)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	wanted := a.requests()
	for rows.Next() {
		var raw []byte
		var version int16
		var settled float64
		if err := rows.Scan(&raw, &version, &settled); err != nil {
			return false, err
		}
		if version == LocalRuleVersion {
			var prior localAlbum
			if err := json.Unmarshal(raw, &prior); err != nil {
				return false, err
			}
			for k := range prior.requests() {
				delete(wanted, k)
			}
		} else {
			// 旧合同只有文字。用原字段、路径和文件时间核对旧答案适用的范围。
			type field struct {
				Value     string
				Requested bool
			}
			var prior struct {
				Title, Artist field
				Directories   []struct {
					Path   string
					Tracks []struct {
						ID            string `json:"track_id"`
						File          string
						Title, Artist field
					}
				}
			}
			if err := json.Unmarshal(raw, &prior); err != nil {
				return false, err
			}
			for _, dir := range prior.Directories {
				for _, old := range dir.Tracks {
					for _, r := range a.Releases {
						for _, t := range r.Tracks {
							if old.ID != t.ID || path.Join(dir.Path, old.File) != t.Path || float64(t.Modified) > settled {
								continue
							}
							source := t.Root + "/" + t.SourcePath
							if prior.Title.Requested && prior.Title.Value == r.Title {
								delete(wanted, "release/"+source+"/title")
							}
							if prior.Artist.Requested && prior.Artist.Value == r.Artist {
								delete(wanted, "release/"+source+"/album_artist")
							}
							if old.Title.Requested && old.Title.Value == t.Title {
								delete(wanted, "track/"+source+"/title")
							}
							if old.Artist.Requested && old.Artist.Value == t.Artist {
								delete(wanted, "track/"+source+"/artist")
							}
						}
					}
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return len(wanted) == 0, nil
}

func (a localAlbum) itemID() string {
	return DerivedKey(a.ReleaseIDs[0], "local:"+strconv.Itoa(int(LocalRuleVersion))+":"+operations.Fingerprint(a))
}
func (a localAlbum) fileDigest() string {
	var data []string
	for _, r := range a.Releases {
		for _, t := range r.Tracks {
			data = append(data, operations.Fingerprint([]any{t.FileID, t.Size, t.Modified, t.SourcePath, t.Cue, t.CueStart, t.CueEnd}))
		}
	}
	slices.Sort(data)
	return operations.Fingerprint(data)
}
