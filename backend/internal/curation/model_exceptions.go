package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/qlqq/roomusic/backend/internal/operations"
)

type ExceptionReview struct {
	ID       string   `json:"id"`
	Model    string   `json:"model"`
	Decision string   `json:"decision"`
	Source   string   `json:"source"`
	Risk     *string  `json:"risk"`
	Reasons  []string `json:"reasons"`
}
type ExceptionRelease struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Artist string `json:"artist"`
}
type ExceptionArtist struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type ExceptionTrack struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}
type ModelException struct {
	Track          *ExceptionTrack   `json:"track,omitempty"`
	ID             string            `json:"id"`
	PendingItemID  string            `json:"pending_item_id"`
	PlanItemID     string            `json:"-"`
	Provider       string            `json:"-"`
	Tool           string            `json:"tool"`
	Input          json.RawMessage   `json:"input"`
	Preview        json.RawMessage   `json:"preview"`
	Review         ExceptionReview   `json:"review"`
	ConversationID *string           `json:"conversation_id"`
	Kind           string            `json:"kind"`
	Release        *ExceptionRelease `json:"release"`
	Artist         *ExceptionArtist  `json:"artist"`
	Status         string            `json:"status"`
	DecidedBy      *string           `json:"decided_by"`
	DecidedAt      *time.Time        `json:"decided_at"`
	OperationIDs   []string          `json:"operation_ids"`
	CreatedAt      time.Time         `json:"created_at"`
}

