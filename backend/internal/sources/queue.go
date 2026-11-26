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
)

type queueKey struct{}

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
	mu      sync.Mutex
	now     func() time.Time
	sources map[string]queueSourceState
}

func NewQueuePolicy(now func() time.Time) *QueuePolicy {
	if now == nil {
		now = time.Now
	}
	return &QueuePolicy{now: now, sources: make(map[string]queueSourceState)}
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
	pause, _ := q.failed(source, "source_unavailable", 0, false)
	return pause
}

func (q *QueuePolicy) failed(source, reason string, retryAfter time.Duration, network bool) (*QueuePause, int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	s := q.sources[source]
	now := q.now()
	if now.Before(s.pause.RetryAt) {
		if until := now.Add(retryAfter); until.After(s.pause.RetryAt) {
			s.pause.RetryAt = until
			q.sources[source] = s
		}
		return &s.pause, s.level
	}
	if network && !s.networkError {
		s.networkError = true
		q.sources[source] = s
		return nil, s.level
	}
	s.networkError = false
	s.level = min(8, s.level+1)
	cooldown := max(SourceRetryDelay(s.level-1), retryAfter)
	s.pause = QueuePause{Source: source, Reason: reason, RetryAt: now.Add(cooldown)}
	q.sources[source] = s
	return &s.pause, s.level
}

// SourceRetryDelay 按此前已排定的连续重试次数计算下一次等待。
func SourceRetryDelay(previous int) time.Duration {
	return min(10*time.Minute, 5*time.Second<<min(previous, 7))
}

func (q *QueuePolicy) recovered(source string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.sources, source)
}
