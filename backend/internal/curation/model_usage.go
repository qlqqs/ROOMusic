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
	_, err := w.db.ExecContext(ctx, `INSERT INTO curation_model_usage(day,input_tokens,output_tokens) VALUES($1,$2,$3)
 ON CONFLICT(day) DO UPDATE SET input_tokens=curation_model_usage.input_tokens+excluded.input_tokens,
 output_tokens=curation_model_usage.output_tokens+excluded.output_tokens,updated_at=now()`, day.Format(time.DateOnly), in, out)
	return err
}
func (w *Writer) TodayUsage(ctx context.Context, day time.Time) (ModelUsage, error) {
	u := ModelUsage{Date: day.Format(time.DateOnly)}
	err := w.db.QueryRowContext(ctx, `SELECT coalesce(sum(input_tokens),0),coalesce(sum(output_tokens),0) FROM curation_model_usage WHERE day=$1`, u.Date).Scan(&u.InputTokens, &u.OutputTokens)
	return u, err
}
