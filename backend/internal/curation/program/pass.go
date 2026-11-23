package program

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

const pageSize = 100

type pageStates struct {
	releases, artworks, lyrics map[string]curation.WorkState
	fingerprintAvailable       bool
}

func (w *Worker) walk(ctx context.Context, lyrics bool, priority func() error, fingerprintAvailable bool) error {
	var after *catalog.CurationCursor
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		next, err := w.page(ctx, after, lyrics, priority, fingerprintAvailable)
		if failureCode(err) == "scan_running" {
			if err := w.closeBatch(ctx); err != nil {
				return err
			}
			w.rest(ctx, err)
			continue
		}
		if err != nil {
			return err
		}
		if next == nil {
			return nil
		}
		after = next
	}
}

func (w *Worker) page(ctx context.Context, after *catalog.CurationCursor, lyrics bool, priority func() error, fingerprintAvailable bool) (*catalog.CurationCursor, error) {
	if err := w.ready(ctx); err != nil {
		return nil, err
	}
	w.setState("running")
	page, err := catalog.PageCurationReleases(ctx, w.db, after, pageSize)
	if err != nil || len(page) == 0 {
		return nil, err
	}
	ids := make([]string, len(page))
	for i, p := range page {
		ids[i] = p.ID
	}
	entries, err := catalog.LoadCurationEvidence(ctx, w.db, ids, w.writer.ArtworkExists)
	if err != nil {
		return nil, err
	}
	states := pageStates{fingerprintAvailable: fingerprintAvailable}
	states.releases, err = curation.LoadWorkStates(ctx, w.db, "release", ids)
	if err != nil {
		return nil, err
	}
	var sameArtistOverrides map[string]bool
	if lyrics {
		var tracks, ambiguous []string
		for _, e := range entries {
			for _, t := range e.Tracks {
				tracks = append(tracks, t.ID)
				if lyricsArtistPlaceholder(t) && !t.LocalLyrics && !t.ExternalLyrics && t.DurationSeconds > 0 {
					ambiguous = append(ambiguous, t.ID)
				}
			}
		}
		sameArtistOverrides, err = automaticTrackArtists(ctx, w.db, ambiguous)
		if err != nil {
			return nil, err
		}
		states.lyrics, err = curation.LoadWorkStates(ctx, w.db, "lyrics", tracks)
	} else {
		states.artworks, err = curation.LoadWorkStates(ctx, w.db, "artwork", ids)
	}
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		e, visible := entries[id]
		if !visible {
			continue
		}
		if lyrics {
			for _, t := range e.Tracks {
				if err := priority(); err != nil {
					return nil, err
				}
				if sameArtistOverrides[t.ID] {
					t.ArtistPlaceholder = false
				}
				if !lyricsGap(t) {
					continue
				}
				if err := w.process(ctx, &e, "lyrics", t.ID, states); err != nil {
					return nil, err
				}
			}
		} else {
			if err := w.process(ctx, &e, "release", id, states); err != nil {
				return nil, err
			}
			if artworkGap(e) {
				if err := w.process(ctx, &e, "artwork", id, states); err != nil {
					return nil, err
				}
			}
		}
	}
	return &page[len(page)-1], nil
}

func subjectDigest(subject string, e catalog.CurationEvidence, id string) string {
	switch subject {
	case "release":
		return ReleaseDigest(e)
	case "artwork":
		return ArtworkDigest(e)
	default:
		for _, t := range e.Tracks {
			if t.ID == id {
				return LyricsDigest(e, t)
			}
		}
		return ""
	}
}

func needsWork(subject string, state curation.WorkState, digest string, now time.Time, fingerprintAvailable bool) bool {
	if digest == "" {
		return false
	}
	if state.RuleVersion != StoredRuleVersion || state.InputDigest != digest || state.Status == "try_later" && state.NextAttemptAt != nil && !now.Before(*state.NextAttemptAt) {
		return true
	}
	if !fingerprintAvailable || subject != "release" || (state.Status != "no_match" && state.Status != "uncertain") {
		return false
	}
	var last struct {
		Fingerprint *string  `json:"fingerprint"`
		Method      string   `json:"method"`
		Conflicts   []string `json:"conflicts"`
	}
	if json.Unmarshal(state.Result, &last) != nil {
		return false
	}
	if last.Fingerprint != nil {
		return *last.Fingerprint == "unavailable"
	}
	return (last.Method == "" || last.Method == "search") && !slices.Contains(last.Conflicts, "track_count")
}

