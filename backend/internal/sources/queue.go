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

// Unavailable 用于返回暂时不可用但没有 HTTP 响应详情的来源。
func (q *QueuePolicy) Unavailable(source string) *QueuePause {
	pause, _ := q.failed(source, "source_unavailable", "", false)
	return pause
}

func (q *QueuePolicy) failed(source, reason, retryAfter string, network bool) (*QueuePause, int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	s := q.sources[source]
	now := q.now()
	if now.Before(s.pause.RetryAt) {
		return &s.pause, s.level
	}
	if network && !s.networkError {
		s.networkError = true
		q.sources[source] = s
		return nil, s.level
	}
	s.networkError = false
	s.level = min(8, s.level+1)
	requested, _ := retryAfterDelay(retryAfter, now)
	cooldown := min(10*time.Minute, 2*time.Second<<(s.level-1))
	s.pause = QueuePause{Source: source, Reason: reason, RetryAt: now.Add(max(cooldown, requested))}
	q.sources[source] = s
	return &s.pause, s.level
}

func (q *QueuePolicy) recovered(source string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	s := q.sources[source]
	s.level = max(0, s.level-1)
	s.pause = QueuePause{}
	s.networkError = false
	if s.level == 0 {
		delete(q.sources, source)
	} else {
		q.sources[source] = s
	}
	return s.level
}
