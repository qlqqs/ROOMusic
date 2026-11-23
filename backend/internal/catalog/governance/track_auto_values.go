package governance

import (
	"context"
	"database/sql"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/writegate"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

const TypeTrackAutoCorrection = "track_auto_correction"

type LockedAutoTrack struct {
	tx              *sql.Tx
	state           TrackCorrectionState
	releaseRevision int64
}

func LockAutoTrack(ctx context.Context, tx *sql.Tx, id string) (*LockedAutoTrack, error) {
	return lockAutoTrack(ctx, tx, id, true)
}

func LockAutoTrackForRevert(ctx context.Context, tx *sql.Tx, id string) (*LockedAutoTrack, error) {
	return lockAutoTrack(ctx, tx, id, false)
}

func lockAutoTrack(ctx context.Context, tx *sql.Tx, id string, present bool) (*LockedAutoTrack, error) {
	if err := writegate.Lock(ctx, tx); err != nil {
		return nil, err
	}
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT r.auto_revision FROM tracks t JOIN media m ON m.id=t.medium_id JOIN releases r ON r.id=m.release_id
 WHERE t.id=$1 AND (NOT $2 OR t.source_status='present') FOR UPDATE OF r`, id, present).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		if present {
			return nil, operations.NotFound("not_found", "曲目不存在")
		}
		return nil, operations.Conflict("revert_conflict", "曲目已不存在")
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT id FROM tracks WHERE id=$1 FOR UPDATE`, id); err != nil {
		return nil, err
	}
	state, err := NewStore(tx).loadTrackCorrectionState(ctx, id, present)
	if err != nil {
		return nil, err
	}
	return &LockedAutoTrack{tx, state, revision}, nil
}

func (t *LockedAutoTrack) ReleaseID() string           { return t.state.ReleaseID }
func (t *LockedAutoTrack) EditRevision() int64         { return t.state.Revision }
func (t *LockedAutoTrack) State() TrackCorrectionState { return t.state }
func (t *LockedAutoTrack) Current(name string) (catalog.AutoValue, bool) {
	for _, f := range t.state.Fields {
		if f.Field == name && f.Auto != nil {
			return *f.Auto, true
		}
	}
	return catalog.AutoValue{}, false
}

type trackAutoRecord struct {
	TrackID    string                    `json:"track_id"`
	TrackTitle string                    `json:"track_title"`
	Fields     map[string]autoFieldState `json:"fields"`
}

func trackAutoSnapshot(state TrackCorrectionState, changes map[string]*ownedAutoIntent) trackAutoRecord {
	out := trackAutoRecord{TrackID: state.TrackID, Fields: make(map[string]autoFieldState, len(changes))}
	for _, f := range state.Fields {
		if f.Field == "title" {
			out.TrackTitle = f.EffectiveValue.(string)
		}
		if _, ok := changes[f.Field]; !ok {
			continue
		}
		entry := autoFieldState{Effective: f.EffectiveValue}
		if f.Auto != nil {
			entry.Auto = &ownedAutoIntent{f.Auto.Intent(), f.Auto.OperationID}
		}
		out.Fields[f.Field] = entry
	}
	return out
}

func (t *LockedAutoTrack) Write(ctx context.Context, id string, fields map[string]catalog.CorrectionIntent) (operations.Outcome, error) {
	next := make(map[string]*ownedAutoIntent, len(fields))
	for name, intent := range fields {
		next[name] = &ownedAutoIntent{intent, id}
	}
	return t.replace(ctx, next)
}

func (t *LockedAutoTrack) replace(ctx context.Context, next map[string]*ownedAutoIntent) (operations.Outcome, error) {
	if len(next) == 0 {
		return operations.Outcome{}, operations.Conflict("nothing_to_apply", "没有需要写入的曲目自动值")
	}
	for name, value := range next {
		var err error
		if value == nil {
			_, err = t.tx.ExecContext(ctx, `DELETE FROM track_auto_values WHERE track_id=$1 AND field=$2`, t.state.TrackID, name)
		} else {
			_, err = t.tx.ExecContext(ctx, `INSERT INTO track_auto_values(track_id,field,mode,text_value,operation_id) VALUES($1,$2,'set',$3,$4)
 ON CONFLICT(track_id,field) DO UPDATE SET text_value=excluded.text_value,operation_id=excluded.operation_id`, t.state.TrackID, name, value.Value, value.OperationID)
		}
		if err != nil {
			return operations.Outcome{}, err
		}
	}
	if _, err := t.tx.ExecContext(ctx, `UPDATE releases SET auto_revision=auto_revision+1 WHERE id=$1`, t.ReleaseID()); err != nil {
		return operations.Outcome{}, err
	}
	_, artist := next["artist"]
	if err := refreshGovernedTrack(ctx, t.tx, t.ReleaseID(), t.state.TrackID, artist); err != nil {
		return operations.Outcome{}, err
	}
	after, err := NewStore(t.tx).loadTrackCorrectionState(ctx, t.state.TrackID, false)
	if err != nil {
		return operations.Outcome{}, err
	}
	return operations.Outcome{TargetType: operations.TargetRelease, TargetID: t.ReleaseID(), ExpectedRevision: t.releaseRevision, ResultRevision: t.releaseRevision + 1, Before: trackAutoSnapshot(t.state, next), After: trackAutoSnapshot(after, next)}, nil
}
