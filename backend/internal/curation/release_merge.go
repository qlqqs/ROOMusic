package curation

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type ReleaseAutoMerge struct {
	CheckPreview                             func(governance.ReleaseMergePreview) error
	ItemID, Token, BatchID, Key, RequestID   string
	ConfirmedBy, ConversationID, ExceptionID string
	Source                                   Source
}

func (w *Writer) PreviewReleaseMerge(ctx context.Context, item PendingItem) (governance.ReleaseMergePreview, error) {
	c, err := ReleaseMergeCandidate(item)
	if err != nil {
		return governance.ReleaseMergePreview{}, err
	}
	return governance.NewStore(w.db).PreviewReleaseMerge(ctx, item.TargetID, c.ReleaseID)
}

type ReleaseMergeApplied struct {
	OperationID string `json:"operation_id"`
	ReleaseID   string `json:"release_id"`
}

func (w *Writer) MergeReleases(ctx context.Context, r ReleaseAutoMerge) (ReleaseMergeApplied, error) {
	var applied ReleaseMergeApplied
	if !uuid.Valid(r.ItemID) || r.Source.Kind != "model" {
		return applied, operations.Invalid("invalid_input", "发行并入需要模型来源和待定项")
	}
	conversation := r.ConversationID
	if r.ConfirmedBy == "" {
		run, ok := ModelRunFrom(ctx)
		if !ok || run.PendingItemID != r.ItemID || !uuid.Valid(run.ActorID) || !uuid.Valid(run.ConversationID) || !uuid.Valid(r.BatchID) || NormalizeProvider(run.Model) != r.Source.Provider {
			return applied, operations.Conflict("out_of_batch", "待定项不属于本次模型运行")
		}
		conversation = run.ConversationID
	} else if !uuid.Valid(r.ExceptionID) {
		return applied, operations.Invalid("invalid_input", "采纳并入需要例外编号")
	}
	item, err := w.PendingItem(ctx, r.ItemID)
	if err != nil {
		return applied, err
	}
	candidate, err := ReleaseMergeCandidate(item)
	if err != nil {
		return applied, err
	}
	request := governance.ReleaseMergeRequest{TargetID: item.TargetID, SourceID: candidate.ReleaseID, Token: r.Token}
	fp := operations.Fingerprint(map[string]string{"type": operations.TypeReleaseAutoMerge, "item": item.ID, "digest": item.InputDigest, "source": candidate.ReleaseID, "target": item.TargetID, "provider": r.Source.Provider})
	spec, err := w.sourceSpec(ctx, operations.TypeReleaseAutoMerge, item.TargetID, fp, r.Source, r.BatchID, r.Key, r.RequestID, r.ConfirmedBy, conversation)
	if err != nil {
		return applied, err
	}
	check := func(ctx context.Context, tx *sql.Tx, preview governance.ReleaseMergePreview) error {
		if err := checkWrite(ctx, tx, spec, r.ConfirmedBy, item.TargetID, 0, 0); err != nil {
			return err
		}
		if r.ConfirmedBy == "" {
			var valid bool
			err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM curation_batches WHERE id=$1 AND tier='model' AND actor_id=$2 AND closed_at IS NULL FOR SHARE)`, r.BatchID, spec.ActorID).Scan(&valid)
			if err != nil {
				return err
			}
			if !valid {
				return operations.Conflict("out_of_batch", "批次不属于本次模型运行")
			}
		}
		if r.ConfirmedBy != "" {
			var valid bool
			err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM curation_model_exceptions
				WHERE id=$1 AND pending_item_id=$2 AND tool='merge_releases' AND status='accepting' AND decided_by=$3)`, r.ExceptionID, item.ID, r.ConfirmedBy).Scan(&valid)
			if err != nil {
				return err
			}
			if !valid {
				return operations.Conflict("exception_stale", "例外或待定项已变更")
			}
		}
		if err := CheckMatchItem(ctx, tx, item, conversation, r.ConfirmedBy != ""); err != nil {
			return err
		}
		p, err := ReadReleaseMergeEvidence(ctx, tx, item.TargetID, candidate.ReleaseID, w.ArtworkExists, false)
		if err != nil {
			return err
		}
		if err = p.check(ctx, tx); err != nil {
			return err
		}
		if p.inputDigest() != item.InputDigest {
			return operations.Conflict("item_stale", "发行并入待定项的摘要已变更")
		}
		if r.CheckPreview != nil {
			return r.CheckPreview(preview)
		}
		return nil
	}
	result, err := operations.Run(ctx, w.db, spec, func(ctx context.Context, tx *sql.Tx, id string) (operations.Outcome, error) {
		out, err := governance.CurationReleaseMergeExecutor(request, check)(ctx, tx, id)
		if err != nil {
			return out, err
		}
		resolution := "model_merged"
		if r.ConfirmedBy != "" {
			if err = FinishException(ctx, tx, r.ExceptionID, r.ConfirmedBy, "accepted", []string{id}); err != nil {
				return out, err
			}
			resolution = "human_accepted"
		}
		if _, err = tx.ExecContext(ctx, `UPDATE curation_pending_items SET resolution=$2 WHERE id=$1`, item.ID, resolution); err != nil {
			return out, err
		}
		out.Response = ReleaseMergeApplied{id, item.TargetID}
		return out, nil
	})
	if err != nil {
		return applied, err
	}
	err = json.Unmarshal(result.Response, &applied)
	return applied, err
}

// MergeProgramEdition 只接受能逐碟逐轨证实的同版互补发行。
func (w *Writer) MergeProgramEdition(ctx context.Context, edition sources.Release, preview governance.ReleaseMergePreview, source Source, batch, key string) error {
	if source.Kind != "source" || source.Provider != "musicbrainz" || !uuid.Valid(batch) {
		return operations.Invalid("invalid_input", "程序并入需要 MusicBrainz 来源与程序批次")
	}
	request := governance.ReleaseMergeRequest{TargetID: preview.Target.ID, SourceID: preview.Source.ID, Token: preview.Token}
	spec, err := w.sourceSpec(ctx, operations.TypeReleaseAutoMerge, request.TargetID, operations.Fingerprint(request), source, batch, key, "", "", "")
	if err != nil {
		return err
	}
	_, err = operations.Run(ctx, w.db, spec, governance.CurationReleaseMergeExecutor(request, func(ctx context.Context, tx *sql.Tx, current governance.ReleaseMergePreview) error {
		if err := checkWrite(ctx, tx, spec, "", request.TargetID, 0, 0); err != nil {
			return err
		}
		ids := []string{request.TargetID, request.SourceID}
		entries, err := catalog.LoadCurationEvidence(ctx, tx, ids, w.ArtworkExists)
		if err != nil {
			return err
		}
		members, err := releaseMergeMembers(ctx, tx, ids, entries)
		if err != nil {
			return err
		}
		pair := mergePair(members[ids[0]], members[ids[1]])
		if err := pair.check(ctx, tx); err != nil {
			return err
		}
		if pair.Target.ExternalID != edition.ID || !coversEdition(edition, current, entries) {
			return operations.Conflict("item_stale", "发行的曲目对应或碟序已变化")
		}
		return nil
	}))
	return err
}
