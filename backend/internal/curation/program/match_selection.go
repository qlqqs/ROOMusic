package program

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

func (m *ModelMatcher) SelectMatch(ctx context.Context, id, actor, key string, in curation.MatchChoice) (curation.ReleaseMatchResult, error) {
	var out curation.ReleaseMatchResult
	if !uuid.Valid(id) || !uuid.Valid(key) || !uuid.Valid(in.ExternalID) || !uuid.Valid(in.SnapshotID) || in.Source != "musicbrainz" {
		return out, operations.Invalid("invalid_input", "选择参数无效")
	}
	// 在多步治理操作之间串行化同张发行的选择。事务关闭自动释放锁。
	serial, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer serial.Rollback()
	if err := lockMatchSelection(ctx, serial, id); err != nil {
		return out, err
	}
	var raw, candidates []byte
	var snapshot, status, files string
	var state []byte
	err = serial.QueryRowContext(ctx, `SELECT coalesce(m.snapshot_id::text,''),m.status,m.selection,m.candidates,s.file_digest,s.result FROM curation_release_match_results m JOIN curation_work_states s ON s.subject='release' AND s.target_id=m.release_id JOIN curation_eligible_releases e ON e.id=s.target_id WHERE m.release_id=$1 AND m.source=$2 AND s.file_digest=curation_release_file_digest(m.release_id)`, id, in.Source).Scan(&snapshot, &status, &raw, &candidates, &files, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return out, staleItem()
	}
	if err != nil {
		return out, err
	}
	readResult := func() (curation.ReleaseMatchResult, error) {
		rows, err := m.writer.ReleaseMatchResults(ctx, id)
		for _, r := range rows {
			if r.Source == in.Source {
				return r, nil
			}
		}
		return out, err
	}
	var saved struct {
		Key        string
		SnapshotID string `json:"snapshot_id"`
		Input      string
		Plan       json.RawMessage
		curation.MatchSelection
	}
	if len(raw) > 0 {
		if err = json.Unmarshal(raw, &saved); err != nil {
			return out, err
		}
		if saved.Key != key || saved.SnapshotID != in.SnapshotID || saved.ExternalID != in.ExternalID || saved.SelectedBy != actor {
			return out, staleItem()
		}
		if status == "matched" {
			result, err := readResult()
			if err == nil && result.Selection == nil {
				return out, staleItem()
			}
			return result, err
		}
	}
	if status != "conflict" || snapshot != in.SnapshotID {
		return out, staleItem()
	}
	var rows []Candidate
	if err = json.Unmarshal(candidates, &rows); err != nil {
		return out, err
	}
	var selected Candidate
	for _, c := range rows {
		if c.ExternalID == in.ExternalID {
			selected = c
			break
		}
	}
	if selected.ExternalID == "" {
		return out, staleItem()
	}
	var plan MatchPlan
	if len(saved.Plan) > 0 {
		plan, err = m.ResumeConfirmation(ctx, curation.PendingItem{TargetID: id, InputDigest: saved.Input}, saved.Plan)
	} else {
		var stored struct {
			Snapshot MatchSnapshot `json:"match_snapshot"`
		}
		if err = json.Unmarshal(state, &stored); err != nil {
			return out, err
		}
		e, err := m.evidence(ctx, m.db, id)
		if err != nil {
			return out, err
		}
		selected.Source = in.Source
		plan, err = m.prepareSelected(ctx, e, selected, stored.Snapshot, false)
		if err != nil {
			return out, err
		}
	}
	if err != nil {
		return out, err
	}
	plan.write.links.RequireAll = true
	proof, err := plan.ConfirmationSnapshot()
	if err != nil {
		return out, err
	}
	receipt := jsonValue(map[string]any{"external_id": in.ExternalID, "selected_by": actor, "snapshot_id": snapshot, "key": key, "input": plan.item.InputDigest, "plan": json.RawMessage(proof)})
	source := curation.Source{WriterLevel: catalog.LevelProgramExternal, Kind: "source", Provider: in.Source, Basis: "人工选定并通过 MusicBrainz 结构核对"}
	opt := ApplyOptions{Key: key, ConfirmedBy: actor, Source: source}
	opt.checkSelection = func(ctx context.Context, tx *sql.Tx) error {
		// 第一个治理操作与重放方案同事务保存；其他候选不能接管半完成的操作。
		res, err := tx.ExecContext(ctx, `UPDATE curation_release_match_results SET selection=coalesce(selection,$5::jsonb)
 WHERE release_id=$1 AND source=$2 AND snapshot_id=$3 AND status='conflict' AND curation_release_file_digest(release_id)=$4
 AND (selection IS NULL OR selection->>'key'=$6)`, id, in.Source, snapshot, files, receipt, key)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err == nil && n != 1 {
			return staleItem()
		}
		return err
	}
	opt.Finish = func(ctx context.Context, tx *sql.Tx, ids []string) error {
		saved := map[string]any{"external_id": in.ExternalID, "selected_by": actor, "selected_at": time.Now().UTC(), "operation_ids": ids, "key": key, "snapshot_id": snapshot}
		_, err := tx.ExecContext(ctx, `UPDATE curation_release_match_results SET selection=$3,snapshot_id=$4 WHERE release_id=$1 AND source=$2`, id, in.Source, jsonValue(saved), snapshot)
		if err != nil {
			return err
		}
		for _, kind := range []string{"release_match", "recording_match"} {
			if err := curation.ResolvePendingItems(ctx, tx, id, kind, "user_selected"); err != nil {
				return err
			}
		}
		return nil
	}
	if _, err = m.Apply(ctx, plan, opt); err != nil {
		return out, err
	}
	return readResult()
}

