package model

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type ExceptionResolver struct {
	accepting sync.Map
	db        *sql.DB
	writer    *curation.Writer
	matcher   *program.ModelMatcher
}

func NewExceptionResolver(db *sql.DB, writer *curation.Writer, matcher *program.ModelMatcher) *ExceptionResolver {
	return &ExceptionResolver{db: db, writer: writer, matcher: matcher}
}

type proposal struct {
	Source     string                                    `json:"source"`
	TrackID    string                                    `json:"track_id"`
	ItemID     string                                    `json:"item_id"`
	ReleaseID  string                                    `json:"release_id"`
	ExternalID string                                    `json:"external_id"`
	Changes    map[string]governance.RawCorrectionChange `json:"changes"`
	Basis      struct {
		Note string `json:"note"`
	} `json:"basis"`
}

func staleException() error {
	return operations.Conflict("exception_stale", "例外的预览已变更")
}
func decidedException() error { return operations.Conflict("exception_decided", "例外已处理") }

func (r *ExceptionResolver) load(ctx context.Context, id string) (curation.ModelException, proposal, error) {
	var in proposal
	if !uuid.Valid(id) {
		return curation.ModelException{}, in, operations.Invalid("invalid_id", "例外标识无效")
	}
	e, err := r.writer.Exception(ctx, id)
	if err == nil {
		err = json.Unmarshal(e.Input, &in)
	}
	return e, in, err
}

func (r *ExceptionResolver) Accept(ctx context.Context, id, actor, requestID string) (curation.ModelException, error) {
	e, in, err := r.load(ctx, id)
	if err != nil {
		return e, err
	}
	// 同一管理员可以续接中断的采纳；同一例外同时只允许一个执行者准备快照。
	if _, busy := r.accepting.LoadOrStore(e.ID, struct{}{}); busy {
		return e, decidedException()
	}
	defer r.accepting.Delete(e.ID)
	if err = r.writer.ClaimException(ctx, id, actor); err != nil {
		return e, err
	}
	key := curation.DerivedKey(e.ID, "accept")
	ctx = operations.WithReviewOrigin(ctx, e.Review.ID, "human")
	switch e.Tool {
	case "correct_release", "correct_track":
		err = r.acceptCorrection(ctx, e, in, actor, key, requestID)
	case "merge_releases":
		err = r.acceptMerge(ctx, e, in, actor, key, requestID)
	case "resolve_artist_identity":
		err = r.acceptArtistIdentity(ctx, e, in, actor, key)
	case "resolve_release_match":
		err = r.acceptMatch(ctx, e, in, actor, key)
	}
	if err != nil {
		var failure *operations.Failure
		if errors.As(err, &failure) || curation.IsSourceUnavailable(err) {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			// 多步采纳一旦已有操作，就保留原操作者、原快照和幂等键。
			written, checkErr := r.matchStarted(cleanup, e, actor, key)
			if checkErr != nil {
				return e, errors.Join(err, checkErr)
			}
			if !written || (failure != nil && failure.Code == "exception_stale") {
				err = errors.Join(err, r.writer.ReopenException(cleanup, e.ID, actor))
			}
		}
		return e, err
	}
	return r.writer.Exception(ctx, id)
}

func compareChanges(e curation.ModelException, changes []program.MatchChange) error {
	var saved struct {
		Changes []program.MatchChange `json:"changes"`
	}
	if err := json.Unmarshal(e.Preview, &saved); err != nil {
		return err
	}
	raw, err := json.Marshal(changes)
	if err != nil {
		return err
	}
	var current []program.MatchChange
	if err = json.Unmarshal(raw, &current); err != nil {
		return err
	}
	if !reflect.DeepEqual(saved.Changes, current) {
		return staleException()
	}
	return nil
}

func correctionIntent(name string, c governance.CorrectionChange) catalog.CorrectionIntent {
	value := catalog.CorrectionIntent{Mode: c.Mode}
	if c.Mode == catalog.CorrectionSet {
		switch name {
		case "year":
			value.Value = c.Year
		case "genre":
			value.Value = c.Genres
		default:
			value.Value = c.Text
		}
	}
	return value
}

