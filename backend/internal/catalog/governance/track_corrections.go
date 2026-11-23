package governance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/writegate"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

const TypeTrackCorrection = "track_correction"

type TrackCorrectionState struct {
	TrackID   string            `json:"track_id"`
	ReleaseID string            `json:"release_id"`
	Revision  int64             `json:"revision"`
	Fields    []CorrectionField `json:"fields"`
}

type TrackCorrectionResult struct {
	TrackCorrectionState
	OperationID string `json:"operation_id"`
}

// 在同一条查询中读两个字段的版本、来源与有效值，避免并发修改产生混合状态。
func (s *Store) LoadTrackCorrectionState(ctx context.Context, trackID string) (TrackCorrectionState, error) {
	return s.loadTrackCorrectionState(ctx, trackID, true)
}

func (s *Store) loadTrackCorrectionState(ctx context.Context, trackID string, present bool) (TrackCorrectionState, error) {
	rows, err := s.database.QueryContext(ctx, `SELECT t.id::text, m.release_id::text, t.edit_revision,
		f.field, f.scan_value, f.effective, COALESCE(obs.inferred,false),
		c.text_value, a.text_value, a.operation_id::text, o.origin->>'provider_kind', o.origin->>'provider'
		FROM tracks t JOIN media m ON m.id=t.medium_id JOIN track_effective e ON e.id=t.id
		CROSS JOIN LATERAL (VALUES (1,'title',t.title,e.title),(2,'artist',t.artist,e.artist)) f(position,field,scan_value,effective)
		LEFT JOIN track_observations obs ON obs.track_id=t.id AND obs.field_name=f.field
		LEFT JOIN track_corrections c ON c.track_id=t.id AND c.field=f.field
		LEFT JOIN track_auto_values a ON a.track_id=t.id AND a.field=f.field
		LEFT JOIN operations o ON o.id=a.operation_id
		WHERE t.id=$1::uuid AND (NOT $2 OR t.source_status='present') ORDER BY f.position`, trackID, present)
	if err != nil {
		return TrackCorrectionState{}, fmt.Errorf("%w: load track corrections: %w", catalog.ErrQuery, err)
	}
	defer rows.Close()
	var state TrackCorrectionState
	for rows.Next() {
		var f CorrectionField
		var corrected, automatic, operation, providerKind, provider sql.NullString
		var scan, effective string
		if err := rows.Scan(&state.TrackID, &state.ReleaseID, &state.Revision, &f.Field, &scan, &effective, &f.Uncertain,
			&corrected, &automatic, &operation, &providerKind, &provider); err != nil {
			return TrackCorrectionState{}, fmt.Errorf("%w: read track correction field: %w", catalog.ErrQuery, err)
		}
		f.ScanValue, f.EffectiveValue = scan, effective
		if corrected.Valid {
			f.Correction = &catalog.CorrectionIntent{Mode: catalog.CorrectionSet, Value: corrected.String}
		}
		if automatic.Valid {
			f.Auto = &catalog.AutoValue{Mode: catalog.CorrectionSet, Value: automatic.String, OperationID: operation.String,
				ProviderKind: providerKind.String, Provider: provider.String}
		}
		state.Fields = append(state.Fields, f)
	}
	if err := rows.Err(); err != nil {
		return TrackCorrectionState{}, fmt.Errorf("%w: read track corrections: %w", catalog.ErrQuery, err)
	}
	if len(state.Fields) == 0 {
		return TrackCorrectionState{}, catalog.ErrTrackNotFound
	}
	return state, nil
}

type TrackCorrectionRequest struct {
	TrackID          string
	ReleaseID        string // 按发行选曲的调用方可指定，写入前在锁内核对。
	ExpectedRevision int64
	Changes          map[string]CorrectionChange
}

func (r TrackCorrectionRequest) Fingerprint() string {
	changes := make(map[string]any, len(r.Changes))
	for name, change := range r.Changes {
		entry := map[string]string{"mode": change.Mode}
		if change.Mode == catalog.CorrectionSet {
			entry["value"] = change.Text
		}
		changes[name] = entry
	}
	return operations.Fingerprint(map[string]any{"type": TypeTrackCorrection, "track_id": strings.ToLower(r.TrackID),
		"expected_revision": r.ExpectedRevision, "changes": changes})
}

// 锁序与扫描一致。曲目 present 同时保证所属发行可见。
func lockTrackCorrectionState(ctx context.Context, tx *sql.Tx, trackID string) (TrackCorrectionState, error) {
	if err := writegate.Lock(ctx, tx); err != nil {
		return TrackCorrectionState{}, err
	}
	var releaseID string
	err := tx.QueryRowContext(ctx, `SELECT r.id::text FROM tracks t JOIN media m ON m.id=t.medium_id
		JOIN releases r ON r.id=m.release_id WHERE t.id=$1::uuid AND t.source_status='present' FOR UPDATE OF r`, trackID).Scan(&releaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return TrackCorrectionState{}, catalog.ErrTrackNotFound
	}
	if err != nil {
		return TrackCorrectionState{}, err
	}
	var id string
	err = tx.QueryRowContext(ctx, `SELECT t.id::text FROM tracks t JOIN media m ON m.id=t.medium_id
		WHERE t.id=$1::uuid AND m.release_id=$2::uuid AND t.source_status='present' FOR UPDATE OF t`, trackID, releaseID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return TrackCorrectionState{}, catalog.ErrTrackNotFound
	}
	if err != nil {
		return TrackCorrectionState{}, err
	}
	return NewStore(tx).LoadTrackCorrectionState(ctx, id)
}

