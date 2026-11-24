package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

type artistMediaKind struct {
	writer                      *Writer
	kind, subject, field, table string
}

func artistImageKind(w *Writer) artistMediaKind {
	return artistMediaKind{w, operations.TypeArtistExternalArtwork, "artist_image", "image", "artist_external_artworks"}
}
func artistBioKind(w *Writer) artistMediaKind {
	return artistMediaKind{w, operations.TypeArtistExternalBio, "artist_bio", "bio", "artist_external_bios"}
}

func ArtistImageOperations(w *Writer) operations.Kind { return artistImageKind(w) }
func ArtistBioOperations(w *Writer) operations.Kind   { return artistBioKind(w) }
func (k artistMediaKind) Type() string                { return k.kind }

func (k artistMediaKind) decode(raw json.RawMessage) (artistMediaValue, error) {
	var state struct {
		Image artistMediaValue `json:"image"`
		Bio   artistMediaValue `json:"bio"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return artistMediaValue{}, err
	}
	value := state.Image
	if k.kind == operations.TypeArtistExternalBio {
		value = state.Bio
	}
	if value.Source == "" || value.ExternalID == "" || value.SourceURL == "" ||
		(k.kind == operations.TypeArtistExternalArtwork && value.Resource == "") ||
		(k.kind == operations.TypeArtistExternalBio && (value.Text == "" || value.Lang == "")) {
		return artistMediaValue{}, errors.New("艺人资料操作记录缺少来源或内容")
	}
	return value, nil
}

func (k artistMediaKind) Revert(ctx context.Context, tx *sql.Tx, operationID string, original operations.Record) (operations.Outcome, error) {
	if err := lockArtistMedia(ctx, tx, original.TargetID); err != nil {
		return operations.Outcome{}, err
	}
	value, err := k.decode(original.After)
	if err != nil {
		return operations.Outcome{}, err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM "+k.table+" WHERE artist_id=$1::uuid AND operation_id=$2::uuid", original.TargetID, original.ID)
	if err != nil {
		return operations.Outcome{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return operations.Outcome{}, err
	}
	if n == 0 {
		return operations.Outcome{}, operations.Conflict("revert_conflict", "艺人资料已移除或由其他操作写入")
	}
	if err := insertSuppression(ctx, tx, "artist", original.TargetID, k.subject, value.identityHash(), value.externalIdentity, operationID, operations.ActorID(ctx)); err != nil {
		return operations.Outcome{}, err
	}
	out, err := artistMediaOutcome(ctx, tx, original.TargetID, original.After, original.Before)
	if value.Resource != "" {
		out.AfterCommit = func() { k.writer.discardArtistImage(ctx, value.Resource, original.TargetID) }
	}
	return out, err
}

func (k artistMediaKind) Describe(ctx context.Context, q operations.Queryer, records []operations.Record) (map[string]operations.Description, error) {
	targets := make([]string, 0, len(records))
	for _, r := range records {
		targets = append(targets, r.TargetID)
	}
	artists, err := governance.LoadOperationArtists(ctx, q, targets)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, "SELECT artist_id::text,operation_id::text FROM "+k.table+" WHERE artist_id=ANY($1::uuid[])", targets)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	owners := map[string]string{}
	for rows.Next() {
		var id, op string
		if err := rows.Scan(&id, &op); err != nil {
			return nil, err
		}
		owners[id] = op
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]operations.Description, len(records))
	for _, r := range records {
		var d operations.Description
		artist, ok := artists[r.TargetID]
		if ok {
			d.Artist = artist.Summary("")
		}
		raw := r.After
		if r.Type == operations.TypeRevert {
			raw = r.Before
		}
		if value, err := k.decode(raw); err == nil {
			change := map[string]any{"kind": k.kind, "source": value.Source, "external_id": value.ExternalID}
			if k.kind == operations.TypeArtistExternalArtwork {
				change["artwork"] = map[string]any{"resource_id": value.Resource, "mime": value.MIME, "width": value.Width, "height": value.Height}
			} else {
				change["lang"] = value.Lang
				change["length"] = utf8.RuneCountInString(value.Text)
			}
			var audit struct{ Replaced []struct{ Source string } }
			if err := json.Unmarshal(raw, &audit); err != nil {
				return nil, err
			}
			if len(audit.Replaced) > 0 {
				names := make([]string, 0, len(audit.Replaced))
				for _, entry := range audit.Replaced {
					names = append(names, entry.Source)
				}
				change["replaced"] = names
				change["note"] = "替换了 " + strings.Join(names, "、") + "，撤销不恢复"
			}
			d.Changes = change
			d.Revertible = artist.Name != "" && r.Type != operations.TypeRevert && r.RevertedByID == "" && owners[r.TargetID] == r.ID
		}
		out[r.ID] = d
	}
	return out, nil
}

func (k artistMediaKind) PreviewRevert(_ context.Context, _ operations.Queryer, r operations.Record) ([]operations.PreviewChange, error) {
	value, err := k.decode(r.After)
	if err != nil {
		return nil, err
	}
	return []operations.PreviewChange{{Field: k.subject, Before: map[string]string{"source": value.Source, "external_id": value.ExternalID}, After: nil}}, nil
}
