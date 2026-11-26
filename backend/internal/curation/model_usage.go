package curation

import (
	"context"
	"time"
)

type ModelUsage struct {
	Date         string `json:"date"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
}

func (w *Writer) AddModelUsage(ctx context.Context, day time.Time, in, out int64) error {
	return w.RecordItemUsage(ctx, day, "", "", in, out)
}

// 共享日额度和当前项记在同一事务。审查及摘要也经过这个入口。
func (w *Writer) RecordItemUsage(ctx context.Context, day time.Time, itemID, conversation string, in, out int64) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO curation_model_usage(day,input_tokens,output_tokens)
 VALUES($1,$2,$3) ON CONFLICT(day) DO UPDATE SET input_tokens=curation_model_usage.input_tokens+excluded.input_tokens,
 output_tokens=curation_model_usage.output_tokens+excluded.output_tokens,updated_at=now()`, day.Format(time.DateOnly), in, out)
	if err != nil {
		return err
	}
	if itemID != "" {
		_, err = tx.ExecContext(ctx, `UPDATE curation_pending_items SET item_tokens=item_tokens+$3
 WHERE id=$1 AND conversation_id=$2 AND status='in_progress'`, itemID, conversation, in+out)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (w *Writer) AddItemCalls(ctx context.Context, itemID, conversation string, calls int) error {
	_, err := w.db.ExecContext(ctx, `UPDATE curation_pending_items SET item_tool_calls=item_tool_calls+$3
 WHERE id=$1 AND conversation_id=$2 AND status='in_progress'`, itemID, conversation, calls)
	return err
}

func (w *Writer) TodayUsage(ctx context.Context, day time.Time) (ModelUsage, error) {
	total := ModelUsage{Date: day.Format(time.DateOnly)}
	err := w.db.QueryRowContext(ctx, `SELECT coalesce(sum(input_tokens),0),coalesce(sum(output_tokens),0) FROM curation_model_usage WHERE day=$1`, total.Date).Scan(&total.InputTokens, &total.OutputTokens)
	return total, err
}

func (u ModelUsage) AtLimit(limit *int64) bool {
	return limit != nil && u.InputTokens+u.OutputTokens >= *limit
}

func NextBudgetDay(now time.Time) time.Time {
	y, m, d := now.Date()
	return time.Date(y, m, d+1, 0, 0, 0, 0, now.Location())
}
