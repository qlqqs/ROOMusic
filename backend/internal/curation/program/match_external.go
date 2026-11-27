package program

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type ExternalMatchInput struct {
	ReleaseID string `json:"release_id"`
	Entity    string `json:"entity"`
	MBID      string `json:"mbid"`
}

func (in *ExternalMatchInput) normalize() error {
	if !uuid.Valid(in.ReleaseID) || !uuid.Valid(in.MBID) || in.Entity != "release" && in.Entity != "recording" {
		return operations.Invalid("invalid_input", "请输入有效的发行编号、外部类型和 MusicBrainz 编号")
	}
	in.ReleaseID, in.MBID = strings.ToLower(in.ReleaseID), strings.ToLower(in.MBID)
	return nil
}

type ExternalMatchPreview struct {
	Target      string
	Edition     curation.ExternalSummary
	Level       string
	MatchedMBID string
	Changes     []MatchChange
}

type ExternalMatchResult struct {
	ExternalMatchInput
	MatchApplied
	Level       string `json:"level"`
	MatchedMBID string `json:"matched_mbid"`
	Message     string `json:"message"`
}

func (in ExternalMatchInput) matchedMBID(m MatchResult) string {
	if m.Level == "release_group" {
		return m.GroupID
	}
	return in.MBID
}

func externalSuppressed() error {
	return operations.Conflict("suppressed", "这个对应已标记为不再提出，请先到自动整理设置的“不再提出”中清除")
}

