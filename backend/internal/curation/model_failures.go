package curation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// ModelFailure 是应用层已分类、可安全展示的调用失败。
type ModelFailure struct {
	Class, Code, Message string
	Status               int
	Delay                *time.Duration
}

func (f *ModelFailure) Error() string { return f.Message }

type ModelState struct {
	ConfigKey, ModelKey string
	Pause, Code, Detail *string
	RetryAt             *time.Time
	Verified            bool
}

const modelStateColumns = "config_key,model_key,pause_kind,reason_code,reason_detail,retry_at,tools_verified"

func readModelState(row *sql.Row) (s ModelState, err error) {
	err = row.Scan(&s.ConfigKey, &s.ModelKey, &s.Pause, &s.Code, &s.Detail, &s.RetryAt, &s.Verified)
	return
}

// ModelState 供整理及独立艺人队列读取同一端点的暂停状态。
func (w *Writer) ModelState(ctx context.Context) (ModelState, error) {
	return readModelState(w.db.QueryRowContext(ctx, "SELECT "+modelStateColumns+" FROM curation_model_state WHERE id"))
}

func (w *Writer) SyncModelConfig(ctx context.Context, config, model string) (ModelState, error) {
	current, err := w.ModelState(ctx)
	if err != nil || current.ConfigKey == config {
		return current, err
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return ModelState{}, err
	}
	defer tx.Rollback()
	if current.ModelKey != "" && current.ModelKey != model {
		if _, err = tx.ExecContext(ctx, `SELECT id FROM curation_pending_items WHERE status='in_progress' OR model_stop_code IS NOT NULL OR empty_count>0 OR single_request ORDER BY id FOR UPDATE`); err != nil {
			return ModelState{}, err
		}
	}
	current, err = readModelState(tx.QueryRowContext(ctx, "SELECT "+modelStateColumns+" FROM curation_model_state WHERE id FOR UPDATE"))
	if err != nil {
		return ModelState{}, err
	}
	if current.ConfigKey == config {
		return current, tx.Commit()
	}
	if current.ModelKey != "" && current.ModelKey != model {
		_, err = tx.ExecContext(ctx, `UPDATE curation_pending_items SET
            status=CASE WHEN status='shelved' THEN 'waiting_model' ELSE status END,
            model_stop_code=NULL,shelved_reason=NULL,empty_count=0,single_request=false,next_attempt_at=NULL,updated_at=now()
            WHERE model_stop_code IS NOT NULL OR empty_count>0 OR single_request`)
		if err != nil {
			return ModelState{}, err
		}
	}
	current, err = readModelState(tx.QueryRowContext(ctx, `UPDATE curation_model_state SET config_key=$1,model_key=$2,
        pause_kind=NULL,reason_code=NULL,reason_detail=NULL,retry_at=NULL,backoff_seconds=0,streak_key='',streak_items='{}',tools_verified=false WHERE id RETURNING `+modelStateColumns, config, model))
	if err != nil {
		return ModelState{}, err
	}
	return current, tx.Commit()
}

type modelConfigContext struct{}

func WithModelConfig(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, modelConfigContext{}, key)
}

func countModelAnswer(ctx context.Context, q Execer, id, conversation string) error {
	key, _ := ctx.Value(modelConfigContext{}).(string)
	if key == "" {
		return nil
	}
	_, err := q.ExecContext(ctx, `WITH answered AS(UPDATE curation_pending_items SET attempts=LEAST(3,attempts+1),empty_count=0,single_request=false
        WHERE id=$1 AND conversation_id=NULLIF($2,'')::uuid AND status='in_progress' RETURNING id)
        UPDATE curation_model_state SET streak_key='',streak_items='{}',backoff_seconds=0 WHERE config_key=$3 AND EXISTS(SELECT FROM answered)`, id, conversation, key)
	return err
}