func TrackCorrectionExecutor(request TrackCorrectionRequest) operations.Executor {
	return func(ctx context.Context, tx *sql.Tx, operationID string) (operations.Outcome, error) {
		state, err := lockTrackCorrectionState(ctx, tx, request.TrackID)
		if errors.Is(err, catalog.ErrTrackNotFound) || (err == nil && request.ReleaseID != "" && !strings.EqualFold(state.ReleaseID, request.ReleaseID)) {
			return operations.Outcome{}, operations.NotFound("not_found", "曲目不存在")
		}
		if err != nil {
			return operations.Outcome{}, err
		}
		if state.Revision != request.ExpectedRevision {
			return operations.Outcome{}, operations.Conflict("revision_conflict", "这首曲目刚被修改过，请刷新后再试")
		}
		changes := map[string]*catalog.CorrectionIntent{}
		for _, field := range catalog.TrackCorrectableFields {
			if change, ok := request.Changes[field.Name]; ok {
				changes[field.Name] = nextIntent(field, change)
			}
		}
		return applyTrackCorrection(ctx, tx, operationID, state, changes)
	}
}

type trackCorrectionRecord struct {
	TrackID    string `json:"track_id"`
	TrackTitle string `json:"track_title"`
	correctionRecordState
}

func (s TrackCorrectionState) snapshot(changes map[string]*catalog.CorrectionIntent) trackCorrectionRecord {
	record := trackCorrectionRecord{TrackID: s.TrackID, correctionRecordState: correctionRecordState{
		Fields: map[string]*catalog.CorrectionIntent{}, Effective: map[string]any{},
	}}
	for _, f := range s.Fields {
		if f.Field == "title" {
			record.TrackTitle = f.EffectiveValue.(string)
		}
		if _, changed := changes[f.Field]; changed {
			record.Fields[f.Field] = f.Correction
			record.Effective[f.Field] = f.EffectiveValue
		}
	}
	return record
}

func applyTrackCorrection(ctx context.Context, tx *sql.Tx, operationID string, state TrackCorrectionState, changes map[string]*catalog.CorrectionIntent) (operations.Outcome, error) {
	for _, f := range state.Fields {
		intent, requested := changes[f.Field]
		if !requested {
			continue
		}
		if (intent == nil && f.Correction == nil) || (intent != nil && f.Correction != nil && *intent == *f.Correction) {
			delete(changes, f.Field)
			continue
		}
		var err error
		if intent == nil {
			_, err = tx.ExecContext(ctx, `DELETE FROM track_corrections WHERE track_id=$1::uuid AND field=$2`, state.TrackID, f.Field)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO track_corrections(track_id,field,mode,text_value,operation_id)
				VALUES ($1::uuid,$2,'set',$3,$4::uuid) ON CONFLICT(track_id,field)
				DO UPDATE SET text_value=EXCLUDED.text_value, operation_id=EXCLUDED.operation_id`, state.TrackID, f.Field, intent.Value, operationID)
		}
		if err != nil {
			return operations.Outcome{}, err
		}
	}
	if len(changes) == 0 {
		return operations.Outcome{}, operations.Invalid("no_changes", "没有需要修改的内容")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tracks SET edit_revision=edit_revision+1 WHERE id=$1::uuid`, state.TrackID); err != nil {
		return operations.Outcome{}, err
	}
	_, artistChanged := changes["artist"]
	if err := refreshGovernedTrack(ctx, tx, state.ReleaseID, state.TrackID, artistChanged); err != nil {
		return operations.Outcome{}, err
	}
	next, err := NewStore(tx).LoadTrackCorrectionState(ctx, state.TrackID)
	if err != nil {
		return operations.Outcome{}, err
	}
	return operations.Outcome{TargetType: operations.TargetRelease, TargetID: state.ReleaseID,
		ExpectedRevision: state.Revision, ResultRevision: next.Revision,
		Before: state.snapshot(changes), After: next.snapshot(changes),
		Response: TrackCorrectionResult{TrackCorrectionState: next, OperationID: operationID}}, nil
}

func refreshGovernedTrack(ctx context.Context, tx *sql.Tx, releaseID, trackID string, artistChanged bool) error {
	if artistChanged {
		if err := artistgraph.Refresh(ctx, tx, []string{releaseID}); err != nil {
			return err
		}
	}
	return searchindex.RefreshTrack(ctx, tx, trackID)
}