func (r *ExceptionResolver) acceptCorrection(ctx context.Context, e curation.ModelException, in proposal, actor, key, requestID string) error {
	item, err := r.writer.PendingItem(ctx, e.PendingItemID)
	if err != nil {
		return err
	}
	if item.Status != "waiting_human" || !strings.EqualFold(in.ReleaseID, item.TargetID) {
		return staleException()
	}
	parse := governance.ParseCorrectionChanges
	kind := operations.TypeReleaseCorrection
	if e.Tool == "correct_track" {
		if !uuid.Valid(in.TrackID) {
			return staleException()
		}
		parse = governance.ParseTrackCorrectionChanges
		kind = governance.TypeTrackCorrection
	}
	fields, err := parse(in.Changes)
	if err != nil {
		return err
	}
	origin := operations.Origin{Source: "curation", ReviewID: e.Review.ID, ReviewDecision: "human"}
	if e.ConversationID != nil {
		origin.ConversationID = *e.ConversationID
	}
	spec := operations.Spec{Type: kind, ActorID: actor, Key: key, RequestID: requestID,
		Fingerprint: operations.Fingerprint(map[string]any{"exception_id": e.ID, "release_id": item.TargetID, "changes": fields}), Origin: &origin}
	_, err = operations.Run(ctx, r.db, spec, func(ctx context.Context, tx *sql.Tx, id string) (operations.Outcome, error) {
		// 扫描与自动层写入也会改变有效前值；在目录写锁内重新预览并执行。
		var currentFields []governance.CorrectionField
		var execute operations.Executor
		if e.Tool == "correct_track" {
			track, err := governance.LockAutoTrack(ctx, tx, in.TrackID)
			if err != nil {
				return operations.Outcome{}, err
			}
			if track.ReleaseID() != item.TargetID {
				return operations.Outcome{}, staleException()
			}
			currentFields = track.State().Fields
			execute = governance.TrackCorrectionExecutor(governance.TrackCorrectionRequest{TrackID: in.TrackID, ReleaseID: item.TargetID, ExpectedRevision: track.EditRevision(), Changes: fields})
		} else {
			if _, err := governance.LockAutoRelease(ctx, tx, item.TargetID); err != nil {
				return operations.Outcome{}, err
			}

			state, err := governance.NewStore(tx).LoadCorrectionState(ctx, item.TargetID)
			if err != nil {
				return operations.Outcome{}, err
			}
			currentFields = state.Fields
			execute = governance.CorrectionExecutor(governance.CorrectionRequest{ReleaseID: item.TargetID, ExpectedRevision: state.Revision, Changes: fields})
		}
		if err := curation.CheckMatchItem(ctx, tx, item, "", true); err != nil {
			return operations.Outcome{}, err
		}
		var changes []program.MatchChange
		for _, field := range currentFields {
			change, ok := fields[field.Field]
			if !ok {
				continue
			}
			if change.Mode == catalog.CorrectionClear {
				return operations.Outcome{}, staleException()
			}
			changes = append(changes, program.MatchChange{Field: field.Field, Before: field.EffectiveValue, After: correctionIntent(field.Field, change).Value})
		}
		if err = compareChanges(e, changes); err != nil {
			return operations.Outcome{}, err
		}
		out, err := execute(ctx, tx, id)
		if err != nil {
			return out, err
		}
		return out, curation.FinishException(ctx, tx, e.ID, actor, "accepted", []string{id})
	})
	return classifyChange(err)
}

func classifyChange(err error) error {
	var f *operations.Failure
	if errors.As(err, &f) {
		switch f.Code {
		case "item_stale", "not_found", "revision_conflict", "release_not_found", "track_count", "position_conflict", "media_count_mismatch", "existing_link", "external_id_mismatch", "suppressed", "identity_mismatch", "not_an_artist":
			return staleException()
		case "curation_paused", "scan_running":
			return operations.Conflict("curation_disabled", f.Message)
		case "no_changes":
			return operations.Conflict("nothing_to_apply", "没有可执行的修改")
		}
	}
	if curation.IsMissingSourceMatch(err) {
		return staleException()
	}
	return err
}

