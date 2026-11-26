package program

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

const rateWindow = 5 * time.Minute

type rateBucket struct {
	second          int64
	requests, items int64
}

type queueMetrics struct {
	state, reason, source string
	buckets               [rateWindow / time.Second]rateBucket
}

func (w *Worker) queueMetricsLocked(subject string) *queueMetrics {
	m := w.metrics[subject]
	if m == nil {
		m = &queueMetrics{state: "idle"}
		w.metrics[subject] = m
	}
	return m
}

func (w *Worker) setQueueState(subject, state, reason string, pause *sources.QueuePause) {
	w.mu.Lock()
	defer w.mu.Unlock()
	m := w.queueMetricsLocked(subject)
	m.state, m.reason, m.source = state, reason, ""
	if pause != nil {
		m.source = pause.Source
	}
}

func (w *Worker) cooling(subject string) bool {
	w.mu.RLock()
	m := w.metrics[subject]
	source := ""
	if m != nil {
		source = m.source
	}
	w.mu.RUnlock()
	return source != "" && w.policy.Pause(source) != nil
}

func (w *Worker) nextDelay(ctx context.Context) (time.Duration, error) {
	var due sql.NullTime
	now := w.opts.Now()
	err := w.db.QueryRowContext(ctx, `SELECT min(next_attempt_at) FROM curation_work_states WHERE subject='release' AND next_attempt_at>$1
        AND (status='unmatched' OR status='try_later' AND result ? 'retry')`, now).Scan(&due)
	if err != nil {
		return 0, err
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	delay := w.opts.PollInterval
	if due.Valid {
		delay = min(delay, due.Time.Sub(now))
	}
	for _, m := range w.metrics {
		if m.source != "" {
			if pause := w.policy.Pause(m.source); pause != nil {
				delay = min(delay, pause.RetryAt.Sub(now))
			}
		}
	}
	return delay, nil
}

func (w *Worker) recordQueueEvent(subject string, request bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	m := w.queueMetricsLocked(subject)
	second := w.opts.Now().Unix()
	b := &m.buckets[uint64(second)%uint64(len(m.buckets))]
	if b.second != second {
		*b = rateBucket{second: second}
	}
	if request {
		b.requests++
	} else {
		w.lastCompletion = w.opts.Now()
		b.items++
	}
}

// 最近五分钟的实测速度包含请求间隔和来源冷却。缓存命中不触发请求计数。
func (w *Worker) queueSnapshot(subject string, now time.Time) (curation.QueueProgress, string, string) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	p := curation.QueueProgress{ID: subject, State: "idle"}
	m := w.metrics[subject]
	if m == nil {
		return p, "", ""
	}
	p.State = m.state
	elapsed := min(rateWindow, now.Sub(w.created)).Seconds()
	if elapsed > 0 {
		var requests, items int64
		start, end := now.Add(-rateWindow).Unix(), now.Unix()
		for _, b := range m.buckets {
			if b.second > start && b.second <= end {
				requests += b.requests
				items += b.items
			}
		}
		if requests > 0 {
			speed := float64(requests) / elapsed
			p.SuccessfulRequestsPerSecond = &speed
		}
		if items > 0 {
			speed := float64(items) / elapsed
			p.CompletedItemsPerSecond = &speed
		}
	}
	return p, m.reason, m.source
}

func (w *Worker) QueueProgress(ctx context.Context) (curation.ProgramQueues, error) {
	var result curation.ProgramQueues
	now := w.opts.Now()
	global := w.State()
	for i, subject := range curation.ProgramQueueSubjects {
		p, reason, source := w.queueSnapshot(subject, now)
		remaining, ready, stable, err := w.queueCounts(ctx, subject)
		if err != nil {
			return result, err
		}
		p.Remaining, p.Ready = remaining, ready
		if global != "running" && p.State == "running" {
			p.State = "idle"
		}
		if source != "" {
			if pause := w.policy.Pause(source); pause != nil {
				reason = pause.Reason
				p.Source, p.RetryAt = &pause.Source, &pause.RetryAt
			} else {
				p.State, reason = "idle", ""
			}
		}
		if subject == "release" && remaining > 0 && ready == 0 {
			var waitSource, waitReason string
			var retryAt time.Time
			err := w.db.QueryRowContext(ctx, queueCandidatesSQL+`SELECT retry_source,retry_reason,next_attempt_at FROM candidates
                WHERE stable AND NOT due AND retry_source IS NOT NULL ORDER BY next_attempt_at,target_id LIMIT 1`, subject, now, false).Scan(&waitSource, &waitReason, &retryAt)
			if err == nil {
				p.State, reason = "paused", waitReason
				p.Source, p.RetryAt = &waitSource, &retryAt
				if active := w.policy.Pause(waitSource); active != nil {
					p.RetryAt = &active.RetryAt
					reason = active.Reason
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				return result, err
			}
		}
		switch {
		case global == "disabled" || global == "paused" || global == "waiting_for_scan":
			p.State, reason = global, global
			p.Source, p.RetryAt = nil, nil
		case p.Remaining == 0:
			p.State = "idle"
			reason = ""
			p.Source, p.RetryAt = nil, nil
		case p.State != "running" && reason != "source_disabled" && p.RetryAt == nil:
			p.State, reason = "idle", ""
			if p.Ready == 0 {
				reason = "waiting_for_stability"
				if stable > 0 {
					reason = "waiting_for_fingerprint"
				}
			}
		}
		if reason != "" {
			p.PauseReason = &reason
			if subject != "release" || global == "disabled" || global == "paused" || global == "waiting_for_scan" {
				p.Ready = 0
			}
		}
		if remaining == 0 {
			eta := int64(0)
			p.ETASeconds = &eta
		} else if p.CompletedItemsPerSecond != nil && (reason == "" || p.RetryAt != nil) {
			seconds := float64(remaining) / *p.CompletedItemsPerSecond
			if p.RetryAt != nil {
				seconds += max(0, p.RetryAt.Sub(now).Seconds())
			}
			eta := int64(math.Ceil(seconds))
			p.ETASeconds = &eta
		}
		result[i] = p
	}
	return result, nil
}
