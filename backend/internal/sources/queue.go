package sources

import (
	"context"
	"sync"
	"time"
)

// QueuePriority 决定同时等候一个来源的后台工作怎样取得下一次许可。
type QueuePriority uint8

const (
	QueueNormal QueuePriority = iota
	QueueModelLookup
	QueueLocalComplete
	QueueArtistMedia
)

type queueKey struct{}
type identificationKey struct{}

// WithIdentification 区分识别请求的无效响应和影响整个来源的临时故障。
func WithIdentification(ctx context.Context) context.Context {
	return context.WithValue(ctx, identificationKey{}, true)
}

type queueRequest struct {
	policy   *QueuePolicy
	priority QueuePriority
	success  func(string)
}

type QueuePause struct {
	Source  string
	Reason  string
	RetryAt time.Time
}

func (p *QueuePause) Error() string { return p.Source + ": " + p.Reason }
func (p *QueuePause) Unwrap() error { return ErrTryLater }

type queueSourceState struct {
	pause        QueuePause
	level        int
	networkError bool
}

// QueuePolicy 在当前实例的各队列之间共享，每个来源单独记录冷却窗口。
type QueuePolicy struct {
	mu               sync.Mutex
	now              func() time.Time
	sources          map[string]queueSourceState
	changed          chan struct{}
	recoveredSources map[string]bool
}

func NewQueuePolicy(now func() time.Time) *QueuePolicy {
	if now == nil {
		now = time.Now
	}
	return &QueuePolicy{now: now, sources: make(map[string]queueSourceState), changed: make(chan struct{}, 1), recoveredSources: make(map[string]bool)}
}

// Context 将限速优先级与实际出站成功的回调附在本次工作上，保留原有来源闸门。
func (q *QueuePolicy) Context(ctx context.Context, priority QueuePriority, success func(string)) context.Context {
	if _, ok := ctx.Value(backgroundKey{}).(caller); !ok {
		ctx = WithBackground(ctx)
	}
	return context.WithValue(ctx, queueKey{}, queueRequest{q, priority, success})
}

func (q *QueuePolicy) Pause(source string) *QueuePause {
	q.mu.Lock()
	defer q.mu.Unlock()
	pause := q.sources[source].pause
	if q.now().Before(pause.RetryAt) {
		return &pause
	}
	return nil
}

func QueueRecoveryTime(ctx context.Context, source string) time.Time {
	if queue, ok := ctx.Value(queueKey{}).(queueRequest); ok {
		if pause := queue.policy.Pause(source); pause != nil {
			return pause.RetryAt
		}
	}
	return time.Time{}
}

// Unavailable 用于返回暂时不可用但没有 HTTP 响应详情的来源。
func (q *QueuePolicy) Unavailable(source string) *QueuePause {
	return q.failed(source, "source_unavailable", 0, false)
}

func (q *QueuePolicy) failed(source, reason string, retryAfter time.Duration, network bool) *QueuePause {
	q.mu.Lock()
	defer q.mu.Unlock()
	s := q.sources[source]
	now := q.now()
	if now.Before(s.pause.RetryAt) {
		if until := now.Add(retryAfter); until.After(s.pause.RetryAt) {
			s.pause.RetryAt = until
			q.sources[source] = s
		}
		return &s.pause
	}
	if network && !s.networkError {
		s.networkError = true
		q.sources[source] = s
		return nil
	}
	s.networkError = false
	s.level = min(8, s.level+1)
	cooldown := max(min(3*time.Minute, SourceRetryDelay(s.level-1)), retryAfter)
	s.pause = QueuePause{Source: source, Reason: reason, RetryAt: now.Add(cooldown)}
	q.sources[source] = s
	return &s.pause
}

// SourceRetryDelay 按此前已排定的连续重试次数计算下一次等待。
func SourceRetryDelay(previous int) time.Duration {
	return min(10*time.Minute, 5*time.Second<<min(previous, 7))
}

func (q *QueuePolicy) recovered(source string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if s := q.sources[source]; !s.pause.RetryAt.IsZero() {
		q.recoveredSources[source] = true
		select {
		case q.changed <- struct{}{}:
		default:
		}
	}
	delete(q.sources, source)
}

// HasFailure 在冷却已到期、首个真实响应尚未回来时仍为真。
func (q *QueuePolicy) HasFailure(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return !q.sources[id].pause.RetryAt.IsZero()
}

func (q *QueuePolicy) Changes() <-chan struct{} { return q.changed }

func (q *QueuePolicy) RecoveredSources() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	var ids []string
	for id := range q.recoveredSources {
		if q.sources[id].pause.RetryAt.IsZero() {
			ids = append(ids, id)
		}
	}
	clear(q.recoveredSources)
	return ids
}

// RestorePause 在重启后的首次取项前恢复持久等待行记录的期限。
func (q *QueuePolicy) RestorePause(p QueuePause) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.now().Before(p.RetryAt) && p.RetryAt.After(q.sources[p.Source].pause.RetryAt) {
		q.sources[p.Source] = queueSourceState{pause: p, level: 1}
	}
}