func (r *ExceptionResolver) acceptMerge(ctx context.Context, e curation.ModelException, in proposal, actor, key, requestID string) error {
	item, err := r.writer.PendingItem(ctx, e.PendingItemID)
	if err != nil {
		return err
	}
	if item.Status != "waiting_human" || !strings.EqualFold(item.ID, in.ItemID) {
		return staleException()
	}
	p, err := r.writer.PreviewReleaseMerge(ctx, item)
	if err == nil {
		req := curation.ReleaseAutoMerge{ItemID: item.ID, Token: p.Token, ConfirmedBy: actor, ExceptionID: e.ID, Key: key, RequestID: requestID,
			Source: curation.Source{Kind: "model", Provider: e.Provider, Basis: truncateBasis(in.Basis.Note)},
			CheckPreview: func(current governance.ReleaseMergePreview) error {
				return compareChanges(e, program.ReleaseMergeChanges(current.Source, current.Target, current.Token, current.Hidden, current.FavoritesMoved))
			}}
		if e.ConversationID != nil {
			req.ConversationID = *e.ConversationID
		}
		_, err = r.writer.MergeReleases(ctx, req)
	}
	var f *operations.Failure
	if errors.As(err, &f) {
		switch f.Code {
		case "merge_changed", "release_merged", "merge_chain", "release_too_large", "manual_structure", "no_strong_evidence", "nothing_to_apply":
			return staleException()
		}
	}
	return classifyChange(err)
}

func truncateBasis(note string) string {
	end := min(500, len(note))
	for end < len(note) && !utf8.RuneStart(note[end]) {
		end--
	}
	return note[:end]
}

func (r *ExceptionResolver) acceptArtistIdentity(ctx context.Context, e curation.ModelException, in proposal, actor, key string) error {
	item, err := r.writer.PendingItem(ctx, e.PendingItemID)
	if err != nil {
		return err
	}
	if item.Kind != "artist_identity" || item.Status != "waiting_human" || !strings.EqualFold(in.ItemID, item.ID) {
		return staleException()
	}
	plan, err := r.matcher.PlanArtistIdentity(ctx, item, in.Source, in.ExternalID)
	if err != nil {
		return classifyChange(err)
	}
	if err = compareChanges(e, plan.Changes); err != nil {
		return err
	}
	opt := program.ApplyOptions{
		Key: key, ConfirmedBy: actor, PendingItemID: item.ID,
		Source: curation.Source{Kind: "model", Provider: e.Provider, Basis: truncateBasis(in.Basis.Note)},
		Finish: func(ctx context.Context, tx *sql.Tx, ids []string) error {
			return curation.FinishException(ctx, tx, e.ID, actor, "accepted", ids)
		},
	}
	if e.ConversationID != nil {
		opt.ConversationID = *e.ConversationID
	}
	_, err = r.matcher.ApplyArtistIdentity(ctx, plan, opt)
	return classifyChange(err)
}

func (r *ExceptionResolver) acceptMatch(ctx context.Context, e curation.ModelException, in proposal, actor, key string) error {
	item, err := r.writer.PendingItem(ctx, e.PendingItemID)
	if err != nil {
		return err
	}
	if item.Status != "waiting_human" || !strings.EqualFold(in.ItemID, item.ID) {
		return staleException()
	}
	plan, err := r.confirmedPlan(ctx, e, item, in.ExternalID, actor, key)
	if err != nil {
		return classifyChange(err)
	}
	opt := program.ApplyOptions{Key: key, ConfirmedBy: actor, PendingItemID: item.ID, Source: curation.Source{Kind: "model", Provider: e.Provider, Basis: truncateBasis(in.Basis.Note)},
		Finish: func(ctx context.Context, tx *sql.Tx, ids []string) error {
			return curation.FinishException(ctx, tx, e.ID, actor, "accepted", ids)
		}}
	if e.ConversationID != nil {
		opt.ConversationID = *e.ConversationID
	}
	_, err = r.matcher.Apply(ctx, plan, opt)
	return classifyChange(err)
}

