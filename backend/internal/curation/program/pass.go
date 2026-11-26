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
	if subject == "release" {
		if state.Status == "unmatched" || state.Status == "conflict" {
			r, _ := previousReview(state)
			if r.Judge != nil {
				return true
			}
		}
		if state.Status == "pending" || state.ManualRequestedAt != nil {
			return true
		}
		previous = state.FileDigest
	}
	if previous != digest || subject != "release" && state.RuleVersion != resourceRuleVersion {
		return true
	}
	if state.Status == "try_later" {
		return state.NextAttemptAt != nil && !now.Before(*state.NextAttemptAt)
	}
	if subject == "release" && state.RequeryMatchVersion == MatchRuleVersion {
		return true
	}
	if subject == "release" && (state.Status == "unmatched" || state.Status == "conflict") {
		return state.NextAttemptAt != nil && !now.Before(*state.NextAttemptAt)
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
	keepSourceResult                                                   bool
	checkedAt                                                          time.Time
	status, group, trackFields, artistLinks, matchReason, releaseMerge string
	match                                                              MatchResult
	proposals                                                          []curation.ArtistCreditProposal
	review                                                             releaseReviewState
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
		var m MatchResult
		m, err = w.searchRelease(ctx, u, e, fingerprintRequired)
		if err == nil {
			out, err = w.concludeMatch(ctx, u, e, m)
		}
		// 结论落库前补查艺人别名也属于当前步骤。
		if err != nil && u.matchCursor.Step != "" {
			return out, &stepFailure{&u.matchCursor, err}
		}
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
		inputs = releaseDigests{digest, IdentityDigest(input), GapDigest(input), input.FileDigest}
		// 旧基线与现有对应的维护一起提交，外部暂时失败时保留原状态。
		creditUpgrade := state.Status == "matched" && state.MatchRuleVersion != MatchRuleVersion && w.hasCachedCreditInput(input)
		if (state.FileDigest == input.FileDigest || state.FileDigest == "" && state.Status == "matched") && !creditUpgrade && state.InputDigest == digest && (state.IdentityDigest == "" || state.IdentityDigest == inputs.identity && state.LocalRuleVersion != LocalRuleVersion && state.MatchRuleVersion == MatchRuleVersion) {
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
		if state.Status == "query_error" && state.FileDigest == input.FileDigest && state.MatchRuleVersion == MatchRuleVersion {
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
		workDigest = inputs.file
		version = MatchRuleVersion
	}
	now := w.opts.Now()
	// 新启用的插件或更新的密钥可以补齐上一轮留下的空缺。
	ordinary := offline != nil || subject == "release" && state.FileDigest != input.FileDigest || needsWork(subject, state, workDigest, now, false) || subject != "release" && currentAttempt(ctx) != nil
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
	u.programOnly = subject == "release" && (state.ManualRequestedAt != nil || state.Status == "try_later" && review.Manual)
	review.Manual = u.programOnly
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
	var failed *stepFailure
	if subject == "release" && errors.As(err, &failed) {
		return w.saveQueryFailure(ctx, state, states.releases[id], failed)
	}
	if curation.IsSourceUnavailable(err) {
		return sourceFailure(ctx, u.source(), err)
	}
	// 最后一次出站遭遇来源故障时，恢复后继续保留上一次有效的来源结论。
	out.keepSourceResult = u.judge != nil && review.Retry != nil && review.Retry.Step == "model_hints" && out.matchReason == "request_limit"
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
		if u.programOnly {
			state.Result = jsonValue(review)
		} else if review.UnlistedSince != nil {
			state.Result = nil
		}
		delay := min(24*time.Hour, 10*time.Minute<<min(state.RetryCount, 8))
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
	out.checkedAt = w.opts.Now().UTC()
	if out.status == "unmatched" && out.matchReason != "candidates_revoked" || out.status == "conflict" && slices.Contains(out.match.Conflicts, "ambiguous_editions") {
		now = out.checkedAt
		next := now.Add(90 * 24 * time.Hour)
		state.NextAttemptAt = &next
		out.review.UnlistedSince = &now
		if review.UnlistedSince != nil {
			out.review.UnlistedSince = review.UnlistedSince
			out.review.RecheckedAt = &now
		}
	}
	out.review.Reason = out.matchReason
	if err := w.saveConclusion(ctx, e, state, out, states, u.source()); err != nil {
		return err
	}
	if out.status == "matched" && out.match.Release != nil {
		if err := w.finishEditionMerge(ctx, &u, *out.match.Release); err != nil {
			return err
		}
		_, err = w.db.ExecContext(ctx, `UPDATE curation_work_states SET result=result || '{"release_merge":"done"}'::jsonb WHERE subject='release' AND target_id=$1 AND generation=$2 AND status='matched'`, e.ID, state.Generation)
	}
	return err
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
		if fresh.FileDigest != state.FileDigest {
			return errReleaseChanged
		}
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
		releaseDigests{postDigest, postIdentity, GapDigest(fresh), fresh.FileDigest}.record(&state)
		state.Result = releaseResult(out)
		if err := curation.SaveArtistCreditProposals(ctx, tx, e.ID, postIdentity, out.proposals); err != nil {
			return err
		}
		if !out.match.Offline && out.status == "matched" {
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
		var patrolRevision int64
		var status string
		previous := states.releases[e.ID]
		err := tx.QueryRowContext(ctx, `SELECT generation,status,patrol_revision FROM curation_work_states
 WHERE subject='release' AND target_id=$1 FOR UPDATE`, e.ID).Scan(&generation, &status, &patrolRevision)
		if errors.Is(err, sql.ErrNoRows) || err == nil && (generation != previous.Generation || status != previous.Status || (out.status != "matched" && patrolRevision != previous.PatrolRevision)) {
			return errReleaseChanged
		}
		if err != nil {
			return err
		}
	}
	if state.Subject == "release" && !out.keepSourceResult && (out.status == "matched" || out.status == "unmatched" || out.status == "conflict") {
		checked := out.checkedAt
		r := curation.ReleaseMatchResult{Source: "musicbrainz", Status: out.status, CheckedAt: &checked, Reason: out.matchReason, Conflicts: out.resultConflicts()}
		if out.status == "conflict" {
			rows := out.match.modelCandidates()
			for i := range rows {
				rows[i].Entity = "release"
				if out.match.Mode == "single" {
					rows[i].Entity = "recording"
					if rows[i].Recording != nil && len(fresh.Tracks) == 1 {
						rows[i].Verification = verifyRecording(fresh, *rows[i].Recording)
						rows[i].Verification.Method = rows[i].Method
					}
				}
				if rows[i].Release != nil {
					for _, medium := range rows[i].Release.Media {
						if medium.Format != "" && !slices.Contains(rows[i].Medium, medium.Format) {
							rows[i].Medium = append(rows[i].Medium, medium.Format)
						}
					}
				}
			}
			r.Candidates = jsonValue(rows)
			r.SnapshotID = curation.DerivedKey(e.ID, digest([]any{state.Generation, state.FileDigest, rows}))
		}
		err := curation.SaveReleaseMatchResult(ctx, tx, e.ID, r)
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

func (out outcome) resultConflicts() []string {
	// 名字查找未通过时，详情及其核对原因留在模型待定项，来源结论记普通未匹配。
	if out.status == "unmatched" && out.matchReason == "" {
		return []string{}
	}
	return out.match.Conflicts
}

func releaseResult(out outcome) []byte {
	type blockedCandidate struct {
		ID        string   `json:"external_id"`
		Conflicts []string `json:"conflicts"`
	}
	m := out.match
	r := struct {
		releaseReviewState
		Snapshot        *MatchSnapshot     `json:"match_snapshot,omitempty"`
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
	}{Verification: m.Verification, releaseReviewState: out.review, Level: m.Level, Rule: m.Rule, Route: m.Route, Credits: m.Credits, ExternalGroupID: m.GroupID, Mode: m.Mode, Method: m.Method, Fingerprint: m.Fingerprint, Conflicts: out.resultConflicts(), Group: out.group, TrackFields: out.trackFields, ArtistLinks: out.artistLinks, ReleaseMerge: out.releaseMerge}
	if out.status == "conflict" {
		r.Snapshot = &m.Snapshot
	}
	if m.Release != nil {
		r.ExternalID, r.ExternalGroupID = m.Release.ID, m.Release.Group.ID
	}
	if out.matchReason == "candidates_revoked" {
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
	// 新一轮程序结论使原先领取的识别失效；旧会话按原项 ID 提交时必须重新核对。
	if _, err := tx.ExecContext(ctx, `UPDATE curation_pending_items SET status='resolved',resolution='superseded',resolved_at=now(),updated_at=now()
	WHERE target_id=$1 AND kind IN ('release_match','recording_match') AND status='in_progress'`, e.ID); err != nil {
		return err
	}
	if m.Status == "matched" {
		return curation.ResolvePendingItems(ctx, tx, e.ID, kind, "program_matched")
	}
	if m.Route == "" {
		return curation.ResolvePendingItems(ctx, tx, e.ID, kind, state.Status)
	}
	if len(m.Candidates) == 0 && slices.Contains(m.Conflicts, "insufficient_evidence") {
		return curation.ResolvePendingItems(ctx, tx, e.ID, kind, "insufficient_evidence")
	}
	evidence := pendingMatchEvidence(e, m.Method)
	evidence["match_snapshot"] = m.savedSnapshot()
	evidence["generation"] = state.Generation
	evidence["classification"] = e.Classification().Class
	return curation.UpsertPendingItem(ctx, tx, curation.PendingItem{TargetID: e.ID, Kind: kind, Route: m.Route, RuleVersion: state.RuleVersion,
		InputDigest: state.InputDigest, MatchRuleVersion: state.MatchRuleVersion, IdentityDigest: state.IdentityDigest, Evidence: jsonValue(evidence), Candidates: jsonValue(m.modelCandidates()), Conflicts: jsonValue(m.Conflicts)})
}

// 保存查询中断，不生成匹配结论、待定项或新的写入代次。
func (w *Worker) saveQueryFailure(ctx context.Context, state, before curation.WorkState, failed *stepFailure) error {
	retry := failed.retry
	a := currentAttempt(ctx)
	if a == nil {
		return failed.err
	}
	if a.item.Generation != before.Generation || a.item.InputDigest != state.IdentityDigest {
		return errReleaseChanged
	}
	state.FileDigest = a.item.Files
	retry.Files = a.item.Files
	source := "musicbrainz"
	var sample *sampleFailure
	if errors.As(failed, &sample) {
		source = sample.source
	}
	retry.Source = source
	prior, err := previousReview(before)
	if err != nil {
		return err
	}
	if prior.Retry == nil || prior.Retry.Step != retry.Step || before.IdentityDigest != state.IdentityDigest || before.MatchRuleVersion != state.MatchRuleVersion {
		state.RetryCount = 0
	}
	var pause *sources.QueuePause
	switch {
	case errors.Is(failed, sources.ErrInvalidResponse), errors.Is(failed, sources.ErrInvalidQuery), errors.Is(failed, errLocalPrint):
		retry.Kind, retry.Reason = "album", "invalid_response"
		if source == "local_fingerprint" {
			retry.Reason = "fingerprint_failed"
		}
		if errors.Is(failed, sources.ErrInvalidQuery) {
			retry.Reason = "invalid_query"
		}
		state.RetryCount++
		if state.RetryCount >= 3 {
			state.Status, state.NextAttemptAt = "query_error", nil
		} else {
			next := w.opts.Now().Add(sources.SourceRetryDelay(state.RetryCount - 1))
			state.Status, state.NextAttemptAt = "try_later", &next
		}
	case errors.As(sourceFailure(ctx, source, failed), &pause):
		retry.Kind, retry.Source, retry.Reason = "source", pause.Source, pause.Reason
		state.Status = "try_later"
	default:
		return failed.err
	}
	if err := w.ready(ctx); err != nil {
		return err
	}
	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	entries, err := catalog.LoadCurationEvidence(ctx, tx, []string{state.TargetID}, w.writer.ArtworkExists)
	if err != nil {
		return err
	}
	e, ok := entries[state.TargetID]
	if !ok || IdentityDigest(e) != state.IdentityDigest {
		return errReleaseChanged
	}
	var locked int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM curation_work_states WHERE subject='release' AND target_id=$1 AND
        generation=$2 AND status=$3 AND updated_at=$4 AND queue_identity=$5 AND manual_requested_at IS NOT DISTINCT FROM $6::timestamptz AND curation_release_file_digest(target_id)=$7 FOR UPDATE`,
		state.TargetID, before.Generation, before.Status, before.UpdatedAt, state.IdentityDigest, before.ManualRequestedAt, retry.Files).Scan(&locked)
	if errors.Is(err, sql.ErrNoRows) {
		return errReleaseChanged
	}
	if err != nil {
		return err
	}
	var result map[string]json.RawMessage
	if len(state.Result) > 0 {
		if err := json.Unmarshal(state.Result, &result); err != nil {
			return err
		}
	}
	if result == nil {
		result = make(map[string]json.RawMessage)
	}
	if before.ManualRequestedAt != nil {
		result["manual_recheck"] = jsonValue(true)
	}
	result["retry"] = jsonValue(retry)
	if retry.Step == "model_hints" {
		var cursor json.RawMessage
		if err = tx.QueryRowContext(ctx, `SELECT result->'judge' FROM curation_work_states WHERE subject='release' AND target_id=$1`, state.TargetID).Scan(&cursor); err != nil {
			return err
		}
		result["judge"] = cursor
	}
	state.Result = jsonValue(result)
	if pause != nil {
		if current := w.policy.Pause(pause.Source); current != nil {
			state.NextAttemptAt = &current.RetryAt
		} else {
			now := w.opts.Now()
			state.NextAttemptAt = &now
		}
	}
	if err := curation.SaveWorkState(ctx, tx, state); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	w.dropPrints(state.TargetID)
	if pause != nil {
		// 提交期间来源可能已恢复，也可能进入了新的暂停窗口。
		current := w.policy.Pause(pause.Source)
		next := *state.NextAttemptAt
		if current != nil {
			next = current.RetryAt
		} else if now := w.opts.Now(); now.Before(next) {
			next = now
		}
		if !next.Equal(*state.NextAttemptAt) {
			_, err := w.db.ExecContext(ctx, `UPDATE curation_work_states SET next_attempt_at=$1 WHERE subject='release' AND target_id=$2
              AND status='try_later' AND generation=$3 AND result->'retry'->>'source'=$4 AND next_attempt_at=$5`, next, state.TargetID, state.Generation, pause.Source, state.NextAttemptAt)
			if err != nil {
				return err
			}
		}
		if current == nil {
			return errAlbumWaiting
		}
		return current
	}
	if state.Status == "try_later" {
		return errAlbumWaiting
	}
	return nil
}
