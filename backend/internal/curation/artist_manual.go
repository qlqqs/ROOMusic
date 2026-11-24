package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

type ArtistEditRequest struct {
	ArtistID, ActorID, Key, RequestID string
	ExpectedRevision                  int64
}

type artistEditResponse struct {
	OperationID string `json:"operation_id"`
	ArtistID    string `json:"artist_id"`
	Revision    int64  `json:"revision"`
	Layer       string `json:"layer,omitempty"`
}

func (r ArtistEditRequest) spec(kind, action string, value any) operations.Spec {
	return operations.Spec{Type: kind, ActorID: r.ActorID, Key: r.Key, RequestID: r.RequestID,
		Fingerprint: operations.Fingerprint(map[string]any{"action": action, "artist_id": r.ArtistID, "expected_revision": r.ExpectedRevision, "value": value})}
}

func effectiveArtist(ctx context.Context, q catalog.Queryer, id string) (string, error) {
	resolved, err := catalog.NewStore(q).ResolveArtists(ctx, []string{id})
	if errors.Is(err, catalog.ErrArtistCatalogPreparing) {
		return "", operations.Conflict("artist_catalog_not_ready", "艺人资料正在整理")
	}
	if err != nil {
		return "", err
	}
	if resolved[id] == "" {
		return "", operations.NotFound("not_found", "艺人不存在")
	}
	return resolved[id], nil
}

func lockEffectiveArtist(ctx context.Context, tx *sql.Tx, id string) (string, int64, error) {
	if err := catalog.AcquireArtistWriteGate(ctx, tx); err != nil {
		return "", 0, err
	}
	canonical, err := effectiveArtist(ctx, tx, id)
	if err != nil {
		return "", 0, err
	}
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT edit_revision FROM artists WHERE id=$1 FOR UPDATE`, canonical).Scan(&revision)
	return canonical, revision, err
}

func checkArtistRevision(current, expected int64) error {
	if expected < 1 || expected >= 9007199254740991 {
		return operations.Invalid("invalid_input", "艺人版本无效")
	}
	if current != expected {
		return operations.Conflict("revision_conflict", "艺人刚被修改过，请刷新后再试")
	}
	return nil
}

func artistEditOutcome(ctx context.Context, tx *sql.Tx, canonical string, before, after any) (operations.Outcome, error) {
	out := operations.Outcome{TargetType: operations.TargetArtist, TargetID: canonical, Before: before, After: after}
	err := tx.QueryRowContext(ctx, `UPDATE artists SET edit_revision=edit_revision+1 WHERE id=$1 RETURNING edit_revision-1,edit_revision`, canonical).Scan(&out.ExpectedRevision, &out.ResultRevision)
	return out, err
}

type manualMediaKind struct {
	writer             *Writer
	kind, field, table string
}

func manualImageKind(w *Writer) manualMediaKind {
	return manualMediaKind{w, operations.TypeArtistManualImage, "image", "artist_manual_artworks"}
}
func manualBioKind(w *Writer) manualMediaKind {
	return manualMediaKind{w, operations.TypeArtistManualBio, "bio", "artist_manual_bios"}
}
func ArtistManualImageOperations(w *Writer) operations.Kind { return manualImageKind(w) }
func ArtistManualBioOperations(w *Writer) operations.Kind   { return manualBioKind(w) }
func (k manualMediaKind) Type() string                      { return k.kind }

// 快照保留实际存储身份与写入者，使家族内继承的人工资料也能原位恢复。
type manualMedia struct {
	ArtistID    string     `json:"artist_id"`
	OperationID string     `json:"operation_id"`
	Resource    string     `json:"storage_key,omitempty"`
	MIME        string     `json:"mime,omitempty"`
	Width       int        `json:"width,omitempty"`
	Height      int        `json:"height,omitempty"`
	Text        string     `json:"text,omitempty"`
	Lang        string     `json:"lang,omitempty"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
}

