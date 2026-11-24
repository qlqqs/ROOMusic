package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

type manualMediaChange struct{ before, after *manualMedia }

func manualChange(r operations.Record) (manualMediaChange, error) {
	var change manualMediaChange
	if err := json.Unmarshal(r.Before, &change.before); err != nil {
		return change, err
	}
	if err := json.Unmarshal(r.After, &change.after); err != nil {
		return change, err
	}
	if change.before == nil && change.after == nil {
		return change, errors.New("人工资料操作缺少快照")
	}
	return change, nil
}

func (c manualMediaChange) owner() string {
	if c.after != nil {
		return c.after.ArtistID
	}
	return c.before.ArtistID
}
func (c manualMediaChange) matches(operationID string) bool {
	if c.after == nil {
		return operationID == ""
	}
	return c.after.OperationID == operationID
}

func (k manualMediaKind) Revert(ctx context.Context, tx *sql.Tx, operationID string, r operations.Record) (operations.Outcome, error) {
	canonical, _, err := lockEffectiveArtist(ctx, tx, r.TargetID)
	if err != nil {
		return operations.Outcome{}, err
	}
	c, err := manualChange(r)
	if err != nil {
		return operations.Outcome{}, err
	}
	owner := c.owner()
	if owner != r.TargetID {
		family, err := effectiveArtist(ctx, tx, owner)
		if err != nil {
			return operations.Outcome{}, err
		}
		if family != canonical {
			return operations.Outcome{}, operations.Conflict("revert_conflict", "艺人资料所属身份已变化")
		}
	}
	current, err := k.read(ctx, tx, owner)
	if err != nil {
		return operations.Outcome{}, err
	}
	var currentID string
	if current != nil {
		currentID = current.OperationID
	}
	if !c.matches(currentID) {
		return operations.Outcome{}, operations.Conflict("revert_conflict", "人工资料已变化")
	}
	if err := k.replace(ctx, tx, owner, c.before); err != nil {
		return operations.Outcome{}, err
	}
	out, err := artistEditOutcome(ctx, tx, canonical, r.After, r.Before)
	if c.after != nil && c.after.Resource != "" {
		out.AfterCommit = func() { k.writer.discardArtistImage(ctx, c.after.Resource, owner) }
	}
	return out, err
}

func (m *manualMedia) summary() any {
	if m == nil {
		return nil
	}
	if m.Resource != "" {
		return map[string]any{"resource_id": m.Resource, "mime": m.MIME, "width": m.Width, "height": m.Height}
	}
	return map[string]any{"lang": m.Lang, "length": utf8.RuneCountInString(m.Text)}
}

func (k manualMediaKind) Describe(ctx context.Context, q operations.Queryer, records []operations.Record) (map[string]operations.Description, error) {
	ids := make([]string, 0, len(records))
	owners := make([]string, 0, len(records))
	changes := make(map[string]manualMediaChange, len(records))
	for _, r := range records {
		ids = append(ids, r.TargetID)
		c, err := manualChange(r)
		if err != nil {
			continue
		}
		changes[r.ID] = c
		owners = append(owners, c.owner())
	}
	artists, err := governance.LoadOperationArtists(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, "SELECT artist_id::text,operation_id::text FROM "+k.table+" WHERE artist_id=ANY($1::uuid[])", owners)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	current := map[string]string{}
	for rows.Next() {
		var id, op string
		if err := rows.Scan(&id, &op); err != nil {
			return nil, err
		}
		current[id] = op
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make(map[string]operations.Description, len(records))
	for _, r := range records {
		var d operations.Description
		if a, ok := artists[r.TargetID]; ok {
			d.Artist = a.Summary("")
		}
		if c, ok := changes[r.ID]; ok {
			d.Changes = map[string]any{"kind": k.kind, "from": c.before.summary(), "to": c.after.summary()}
			d.Revertible = d.Artist != nil && r.Type != operations.TypeRevert && r.RevertedByID == "" && c.matches(current[c.owner()])
		}
		result[r.ID] = d
	}
	return result, nil
}

func (k manualMediaKind) PreviewRevert(_ context.Context, _ operations.Queryer, r operations.Record) ([]operations.PreviewChange, error) {
	c, err := manualChange(r)
	if err != nil {
		return nil, err
	}
	return []operations.PreviewChange{{Field: k.field, Before: c.after.summary(), After: c.before.summary()}}, nil
}
