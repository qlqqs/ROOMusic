package curation

import (
	"context"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

type ProgramStatus interface {
	State() string
	CurrentBatch() (id string, openedAt time.Time, operations int, ok bool)
	LastPassFinishedAt() *time.Time
}

type ReleaseProgress struct {
	Total     int64 `json:"total"`
	Processed int64 `json:"processed"`
	Matched   int64 `json:"matched"`
	Uncertain int64 `json:"uncertain"`
	NoMatch   int64 `json:"no_match"`
	TryLater  int64 `json:"try_later"`
}
type PendingProgress struct {
	WaitingModel int64 `json:"waiting_model"`
}
type BatchProgress struct {
	ID         string    `json:"id"`
	OpenedAt   time.Time `json:"opened_at"`
	Operations int       `json:"operations"`
}
type Progress struct {
	State              string          `json:"state"`
	RuleVersion        int16           `json:"rule_version"`
	Releases           ReleaseProgress `json:"releases"`
	Pending            PendingProgress `json:"pending"`
	Batch              *BatchProgress  `json:"batch"`
	LastPassFinishedAt *time.Time      `json:"last_pass_finished_at"`
}

func (w *Writer) Progress(ctx context.Context, ruleVersion int16, status ProgramStatus) (Progress, error) {
	p := Progress{State: "unavailable", RuleVersion: ruleVersion}
	r := &p.Releases
	err := w.db.QueryRowContext(ctx, `WITH visible AS (`+catalog.VisibleReleaseIDsSQL+`)
 SELECT count(*),count(*) FILTER (WHERE s.status <> 'try_later'),
 count(*) FILTER (WHERE s.status='matched'),count(*) FILTER (WHERE s.status='uncertain'),
 count(*) FILTER (WHERE s.status='no_match'),count(*) FILTER (WHERE s.status='try_later'),
 (SELECT count(*) FROM curation_pending_items p WHERE `+PendingTargetVisibleSQL+` AND p.status='waiting_model')
 FROM visible v LEFT JOIN curation_work_states s ON s.subject='release' AND (COALESCE(s.match_rule_version,s.rule_version)=$1 OR s.status='matched' AND s.match_rule_version IS NOT NULL) AND s.target_id=v.id`, ruleVersion).
		Scan(&r.Total, &r.Processed, &r.Matched, &r.Uncertain, &r.NoMatch, &r.TryLater, &p.Pending.WaitingModel)
	if err != nil {
		return Progress{}, err
	}
	if status != nil {
		p.State = status.State()
		p.LastPassFinishedAt = status.LastPassFinishedAt()
		if id, openedAt, n, ok := status.CurrentBatch(); ok {
			p.Batch = &BatchProgress{id, openedAt, n}
		}
	}
	return p, nil
}

type RuleVersions struct {
	LocalRule         int16 `json:"local_rule"`
	ExternalMatchRule int16 `json:"external_match_rule"`
	PatrolPrompt      int16 `json:"patrol_prompt"`
}

type Classifications struct {
	Versions RuleVersions                `json:"versions"`
	Releases catalog.CurationClassCounts `json:"releases"`
}

func (w *Writer) Classifications(ctx context.Context, versions RuleVersions) (Classifications, error) {
	counts, err := catalog.CountCurationClasses(ctx, w.db)
	return Classifications{Versions: versions, Releases: counts}, err
}
