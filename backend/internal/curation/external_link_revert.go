package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

type externalLinkKind struct{}

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
	for _, l := range s.Links {
		owner, err := previousLink(tx.QueryRowContext(ctx, `SELECT `+linkColumns+` FROM external_links WHERE source=$1 AND object_type=$2 AND object_id=$3 FOR UPDATE`, s.Source, l.ObjectType, l.ObjectID))
		if err != nil {
			return operations.Outcome{}, err
		}
		if owner == nil {
			return operations.Outcome{}, operations.Conflict("revert_conflict", "外部对应已移除")
		}
		if owner.OperationID != original.ID {
			message := "外部对应已被改写"
			if owner.OperationID != "" {
				message += "，操作 " + owner.OperationID
			}
			return operations.Outcome{}, operations.Conflict("revert_conflict", message)
		}
		if _, err := RemoveLink(ctx, tx, s.Source, l.ObjectType, l.ObjectID); err != nil {
			return operations.Outcome{}, err
		}
		identity := externalIdentity{s.Source, l.ExternalID}
		if err := insertSuppression(ctx, tx, l.ObjectType, l.ObjectID, "external_link", identity.identityHash(), identity, operationID, operations.ActorID(ctx)); err != nil {
			return operations.Outcome{}, err
		}
	}
	out, err := externalOutcome(ctx, release, original.After, map[string]any{})
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
			d.Changes = struct {
				Kind string `json:"kind"`
				linkState
			}{operations.TypeExternalLink, s}
			if r.Type != operations.TypeRevert && r.RevertedByID == "" {
				d.Revertible = true
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
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT o.owner::text FROM jsonb_to_recordset($1::jsonb) AS o(source text,object_type text,object_id uuid,owner uuid)
 LEFT JOIN external_links l USING(source,object_type,object_id) WHERE l.operation_id IS DISTINCT FROM o.owner`, raw)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		d := descriptions[id]
		d.Revertible = false
		descriptions[id] = d
	}
	return descriptions, rows.Err()
}

func (externalLinkKind) PreviewRevert(_ context.Context, _ operations.Queryer, record operations.Record) ([]operations.PreviewChange, error) {
	s, err := readLinkState(record.After)
	if err != nil {
		return nil, err
	}
	changes := make([]operations.PreviewChange, 0, len(s.Links))
	for _, l := range s.Links {
		changes = append(changes, operations.PreviewChange{Field: l.key(), Before: externalIdentity{s.Source, l.ExternalID}, After: nil})
	}
	return changes, nil
}