func (w *Worker) fingerprintStep(ctx context.Context) (*FingerprintStep, error) {
	if err := w.sources.Check(ctx, "acoustid"); err != nil {
		switch failureCode(err) {
		case "not_found", "source_disabled":
			return nil, nil
		default:
			return nil, err
		}
	}
	f, ok := w.sources.FingerprintFinder("acoustid")
	if !ok || w.opts.Fingerprints == nil {
		return nil, nil
	}
	return &FingerprintStep{Finder: f, Prints: w.opts.Fingerprints}, nil
}

func subjectSource(subject string) string {
	switch subject {
	case "artwork":
		return "coverartarchive"
	case "lyrics":
		return "lrclib"
	default:
		return "musicbrainz"
	}
}

// 旧的发行对应缺少操作后态。只有已核对为同一外部发行时，封面才可退到匹配结果中的专辑。
func subjectEvidence(subject string, e catalog.CurationEvidence, state curation.WorkState) catalog.CurationEvidence {
	if subject == "artwork" && e.ExternalGroupID == "" && e.ExternalID != "" && state.Status == "matched" && state.RuleVersion == StoredRuleVersion {
		var matched struct {
			ID    string `json:"external_id"`
			Group string `json:"external_group_id"`
		}
		if json.Unmarshal(state.Result, &matched) == nil && matched.ID == e.ExternalID {
			e.ExternalGroupID = matched.Group
		}
	}
	return e
}

type outcome struct {
	status, group, trackFields, artistLinks string
	match                                   MatchResult
}

func (w *Worker) evaluate(ctx context.Context, u *unit, e catalog.CurationEvidence, fingerprintRequired bool) (out outcome, err error) {
	switch u.subject {
	case "artwork":
		out.status, err = w.applyArtwork(ctx, u, e)
	case "lyrics":
		for _, t := range e.Tracks {
			if t.ID == u.id {
				out.status, err = w.applyLyrics(ctx, u, e, t)
				break
			}
		}
	case "release":
		if err = w.sources.Check(ctx, "musicbrainz"); err != nil {
			return out, err
		}
		r, hasReleases := w.sources.ReleaseFinder("musicbrainz")
		f, hasRecordings := w.sources.RecordingFinder("musicbrainz")
		if !hasRecordings || !hasReleases {
			return out, operations.Conflict("source_disabled", "发行或录音查询能力未启用")
		}
		var step *FingerprintStep
		step, err = w.fingerprintStep(ctx)
		if err != nil {
			return out, err
		}
		if fingerprintRequired && step == nil {
			return out, operations.Conflict("source_disabled", "声学指纹查询当前不可用")
		}
		out.match, err = MatchWithFingerprint(ctx, e, r, f, step)
		if err == nil && out.match.Status == "matched" {
			out.group, out.artistLinks, err = w.applyMatch(ctx, u, e, out.match)
			out.trackFields = "done"
		}
		out.status = out.match.Status
	}
	return out, err
}