func (m *ModelMatcher) planExternal(ctx context.Context, tx *sql.Tx, in ExternalMatchInput) (MatchPlan, curation.WorkState, error) {
	var state curation.WorkState
	// 此处直接读目录证据。预览也经过这里，不能登记自动字段作废操作。
	entries, err := catalog.LoadCurationEvidence(ctx, tx, []string{in.ReleaseID}, m.writer.ArtworkExists)
	if err != nil {
		return MatchPlan{}, state, err
	}
	e, visible := entries[in.ReleaseID]
	if !visible {
		return MatchPlan{}, state, operations.NotFound("not_found", "发行不存在或不可见")
	}
	var eligible bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM curation_eligible_releases WHERE id=$1)`, e.ID).Scan(&eligible); err != nil {
		return MatchPlan{}, state, err
	}
	if !eligible {
		return MatchPlan{}, state, operations.Conflict("not_eligible", "发行没有自动整理资格")
	}
	if e.ExternalID != "" || e.ReleaseGroupMatchID != "" || e.CandidateKind == "loose_unknown" && len(e.Tracks) == 1 && e.Tracks[0].ExternalID != "" {
		return MatchPlan{}, state, operations.Conflict("already_matched", "已有 MusicBrainz 对应，请先用 revert_operation 撤销原对应")
	}
	target, id := "release", e.ID
	if in.Entity == "recording" {
		if e.CandidateKind != "loose_unknown" || len(e.Tracks) != 1 {
			return MatchPlan{}, state, operations.Invalid("invalid_input", "录音对应只适用于单轨散曲")
		}
		target, id = "track", e.Tracks[0].ID
	} else if e.CandidateKind == "loose_unknown" {
		return MatchPlan{}, state, operations.Invalid("invalid_input", "散曲须使用录音对应")
	}
	suppressed, err := curation.LinkSuppressed(ctx, tx, target, id, "musicbrainz", in.MBID)
	if err != nil {
		return MatchPlan{}, state, err
	}
	if suppressed {
		return MatchPlan{}, state, externalSuppressed()
	}
	states, err := curation.LoadWorkStates(ctx, tx, "release", []string{e.ID})
	if err != nil {
		return MatchPlan{}, state, err
	}
	c := Candidate{ExternalID: in.MBID, Entity: in.Entity, Method: "model", Source: "musicbrainz"}
	plan, err := m.prepareSelected(ctx, e, c, MatchSnapshot{}, true)
	return plan, states[e.ID], err
}

func (m *ModelMatcher) PreviewExternal(ctx context.Context, in ExternalMatchInput, actor string) (ExternalMatchPreview, error) {
	if err := in.normalize(); err != nil {
		return ExternalMatchPreview{}, err
	}
	tx, err := m.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return ExternalMatchPreview{}, err
	}
	defer tx.Rollback()
	ctx = curation.WithManualSourceActor(ctx, actor)
	plan, _, err := m.planExternal(ctx, tx, in)
	if err != nil {
		return ExternalMatchPreview{}, err
	}
	out := ExternalMatchPreview{Target: plan.Target, Edition: plan.write.links.External, Level: plan.match.Level, MatchedMBID: in.matchedMBID(plan.match)}
	if c := plan.match.Candidates[0]; c.Release != nil {
		out.Edition = curation.ExternalSummary{Title: c.Title, Artist: c.Artist, Date: c.Date, Country: c.Country, DiscCount: len(c.Release.Media), TrackCount: c.TrackCount}
	} else {
		out.Edition.TrackCount = 1
	}
	for _, field := range slices.Sorted(maps.Keys(plan.write.fields)) {
		out.Changes = append(out.Changes, MatchChange{Field: field, After: plan.write.fields[field].Value})
	}
	tracks := make(map[string]catalog.CurationTrack, len(plan.write.evidence.Tracks))
	for _, t := range plan.write.evidence.Tracks {
		tracks[t.ID] = t
	}
	for _, r := range plan.write.tracks {
		t := tracks[r.TrackID]
		for _, field := range slices.Sorted(maps.Keys(r.Fields)) {
			if field == "title" && (t.TitleCorrected || t.TitleAutomatic && t.LocalTitle == "") || field == "artist" && (t.ArtistCorrected || t.ArtistAutomatic && t.LocalArtist == "") {
				continue
			}
			out.Changes = append(out.Changes, MatchChange{Field: "track/" + r.TrackID + "/" + field, After: r.Fields[field].Value})
		}
	}
	return out, tx.Commit()
}

func (m *ModelMatcher) SelectExternal(ctx context.Context, in ExternalMatchInput, actor, key string, origin operations.Origin, requestID string) (ExternalMatchResult, error) {
	var out ExternalMatchResult
	if err := in.normalize(); err != nil {
		return out, err
	}
	if !uuid.Valid(actor) || !uuid.Valid(key) || origin.Source != "chat" && origin.Source != "mcp" {
		return out, operations.Invalid("invalid_input", "操作者、幂等键或管家来源无效")
	}
	ctx = curation.WithManualSourceActor(ctx, actor)
	err := m.writer.StewardMatchWrite(ctx, origin, requestID, func(tx *sql.Tx, writer *curation.Writer) error {
		if err := lockMatchSelection(ctx, tx, in.ReleaseID); err != nil {
			return err
		}
		saved, err := operations.LookupResult(ctx, tx, actor, key)
		if err == nil {
			if err := json.Unmarshal(saved.Response, &out); err != nil {
				return err
			}
			if out.ExternalMatchInput != in || out.Level == "" {
				return operations.Conflict("idempotency_conflict", "幂等键已用于其他操作")
			}
			return nil
		}
		if !errors.Is(err, operations.ErrNotFound) {
			return err
		}
		plan, state, err := m.planExternal(ctx, tx, in)
		if err != nil {
			return err
		}
		// 艺人身份与拆名留给对应后的回填轮次。
		plan.write.albumCredit = nil
		for i := range plan.write.tracks {
			plan.write.tracks[i].ArtistCredit = nil
		}
		scoped := *m
		scoped.writer = writer
		opt := ApplyOptions{selectionTx: tx, groupChangeSource: origin.Source, Key: key, ConfirmedBy: actor, ConversationID: origin.ConversationID,
			Source: curation.Source{Kind: "source", Provider: "steward", WriterLevel: catalog.LevelProgramExternal, Basis: "管家选定并通过 MusicBrainz 结构核对"}}
		locked := false
		opt.checkSelection = func(ctx context.Context, tx *sql.Tx) error {
			if locked {
				return nil
			}
			if err := lockExternalState(ctx, tx, plan.write.evidence, state); err != nil {
				return err
			}
			locked = true
			return nil
		}
		opt.Finish = func(ctx context.Context, tx *sql.Tx, ids []string) error {
			if len(ids) == 0 {
				return staleItem()
			}
			out = ExternalMatchResult{ExternalMatchInput: in, MatchApplied: MatchApplied{OperationID: ids[0], OperationIDs: ids}, Level: plan.match.Level, MatchedMBID: in.matchedMBID(plan.match), Message: "署名、封面等稍后自动补齐"}
			// 旧的冲突选择不属于这次新对应。
			if _, err := tx.ExecContext(ctx, `UPDATE curation_release_match_results SET selection=NULL, checked_at=statement_timestamp() WHERE release_id=$1 AND source='musicbrainz'`, in.ReleaseID); err != nil {
				return err
			}
			// 运行时也按首条操作的键恢复结果，整次执行的收据随结案一起落库。
			_, err := tx.ExecContext(ctx, `UPDATE operations SET response=response || $2::jsonb WHERE id=$1`, ids[0], jsonValue(out))
			return err
		}
		_, err = scoped.Apply(ctx, plan, opt)
		return err
	})
	if retrySnapshot(err) {
		return ExternalMatchResult{}, staleItem()
	}
	if err != nil {
		return ExternalMatchResult{}, err
	}
	return out, nil
}

// 写入目录锁内按后台结案的顺序先结束待定项，再锁工作状态。任何复核失败都随整次对应回滚。
func lockExternalState(ctx context.Context, tx *sql.Tx, e catalog.CurationEvidence, state curation.WorkState) error {
	for _, kind := range []string{"release_match", "recording_match"} {
		if err := curation.ResolvePendingItems(ctx, tx, e.ID, kind, "user_selected"); err != nil {
			return err
		}
	}
	if state.TargetID == "" {
		r, err := tx.ExecContext(ctx, `INSERT INTO curation_work_states(subject,target_id,rule_version,input_digest,status)
 VALUES('release',$1,$2,'','pending') ON CONFLICT DO NOTHING`, e.ID, MatchRuleVersion)
		if err != nil {
			return err
		}
		n, err := r.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return staleItem()
		}
		state.Status = "pending"
	}
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM curation_work_states s JOIN curation_eligible_releases e ON e.id=s.target_id
 WHERE s.subject='release' AND s.target_id=$1 AND curation_release_file_digest(e.id)=$2 AND s.generation=$3 AND s.status=$4 FOR UPDATE OF s)`,
		e.ID, e.FileDigest, state.Generation, state.Status).Scan(&valid)
	if err == nil && !valid {
		return staleItem()
	}
	return err
}
