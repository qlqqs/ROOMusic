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

type artistLinkKind struct{}

func ArtistLinkOperations(_ *Writer) operations.Kind { return artistLinkKind{} }
func (artistLinkKind) Type() string                  { return operations.TypeArtistLink }

func artistLinkStates(r operations.Record) (before, after artistLinkState, err error) {
	if err = json.Unmarshal(r.Before, &before); err != nil {
		return
	}
	if err = json.Unmarshal(r.After, &after); err != nil {
		return
	}
	if before.Links == nil || after.Links == nil {
		err = errors.New("艺人对应操作缺少对应快照")
	}
	return
}

func priorArtistLink(links []artistLinkRow, source string) *artistLinkRow {
	for i := range links {
		if links[i].Source == source {
			return &links[i]
		}
	}
	return nil
}

func restoreArtistMedia(ctx context.Context, tx *sql.Tx, id, table string, rows []json.RawMessage) error {
	if len(rows) == 0 {
		return nil
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `WITH `+catalog.ArtistMediaFamilyCTE+` INSERT INTO `+table+`
		SELECT m.* FROM jsonb_array_elements($2::jsonb) snapshots(value),
		LATERAL jsonb_populate_record(NULL::`+table+`, '{"citations":[]}'::jsonb || snapshots.value) m JOIN artist_media_family f USING(artist_id)
		WHERE f.canonical_id=$1
		ON CONFLICT (artist_id) DO NOTHING`, id, raw)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count != int64(len(rows)) {
		return operations.Conflict("revert_conflict", "原艺人资料的归属或内容已变化")
	}
	return err
}

type artistLinkRevertCheck struct {
	OperationID string          `json:"operation_id"`
	TargetID    string          `json:"target_id"`
	CanonicalID string          `json:"canonical_id"`
	Before      artistLinkState `json:"before_state"`
	After       artistLinkState `json:"after_state"`
}

// 自动资料的依据指向被撤销的条目，清除时只保留图片内容键。
const artistLinkRevertMedia = `EXISTS (
	SELECT FROM external_links l WHERE l.object_type='artist' AND l.object_id=p.target_id AND l.operation_id=p.operation_id
	  AND l.source=m.basis_source AND l.external_id=m.basis_id)`

func artistLinkRevertConflicts(ctx context.Context, q operations.Queryer, checks []artistLinkRevertCheck) (map[string]string, error) {
	if len(checks) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(checks)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `WITH `+catalog.ArtistMediaFamilyCTE+`, plans AS (
		SELECT * FROM jsonb_to_recordset($1::jsonb) p(operation_id uuid,target_id uuid,canonical_id uuid,before_state jsonb,after_state jsonb)
	), restored_media AS (
		SELECT p.operation_id, m.artist_id, 'image' AS field FROM plans p, jsonb_to_recordset(p.before_state->'images') m(artist_id uuid)
		UNION ALL
		SELECT p.operation_id, m.artist_id, 'bio' AS field FROM plans p, jsonb_to_recordset(p.before_state->'bios') m(artist_id uuid)
	), current_media AS NOT MATERIALIZED (
		SELECT artist_id, basis_source, basis_id, 'image' AS field FROM artist_external_artworks
		UNION ALL
		SELECT artist_id, basis_source, basis_id, 'bio' AS field FROM artist_external_bios
	) SELECT p.operation_id::text,
		NOT EXISTS (
			SELECT FROM jsonb_to_recordset(p.after_state->'links') a(id uuid,operation_id uuid)
			LEFT JOIN external_links l ON l.id=a.id AND l.operation_id=a.operation_id
			WHERE a.operation_id=p.operation_id AND l.id IS NULL
		),
		NOT EXISTS (
			SELECT FROM restored_media r
			LEFT JOIN artist_media_family f ON f.artist_id=r.artist_id AND f.canonical_id=p.canonical_id
			LEFT JOIN current_media m ON m.artist_id=r.artist_id AND m.field=r.field
			WHERE r.operation_id=p.operation_id AND (f.artist_id IS NULL OR (m.artist_id IS NOT NULL AND NOT `+artistLinkRevertMedia+`))
		) FROM plans p`, raw)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	conflicts := make(map[string]string)
	for rows.Next() {
		var id string
		var linksOK, mediaOK bool
		if err := rows.Scan(&id, &linksOK, &mediaOK); err != nil {
			return nil, err
		}
		switch {
		case !linksOK:
			conflicts[id] = "艺人对应已移除或由其他操作写入"
		case !mediaOK:
			conflicts[id] = "原艺人资料的归属或内容已变化"
		}
	}
	return conflicts, rows.Err()
}