func (w *Worker) process(ctx context.Context, e *catalog.CurationEvidence, subject, id string, states pageStates) error {
	var state curation.WorkState
	switch subject {
	case "release":
		state = states.releases[id]
	case "artwork":
		state = states.artworks[id]
	case "lyrics":
		state = states.lyrics[id]
	}
	input := subjectEvidence(subject, *e, states.releases[e.ID])
	digest := subjectDigest(subject, input, id)
	now := w.opts.Now()
	ordinary := needsWork(subject, state, digest, now, false)
	if subject == "release" && !ordinary {
		tracks, artists := matchBackfills(state, input)
		if tracks || artists {
			return w.backfillMatch(ctx, e, state, states, tracks, artists)
		}
	}
	if !ordinary && !needsWork(subject, state, digest, now, states.fingerprintAvailable) {
		return nil
	}
	if err := w.ready(ctx); err != nil {
		return err
	}
	if err := w.rotateBatch(ctx); err != nil {
		return err
	}
	u := unit{w: w, subject: subject, id: id, generation: state.Generation + 1, releaseID: e.ID, inputDigest: digest, releaseState: states.releases[e.ID]}
	out, err := w.evaluate(ctx, &u, input, !ordinary)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	state.Subject, state.TargetID, state.RuleVersion, state.InputDigest = subject, id, StoredRuleVersion, digest
	if failureCode(err) == "source_disabled" {
		return nil
	}
	retry := errors.Is(err, sources.ErrTryLater)
	if err != nil && !retry {
		return err
	}
	// 出站期间或过滤写入时也可能失去权限，保存结论或退避前再次核对。
	if err := w.sources.Check(ctx, subjectSource(subject)); err != nil {
		if failureCode(err) == "source_disabled" {
			return nil
		}
		return err
	}
	if retry || out.status == "try_later" {
		delay := min(24*time.Hour, 10*time.Minute<<min(state.RetryCount, 8))
		var local *fingerprintRetry
		if errors.As(err, &local) {
			delay = max(delay, local.delay)
		}
		next := w.opts.Now().Add(delay)
		state.Status, state.NextAttemptAt, state.RetryCount = "try_later", &next, state.RetryCount+1
		if out.status == "try_later" {
			state.Generation = u.generation
			return w.saveConclusion(ctx, e, state, out, states)
		}
		if err := curation.SaveWorkState(ctx, w.db, state); err != nil {
			return err
		}
		if subject == "release" {
			states.releases[id] = state
		}
		return nil
	}
	state.Status, state.Attempts, state.Generation = out.status, state.Attempts+1, u.generation
	state.RetryCount, state.NextAttemptAt, state.Result = 0, nil, nil
	return w.saveConclusion(ctx, e, state, out, states)
}

func (w *Worker) saveConclusion(ctx context.Context, e *catalog.CurationEvidence, state curation.WorkState, out outcome, states pageStates) error {
	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	entries, err := catalog.LoadCurationEvidence(ctx, tx, []string{e.ID}, w.writer.ArtworkExists)
	if err != nil {
		return err
	}
	fresh, visible := entries[e.ID]
	if !visible {
		return errReleaseChanged
	}
	postDigest := subjectDigest(state.Subject, subjectEvidence(state.Subject, fresh, states.releases[e.ID]), state.TargetID)
	if postDigest == "" {
		return errReleaseChanged
	}
	if postDigest != state.InputDigest {
		var expected catalog.CurationEvidence
		switch {
		case state.Subject == "release" && out.status == "matched":
			expected = afterMatch(*e, fresh, out)
		case state.Subject != "release" && (out.status == "done" || out.status == "try_later"):
			expected = afterMedia(*e, fresh, state.Subject, state.TargetID)
		default:
			return errReleaseChanged
		}
		if subjectDigest(state.Subject, subjectEvidence(state.Subject, expected, states.releases[e.ID]), state.TargetID) != postDigest {
			return errReleaseChanged
		}
	}
	state.InputDigest = postDigest
	var related *curation.WorkState
	if state.Subject == "release" {
		state.Result = releaseResult(out)
		if err := savePending(ctx, tx, fresh, state, out.match); err != nil {
			return err
		}
	} else if s, ok := states.releases[e.ID]; ok && s.RuleVersion == StoredRuleVersion &&
		s.InputDigest == ReleaseDigest(*e) && s.InputDigest != ReleaseDigest(fresh) &&
		ReleaseDigest(afterMedia(*e, fresh, state.Subject, state.TargetID)) == ReleaseDigest(fresh) {
		// 封面与歌词存在位也是发行摘要的一部分，自己的这次写入不能触发整张重查。
		s.InputDigest = ReleaseDigest(fresh)
		if err := curation.SaveWorkState(ctx, tx, s); err != nil {
			return err
		}
		related = &s
	}
	if err := curation.SaveWorkState(ctx, tx, state); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if state.Subject == "release" {
		states.releases[e.ID] = state
	}
	if related != nil {
		states.releases[e.ID] = *related
	}
	*e = fresh
	return nil
}

