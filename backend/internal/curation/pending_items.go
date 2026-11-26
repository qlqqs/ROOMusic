package curation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

const pendingColumns = `id,target_type,coalesce(target_id::text,''),coalesce(artist_id::text,''),kind,status,rule_version,input_digest,evidence,candidates,conflicts,
 resolution,created_at,updated_at,resolved_at,coalesce(conversation_id::text,''),claimed_at,next_attempt_at,attempts,review_retries,shelved_reason,COALESCE(match_rule_version,0),COALESCE(identity_digest,''),COALESCE(route,''),item_tool_calls,item_tokens,source_retry_count,claim_source_recovery_at,empty_count,single_request,artist_web_generation`

func scanPending(row interface{ Scan(...any) error }) (PendingItem, error) {
	var p PendingItem
	err := row.Scan(&p.ID, &p.TargetType, &p.TargetID, &p.ArtistID, &p.Kind, &p.Status, &p.RuleVersion, &p.InputDigest, &p.Evidence, &p.Candidates, &p.Conflicts, &p.Resolution, &p.CreatedAt, &p.UpdatedAt, &p.ResolvedAt, &p.ConversationID, &p.ClaimedAt, &p.NextAttemptAt, &p.Attempts, &p.ReviewRetries, &p.ShelvedReason, &p.MatchRuleVersion, &p.IdentityDigest, &p.Route, &p.ItemToolCalls, &p.ItemTokens, &p.SourceRetryCount, &p.ClaimSourceRecoveryAt, &p.EmptyCount, &p.SingleRequest, &p.ArtistWebGeneration)
	return p, err
}
func pendingRows(rows *sql.Rows, err error) ([]PendingItem, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PendingItem{}
	for rows.Next() {
		p, err := scanPending(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

func (w *Writer) PendingItem(ctx context.Context, id string) (PendingItem, error) {
	p, err := scanPending(w.db.QueryRowContext(ctx, `SELECT `+pendingColumns+` FROM curation_pending_items WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, operations.NotFound("not_found", "待定项不存在")
	}
	return p, err
}

// 锁住预览对应的待定项，避免认领、交给人工或程序先匹配后仍按旧计划写入。
func CheckMatchItem(ctx context.Context, tx *sql.Tx, item PendingItem, conversation string, confirmed bool) error {
	access, _ := ctx.Value(suggestionAccessKey{}).(suggestionAccess)
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM curation_pending_items
 WHERE id=$1 AND (target_id=NULLIF($2,'')::uuid OR (artist_id=NULLIF($7,'')::uuid
 AND NOT EXISTS(SELECT FROM artist_overrides o WHERE o.artist_id=curation_pending_items.artist_id AND o.redirect_artist_id IS NOT NULL)))
 AND kind=$3 AND input_digest=$4
 AND ((NOT $6 AND conversation_id=NULLIF($5,'')::uuid AND status='in_progress') OR ($6 AND status='resolved' AND EXISTS (
 SELECT FROM curation_model_exceptions x WHERE x.id=NULLIF($8,'')::uuid AND x.pending_item_id=curation_pending_items.id AND (resolution='suggestion_recorded' OR x.reason_code='unverified_fact') AND
 (($10 AND x.status='open') OR (NOT $10 AND x.status='accepting' AND x.decided_by=NULLIF($9,'')::uuid)) FOR SHARE OF x))) FOR SHARE)`,
		item.ID, item.TargetID, item.Kind, item.InputDigest, conversation, confirmed, item.ArtistID, access.id, access.actor, access.declining).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return operations.Conflict("item_stale", "待定项已变更")
	}
	return nil
}

// PendingTargetVisibleSQL 与 visible 发行 CTE 共用，只调度未并入其他身份的艺人。
const PendingTargetVisibleSQL = `(EXISTS(SELECT FROM visible v WHERE v.id=p.target_id)
 OR (p.artist_id IS NOT NULL AND NOT EXISTS(SELECT FROM artist_overrides o WHERE o.artist_id=p.artist_id AND o.redirect_artist_id IS NOT NULL)))`

func (w *Writer) ClaimPendingItems(ctx context.Context, conversation string, now time.Time, n int, targetTypes ...string) ([]PendingItem, error) {
	if len(targetTypes) == 0 {
		targetTypes = []string{"release", "artist"}
	}
	return w.ClaimModelKind(ctx, conversation, now, n, targetTypes, "", "")
}

// ClaimModelKind 在同一语句内复核类别、释放时间及可见性并领取；第二档还限定已巡检的发行。
func (w *Writer) ClaimModelKind(ctx context.Context, conversation string, now time.Time, n int, targets []string, kind, releaseID string, excluded ...string) ([]PendingItem, error) {
	return w.modelKindItems(ctx, conversation, now, n, targets, kind, releaseID, nil, excluded)
}

// SelectionItems 读取同类首批项，取得候选详情期间不持有行锁。
func (w *Writer) SelectionItems(ctx context.Context, now time.Time, n int, excluded []string) ([]PendingItem, error) {
	return w.PeekModelKind(ctx, now, n, "select", excluded)
}

func (w *Writer) PeekModelKind(ctx context.Context, now time.Time, n int, kind string, excluded []string) ([]PendingItem, error) {
	return w.modelKindItems(ctx, "", now, n, []string{"release", "artist"}, kind, "", nil, excluded)
}

// ClaimPreparedSelection 只领取仍就绪且准备期间未变更的项，保留已补全的候选和依据。
func (w *Writer) ClaimPreparedSelection(ctx context.Context, conversation string, now time.Time, prepared []PendingItem) ([]PendingItem, error) {
	return w.ClaimPreparedKind(ctx, conversation, now, "select", prepared)
}

func (w *Writer) ClaimPreparedKind(ctx context.Context, conversation string, now time.Time, kind string, prepared []PendingItem) ([]PendingItem, error) {
	claimed, err := w.modelKindItems(ctx, conversation, now, len(prepared), []string{"release", "artist"}, kind, "", prepared, nil)
	byID := make(map[string]PendingItem, len(prepared))
	for _, p := range prepared {
		byID[p.ID] = p
	}
	for i, p := range claimed {
		row := byID[p.ID]
		claimed[i].PreparedLocal, claimed[i].PreparedVerification = row.PreparedLocal, row.PreparedVerification
		claimed[i].Evidence, claimed[i].Candidates = row.Evidence, row.Candidates
		claimed[i].CanonicalTitle, claimed[i].CanonicalArtist = row.CanonicalTitle, row.CanonicalArtist
	}
	return claimed, err
}

func (w *Writer) DeferSelection(ctx context.Context, item PendingItem, next time.Time) error {
	_, err := w.db.ExecContext(ctx, `UPDATE curation_pending_items SET next_attempt_at=$3,source_retry_count=source_retry_count+1,updated_at=now()
 WHERE id=$1 AND updated_at=$2 AND status IN ('waiting_model','waiting_route')`, item.ID, item.UpdatedAt, next)
	return err
}

func (w *Writer) modelKindItems(ctx context.Context, conversation string, now time.Time, n int, targets []string, kind, releaseID string, prepared []PendingItem, excluded []string) ([]PendingItem, error) {
	// 按同一发行或艺人分组。只有自查和联网需要艺人投影。
	prefix := "WITH visible AS (" + catalog.VisibleReleaseIDsSQL + ")"
	group, albums := "p.kind", "0"
	if kind == "patrol_local" || kind == "local" {
		// 同目录的本地整理共享一次会话。名录块已经由巡检按艺人或专辑分好。
		group = "CASE WHEN p.kind='local_cleanup' THEN COALESCE(p.evidence->>'directory_key',p.id::text) ELSE p.id::text END"
	}
	if kind == "lookup" || kind == "web" {
		prefix += `, credits AS (` + ArtistReleaseCreditsSQL + ` SELECT * FROM credits),
 release_artist AS (SELECT release_id,min(artist_id::text) AS artist FROM credits GROUP BY release_id HAVING count(DISTINCT artist_id)=1)`
		artist := "COALESCE(p.artist_id::text,(SELECT artist FROM release_artist WHERE release_id=p.target_id))"
		group = "COALESCE(" + artist + ",p.id::text)"
		if kind == "web" {
			prefix += `, album_counts AS (SELECT c.artist_id::text artist,count(DISTINCT rel.group_id) n
 FROM credits c JOIN visible v ON v.id=c.release_id JOIN releases rel ON rel.id=c.release_id GROUP BY c.artist_id)`
			albums = "COALESCE((SELECT n FROM album_counts WHERE artist=" + artist + "),0)"
		}
	}
	group = "CASE WHEN p.single_request THEN p.id::text ELSE " + group + " END"
	query := prefix + `, eligible AS (
 SELECT p.id,p.target_id,p.artist_id,p.created_at,` + group + ` AS pack,` + albums + ` AS albums,
 CASE WHEN $4='artist_web' THEN p.artist_web_priority WHEN $4='' THEN 0 ELSE ` + QueueRankSQL + ` END AS priority` + modelQueueFromSQL + `
 AND p.target_type=ANY($3::text[]) AND (p.next_attempt_at IS NULL OR p.next_attempt_at<=$1)
 AND ($4='' OR ` + modelKindSQL + `=$4 OR ($4='unavailable_web' AND p.route='web'))
 AND NOT (p.id=ANY(COALESCE($6::uuid[], '{}')))`
	args := []any{now, n, targets, kind, releaseID, excluded}
	if kind == "local" {
		query += localDirectoryReadySQL("$1", "$6")
	}
	if conversation != "" {
		args = append(args, conversation)
	}
	if len(prepared) > 0 {
		var ids []string
		var versions []time.Time
		for _, p := range prepared {
			ids = append(ids, p.ID)
			versions = append(versions, p.UpdatedAt)
		}
		query += ` AND (p.id,p.updated_at) IN (SELECT * FROM unnest($8::uuid[],$9::timestamptz[]))`
		args = append(args, ids, versions)
	}
	if kind == "artist_web" {
		args = append(args, ArtistWebActor(ctx))
		query += fmt.Sprintf(" AND p.artist_web_actor=$%d", len(args))
	} else if kind == "" {
		query += " AND p.artist_web_generation IS NULL"
	}
	query += `), seed AS (
 SELECT pack,priority FROM eligible WHERE $5='' OR target_id=NULLIF($5,'')::uuid
 ORDER BY albums DESC,priority,CASE WHEN $4='artist_web' THEN artist_id END,created_at,id LIMIT 1
 ), picked AS (
 SELECT p.id,e.priority,e.albums FROM eligible e JOIN curation_pending_items p ON p.id=e.id
 WHERE ($4='' OR e.pack=(SELECT pack FROM seed)) AND ($4 NOT IN ('select','verify') OR e.priority=(SELECT priority FROM seed)) AND p.status IN ('waiting_model','waiting_route')`
	query += ` ORDER BY (e.target_id=NULLIF($5,'')::uuid) DESC NULLS LAST,e.albums DESC,e.priority,CASE WHEN $4='artist_web' THEN e.artist_id END,e.created_at,e.id LIMIT CASE WHEN $4='local' THEN NULL ELSE $2::int END`
	rows := "curation_pending_items"
	if conversation != "" {
		query += ` FOR UPDATE OF p SKIP LOCKED
 ), claimed AS (UPDATE curation_pending_items SET status='in_progress',conversation_id=$7,claimed_at=$1,updated_at=now(),next_attempt_at=NULL,
 item_tool_calls=CASE WHEN reset_item_usage_on_claim THEN 0 ELSE item_tool_calls END,
 item_tokens=CASE WHEN reset_item_usage_on_claim THEN 0 ELSE item_tokens END,
 source_retry_count=CASE WHEN reset_item_usage_on_claim OR $4='select' THEN 0 ELSE source_retry_count END,reset_item_usage_on_claim=false
 WHERE id IN (SELECT id FROM picked) RETURNING *)`
		rows = "claimed"
	} else {
		query += ")"
	}
	return pendingRows(w.db.QueryContext(ctx, query+` SELECT `+pendingColumns+` FROM (SELECT c.*,p.priority,p.albums FROM `+rows+` c JOIN picked p ON p.id=c.id) ordered
 ORDER BY albums DESC,priority,created_at,id`, args...))
}

func (w *Writer) ClaimedItems(ctx context.Context, conversation string) ([]PendingItem, error) {
	return pendingRows(w.db.QueryContext(ctx, `SELECT `+pendingColumns+` FROM curation_pending_items
 WHERE status='in_progress' AND conversation_id=$1 ORDER BY created_at,id`, conversation))
}

func (w *Writer) ReleaseClaimed(ctx context.Context, conversation string, countAttempt bool, nextAttemptAt *time.Time) error {
	return w.ReleaseModelItems(ctx, conversation, nil, countAttempt, nextAttemptAt)
}

func (w *Writer) ReleaseModelItems(ctx context.Context, conversation string, ids []string, countAttempt bool, nextAttemptAt *time.Time) error {
	_, err := w.db.ExecContext(ctx, `UPDATE curation_pending_items SET
 attempts=attempts+$2::boolean::int,status=CASE WHEN $2 AND attempts>=2 THEN 'shelved' WHEN route IN ('lookup','web') THEN 'waiting_route' ELSE 'waiting_model' END,
 source_retry_count=CASE WHEN $2 THEN 0 ELSE source_retry_count END,
 shelved_reason=CASE WHEN $2 AND attempts>=2 THEN 'unhandled' END,
 conversation_id=NULL,claimed_at=NULL,review_retries=0,next_attempt_at=$3,updated_at=now()
 WHERE status='in_progress' AND conversation_id=$1 AND ($4::uuid[] IS NULL OR id=ANY($4::uuid[]))`, conversation, countAttempt, nextAttemptAt, ids)
	return err
}

// RecordClaimSourceFailure 保留本次领取里最晚的来源恢复时刻，重审与进程恢复共用。
func (w *Writer) RecordClaimSourceFailure(ctx context.Context, item PendingItem, conversation string, recovery time.Time) error {
	_, err := w.db.ExecContext(ctx, `UPDATE curation_pending_items SET claim_source_recovery_at=GREATEST(claim_source_recovery_at,$4)
 WHERE id=$1 AND input_digest=$2 AND conversation_id=$3 AND status='in_progress' AND COALESCE(identity_digest,'')=$5`,
		item.ID, item.InputDigest, conversation, recovery, item.IdentityDigest)
	return err
}

func (w *Writer) ClearResolvedSourceFailures(ctx context.Context, itemID, conversation string) error {
	_, err := w.db.ExecContext(ctx, `UPDATE curation_pending_items SET source_retry_count=0,claim_source_recovery_at=NULL
 WHERE id=$1 AND conversation_id=$2 AND status='resolved'`, itemID, conversation)
	return err
}

func (w *Writer) InterruptedSourceItems(ctx context.Context) ([]PendingItem, error) {
	return pendingRows(w.db.QueryContext(ctx, `SELECT `+pendingColumns+` FROM curation_pending_items WHERE status='in_progress'
 AND (claim_source_recovery_at IS NOT NULL OR source_retry_count>0)`))
}

// DeferModelItem 保留该项的原路线和尝试次数，保留本档用量。触限后只在到期领取时重置。
func (w *Writer) DeferModelItem(ctx context.Context, item PendingItem, conversation, route string, next time.Time, resetOnClaim bool) error {
	r, err := w.db.ExecContext(ctx, `UPDATE curation_pending_items SET status='waiting_route',route=$4,
 conversation_id=NULL,claimed_at=NULL,next_attempt_at=$5,review_retries=0,source_retry_count=source_retry_count+1,claim_source_recovery_at=NULL,reset_item_usage_on_claim=$8,updated_at=now()
 WHERE id=$1 AND input_digest=$2 AND conversation_id=$3 AND status='in_progress'
 AND COALESCE(identity_digest,'')=$6 AND COALESCE(route,'')=$7`, item.ID, item.InputDigest, conversation, route, next, item.IdentityDigest, item.Route, resetOnClaim)
	return requireChanged(r, err, "item_stale", "待定项已变更")
}

func (w *Writer) RetryClaimedReview(ctx context.Context, conversation string, next time.Time) error {
	_, err := w.db.ExecContext(ctx, `UPDATE curation_pending_items SET review_retries=review_retries+1,next_attempt_at=$2,updated_at=now()
 WHERE conversation_id=$1 AND status='in_progress' AND review_retries<3`, conversation, next)
	return err
}

func (w *Writer) RecoverInProgress(ctx context.Context) error {
	_, err := w.db.ExecContext(ctx, `UPDATE curation_pending_items SET status=CASE WHEN route IN ('lookup','web') THEN 'waiting_route' ELSE 'waiting_model' END,conversation_id=NULL,claimed_at=NULL,review_retries=0,next_attempt_at=NULL,updated_at=now() WHERE status='in_progress'`)
	return err
}

func (w *Writer) ShelvePendingItem(ctx context.Context, id, conversation, reason string) error {
	if len(reason) == 0 || len(reason) > 1000 {
		return operations.Invalid("invalid_input", "搁置原因长度无效")
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if run, ok := ModelRunFrom(ctx); ok {
		actor, err := activeEnabler(ctx, tx)
		if err != nil {
			return err
		}
		if actor != run.ActorID || run.ConversationID != conversation || run.PendingItemID != id {
			return operations.Conflict("out_of_batch", "待定项不属于本次运行")
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE curation_pending_items SET status='shelved',shelved_reason=$3,next_attempt_at=NULL,updated_at=now()
 WHERE id=$1 AND conversation_id=$2 AND status='in_progress'`, id, conversation, reason)
	if err := requireChanged(result, err, "out_of_batch", "待定项不属于本次运行"); err != nil {
		return err
	}
	return tx.Commit()
}

func ResolvePendingItem(ctx context.Context, q Execer, id, conversation, resolution string) error {
	if resolution != "stale" {
		if err := countModelAnswer(ctx, q, id, conversation); err != nil {
			return err
		}
	}
	result, err := q.ExecContext(ctx, `UPDATE curation_pending_items SET status='resolved',resolution=$3,resolved_at=coalesce(resolved_at,now()),updated_at=now(),next_attempt_at=NULL
 WHERE id=$1 AND ((status='in_progress' AND conversation_id=NULLIF($2,'')::uuid) OR (status='waiting_human' AND $2='') OR (status='resolved' AND resolution=$3))`, id, conversation, resolution)
	return requireChanged(result, err, "item_stale", "待定项状态已变更")
}

func requireChanged(result sql.Result, err error, code, message string) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return operations.Conflict(code, message)
	}
	return err
}

type RejectedSubmission struct {
	ItemID, Tool, Code, Reason string
}

// RecordRejectedModelTool 只收录当前领取项的提交失败，供本项后续路线读取。
func (w *Writer) RecordRejectedModelTool(ctx context.Context, item PendingItem, conversation, route string, submission RejectedSubmission) error {
	_, err := w.db.ExecContext(ctx, `WITH rejection AS (SELECT jsonb_build_array(jsonb_build_object(
 'from',$5::text,'tool',$6::text,'code',$7::text,'reason',$8::text)) AS entry)
 UPDATE curation_pending_items SET evidence=jsonb_set(evidence,'{route_history}',COALESCE(evidence->'route_history','[]'::jsonb) || entry),
 updated_at=now() FROM rejection WHERE id=$1 AND status='in_progress' AND conversation_id=$2 AND input_digest=$3
 AND COALESCE(identity_digest,'')=$4 AND NOT COALESCE(evidence->'route_history','[]'::jsonb) @> entry`,
		item.ID, conversation, item.InputDigest, item.IdentityDigest, route, submission.Tool, submission.Code, submission.Reason)
	return err
}

// AdvanceModelItem 同时释放本档领取、登记下一档并重置单项用量。
func (w *Writer) AdvanceModelItem(ctx context.Context, item PendingItem, conversation, from, route, reason string) error {
	if route != "lookup" && route != "web" {
		return operations.Invalid("invalid_input", "模型处理路线无效")
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	identity := item.IdentityDigest
	if identity == "" && (item.Kind == "release_match" || item.Kind == "recording_match") {
		// 老项仅保存完整指纹，升级路线前用当前依据核实并补全识别摘要。
		rows, err := catalog.LoadCurationEvidence(ctx, tx, []string{item.TargetID}, w.ArtworkExists)
		if err != nil {
			return err
		}
		e, ok := rows[item.TargetID]
		if !ok || ReleaseDigest(e) != item.InputDigest {
			return operations.Conflict("item_stale", "发行识别依据已变化")
		}
		identity = ReleaseIdentityDigest(e)
		_, err = tx.ExecContext(ctx, `UPDATE curation_work_states SET identity_digest=$3
 WHERE subject='release' AND target_id=$1 AND input_digest=$2 AND identity_digest IS NULL AND status='uncertain'`, item.TargetID, item.InputDigest, identity)
		if err != nil {
			return err
		}
	}
	r, err := tx.ExecContext(ctx, `UPDATE curation_pending_items SET route=$4,status='waiting_route',identity_digest=NULLIF($6,''),
 evidence=jsonb_set(evidence,'{route_history}',COALESCE(evidence->'route_history','[]'::jsonb) ||
 jsonb_build_object('from',$8::text,'to',$4::text,'reason',$9::text)),
 conversation_id=NULL,claimed_at=NULL,next_attempt_at=NULL,attempts=0,review_retries=0,item_tool_calls=0,item_tokens=0,source_retry_count=0,claim_source_recovery_at=NULL,reset_item_usage_on_claim=false,updated_at=now()
 WHERE id=$1 AND input_digest=$2 AND conversation_id=$3 AND status='in_progress' AND COALESCE(identity_digest,'')=$5 AND COALESCE(route,'')=$7`, item.ID, item.InputDigest, conversation, route, item.IdentityDigest, identity, item.Route, from, reason)
	if err := requireChanged(r, err, "item_stale", "待定项已变更"); err != nil {
		return err
	}
	return tx.Commit()
}

func (w *Writer) FinishModelItem(ctx context.Context, item PendingItem, conversation, resolution string) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = finishModelItem(ctx, tx, item, conversation, resolution); err != nil {
		return err
	}
	return tx.Commit()
}

func finishModelItem(ctx context.Context, tx *sql.Tx, item PendingItem, conversation, resolution string) error {
	if err := countModelAnswer(ctx, tx, item.ID, conversation); err != nil {
		return err
	}
	r, err := tx.ExecContext(ctx, `UPDATE curation_pending_items SET status='resolved',resolution=$4,resolved_at=now(),updated_at=now(),claimed_at=NULL,next_attempt_at=NULL
 WHERE id=$1 AND input_digest=$2 AND conversation_id=$3 AND status='in_progress' AND COALESCE(identity_digest,'')=$5`, item.ID, item.InputDigest, conversation, resolution, item.IdentityDigest)
	return requireChanged(r, err, "item_stale", "待定项已变更")
}
