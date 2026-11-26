package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

type externalLinkKind struct{}

type externalLinkChanges struct {
	Kind string `json:"kind"`
	linkState
	SkippedArtistLinks int `json:"skipped_artist_links,omitempty"`
}

func ExternalLinkOperations(_ *Writer) operations.Kind { return externalLinkKind{} }
func (externalLinkKind) Type() string                  { return operations.TypeExternalLink }

func readLinkState(raw json.RawMessage) (linkState, error) {
	var s linkState
	if err := json.Unmarshal(raw, &s); err != nil {
		return linkState{}, err
	}
	if len(s.Links) == 0 || s.Source == "" {
		return linkState{}, errors.New("外部对应记录缺少对应项")
	}
	return s, nil
}

func (externalLinkKind) Revert(ctx context.Context, tx *sql.Tx, operationID string, original operations.Record) (operations.Outcome, error) {
	release, err := governance.LockAutoReleaseForRevert(ctx, tx, original.TargetID)
	if err != nil {
		var failure *operations.Failure
		if errors.As(err, &failure) && failure.Class == operations.ClassNotFound {
			return operations.Outcome{}, operations.Conflict("revert_conflict", "外部对应所属的发行已移除")
		}
		return operations.Outcome{}, err
	}
	s, err := readLinkState(original.After)
	if err != nil {
		return operations.Outcome{}, err
	}
	if s.Split != nil {
		r, err := tx.ExecContext(ctx, `DELETE FROM release_match_credit_parts WHERE operation_id=$1`, original.ID)
		if err != nil {
			return operations.Outcome{}, err
		}
		if n, err := r.RowsAffected(); err != nil || n == 0 {
			if err == nil {
				err = operations.Conflict("revert_conflict", "拆分依据已失效")
			}
			return operations.Outcome{}, err
		}
		if err := catalog.RefreshMatchCredits(ctx, tx, original.TargetID); err != nil {
			return operations.Outcome{}, err
		}
		if err := insertSuppression(ctx, tx, "release", original.TargetID, "split_credit", operations.Fingerprint(*s.Split), s.Split, operationID, operations.ActorID(ctx)); err != nil {
			return operations.Outcome{}, err
		}
		return externalOutcome(ctx, release, original.After, map[string]any{})
	}
	var derived bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM release_match_credit_parts WHERE release_id=$1)`, original.TargetID).Scan(&derived); err != nil {
		return operations.Outcome{}, err
	}
	skipped := 0
	var unlinkedArtists []string
	for _, l := range s.Links {
		owner, err := previousLink(tx.QueryRowContext(ctx, `SELECT `+linkColumns+` FROM external_links WHERE source=$1 AND object_type=$2 AND object_id=$3 FOR UPDATE`, s.Source, l.ObjectType, l.ObjectID))
		if err != nil {
			return operations.Outcome{}, err
		}
		if owner == nil {
			return operations.Outcome{}, operations.Conflict("revert_conflict", "外部对应已移除")
		}
		if owner.OperationID != original.ID {
			if l.ObjectType == "artist" && owner.OperationID != "" {
				var artistLink bool
				err := tx.QueryRowContext(ctx, "SELECT operation_type='artist_link' FROM operations WHERE id=$1", owner.OperationID).Scan(&artistLink)
				if err != nil {
					return operations.Outcome{}, err
				}
				if artistLink {
					skipped++
					continue
				}
			}
			message := "外部对应已被改写"
			if owner.OperationID != "" {
				message += "，操作 " + owner.OperationID
			}
			return operations.Outcome{}, operations.Conflict("revert_conflict", message)
		}
		if _, err := RemoveLink(ctx, tx, s.Source, l.ObjectType, l.ObjectID); err != nil {
			return operations.Outcome{}, err
		}
		if l.ObjectType == "artist" {
			unlinkedArtists = append(unlinkedArtists, l.ObjectID)
		}
		if err := SuppressExternalLink(ctx, tx, l.ObjectType, l.ObjectID, s.Source, l.ExternalID, operationID, operations.ActorID(ctx)); err != nil {
			return operations.Outcome{}, err
		}
	}
	if derived {
		if err := catalog.RefreshMatchCredits(ctx, tx, original.TargetID); err != nil {
			return operations.Outcome{}, err
		}
	}
	if err := refreshFieldArtistLinks(ctx, tx, original.TargetID, "", nil); err != nil {
		return operations.Outcome{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE curation_artist_credit_proposals SET status='stale' WHERE operation_id=$1 AND status='open'`, original.ID); err != nil {
		return operations.Outcome{}, err
	}
	if err := catalog.RefreshMergedArtistProfiles(ctx, tx, unlinkedArtists); err != nil {
		return operations.Outcome{}, err
	}
	out, err := externalOutcome(ctx, release, original.After, map[string]any{})
	if skipped > 0 {
		out.After = map[string]int{"skipped_artist_links": skipped}
	}
	out.ResponseRevision = release.EditRevision()
	return out, err
}

