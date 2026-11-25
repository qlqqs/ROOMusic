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
	"github.com/qlqq/roomusic/backend/internal/sources"
)

const pageSize = 100

type pageStates struct {
	releases, artworks, lyrics map[string]curation.WorkState
	fingerprintAvailable       bool
}

func (w *Worker) walk(ctx context.Context, lyrics bool, _ func() error, _ bool) error {
	subject := "release"
	if lyrics {
		subject = "lyrics"
	}
	w.marker = nil
	return w.drainQueues(ctx, []string{subject})
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
	previous := state.InputDigest
	if subject == "release" && state.IdentityDigest != "" {
		previous = state.IdentityDigest
	}
	if previous != digest || subject != "release" && state.RuleVersion != resourceRuleVersion {
		return true
	}
	if state.Status == "try_later" || subject == "release" && state.Status == "unlisted" {
		return state.NextAttemptAt != nil && !now.Before(*state.NextAttemptAt)
	}
	if subject == "release" && state.RequeryMatchVersion == MatchRuleVersion {
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
	if subject == "artwork" && e.ExternalGroupID == "" && e.ExternalID != "" && state.Status == "matched" {
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
	status, group, trackFields, artistLinks, matchReason string
	match                                                MatchResult
	proposals                                            []curation.ArtistCreditProposal
	review                                               releaseReviewState
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
		m, err := w.searchRelease(ctx, u, e, fingerprintRequired)
		if err != nil {
			return out, err
		}
		return w.concludeMatch(ctx, u, e, m)
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
	workDigest := digest
	version := resourceRuleVersion
	var inputs releaseDigests
	var offline *MatchResult
	var review releaseReviewState
	if subject == "release" {
		inputs = releaseDigests{digest, IdentityDigest(input), GapDigest(input)}
		// 旧基线与现有对应的维护一起提交，外部暂时失败时保留原状态。
		if state.InputDigest == digest && (state.IdentityDigest == "" || state.IdentityDigest == inputs.identity && state.LocalRuleVersion != LocalRuleVersion) {
			tracks, artists, merges := matchBackfills(state, input)
			if tracks || artists || merges {
				return w.backfillMatch(ctx, e, state, states, tracks, artists, merges)
			}
		}
		var deferred bool
		var err error
		review, err = previousReview(state)
		if err != nil {
			return err
		}
		if state.Status == "no_usable_candidates" && review.BlockDigest == blockedLinkDigest(input) && state.MatchRuleVersion == MatchRuleVersion {
			return nil
		}
		deferred, offline, err = w.prepareRelease(ctx, input, inputs, &state)
		if err != nil {
			return err
		}
		if state.TargetID != "" {
			states.releases[id] = state
		}
		if deferred {
			return nil
		}
		if state.IdentityDigest != "" {
			workDigest = inputs.identity
		}
		version = MatchRuleVersion
	}
	now := w.opts.Now()
	// 新启用的插件或更新的密钥可以补齐上一轮留下的空缺。
	ordinary := offline != nil || state.Status == "no_usable_candidates" && review.BlockDigest != blockedLinkDigest(input) || needsWork(subject, state, workDigest, now, false) || subject != "release" && currentAttempt(ctx) != nil
	if subject == "release" && !ordinary {
		tracks, artists, merges := matchBackfills(state, input)
		if tracks || artists || merges {
			return w.backfillMatch(ctx, e, state, states, tracks, artists, merges)
		}
	}
	if !ordinary && !needsWork(subject, state, workDigest, now, states.fingerprintAvailable) {
		return nil
	}
	if err := w.ready(ctx); err != nil {
		return err
	}
	u := unit{w: w, subject: subject, id: id, ruleVersion: version, generation: state.Generation + 1, releaseID: e.ID, inputDigest: digest, releaseState: states.releases[e.ID]}
	u.offline = offline != nil
	u.programOnly = subject == "release" && state.IdentityDigest == inputs.identity && review.UnlistedSince != nil && (state.Status == "unlisted" || state.Status == "try_later")
	if subject == "release" {
		u.releaseInput, u.identityDigest = &input, inputs.identity
	}
	var out outcome
	var err error
	if offline != nil {
		out, err = w.concludeMatch(ctx, &u, input, *offline)
	} else {
		out, err = w.evaluate(ctx, &u, input, !ordinary)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	state.Subject, state.TargetID, state.RuleVersion, state.InputDigest = subject, id, version, digest
	if subject == "release" {
		inputs.record(&state)
		state.MatchRuleVersion, state.RequeryMatchVersion = version, 0
	}
	if failureCode(err) == "source_disabled" {
		return err
	}
	var local *fingerprintRetry
	if !errors.As(err, &local) && curation.IsSourceUnavailable(err) {
		return sourceFailure(ctx, u.source(), err)
	}
	retry := errors.Is(err, sources.ErrTryLater)
	if err != nil && !retry {
		return err
	}
	// 出站期间或过滤写入时也可能失去权限，保存结论或退避前再次核对。
	if err := w.sources.Check(ctx, u.source()); err != nil {
		return err
	}
	if err := w.ready(ctx); err != nil {
		return err
	}
	if retry || out.status == "try_later" {
		if !u.programOnly && review.UnlistedSince != nil {
			state.Result = nil
		}
		delay := min(24*time.Hour, 10*time.Minute<<min(state.RetryCount, 8))
		if errors.As(err, &local) {
			delay = max(delay, local.delay)
		}
		next := w.opts.Now().Add(delay)
		state.Status, state.NextAttemptAt, state.RetryCount = "try_later", &next, state.RetryCount+1
		if out.status == "try_later" {
			state.Generation = u.generation
			return w.saveConclusion(ctx, e, state, out, states, u.source())
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
	if u.programOnly {
		out.review.UnlistedSince = review.UnlistedSince
		out.review.RecheckedAt = &now
		if out.status == "unlisted" {
			out.review.Reason = "program_recheck_inconclusive"
		}
	} else if out.status == "unlisted" {
		next := now.Add(90 * 24 * time.Hour)
		state.NextAttemptAt = &next
		out.review.UnlistedSince = &now
		out.review.Reason = out.matchReason
	}
	if out.status == "no_usable_candidates" {
		out.review.BlockDigest = blockedLinkDigest(input)
	}
	return w.saveConclusion(ctx, e, state, out, states, u.source())
}

func (w *Worker) saveConclusion(ctx context.Context, e *catalog.CurationEvidence, state curation.WorkState, out outcome, states pageStates, source string) error {
	for {
		err := w.saveConclusionOnce(ctx, e, state, out, states)
		if !retrySnapshot(err) {
			return err
		}
		// 并行队列可能只改了同一行的排队投影。保留已取回的结论，用新快照复核证据。
		if err := w.ready(ctx); err != nil {
			return err
		}
		if err := w.sources.Check(ctx, source); err != nil {
			return err
		}
	}
}

func (w *Worker) saveConclusionOnce(ctx context.Context, e *catalog.CurationEvidence, state curation.WorkState, out outcome, states pageStates) error {
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
	var postIdentity string
	if state.Subject == "release" {
		postIdentity = IdentityDigest(fresh)
	}
	if postDigest != state.InputDigest || state.Subject == "release" && postIdentity != state.IdentityDigest {
		expected := *e
		if state.Subject == "release" {
			expected = withAddedMedia(expected, fresh)
		}
		switch {
		case state.Subject == "release" && out.status == "matched":
			expected = afterMatch(expected, fresh, out)
		case state.Subject != "release" && (out.status == "done" || out.status == "try_later"):
			expected = afterMedia(*e, fresh, state.Subject, state.TargetID)
		}
		if subjectDigest(state.Subject, subjectEvidence(state.Subject, expected, states.releases[e.ID]), state.TargetID) != postDigest ||
			state.Subject == "release" && IdentityDigest(expected) != postIdentity {
			return errReleaseChanged
		}
	}
	state.InputDigest = postDigest
	var related *curation.WorkState
	if state.Subject == "release" {
		releaseDigests{postDigest, postIdentity, GapDigest(fresh)}.record(&state)
		state.Result = releaseResult(out)
		if err := curation.SaveArtistCreditProposals(ctx, tx, e.ID, postIdentity, out.proposals); err != nil {
			return err
		}
		if out.review.RecheckedAt == nil && !out.match.Offline && out.status == "matched" {
			if err := saveNameDifferences(ctx, tx, fresh, out.match); err != nil {
				return err
			}
		}
		if err := savePending(ctx, tx, fresh, state, out.match); err != nil {
			return err
		}
	} else if s, ok := states.releases[e.ID]; ok && s.IdentityDigest != "" && s.IdentityDigest == IdentityDigest(fresh) &&
		s.InputDigest == ReleaseDigest(*e) && s.InputDigest != ReleaseDigest(fresh) &&
		ReleaseDigest(afterMedia(*e, fresh, state.Subject, state.TargetID)) == ReleaseDigest(fresh) {
		// 同步媒体写后的缺口和完整指纹，保留识别结论。
		inputs := releaseInputs(fresh)
		if err := updateReleaseProjection(ctx, tx, fresh, &s, inputs, s.MatchRuleVersion); err != nil {
			return err
		}
		related = &s
	}
	if state.Subject == "release" {
		// 与模型结项沿用相同锁序，在待定项之后锁定工作状态并复核取证时的结论。
		var generation int
		var status string
		previous := states.releases[e.ID]
		err := tx.QueryRowContext(ctx, `SELECT generation,status FROM curation_work_states
 WHERE subject='release' AND target_id=$1 FOR UPDATE`, e.ID).Scan(&generation, &status)
		if errors.Is(err, sql.ErrNoRows) || err == nil && (generation != previous.Generation || status != previous.Status) {
			return errReleaseChanged
		}
		if err != nil {
			return err
		}
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

// 独立媒体队列可以在识别期间补齐封面或歌词。撤销、删除和其它证据仍须重新取项。
func withAddedMedia(before, after catalog.CurationEvidence) catalog.CurationEvidence {
	before.ExternalArtwork = before.ExternalArtwork || after.ExternalArtwork
	before.ArtworkFile = before.ArtworkFile || after.ArtworkFile
	before.Tracks = slices.Clone(before.Tracks)
	for i, t := range before.Tracks {
		if i < len(after.Tracks) && t.ID == after.Tracks[i].ID {
			before.Tracks[i].ExternalLyrics = t.ExternalLyrics || after.Tracks[i].ExternalLyrics
		}
	}
	return before
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
	before = afterGroupMatch(before, after, out)
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
	type blockedCandidate struct {
		ID        string   `json:"external_id"`
		Conflicts []string `json:"conflicts"`
	}
	m := out.match
	r := struct {
		releaseReviewState
		Verification    *MatchVerification `json:"verification,omitempty"`
		Blocked         []blockedCandidate `json:"blocked,omitempty"`
		Level           string             `json:"level,omitempty"`
		Rule            string             `json:"rule,omitempty"`
		Route           string             `json:"route,omitempty"`
		Credits         []sources.Credit   `json:"credits,omitempty"`
		Mode            string             `json:"mode"`
		Method          string             `json:"method"`
		Fingerprint     string             `json:"fingerprint"`
		ExternalID      string             `json:"external_id,omitempty"`
		ExternalGroupID string             `json:"external_group_id,omitempty"`
		Conflicts       []string           `json:"conflicts"`
		Group           string             `json:"group,omitempty"`
		ReleaseMerge    string             `json:"release_merge,omitempty"`
		ArtistLinks     string             `json:"artist_links,omitempty"`
		TrackFields     string             `json:"track_fields,omitempty"`
	}{Verification: m.Verification, releaseReviewState: out.review, Level: m.Level, Rule: m.Rule, Route: m.Route, Credits: m.Credits, ExternalGroupID: m.GroupID, Mode: m.Mode, Method: m.Method, Fingerprint: m.Fingerprint, Conflicts: m.Conflicts, Group: out.group, TrackFields: out.trackFields, ArtistLinks: out.artistLinks}
	if m.Release != nil {
		r.ExternalID, r.ExternalGroupID = m.Release.ID, m.Release.Group.ID
		if out.status == "matched" {
			r.ReleaseMerge = "done"
		}
	}
	if out.status == "no_usable_candidates" {
		for _, c := range m.Candidates {
			r.Blocked = append(r.Blocked, blockedCandidate{c.ExternalID, c.Conflicts})
		}
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
	if m.Status == "no_usable_candidates" || m.Status == "unlisted" {
		return curation.ResolvePendingItems(ctx, tx, e.ID, kind, m.Status)
	}
	if slices.Contains(m.Conflicts, "insufficient_evidence") {
		return curation.ResolvePendingItems(ctx, tx, e.ID, kind, "insufficient_evidence")
	}
	evidence := pendingMatchEvidence(e, m.Method)
	evidence["match_snapshot"] = m.savedSnapshot()
	evidence["classification"] = e.Classification().Class
	return curation.UpsertPendingItem(ctx, tx, curation.PendingItem{TargetID: e.ID, Kind: kind, Route: m.Route, RuleVersion: state.RuleVersion,
		InputDigest: state.InputDigest, MatchRuleVersion: state.MatchRuleVersion, IdentityDigest: state.IdentityDigest, Evidence: jsonValue(evidence), Candidates: jsonValue(m.modelCandidates()), Conflicts: jsonValue(m.Conflicts)})
}
