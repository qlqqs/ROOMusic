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

// 尚未送审的建议没有审查编号。
func (r ExceptionReview) MarshalJSON() ([]byte, error) {
	if r.ID == "" {
		return []byte("null"), nil
	}
	type review ExceptionReview
	return json.Marshal(review(r))
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
	ExpectedClaimDigest string            `json:"-"`
	ReasonCode          string            `json:"reason_code"`
	Reasons             []string          `json:"reasons"`
	Evidence            json.RawMessage   `json:"evidence"`
	CanAccept           bool              `json:"can_accept"`
	Track               *ExceptionTrack   `json:"track,omitempty"`
	ID                  string            `json:"id"`
	PendingItemID       string            `json:"pending_item_id"`
	PlanItemID          string            `json:"-"`
	Provider            string            `json:"-"`
	Tool                string            `json:"tool"`
	Input               json.RawMessage   `json:"input"`
	Preview             json.RawMessage   `json:"preview"`
	Review              ExceptionReview   `json:"review"`
	ConversationID      *string           `json:"conversation_id"`
	Kind                string            `json:"kind"`
	Release             *ExceptionRelease `json:"release"`
	Artist              *ExceptionArtist  `json:"artist"`
	Status              string            `json:"status"`
	DecidedBy           *string           `json:"decided_by"`
	DecidedAt           *time.Time        `json:"decided_at"`
	OperationIDs        []string          `json:"operation_ids"`
	CreatedAt           time.Time         `json:"created_at"`
}

func (w *Writer) CreateException(ctx context.Context, e ModelException) (string, error) {
	return w.RecordSuggestion(ctx, e)
}