// 更新项时同时校验领取和输入，使重复收尾不能重复累计。
func (w *Writer) FinishUndetermined(ctx context.Context, p PendingItem, id, reason string) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = countModelAnswer(ctx, tx, p.ID, id); err != nil {
		return err
	}
	r, err := tx.ExecContext(ctx, `UPDATE curation_pending_items SET status='resolved',resolution='undetermined',resolved_at=now(),updated_at=now(),
        claimed_at=NULL,next_attempt_at=NULL,evidence=evidence||jsonb_build_object('decision_reason',$5::text)
        WHERE id=$1 AND input_digest=$2 AND coalesce(identity_digest,'')=$3 AND conversation_id=$4 AND status='in_progress'`, p.ID, p.InputDigest, p.IdentityDigest, id, reason)
	if err = requireChanged(r, err, "item_stale", "待定项已变更"); err != nil {
		return err
	}
	return tx.Commit()
}

func (w *Writer) SetModelFailure(ctx context.Context, config string, f ModelFailure, now time.Time) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var pause *string
	var until *time.Time
	var backoff int
	err = tx.QueryRowContext(ctx, `SELECT pause_kind,retry_at,backoff_seconds FROM curation_model_state WHERE config_key=$1 FOR UPDATE`, config).Scan(&pause, &until, &backoff)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if pause != nil && (*pause == "configuration" || *pause == "empty_replies") && f.Class != "configuration" {
		return nil
	}
	if pause != nil && *pause == "provider_quota" && f.Class == "temporary" {
		return nil
	}
	var retry *time.Time
	switch f.Class {
	case "provider_quota":
		t := now.Add(time.Hour)
		retry = &t
	case "temporary":
		delay := f.Delay
		if delay == nil {
			if until == nil || !until.After(now) || backoff == 0 {
				backoff = min(180, max(5, 2*backoff))
			}
			d := time.Duration(backoff) * time.Second
			delay = &d
		}
		t := now.Add(*delay)
		if until != nil && until.After(t) {
			t = *until
		}
		retry = &t
	}
	if f.Code == "" {
		f.Code = f.Class
		if f.Status > 0 {
			f.Code = fmt.Sprintf("http_%d", f.Status)
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE curation_model_state SET pause_kind=$1,reason_code=$2,reason_detail=$3,retry_at=$4,backoff_seconds=$5 WHERE config_key=$6`, f.Class, f.Code, f.Message, retry, backoff, config)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (w *Writer) StartQuotaProbe(ctx context.Context, key string, now time.Time) (bool, error) {
	r, err := w.db.ExecContext(ctx, `UPDATE curation_model_state SET retry_at=$2 WHERE config_key=$1 AND pause_kind='provider_quota' AND retry_at<=$3`, key, now.Add(time.Hour), now)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}

func (w *Writer) RecordProbeDetail(ctx context.Context, key, detail string) error {
	_, err := w.db.ExecContext(ctx, `UPDATE curation_model_state SET reason_detail=$2 WHERE config_key=$1`, key, detail)
	return err
}

func (w *Writer) ModelProbeSucceeded(ctx context.Context, key string, tools bool) error {
	_, err := w.db.ExecContext(ctx, `UPDATE curation_model_state SET pause_kind=NULL,reason_code=NULL,reason_detail=NULL,retry_at=NULL,
        streak_key='',streak_items='{}',backoff_seconds=0,tools_verified=tools_verified OR $2 WHERE config_key=$1 AND ($2 OR pause_kind='provider_quota')`, key, tools)
	return err
}

var requestID = regexp.MustCompile(`(?i)\b(?:req[-_]|request[-_]id[=: ]+)[a-z0-9_-]+\b`)

func rejectionKey(f ModelFailure) string {
	detail := f.Code
	if detail == "" || detail == "error" || detail == "invalid_request_error" {
		detail = strings.Join(strings.Fields(requestID.ReplaceAllString(f.Message, "")), " ")
	}
	return fmt.Sprintf("reject/%d/%s", f.Status, detail)
}

func (w *Writer) FailModelItems(ctx context.Context, config, conversation string, items []PendingItem, f ModelFailure, now time.Time) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var itemIDs []string
	for _, p := range items {
		itemIDs = append(itemIDs, p.ID)
	}
	if _, err = tx.ExecContext(ctx, `SELECT id FROM curation_pending_items WHERE conversation_id=$1 AND id=ANY($2::uuid[]) AND status='in_progress' ORDER BY id FOR UPDATE`, conversation, itemIDs); err != nil {
		return err
	}
	var key string
	var ids []string
	var encoded string
	err = tx.QueryRowContext(ctx, `SELECT streak_key,coalesce(array_to_string(streak_items,','),'') FROM curation_model_state WHERE config_key=$1 FOR UPDATE`, config).Scan(&key, &encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if encoded != "" {
		ids = strings.Split(encoded, ",")
	}
	split := f.Class == "request_rejected" && f.Status == 400 && len(items) > 1
	for _, p := range items {
		state, code, reason, series := "shelved", f.Class, f.Message, ""
		var next *time.Time
		empty := p.EmptyCount
		switch {
		case split:
			state, code, reason = "waiting_model", "", ""
		case f.Class == "empty_reply":
			series = "empty"
			empty = min(3, empty+1)
			if empty < 3 {
				state, code, reason = "waiting_model", "", ""
				t := now.Add((5 * time.Second) << (empty - 1))
				next = &t
			} else {
				code, reason = "no_result", "模型没给出结果"
			}
		case f.Class == "request_rejected":
			series = rejectionKey(f)
			reason = "模型拒绝了请求：" + reason
		}
		if len(reason) > 1000 {
			reason = reason[:1000]
			for !utf8.ValidString(reason) {
				reason = reason[:len(reason)-1]
			}
		}
		r, err := tx.ExecContext(ctx, `UPDATE curation_pending_items SET status=$5,model_stop_code=NULLIF($6,''),shelved_reason=NULLIF($7,''),
            empty_count=$8,single_request=(single_request OR $9),conversation_id=NULL,claimed_at=NULL,next_attempt_at=$10,updated_at=now()
            WHERE id=$1 AND conversation_id=$2 AND input_digest=$3 AND coalesce(identity_digest,'')=$4 AND status='in_progress'`, p.ID, conversation, p.InputDigest, p.IdentityDigest, state, code, reason, empty, split, next)
		if err != nil {
			return err
		}
		n, err := r.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 || series == "" {
			continue
		}
		if series != key {
			key, ids = series, nil
		}
		if len(ids) < 10 && !slices.Contains(ids, p.ID) {
			ids = append(ids, p.ID)
		}
	}
	if len(ids) >= 10 {
		kind, reason := "configuration", "模型连续拒绝了十项相同类型的请求："+f.Message
		if key == "empty" {
			kind, reason = "empty_replies", "这个模型可能不支持工具调用，建议换一个"
		}
		_, err = tx.ExecContext(ctx, `UPDATE curation_model_state SET pause_kind=$1,reason_code=$1,reason_detail=$2,retry_at=NULL WHERE config_key=$3 AND (pause_kind IS NULL OR pause_kind='temporary')`, kind, reason, config)
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE curation_model_state SET streak_key=$2,streak_items=$3::uuid[] WHERE config_key=$1`, config, key, append([]string{}, ids...))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (w *Writer) StopLongInputs(ctx context.Context, config string, budget int64, items []PendingItem) error {
	var ids []string
	var versions []time.Time
	for _, p := range items {
		ids = append(ids, p.ID)
		versions = append(versions, p.UpdatedAt)
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var currentBudget int64
	if err = tx.QueryRowContext(ctx, `SELECT max_request_tokens FROM curation_settings WHERE id=1 FOR SHARE`).Scan(&currentBudget); err != nil {
		return err
	}
	if currentBudget != budget {
		return nil
	}
	if _, err = tx.ExecContext(ctx, `SELECT id FROM curation_pending_items WHERE id=ANY($1::uuid[]) ORDER BY id FOR UPDATE`, ids); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE curation_pending_items SET status='shelved',model_stop_code='content_too_long',shelved_reason='内容太长',next_attempt_at=NULL,updated_at=now()
        WHERE (id,updated_at) IN (SELECT * FROM unnest($1::uuid[],$2::timestamptz[])) AND status IN ('waiting_model','waiting_route')
        AND EXISTS(SELECT FROM curation_model_state WHERE config_key=$3 FOR SHARE)`, ids, versions, config)
	if err != nil {
		return err
	}
	return tx.Commit()
}