func lockMatchSelection(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,1))`, id)
	return err
}

func (m *ModelMatcher) prepareSelected(ctx context.Context, e catalog.CurationEvidence, selected Candidate, snapshot MatchSnapshot, external bool) (MatchPlan, error) {
	f, ok := m.sources.ReleaseFinder(selected.Source)
	if !ok && selected.Entity != "recording" {
		return MatchPlan{}, sources.ErrUnavailable
	}
	rf, _ := m.sources.RecordingFinder(selected.Source)
	mode := "album"
	if selected.Entity == "recording" {
		mode = "single"
	}
	selected.Release = nil
	match, err := checkHintCandidate(ctx, e, mode, &selected, selected.Method, curation.JudgeGroup{}, snapshot, f, rf, false, true)
	if err != nil {
		return MatchPlan{}, err
	}
	if match.Status != "matched" || match.Level == "artist" || !external && match.Level == "release_group" {
		if !external {
			return MatchPlan{}, matchConflict("insufficient_corroboration")
		}
		if slices.Contains(match.Conflicts, "suppressed") {
			return MatchPlan{}, externalSuppressed()
		}
		codes := match.Conflicts
		if len(codes) == 0 {
			codes = []string{"external_id_mismatch"}
		}
		return MatchPlan{}, insufficientCorroboration(codes)
	}
	item := curation.PendingItem{TargetID: e.ID, InputDigest: ReleaseDigest(e)}
	if external && match.Release != nil {
		var positions comparison
		positions.compareTracks(e, "model", *match.Release)
		if slices.Contains(positions.conflicts, "position_conflict") || slices.ContainsFunc(match.Pairs, func(p TrackPair) bool { return p.RecordingID == "" }) {
			return MatchPlan{}, insufficientCorroboration([]string{"position_conflict"})
		}
		match.Verification = match.Candidates[0].Verification
		if match.Verification.Contradicted {
			return MatchPlan{}, insufficientCorroboration(match.Verification.Conflicts)
		}
		checked, err := m.checkModelEditions(ctx, e, item, match)
		if err != nil {
			return MatchPlan{}, err
		}
		match = checked
	}
	if external && match.Level == "release_group" && match.GroupFirstReleaseDate == "" && e.Year == 0 && !e.Corrected["year"] && !e.Automatic["year"] {
		date, err := m.sources.GroupFirstReleaseDate(ctx, selected.Source, match.GroupID)
		if err != nil {
			return MatchPlan{}, err
		}
		match.GroupFirstReleaseDate = date
	}
	p, err := planMatch(ctx, m.db, e, match)
	if err != nil {
		return MatchPlan{}, err
	}
	p.links.RequireAll = true
	return MatchPlan{write: p, match: match, item: item, Target: e.Title}, nil
}

func insufficientCorroboration(codes []string) error {
	return operations.Conflict("insufficient_corroboration", "外部资料未通过核对（"+strings.Join(codes, "、")+"）")
}