func afterMedia(before, after catalog.CurationEvidence, subject, id string) catalog.CurationEvidence {
	if subject == "artwork" {
		before.ExternalArtwork, before.ArtworkFile = after.ExternalArtwork, after.ArtworkFile
	} else {
		before.Tracks = slices.Clone(before.Tracks)
		for i, t := range before.Tracks {
			if t.ID == id && i < len(after.Tracks) && after.Tracks[i].ID == id {
				before.Tracks[i].ExternalLyrics = after.Tracks[i].ExternalLyrics
			}
		}
	}
	return before
}

// 只允许本次匹配计划的新增对应、自动字段以及归入目标组改变摘要。
// 输入若有其他变化，重新判定，避免把旧候选登记在新的输入上。
func afterMatch(before, after catalog.CurationEvidence, out outcome) catalog.CurationEvidence {
	if r := out.match.Release; r != nil {
		if before.ExternalID == "" && after.ExternalID == r.ID {
			before.ExternalID, before.ExternalGroupID = r.ID, r.Group.ID
		}
		if out.group == "joined" && after.GroupChangeSource == "curation" && after.GroupExternalID == r.Group.ID {
			before.GroupID, before.GroupChangeSource = after.GroupID, after.GroupChangeSource
			before.GroupExternalID = after.GroupExternalID
		}
		if out.group == "linked" && after.GroupExternalID == r.Group.ID {
			before.GroupExternalID = r.Group.ID
		}
		auto := maps.Clone(before.Automatic)
		if auto == nil {
			auto = make(map[string]bool)
		}
		for field := range missingFields(before, *r) {
			if after.Automatic[field] {
				auto[field] = true
			}
		}
		before.Automatic = auto
	}
	partners := make(map[string]string, len(out.match.Pairs))
	for _, p := range out.match.Pairs {
		partners[p.TrackID] = p.RecordingID
	}
	before.Tracks = slices.Clone(before.Tracks)
	for i, t := range before.Tracks {
		if t.ExternalID == "" && i < len(after.Tracks) && after.Tracks[i].ID == t.ID && partners[t.ID] == after.Tracks[i].ExternalID {
			before.Tracks[i].ExternalID = after.Tracks[i].ExternalID
		}
	}
	return before
}

func releaseResult(out outcome) []byte {
	m := out.match
	r := struct {
		Mode            string   `json:"mode"`
		Method          string   `json:"method"`
		Fingerprint     string   `json:"fingerprint"`
		ExternalID      string   `json:"external_id,omitempty"`
		ExternalGroupID string   `json:"external_group_id,omitempty"`
		Conflicts       []string `json:"conflicts"`
		Group           string   `json:"group,omitempty"`
		ArtistLinks     string   `json:"artist_links,omitempty"`
		TrackFields     string   `json:"track_fields,omitempty"`
	}{Mode: m.Mode, Method: m.Method, Fingerprint: m.Fingerprint, Conflicts: m.Conflicts, Group: out.group, TrackFields: out.trackFields, ArtistLinks: out.artistLinks}
	if m.Release != nil {
		r.ExternalID, r.ExternalGroupID = m.Release.ID, m.Release.Group.ID
	}
	if m.Recording != nil {
		r.ExternalID = m.Recording.ID
	}
	return jsonValue(r)
}

func savePending(ctx context.Context, tx *sql.Tx, e catalog.CurationEvidence, state curation.WorkState, m MatchResult) error {
	kind := "release_match"
	if m.Mode == "single" {
		kind = "recording_match"
	}
	if m.Status == "matched" {
		return curation.ResolvePendingItems(ctx, tx, e.ID, kind, "program_matched")
	}
	if slices.Contains(m.Conflicts, "insufficient_evidence") {
		return nil
	}
	return curation.UpsertPendingItem(ctx, tx, curation.PendingItem{TargetID: e.ID, Kind: kind, RuleVersion: state.RuleVersion,
		InputDigest: state.InputDigest, Evidence: jsonValue(PendingEvidence(e)), Candidates: jsonValue(m.Candidates), Conflicts: jsonValue(m.Conflicts)})
}