func removeArtistLinkMedia(ctx context.Context, tx *sql.Tx, canonical, target, original, revert string) error {
	_, err := tx.ExecContext(ctx, `WITH `+catalog.ArtistMediaFamilyCTE+`, p AS (
		SELECT $2::uuid AS target_id, $3::uuid AS operation_id
	), removed AS (
		DELETE FROM artist_external_artworks m USING artist_media_family f, p
		WHERE f.canonical_id=$1 AND f.artist_id=m.artist_id AND `+artistLinkRevertMedia+`
		RETURNING m.storage_key
	), retained AS (
		INSERT INTO artwork_retained(storage_key,operation_id) SELECT DISTINCT storage_key,$4::uuid FROM removed ON CONFLICT DO NOTHING
	) DELETE FROM artist_external_bios m USING artist_media_family f, p
	WHERE f.canonical_id=$1 AND f.artist_id=m.artist_id AND `+artistLinkRevertMedia, canonical, target, original, revert)
	return err
}

func lockArtistLinkRestoreOperations(ctx context.Context, tx *sql.Tx, before json.RawMessage) error {
	// 恢复行引用原写入操作，须在目录锁前取得外键所需的锁。
	_, err := tx.ExecContext(ctx, `SELECT id FROM operations WHERE id IN (
		SELECT (entry->>'operation_id')::uuid FROM jsonb_array_elements(
			COALESCE($1::jsonb->'links','[]') || COALESCE($1::jsonb->'images','[]') || COALESCE($1::jsonb->'bios','[]')
		) saved(entry)
	) ORDER BY id FOR KEY SHARE`, before)
	return err
}

const artistLinkWriterActive = `NOT EXISTS (SELECT FROM operations WHERE reverts_operation_id=l.operation_id)`

func (artistLinkKind) Revert(ctx context.Context, tx *sql.Tx, op string, r operations.Record) (operations.Outcome, error) {
	before, after, err := artistLinkStates(r)
	if err != nil {
		return operations.Outcome{}, err
	}
	if err := lockArtistLinkRestoreOperations(ctx, tx, r.Before); err != nil {
		return operations.Outcome{}, err
	}
	id, _, err := lockEffectiveArtist(ctx, tx, r.TargetID)
	if err != nil {
		return operations.Outcome{}, err
	}
	conflicts, err := artistLinkRevertConflicts(ctx, tx, []artistLinkRevertCheck{{r.ID, r.TargetID, id, before, after}})
	if err != nil {
		return operations.Outcome{}, err
	}
	if message := conflicts[r.ID]; message != "" {
		return operations.Outcome{}, operations.Conflict("revert_conflict", message)
	}
	if err := removeArtistLinkMedia(ctx, tx, id, r.TargetID, r.ID, op); err != nil {
		return operations.Outcome{}, err
	}
	for _, next := range after.Links {
		if next.OperationID == nil || *next.OperationID != r.ID {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM external_links WHERE id=$1 AND operation_id=$2`, next.ID, r.ID); err != nil {
			return operations.Outcome{}, err
		}
		if old := priorArtistLink(before.Links, next.Source); old != nil {
			raw, err := json.Marshal(old)
			if err != nil {
				return operations.Outcome{}, err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO external_links SELECT l.* FROM jsonb_populate_record(NULL::external_links,$1::jsonb) l
				WHERE `+artistLinkWriterActive, raw); err != nil {
				return operations.Outcome{}, err
			}
		}
		if len(r.Origin) > 0 {
			if err := SuppressExternalLink(ctx, tx, "artist", r.TargetID, next.Source, next.ExternalID, op, operations.ActorID(ctx)); err != nil {
				return operations.Outcome{}, err
			}
		}
	}
	// 人工对应占用期间，原来的自动署名或发行对应可能已经撤销。
	if err := reconcileFieldArtistLinks(ctx, tx, []string{r.TargetID}, "", nil); err != nil {
		return operations.Outcome{}, err
	}
	if err = restoreArtistMedia(ctx, tx, id, "artist_external_artworks", before.Images); err != nil {
		return operations.Outcome{}, err
	}
	if err = restoreArtistMedia(ctx, tx, id, "artist_external_bios", before.Bios); err != nil {
		return operations.Outcome{}, err
	}
	if err = catalog.RefreshMergedArtistProfiles(ctx, tx, []string{id}); err != nil {
		return operations.Outcome{}, err
	}
	before.Links, err = readArtistLinkRows(ctx, tx, r.TargetID)
	if err != nil {
		return operations.Outcome{}, err
	}
	return artistEditOutcome(ctx, tx, id, r.After, before)
}