// RecordSuggestion 将一个已领取项的具体主张和终态一起写入；同批的独立主张各保留一条。
func (w *Writer) RecordSuggestion(ctx context.Context, e ModelException) (string, error) {
	if run, ok := ModelRunFrom(ctx); ok && run.DeferSuggestion != nil && run.PendingItemID == e.PendingItemID && run.DeferSuggestion() {
		return "", nil
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var item PendingItem
	if e.ConversationID != nil {
		if err = countModelAnswer(ctx, tx, e.PendingItemID, *e.ConversationID); err != nil {
			return "", err
		}
	}
	err = tx.QueryRowContext(ctx, `UPDATE curation_pending_items SET status='resolved',resolution='suggestion_recorded',
 resolved_at=COALESCE(resolved_at,now()),claimed_at=NULL,next_attempt_at=NULL,updated_at=now()
 WHERE id=$1 AND conversation_id=$2 AND (status='in_progress' OR (status='resolved' AND resolution='suggestion_recorded'))
 RETURNING id,kind,input_digest,COALESCE(identity_digest,''),evidence,artist_web_generation,COALESCE(artist_id::text,'')`, e.PendingItemID, e.ConversationID).Scan(&item.ID, &item.Kind, &item.InputDigest, &item.IdentityDigest, &item.Evidence, &item.ArtistWebGeneration, &item.ArtistID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", operations.Conflict("item_stale", "建议所据的待定项已变更")
	}
	if err != nil {
		return "", err
	}
	if err = CheckArtistWebInput(ctx, tx, item); err != nil {
		return "", err
	}
	if run, ok := ModelRunFrom(ctx); ok && run.ClaimDigests != nil && run.ClaimDigests[item.ID] != item.ModelClaimDigest() {
		return "", operations.Conflict("item_stale", "建议所据的领取摘要已变更")
	}
	if e.ExpectedClaimDigest != "" && e.ExpectedClaimDigest != item.ModelClaimDigest() {
		return "", operations.Conflict("item_stale", "建议所据的领取摘要已变更")
	}
	if e.ReasonCode == "" {
		switch e.Review.Decision {
		case "rejected":
			e.ReasonCode = "review_rejected"
		case "needs_human_confirmation":
			e.ReasonCode = "review_uncertain"
		default:
			e.ReasonCode = "no_evidence"
		}
	}
	if e.Reasons == nil {
		e.Reasons = e.Review.Reasons
	}
	if e.Reasons == nil {
		e.Reasons = []string{}
	}
	if e.Tool == "submit_verification" && e.Review.ID == "" {
		// 即使未获佐证，保留原主张的前后值，管理员可以看到并显式采纳。
		var v VerificationCandidate
		if item.Kind == "verify_artist_credit" {
			var p ArtistCreditProposal
			if err = json.Unmarshal(item.Evidence, &p); err != nil {
				return "", err
			}
			v.Field, v.Before, v.After = "artist_link", p.Local.Name, p.External.CreditName
		} else if err = json.Unmarshal(item.Evidence, &v); err != nil {
			return "", err
		}
		if v.Field != "" && v.After != "" {
			e.Preview, err = json.Marshal(map[string]any{"changes": []map[string]any{{"field": v.Field, "before": v.Before, "after": v.After}}})
			if err != nil {
				return "", err
			}
		}
	}
	if len(e.Evidence) == 0 {
		e.Evidence = item.Evidence
	}
	if err = FinishFactualSuggestions(ctx, tx, item.ID, *e.ConversationID); err != nil {
		return "", err
	}
	id, err := insertSuggestion(ctx, tx, e, item.InputDigest)
	if err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE curation_artist_credit_proposals SET status='declined'
 WHERE id=$1 AND status='open'`, e.PendingItemID); err != nil {
		return "", err
	}
	return id, tx.Commit()
}

func insertSuggestion(ctx context.Context, tx *sql.Tx, e ModelException, digest string) (string, error) {
	reasons, err := json.Marshal(e.Reasons)
	if err != nil {
		return "", err
	}
	// 已审方案用方案项编号重放；未送审的主张按原始项和实际输入去重。
	var id string
	err = tx.QueryRowContext(ctx, `INSERT INTO curation_model_exceptions(pending_item_id,plan_item_id,tool,input,preview,provider,
 review_id,review_model,review_decision,review_source,risk,reasons,conversation_id,reason_code,evidence,suggestion_key)
 VALUES($1,NULLIF($2,'')::uuid,$3,$4,$5,$6,NULLIF($7::text,'')::uuid,
 CASE WHEN $7::text='' THEN NULL ELSE $8::text END,NULLIF($9,''),NULLIF($10,''),$11,$12,$13,$14,COALESCE($15::jsonb,'{}'),$16)
 ON CONFLICT(suggestion_key) DO UPDATE SET suggestion_key=excluded.suggestion_key RETURNING id`,
		e.PendingItemID, e.PlanItemID, e.Tool, e.Input, e.Preview, NormalizeProvider(e.Provider),
		e.Review.ID, e.Review.Model, e.Review.Decision, e.Review.Source, e.Review.Risk, reasons, e.ConversationID, e.ReasonCode, e.Evidence,
		suggestionKey(e, digest)).Scan(&id)
	if err != nil {
		return "", err
	}
	return id, nil
}

func suggestionKey(e ModelException, digest string) string {
	if e.PlanItemID != "" {
		return e.PlanItemID
	}
	return DerivedKey(e.PendingItemID, operations.Fingerprint([]any{digest, e.Tool, e.Input}))
}

const exceptionSelect = `SELECT x.id,x.pending_item_id,COALESCE(x.plan_item_id::text,''),x.provider,x.tool,x.input,
 (CASE WHEN x.tool='observation' THEN x.preview || '{"changes":[]}'::jsonb ELSE x.preview END)-'confirmation_snapshot',COALESCE(x.review_id::text,''),COALESCE(x.review_model,''),
 COALESCE(x.review_decision,''),COALESCE(x.review_source,''),x.risk,x.reasons,x.conversation_id,p.kind,r.id,r.title,r.artist,a.id,coalesce(ap.effective_display_name,a.initial_name),x.status,x.decided_by,
 x.decided_at,to_json(x.operation_ids),x.created_at,t.id,t.title,x.reason_code,x.evidence FROM curation_model_exceptions x JOIN curation_pending_items p ON p.id=x.pending_item_id
 LEFT JOIN release_effective r ON r.id=p.target_id
 LEFT JOIN artist_overrides ao ON ao.artist_id=p.artist_id
 LEFT JOIN artists a ON a.id=coalesce(ao.redirect_artist_id,p.artist_id)
 LEFT JOIN artist_profiles ap ON ap.artist_id=a.id
 LEFT JOIN track_effective t ON t.id=CASE WHEN x.tool='correct_track' THEN NULLIF(x.input->>'track_id','')::uuid
 WHEN x.tool='submit_verification' AND p.kind='verify_name' THEN NULLIF(p.evidence->>'track_id','')::uuid END`

func scanException(row interface{ Scan(...any) error }) (ModelException, error) {
	var e ModelException
	var trackID, title, releaseID, releaseTitle, releaseArtist, artistID, artistName *string
	var reasons, ops []byte
	err := row.Scan(&e.ID, &e.PendingItemID, &e.PlanItemID, &e.Provider, &e.Tool, &e.Input, &e.Preview, &e.Review.ID, &e.Review.Model, &e.Review.Decision, &e.Review.Source, &e.Review.Risk, &reasons, &e.ConversationID, &e.Kind, &releaseID, &releaseTitle, &releaseArtist, &artistID, &artistName, &e.Status, &e.DecidedBy, &e.DecidedAt, &ops, &e.CreatedAt, &trackID, &title, &e.ReasonCode, &e.Evidence)
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
	if err = json.Unmarshal(reasons, &e.Reasons); err != nil {
		return e, err
	}
	if e.Review.ID != "" {
		e.Review.Reasons = e.Reasons
	}
	e.CanAccept = (e.Status == "open" || e.Status == "accepting") && actionableSuggestion(e)
	err = json.Unmarshal(ops, &e.OperationIDs)
	return e, err
}

func actionableSuggestion(e ModelException) bool {
	switch e.Tool {
	case "resolve_release_match":
		var proof struct {
			Contradicted bool `json:"contradicted"`
		}
		return json.Unmarshal(e.Evidence, &proof) == nil && !proof.Contradicted
	case "resolve_artist_identity", "correct_release", "correct_track":
		return true
	case "submit_verification", "submit_artist_media", "merge_releases":
		var preview struct {
			Changes []json.RawMessage `json:"changes"`
		}
		return json.Unmarshal(e.Preview, &preview) == nil && len(preview.Changes) > 0
	}
	return false
}

// SuggestionRecorded 检查结清原因，避免把另一轮已解决的目标用于人工采纳。
func SuggestionRecorded(p PendingItem) bool {
	return p.Status == "resolved" && p.Resolution != nil && *p.Resolution == "suggestion_recorded"
}

type suggestionAccess struct {
	id, actor string
	declining bool
}
type suggestionAccessKey struct{}

// WithSuggestionDecision 将本次人工决定绑定到具体的建议。写入在目标锁内重新核实领取人。
func WithSuggestionDecision(ctx context.Context, id, actor string, declining bool) context.Context {
	return context.WithValue(ctx, suggestionAccessKey{}, suggestionAccess{id, actor, declining})
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
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var p string
	err = tx.QueryRowContext(ctx, `SELECT p.id FROM curation_pending_items p JOIN curation_model_exceptions x ON x.pending_item_id=p.id
 WHERE x.id=$1 AND p.status='resolved' AND (p.resolution='suggestion_recorded' OR x.reason_code='unverified_fact') FOR UPDATE OF p`, id).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) {
		return operations.Conflict("exception_decided", "建议已失效")
	}
	if err != nil {
		return err
	}
	r, err := tx.ExecContext(ctx, `UPDATE curation_model_exceptions x SET status='accepting',decided_by=$2
 WHERE x.id=$1 AND (x.status='open' OR (x.status='accepting' AND x.decided_by=$2))
 AND NOT EXISTS (SELECT FROM curation_model_exceptions sibling WHERE sibling.pending_item_id=$3 AND sibling.id<>x.id
 AND x.tool IN ('resolve_release_match','resolve_artist_identity','merge_releases') AND sibling.tool=x.tool AND sibling.status='accepting')`, id, actor, p)
	if err = requireChanged(r, err, "exception_decided", "建议已由其他操作处理"); err != nil {
		return err
	}
	return tx.Commit()
}
func (w *Writer) ReopenException(ctx context.Context, id, actor string) error {
	r, err := w.db.ExecContext(ctx, `UPDATE curation_model_exceptions SET status='open',decided_by=NULL,preview=preview-'confirmation_snapshot' WHERE id=$1 AND decided_by=$2 AND status='accepting'`, id, actor)
	return requireChanged(r, err, "exception_decided", "例外状态已变更")
}

// 与采纳操作或抑制标记共用事务；原待定项在产生建议时已经结清。
func FinishException(ctx context.Context, tx *sql.Tx, id, actor, decision string, operationIDs []string) error {
	if decision != "accepted" && decision != "declined" {
		return operations.Invalid("invalid_input", "例外结论无效")
	}
	var pending, tool string
	err := tx.QueryRowContext(ctx, `SELECT p.id,x.tool FROM curation_pending_items p JOIN curation_model_exceptions x ON x.pending_item_id=p.id WHERE x.id=$1 FOR UPDATE OF p`, id).Scan(&pending, &tool)
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
	if (tool == "resolve_artist_identity" || tool == "resolve_release_match" || tool == "merge_releases") && decision == "accepted" {
		_, err = tx.ExecContext(ctx, `UPDATE curation_model_exceptions SET status='superseded',decided_by=$2,decided_at=now()
 WHERE pending_item_id=$1 AND tool=$3 AND status='open'`, pending, actor, tool)
		if err != nil {
			return err
		}
	}
	return nil
}