func (externalLinkKind) Describe(ctx context.Context, q operations.Queryer, records []operations.Record) (map[string]operations.Description, error) {
	releaseIDs := make([]string, 0, len(records))
	for _, r := range records {
		releaseIDs = append(releaseIDs, r.TargetID)
	}
	infos, err := governance.LoadOperationReleases(ctx, q, releaseIDs)
	if err != nil {
		return nil, err
	}
	descriptions := make(map[string]operations.Description, len(records))
	// 一次读取整页尚可撤销的对应，以对象的唯一键核对当前写入者。
	type object struct {
		Source string `json:"source"`
		LinkIntent
		Owner string `json:"owner"`
	}
	var objects []object
	for _, r := range records {
		var d operations.Description
		if info, ok := infos[r.TargetID]; ok {
			d.Release = &info
		}
		raw := r.After
		if r.Type == operations.TypeRevert {
			raw = r.Before
		}
		if s, err := readLinkState(raw); err == nil {
			change := externalLinkChanges{Kind: operations.TypeExternalLink, linkState: s}
			if r.Type == operations.TypeRevert {
				var result struct {
					Skipped int `json:"skipped_artist_links"`
				}
				if err := json.Unmarshal(r.After, &result); err == nil {
					change.SkippedArtistLinks = result.Skipped
				}
			}
			d.Changes = change
			if r.Type != operations.TypeRevert && r.RevertedByID == "" {
				d.Revertible = true
				if s.Split != nil {
					if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM release_match_credit_parts WHERE operation_id=$1)`, r.ID).Scan(&d.Revertible); err != nil {
						return nil, err
					}
					descriptions[r.ID] = d
					continue
				}
				for _, l := range s.Links {
					objects = append(objects, object{s.Source, l, r.ID})
				}
			}
		}
		descriptions[r.ID] = d
	}
	if len(objects) == 0 {
		return descriptions, nil
	}
	raw, err := json.Marshal(objects)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT o.owner::text,
		count(*) FILTER (WHERE o.object_type='artist' AND writer.operation_type='artist_link'),
		bool_and(COALESCE(o.object_type='artist' AND writer.operation_type='artist_link',false))
 FROM jsonb_to_recordset($1::jsonb) AS o(source text,object_type text,object_id uuid,owner uuid)
 LEFT JOIN external_links l USING(source,object_type,object_id)
 LEFT JOIN operations writer ON writer.id=l.operation_id
 WHERE l.operation_id IS DISTINCT FROM o.owner GROUP BY o.owner`, raw)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var skipped int
		var revertible bool
		if err := rows.Scan(&id, &skipped, &revertible); err != nil {
			return nil, err
		}
		d := descriptions[id]
		d.Revertible = revertible
		change := d.Changes.(externalLinkChanges)
		change.SkippedArtistLinks = skipped
		d.Changes = change
		descriptions[id] = d
	}
	return descriptions, rows.Err()
}

func (externalLinkKind) PreviewRevert(ctx context.Context, q operations.Queryer, record operations.Record) ([]operations.PreviewChange, error) {
	s, err := readLinkState(record.After)
	if err != nil {
		return nil, err
	}
	if s.Split != nil {
		return []operations.PreviewChange{{Field: s.Split.Role, Before: s.Split.Credits, After: s.Split.LocalText}}, nil
	}
	var artistIDs []string
	for _, l := range s.Links {
		if l.ObjectType == "artist" {
			artistIDs = append(artistIDs, l.ObjectID)
		}
	}
	skipped := map[string]bool{}
	if len(artistIDs) > 0 {
		rows, err := q.QueryContext(ctx, `SELECT l.object_id::text FROM external_links l JOIN operations o ON o.id=l.operation_id
			WHERE l.object_type='artist' AND l.source=$1 AND l.object_id=ANY($2::uuid[]) AND o.operation_type='artist_link'`, s.Source, artistIDs)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			skipped[id] = true
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	changes := make([]operations.PreviewChange, 0, len(s.Links))
	for _, l := range s.Links {
		if l.ObjectType == "artist" && skipped[l.ObjectID] {
			continue
		}
		changes = append(changes, operations.PreviewChange{Field: l.key(), Before: externalIdentity{s.Source, l.ExternalID}, After: nil})
	}
	return changes, nil
}
