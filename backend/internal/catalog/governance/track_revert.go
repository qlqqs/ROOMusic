package governance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

type trackCorrectionKind struct{}

func TrackCorrectionOperations() operations.Kind { return trackCorrectionKind{} }
func (trackCorrectionKind) Type() string         { return TypeTrackCorrection }

func (trackCorrectionKind) Revert(ctx context.Context, tx *sql.Tx, operationID string, original operations.Record) (operations.Outcome, error) {
	var before trackCorrectionRecord
	if err := json.Unmarshal(original.Before, &before); err != nil {
		return operations.Outcome{}, err
	}
	state, err := lockTrackCorrectionState(ctx, tx, before.TrackID)
	if errors.Is(err, catalog.ErrTrackNotFound) || (err == nil && state.Revision != original.ResultRevision) {
		return operations.Outcome{}, operations.Conflict("revert_conflict", "这首曲目之后又被修改过，不能撤销这一条")
	}
	if err != nil {
		return operations.Outcome{}, err
	}
	return applyTrackCorrection(ctx, tx, operationID, state, before.Fields)
}

func (trackCorrectionKind) PreviewRevert(ctx context.Context, q operations.Queryer, record operations.Record) ([]operations.PreviewChange, error) {
	var before trackCorrectionRecord
	if err := json.Unmarshal(record.Before, &before); err != nil {
		return nil, err
	}
	state, err := NewStore(q).LoadTrackCorrectionState(ctx, before.TrackID)
	if err != nil {
		return nil, err
	}
	return previewCorrectionFields(state.Fields, before.Fields), nil
}

func (trackCorrectionKind) Describe(ctx context.Context, q operations.Queryer, records []operations.Record) (map[string]operations.Description, error) {
	infos, err := loadReleaseInfos(ctx, q, records)
	if err != nil {
		return nil, err
	}
	descriptions := make(map[string]operations.Description, len(records))
	tracks := make(map[string]string, len(records))
	for _, record := range records {
		description := infos.describe(record)
		description.Revertible = false
		var before, after trackCorrectionRecord
		if json.Unmarshal(record.Before, &before) == nil && json.Unmarshal(record.After, &after) == nil && after.TrackID != "" {
			tracks[record.ID] = after.TrackID
			var fields []correctionFieldChange
			for _, field := range catalog.TrackCorrectableFields {
				if _, changed := after.Fields[field.Name]; changed {
					fields = append(fields, correctionFieldChange{Field: field.Name,
						Before: correctionFieldState{Correction: before.Fields[field.Name], Effective: before.Effective[field.Name]},
						After:  correctionFieldState{Correction: after.Fields[field.Name], Effective: after.Effective[field.Name]}})
				}
			}
			description.Changes = map[string]any{"kind": TypeTrackCorrection, "track": map[string]string{"id": after.TrackID, "title": after.TrackTitle}, "fields": fields}
		}
		descriptions[record.ID] = description
	}
	ids := make([]string, 0, len(tracks))
	for _, id := range tracks {
		ids = append(ids, id)
	}
	rows, err := q.QueryContext(ctx, `SELECT id::text,edit_revision FROM tracks WHERE id=ANY($1::text[]::uuid[]) AND source_status='present'`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	revisions := make(map[string]int64, len(ids))
	for rows.Next() {
		var id string
		var revision int64
		if err := rows.Scan(&id, &revision); err != nil {
			return nil, err
		}
		revisions[id] = revision
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, record := range records {
		description := descriptions[record.ID]
		revision, present := revisions[tracks[record.ID]]
		description.Revertible = present && revision == record.ResultRevision
		descriptions[record.ID] = description
	}
	return descriptions, nil
}