func (k manualMediaKind) read(ctx context.Context, q operations.Queryer, id string) (*manualMedia, error) {
	var raw []byte
	err := q.QueryRowContext(ctx, "SELECT to_jsonb(m) FROM "+k.table+" m WHERE artist_id=$1", id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state manualMedia
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (k manualMediaKind) replace(ctx context.Context, tx *sql.Tx, id string, state *manualMedia) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM "+k.table+" WHERE artist_id=$1", id); err != nil {
		return err
	}
	if state == nil {
		return nil
	}
	if k.field == "image" {
		_, err := tx.ExecContext(ctx, `INSERT INTO artist_manual_artworks(artist_id,storage_key,mime,width,height,operation_id,updated_at)
           VALUES($1,$2,$3,$4,$5,$6,COALESCE($7,now()))`, id, state.Resource, state.MIME, state.Width, state.Height, state.OperationID, state.UpdatedAt)
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO artist_manual_bios(artist_id,text,lang,operation_id,updated_at)
        VALUES($1,$2,$3,$4,COALESCE($5,now()))`, id, state.Text, state.Lang, state.OperationID, state.UpdatedAt)
	return err
}

func (k manualMediaKind) change(ctx context.Context, tx *sql.Tx, operationID, canonical, owner string, next *manualMedia, layer string) (operations.Outcome, error) {
	before, err := k.read(ctx, tx, owner)
	if err != nil {
		return operations.Outcome{}, err
	}
	if before != nil && before.Resource != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO artwork_retained(storage_key,operation_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, before.Resource, operationID); err != nil {
			return operations.Outcome{}, err
		}
	}
	if next != nil {
		next.ArtistID = owner
		next.OperationID = operationID
	}
	if err := k.replace(ctx, tx, owner, next); err != nil {
		return operations.Outcome{}, err
	}
	out, err := artistEditOutcome(ctx, tx, canonical, before, next)
	out.Response = artistEditResponse{operationID, canonical, out.ResultRevision, layer}
	return out, err
}

func (k manualMediaKind) put(ctx context.Context, r ArtistEditRequest, value manualMedia, storeImage func(context.Context, *sql.Tx) error) (operations.Result, error) {
	spec := r.spec(k.Type(), k.Type(), value)
	return operations.Run(ctx, k.writer.db, spec, func(ctx context.Context, tx *sql.Tx, operationID string) (operations.Outcome, error) {
		canonical, revision, err := lockEffectiveArtist(ctx, tx, r.ArtistID)
		if err != nil {
			return operations.Outcome{}, err
		}
		if err := checkArtistRevision(revision, r.ExpectedRevision); err != nil {
			return operations.Outcome{}, err
		}
		if storeImage != nil {
			if err := storeImage(ctx, tx); err != nil {
				return operations.Outcome{}, err
			}
		}
		return k.change(ctx, tx, operationID, canonical, canonical, &value, "")
	})
}

func (w *Writer) PutArtistBio(ctx context.Context, r ArtistEditRequest, text, lang string) (operations.Result, error) {
	text = strings.TrimSpace(text)
	if (lang != "zh" && lang != "en") || text == "" || !utf8.ValidString(text) || strings.ContainsRune(text, 0) || utf8.RuneCountInString(text) > 4000 {
		return operations.Result{}, operations.Invalid("invalid_input", "艺人简介文本或语言无效")
	}
	return manualBioKind(w).put(ctx, r, manualMedia{Text: text, Lang: lang}, nil)
}

func (w *Writer) PutArtistImage(ctx context.Context, r ArtistEditRequest, data []byte) (operations.Result, error) {
	stored, err := w.artworks.Describe(data)
	if err != nil || stored.MIMEType == "image/gif" || stored.Width > math.MaxInt32 || stored.Height > math.MaxInt32 {
		return operations.Result{}, operations.Invalid("invalid_input", "艺人图片格式或尺寸无效")
	}
	value := manualMedia{Resource: stored.Key, MIME: stored.MIMEType, Width: stored.Width, Height: stored.Height}
	wrote := false
	result, err := manualImageKind(w).put(ctx, r, value, func(ctx context.Context, tx *sql.Tx) error {
		var writeErr error
		wrote, writeErr = w.artworks.WriteIfChanged(ctx, tx, data, stored)
		return writeErr
	})
	if err != nil && wrote {
		w.discardArtistImage(ctx, stored.Key, r.ArtistID)
	}
	return result, err
}
