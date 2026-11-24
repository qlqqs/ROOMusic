package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
)

// DiscoverReleaseMerges 与扫描、并入事务共用目录写锁，避免并发匹配把两个方向分别入队。
func (w *Writer) DiscoverReleaseMerges(ctx context.Context, external string, version int16) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = governance.LockCatalogWrites(ctx, tx); err != nil {
		return err
	}
	if err = w.discoverReleaseMerges(ctx, tx, external, version); err != nil {
		return err
	}
	return tx.Commit()
}

func orderedMergeReleases(ctx context.Context, tx *sql.Tx, ids []string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT r.id FROM releases r CROSS JOIN LATERAL (
 SELECT t.disc_number,t.position FROM tracks t JOIN media m ON m.id=t.medium_id
 WHERE m.release_id=r.id AND t.source_status='present' ORDER BY t.disc_number,t.position LIMIT 1) first
 WHERE r.id=ANY($1::uuid[]) AND NOT EXISTS(SELECT 1 FROM release_merges rm WHERE rm.source_release_id=r.id)
 ORDER BY first.disc_number,first.position,r.created_at,r.id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ordered []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ordered = append(ordered, id)
	}
	return ordered, rows.Err()
}

func (w *Writer) discoverReleaseMerges(ctx context.Context, tx *sql.Tx, external string, version int16) error {
	ids, err := ObjectsLinkedTo(ctx, tx, "musicbrainz", "release", external)
	if err != nil {
		return err
	}
	// 已结束项留下续排标志，重启或人工并入后同样可以推进下一对。
	if _, err = tx.ExecContext(ctx, `UPDATE curation_pending_items SET evidence=evidence-'queued'
 WHERE kind='release_merge' AND status='resolved' AND evidence->>'external_id'=$1 AND evidence ? 'queued'`, external); err != nil {
		return err
	}
	var ordered []string
	if len(ids) > 1 {
		ordered, err = orderedMergeReleases(ctx, tx, ids)
	}
	if err != nil {
		return err
	}
	entries, err := catalog.LoadCurationEvidence(ctx, tx, ordered, w.ArtworkExists)
	if err != nil {
		return err
	}
	members, err := releaseMergeMembers(ctx, tx, ordered, entries)
	if err != nil {
		return err
	}
	ordered = slices.DeleteFunc(ordered, func(id string) bool { return members[id].OperationID == "" || members[id].RecordingCount == 0 })
	var pairs []ReleaseMergeEvidence
	for i := 0; i+1 < len(ordered) && len(pairs) == 0; i++ {
		for _, id := range ordered[i+1:] {
			p := mergePair(members[ordered[i]], members[id])
			err := p.check(ctx, tx)
			if err == nil {
				_, err = governance.NewStore(tx).PreviewReleaseMerge(ctx, p.Target.ReleaseID, id)
			}
			if skipMergePair(err) {
				continue
			}
			if err != nil {
				return err
			}
			pairs = append(pairs, p)
		}
	}
	var target string
	if len(pairs) > 0 {
		target = pairs[0].Target.ReleaseID
	}
	if _, err = tx.ExecContext(ctx, `UPDATE curation_pending_items SET status='resolved',resolution='superseded',resolved_at=now(),updated_at=now(),evidence=evidence-'queued'
 WHERE kind='release_merge' AND status<>'resolved' AND evidence->>'external_id'=$1 AND target_id::text<>$2`, external, target); err != nil {
		return err
	}
	if len(pairs) == 0 {
		return nil
	}
	item, err := scanPending(tx.QueryRowContext(ctx, `SELECT `+pendingColumns+` FROM curation_pending_items WHERE kind='release_merge' AND target_id=$1 AND status<>'resolved' FOR UPDATE`, target))
	existing := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	next := pairs[0]
	if existing {
		candidate, err := ReleaseMergeCandidate(item)
		if err != nil {
			return err
		}
		for _, p := range pairs {
			if p.Source.ReleaseID == candidate.ReleaseID {
				next = p
				break
			}
		}
		if item.InputDigest == next.inputDigest() {
			if len(pairs) > 1 {
				_, err = tx.ExecContext(ctx, `UPDATE curation_pending_items SET evidence=evidence || '{"queued":true}'::jsonb WHERE id=$1 AND NOT evidence ? 'queued'`, item.ID)
			}
			return err
		}
		// 保持已领取项及例外的候选不变，旧摘要的项结束后用新 ID 建项。
		if _, err = tx.ExecContext(ctx, `UPDATE curation_pending_items SET status='resolved',resolution='superseded',resolved_at=now(),updated_at=now(),evidence=evidence-'queued' WHERE id=$1`, item.ID); err != nil {
			return err
		}
	}
	evidence := map[string]any{"external_id": external, "target": next.Target, "source": next.Source}
	if len(pairs) > 1 {
		evidence["queued"] = true
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	candidates, err := json.Marshal([]MergeCandidate{next.Source.MergeCandidate})
	if err != nil {
		return err
	}
	return UpsertPendingItem(ctx, tx, PendingItem{TargetID: target, Kind: "release_merge", RuleVersion: version, InputDigest: next.inputDigest(), Evidence: raw, Candidates: candidates, Conflicts: json.RawMessage(`["same_external_release"]`)})
}

func (w *Writer) AdvanceReleaseMergeQueues(ctx context.Context, version int16) error {
	rows, err := w.db.QueryContext(ctx, `SELECT DISTINCT evidence->>'external_id' FROM curation_pending_items WHERE kind='release_merge' AND (status<>'resolved' OR evidence ? 'queued') AND evidence ? 'external_id' ORDER BY 1`)
	if err != nil {
		return err
	}
	var ids []string
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := w.DiscoverReleaseMerges(ctx, id, version); err != nil {
			return err
		}
	}
	return nil
}