func (artistLinkKind) PreviewRevert(ctx context.Context, q operations.Queryer, r operations.Record) ([]operations.PreviewChange, error) {
	before, after, err := artistLinkStates(r)
	if err != nil {
		return nil, err
	}
	var changes []operations.PreviewChange
	for _, l := range after.Links {
		if l.OperationID == nil || *l.OperationID != r.ID {
			continue
		}
		change := operations.PreviewChange{Field: l.Source, Before: l.ExternalID}
		if old := priorArtistLink(before.Links, l.Source); old != nil {
			var active bool
			if err := q.QueryRowContext(ctx, `SELECT `+artistLinkWriterActive+` FROM (SELECT $1::uuid AS operation_id) l`, old.OperationID).Scan(&active); err != nil {
				return nil, err
			}
			if active {
				change.After = old.ExternalID
			}
		}
		changes = append(changes, change)
	}
	for _, group := range []struct {
		field string
		rows  []json.RawMessage
	}{{"image", before.Images}, {"bio", before.Bios}} {
		for _, row := range group.rows {
			var media struct {
				ArtistID  string `json:"artist_id"`
				Source    string `json:"source"`
				SourceURL string `json:"source_url"`
			}
			if err := json.Unmarshal(row, &media); err != nil {
				return nil, err
			}
			changes = append(changes, operations.PreviewChange{Field: group.field, After: media})
		}
	}
	return changes, nil
}

func (artistLinkKind) Describe(ctx context.Context, q operations.Queryer, records []operations.Record) (map[string]operations.Description, error) {
	targets := make([]string, 0, len(records))
	for _, r := range records {
		targets = append(targets, r.TargetID)
	}
	artists, err := governance.LoadOperationArtists(ctx, q, targets)
	if err != nil {
		return nil, err
	}
	var checks []artistLinkRevertCheck
	out := make(map[string]operations.Description, len(records))
	for _, r := range records {
		var d operations.Description
		artist, ok := artists[r.TargetID]
		if ok {
			d.Artist = artist.Summary("")
		}
		before, after, err := artistLinkStates(r)
		if err == nil {
			op := r.ID
			if r.Type == operations.TypeRevert {
				op = r.RevertsID
				before, after = after, before
			}
			if artist.Name != "" && r.Type != operations.TypeRevert && r.RevertedByID == "" {
				canonical := r.TargetID
				if artist.RedirectArtistID != nil {
					canonical = *artist.RedirectArtistID
				}
				checks = append(checks, artistLinkRevertCheck{r.ID, r.TargetID, canonical, before, after})
			}
			changes := map[string]string{}
			for _, l := range after.Links {
				if l.OperationID != nil && *l.OperationID == op {
					changes[l.Source] = l.ExternalID
				}
			}
			d.Changes = map[string]any{"kind": operations.TypeArtistLink, "links": changes, "removed_images": len(before.Images), "removed_bios": len(before.Bios)}
		}
		out[r.ID] = d
	}
	conflicts, err := artistLinkRevertConflicts(ctx, q, checks)
	if err != nil {
		return nil, err
	}
	for _, check := range checks {
		d := out[check.OperationID]
		d.Revertible = conflicts[check.OperationID] == ""
		out[check.OperationID] = d
	}
	return out, nil
}