func (r *ExceptionResolver) matchStarted(ctx context.Context, e curation.ModelException, actor, key string) (bool, error) {
	if e.Tool != "resolve_release_match" {
		return false, nil
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	_, err = operations.LookupResult(ctx, tx, actor, key)
	if errors.Is(err, operations.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (r *ExceptionResolver) confirmedPlan(ctx context.Context, e curation.ModelException, item curation.PendingItem, external, actor, key string) (program.MatchPlan, error) {
	started, err := r.matchStarted(ctx, e, actor, key)
	if err != nil {
		return program.MatchPlan{}, err
	}
	if started {
		var snapshot []byte
		err := r.db.QueryRowContext(ctx, `SELECT preview->'confirmation_snapshot' FROM curation_model_exceptions WHERE id=$1 AND status='accepting' AND decided_by=$2`, e.ID, actor).Scan(&snapshot)
		if err != nil {
			return program.MatchPlan{}, err
		}
		if len(snapshot) == 0 {
			return program.MatchPlan{}, staleException()
		}
		return r.matcher.ResumeConfirmation(ctx, item, snapshot)
	}
	plan, err := r.matcher.Plan(ctx, item, external)
	if err != nil {
		return plan, err
	}
	if err = compareChanges(e, plan.Changes); err != nil {
		return plan, err
	}
	snapshot, err := plan.ConfirmationSnapshot()
	if err != nil {
		return plan, err
	}
	var id string
	err = r.db.QueryRowContext(ctx, `UPDATE curation_model_exceptions SET preview=jsonb_set(preview,'{confirmation_snapshot}',$3::jsonb)
	WHERE id=$1 AND status='accepting' AND decided_by=$2 RETURNING id`, e.ID, actor, snapshot).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return plan, decidedException()
	}
	return plan, err
}

func (r *ExceptionResolver) Decline(ctx context.Context, id, actor string) (curation.ModelException, error) {
	e, in, err := r.load(ctx, id)
	if err != nil {
		return e, err
	}
	if e.Status != "open" {
		return e, decidedException()
	}
	item, err := r.writer.PendingItem(ctx, e.PendingItemID)
	if err != nil {
		return e, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return e, err
	}
	defer tx.Rollback()
	if e.Tool == "merge_releases" {
		if err = governance.LockCatalogWrites(ctx, tx); err != nil {
			return e, err
		}
	}
	if e.Tool == "resolve_artist_identity" {
		if err = catalog.AcquireArtistWriteGate(ctx, tx); err != nil {
			return e, err
		}
		ids, err := catalog.NewStore(tx).ResolveArtists(ctx, []string{item.ArtistID})
		if err != nil {
			return e, err
		}
		if ids[item.ArtistID] != item.ArtistID || item.Status != "waiting_human" {
			return e, staleException()
		}
		if err = curation.CheckMatchItem(ctx, tx, item, "", true); err != nil {
			return e, classifyChange(err)
		}
	}
	if err = curation.FinishException(ctx, tx, id, actor, "declined", nil); err != nil {
		return e, err
	}
	switch {
	case e.Tool == "resolve_artist_identity":
		externalID := in.ExternalID
		if in.Source == "musicbrainz" {
			externalID = strings.ToLower(externalID)
		}
		if err = curation.SuppressExternalLink(ctx, tx, "artist", item.ArtistID, in.Source, externalID, "", actor); err != nil {
			return e, err
		}
	case e.Tool == "correct_release" || e.Tool == "correct_track":
		parse, suppress, target := governance.ParseCorrectionChanges, r.writer.SuppressReleaseValue, item.TargetID
		if e.Tool == "correct_track" {
			parse, suppress, target = governance.ParseTrackCorrectionChanges, r.writer.SuppressTrackValue, in.TrackID
		}
		fields, err := parse(in.Changes)
		if err != nil {
			return e, err
		}
		for name, field := range fields {
			if err = suppress(ctx, tx, target, name, correctionIntent(name, field), "", actor); err != nil {
				return e, err
			}
		}
	case e.Tool == "merge_releases":
		candidate, err := curation.ReleaseMergeCandidate(item)
		if err != nil {
			return e, err
		}
		if err = r.writer.SuppressReleaseMerge(ctx, tx, item.TargetID, candidate.ReleaseID, "", actor); err != nil {
			return e, err
		}
	case item.Kind == "recording_match":
		var evidence struct {
			Tracks []struct {
				ID string `json:"id"`
			} `json:"tracks"`
		}
		if err = json.Unmarshal(item.Evidence, &evidence); err != nil {
			return e, err
		}
		for _, t := range evidence.Tracks {
			if err = curation.SuppressExternalLink(ctx, tx, "track", t.ID, "musicbrainz", strings.ToLower(in.ExternalID), "", actor); err != nil {
				return e, err
			}
		}
	default:
		if err = curation.SuppressExternalLink(ctx, tx, "release", item.TargetID, "musicbrainz", strings.ToLower(in.ExternalID), "", actor); err != nil {
			return e, err
		}
	}
	if err = tx.Commit(); err != nil {
		return e, err
	}
	return r.writer.Exception(ctx, id)
}
