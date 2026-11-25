package sources

import (
	"context"
	"sync"
	"time"
)

type Limiter struct {
	mu        sync.Mutex
	interval  time.Duration
	now       func() time.Time
	after     func(time.Duration) <-chan time.Time
	changed   chan struct{}
	next      time.Time
	busy      bool
	waiting   [4]int
	lastStart time.Time
	level     int
}

func NewLimiter(interval time.Duration, now func() time.Time, after func(time.Duration) <-chan time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	if after == nil {
		after = time.After
	}
	return &Limiter{interval: interval, now: now, after: after, changed: make(chan struct{})}
}

var globalLimiters = struct {
	sync.Mutex
	byID map[string]*Limiter
}{byID: make(map[string]*Limiter)}

func DefaultLimiter(d Declaration) *Limiter {
	globalLimiters.Lock()
	defer globalLimiters.Unlock()
	l := globalLimiters.byID[d.ID]
	if l == nil {
		l = NewLimiter(d.MinInterval, nil, nil)
		globalLimiters.byID[d.ID] = l
	}
	return l
}

func (l *Limiter) notify() {
	close(l.changed)
	l.changed = make(chan struct{})
}

// acquire 在同一把锁下选择等待者。后台等待间隔时不占许可，对话可先取得下一次出站机会。
func (l *Limiter) acquire(ctx context.Context, background bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	rank := 0
	if background {
		rank = 1
		if queue, ok := ctx.Value(queueKey{}).(queueRequest); ok {
			rank += int(min(queue.priority, QueueLocalComplete))
		}
	}
	l.waiting[rank]++
	defer func() { l.waiting[rank]--; l.notify() }()
	var deadline time.Time
	var timeout <-chan time.Time
	if !background {
		deadline = l.now().Add(10 * time.Second)
		timeout = l.after(10 * time.Second)
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		now := l.now()
		if !background && !now.Before(deadline) {
			return ErrRateLimited
		}
		var tick <-chan time.Time
		if !l.busy && l.isNext(rank) {
			delay := l.next.Sub(now)
			if delay <= 0 {
				l.busy = true
				return nil
			}
			tick = l.after(delay)
		}
		changed := l.changed
		l.mu.Unlock()
		var err error
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-timeout:
			err = ErrRateLimited
		case <-changed:
		case <-tick:
		}
		l.mu.Lock()
		if err != nil {
			return err
		}
	}
}

func (l *Limiter) isNext(rank int) bool {
	for _, count := range l.waiting[:rank] {
		if count > 0 {
			return false
		}
	}
	return true
}

func (l *Limiter) effectiveInterval() time.Duration {
	if l.interval >= time.Minute {
		return l.interval
	}
	return min(time.Minute, l.interval<<l.level)
}

func (l *Limiter) setLevel(level int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
	l.next = l.lastStart.Add(l.effectiveInterval())
}

func (l *Limiter) release(outbound bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if outbound {
		l.lastStart = l.now()
		l.next = l.lastStart.Add(l.effectiveInterval())
	}
	l.busy = false
	l.notify()
}

// startSerial 记下出站时刻，等响应读完再释放许可。
func (l *Limiter) startSerial() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lastStart = l.now()
	l.next = l.lastStart.Add(l.effectiveInterval())
}
