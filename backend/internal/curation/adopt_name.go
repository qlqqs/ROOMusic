package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type AdoptName struct {
	TargetID string `json:"target_id"`
	Field    string `json:"field"`
	Basis    string `json:"basis_operation_id"`
	Revision int64  `json:"expected_revision"`
}

func (w *Writer) AdoptName(ctx context.Context, id, actor, key string, in AdoptName) (json.RawMessage, error) {
	if !uuid.Valid(id) || !uuid.Valid(key) || !uuid.Valid(in.Basis) || !uuid.Valid(in.TargetID) || in.Revision < 1 {
		return nil, operations.Invalid("invalid_input", "名称、依据或版本无效")
	}
	spec := operations.Spec{ActorID: actor, Key: key, Type: operations.TypeReleaseCorrection, Fingerprint: operations.Fingerprint([]any{id, in})}
	track := in.TargetID != id
	if track {
		spec.Type = governance.TypeTrackCorrection
	}
	res, err := operations.Run(ctx, w.db, spec, func(ctx context.Context, tx *sql.Tx, op string) (operations.Outcome, error) {
		var out operations.Outcome
		if err := governance.LockCatalogWrites(ctx, tx); err != nil {
			return out, err
		}
		var value string
		err := tx.QueryRowContext(ctx, `SELECT n.mb_value FROM curation_match_names n JOIN external_links l ON l.id=n.basis_link_id AND l.operation_id=n.basis_operation_id AND l.external_id=n.external_id
 WHERE n.release_id=$1 AND n.target_id=$2 AND n.field=$3 AND n.basis_operation_id=$4 FOR SHARE OF n,l`, id, in.TargetID, in.Field, in.Basis).Scan(&value)
		if errors.Is(err, sql.ErrNoRows) {
			return out, operations.Conflict("item_stale", "名称对应已变化")
		}
		if err != nil {
			return out, err
		}
		raw, _ := json.Marshal(value)
		fields := map[string]governance.RawCorrectionChange{in.Field: {Mode: catalog.CorrectionSet, Value: raw}}
		var execute operations.Executor
		if track {
			changes, err := governance.ParseTrackCorrectionChanges(fields)
			if err != nil {
				return out, err
			}
			execute = governance.TrackCorrectionExecutor(governance.TrackCorrectionRequest{TrackID: in.TargetID, ReleaseID: id, ExpectedRevision: in.Revision, Changes: changes})
		} else {
			changes, err := governance.ParseCorrectionChanges(fields)
			if err != nil {
				return out, err
			}
			execute = governance.CorrectionExecutor(governance.CorrectionRequest{ReleaseID: id, ExpectedRevision: in.Revision, Changes: changes})
		}
		out, err = execute(ctx, tx, op)
		if err == nil {
			out.Response = map[string]any{"field": in.Field, "effective_value": value, "revision": out.ResultRevision, "operation_id": op}
		}
		return out, err
	})
	if err != nil {
		return nil, err
	}
	return res.Response, nil
}