func (w *Writer) CreateException(ctx context.Context, e ModelException) (string, error) {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE curation_pending_items SET status='waiting_human',next_attempt_at=NULL,updated_at=now()
 WHERE id=$1 AND conversation_id=$2 AND status IN ('waiting_human','in_progress')`, e.PendingItemID, e.ConversationID)
	if err := requireChanged(result, err, "item_stale", "待定项已变更"); err != nil {
		return "", err
	}
	if e.Review.Reasons == nil {
		e.Review.Reasons = []string{}
	}
	reasons, err := json.Marshal(e.Review.Reasons)
	if err != nil {
		return "", err
	}
	var id string
	err = tx.QueryRowContext(ctx, `INSERT INTO curation_model_exceptions(pending_item_id,plan_item_id,tool,input,preview,provider,
 review_id,review_model,review_decision,review_source,risk,reasons,conversation_id)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
 ON CONFLICT(plan_item_id) DO UPDATE SET plan_item_id=excluded.plan_item_id RETURNING id`, e.PendingItemID, e.PlanItemID, e.Tool, e.Input, e.Preview, NormalizeProvider(e.Provider), e.Review.ID, e.Review.Model, e.Review.Decision, e.Review.Source, e.Review.Risk, reasons, e.ConversationID).Scan(&id)
	if err != nil {
		return "", err
	}
	return id, tx.Commit()
}

const exceptionSelect = `SELECT x.id,x.pending_item_id,x.plan_item_id,x.provider,x.tool,x.input,(x.preview-'confirmation_snapshot'),x.review_id,x.review_model,
 x.review_decision,x.review_source,x.risk,x.reasons,x.conversation_id,p.kind,r.id,r.title,r.artist,a.id,coalesce(ap.effective_display_name,a.initial_name),x.status,x.decided_by,
 x.decided_at,to_json(x.operation_ids),x.created_at,t.id,t.title FROM curation_model_exceptions x JOIN curation_pending_items p ON p.id=x.pending_item_id
 LEFT JOIN release_effective r ON r.id=p.target_id
 LEFT JOIN artist_overrides ao ON ao.artist_id=p.artist_id
 LEFT JOIN artists a ON a.id=coalesce(ao.redirect_artist_id,p.artist_id)
 LEFT JOIN artist_profiles ap ON ap.artist_id=a.id
 LEFT JOIN track_effective t ON x.tool='correct_track' AND t.id=(x.input->>'track_id')::uuid`

func scanException(row interface{ Scan(...any) error }) (ModelException, error) {
	var e ModelException
	var trackID, title, releaseID, releaseTitle, releaseArtist, artistID, artistName *string
	var reasons, ops []byte
	err := row.Scan(&e.ID, &e.PendingItemID, &e.PlanItemID, &e.Provider, &e.Tool, &e.Input, &e.Preview, &e.Review.ID, &e.Review.Model, &e.Review.Decision, &e.Review.Source, &e.Review.Risk, &reasons, &e.ConversationID, &e.Kind, &releaseID, &releaseTitle, &releaseArtist, &artistID, &artistName, &e.Status, &e.DecidedBy, &e.DecidedAt, &ops, &e.CreatedAt, &trackID, &title)
	if err != nil {
		return e, err
	}
	if releaseID != nil {
		e.Release = &ExceptionRelease{*releaseID, *releaseTitle, *releaseArtist}
	}
	if artistID != nil {
		e.Artist = &ExceptionArtist{*artistID, *artistName}
	}
	if trackID != nil {
		e.Track = &ExceptionTrack{ID: *trackID, Title: *title}
	}
	if err = json.Unmarshal(reasons, &e.Review.Reasons); err != nil {
		return e, err
	}
	err = json.Unmarshal(ops, &e.OperationIDs)
	return e, err
}

func (w *Writer) Exception(ctx context.Context, id string) (ModelException, error) {
	e, err := scanException(w.db.QueryRowContext(ctx, exceptionSelect+" WHERE x.id=$1", id))
	if errors.Is(err, sql.ErrNoRows) {
		return e, operations.NotFound("not_found", "例外不存在")
	}
	return e, err
}
func (w *Writer) ListExceptions(ctx context.Context, status string, page, size int) ([]ModelException, int64, error) {
	if (status != "open" && status != "all") || page < 1 || size < 1 || size > 100 {
		return nil, 0, operations.Invalid("invalid_input", "分页或例外状态无效")
	}
	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	const filter = " WHERE ($1='all' OR x.status='open')"
	var total int64
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM curation_model_exceptions x"+filter, status).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := tx.QueryContext(ctx, exceptionSelect+filter+" ORDER BY x.created_at DESC,x.id DESC LIMIT $2 OFFSET $3", status, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []ModelException{}
	for rows.Next() {
		e, err := scanException(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, tx.Commit()
}
func (w *Writer) ClaimException(ctx context.Context, id, actor string) error {
	r, err := w.db.ExecContext(ctx, `UPDATE curation_model_exceptions SET status='accepting',decided_by=$2
 WHERE id=$1 AND (status='open' OR (status='accepting' AND decided_by=$2))`, id, actor)
	return requireChanged(r, err, "exception_decided", "例外已由其他操作处理")
}
func (w *Writer) ReopenException(ctx context.Context, id, actor string) error {
	r, err := w.db.ExecContext(ctx, `UPDATE curation_model_exceptions SET status='open',decided_by=NULL,preview=preview-'confirmation_snapshot' WHERE id=$1 AND decided_by=$2 AND status='accepting'`, id, actor)
	return requireChanged(r, err, "exception_decided", "例外状态已变更")
}

// 与不再提出标记共用调用方事务；先锁待定项，让同一项的最后一个结论负责收尾。
func FinishException(ctx context.Context, tx *sql.Tx, id, actor, decision string, operationIDs []string) error {
	if decision != "accepted" && decision != "declined" {
		return operations.Invalid("invalid_input", "例外结论无效")
	}
	var pending, kind string
	err := tx.QueryRowContext(ctx, `SELECT p.id,p.kind FROM curation_pending_items p JOIN curation_model_exceptions x ON x.pending_item_id=p.id WHERE x.id=$1 FOR UPDATE OF p`, id).Scan(&pending, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return operations.NotFound("not_found", "例外不存在")
	}
	if err != nil {
		return err
	}
	r, err := tx.ExecContext(ctx, `UPDATE curation_model_exceptions SET status=$3,decided_by=$2,decided_at=now(),preview=preview-'confirmation_snapshot',operation_ids=coalesce($4::uuid[],'{}')
 WHERE id=$1 AND ((status='accepting' AND decided_by=$2 AND $3='accepted') OR (status='open' AND $3='declined'))`, id, actor, decision, operationIDs)
	if err := requireChanged(r, err, "exception_decided", "例外已处理"); err != nil {
		return err
	}
	if kind == "artist_identity" && decision == "accepted" {
		_, err = tx.ExecContext(ctx, `UPDATE curation_model_exceptions SET status='superseded',decided_by=$2,decided_at=now()
 WHERE pending_item_id=$1 AND status='open'`, pending, actor)
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `WITH summary AS (
 SELECT bool_or(status IN ('open','accepting')) AS pending,
 bool_or(tool IN ('resolve_release_match','merge_releases','resolve_artist_identity') AND status='accepted') AS accepted,
 bool_or(tool IN ('resolve_release_match','merge_releases') AND status='declined') AS declined FROM curation_model_exceptions WHERE pending_item_id=$1)
 UPDATE curation_pending_items SET status=CASE WHEN accepted OR declined THEN 'resolved' ELSE 'waiting_model' END,
 resolution=CASE WHEN accepted THEN 'human_accepted' WHEN declined THEN 'human_declined' END,
 resolved_at=CASE WHEN accepted OR declined THEN now() END,
 conversation_id=CASE WHEN accepted OR declined THEN conversation_id END,claimed_at=NULL,review_retries=0,next_attempt_at=NULL,updated_at=now()
 FROM summary WHERE id=$1 AND status='waiting_human' AND NOT pending`, pending)
	return err
}
