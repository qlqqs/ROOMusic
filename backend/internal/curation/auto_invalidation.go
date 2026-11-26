package curation

import (
	"context"
	"database/sql"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// LoadCurationEvidence 在只读快照之外记录作废，重新读取后再把证据交给整理链。
func (w *Writer) LoadCurationEvidence(ctx context.Context, ids []string) (map[string]catalog.CurationEvidence, error) {
	for {
		entries, err := catalog.LoadCurationEvidence(ctx, w.db, ids, w.ArtworkExists)
		if err != nil {
			return nil, err
		}
		reload := false
		for _, id := range ids {
			for _, candidate := range entries[id].StaleAuto {
				if err := w.invalidateFields(ctx, id, candidate); err != nil {
					return nil, err
				}
				reload = true
			}
		}
		if !reload {
			return entries, nil
		}
	}
}

func (w *Writer) invalidateFields(ctx context.Context, releaseID string, candidate catalog.StaleAutoFields) error {
	actor, err := w.writerActor(ctx, "", "", "")
	if err != nil {
		return err
	}
	kind := operations.TypeReleaseAutoCorrection
	if candidate.TrackID != "" {
		kind = governance.TypeTrackAutoCorrection
	}
	spec := operations.Spec{Type: kind, ActorID: actor, Key: uuid.New(),
		Origin:      &operations.Origin{Source: "curation", ProviderKind: "source", Provider: "scan", Basis: "对应字段的扫描值已变化"},
		Fingerprint: operations.Fingerprint([]any{kind, releaseID, candidate})}
	_, err = operations.Run(ctx, w.db, spec, func(ctx context.Context, tx *sql.Tx, id string) (operations.Outcome, error) {
		out, err := governance.InvalidateAutoFields(ctx, tx, releaseID, candidate)
		if err != nil {
			return out, err
		}
		if err := checkExternalWriter(ctx, tx, spec); err != nil {
			return operations.Outcome{}, err
		}
		if err := refreshFieldArtistLinks(ctx, tx, releaseID, "", nil); err != nil {
			return operations.Outcome{}, err
		}
		out.Response = map[string]string{"operation_id": id}
		return out, nil
	})
	var f *operations.Failure
	if errors.As(err, &f) && (f.Code == "nothing_to_apply" || f.Code == "item_stale" || f.Code == "not_found") {
		return nil
	}
	return err
}
