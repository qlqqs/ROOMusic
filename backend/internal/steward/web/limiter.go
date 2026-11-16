package web

import (
	"context"
	"sync"
	"time"
)

const (
	fetchPagePerMinute     = 30
	musicMetadataPerMinute = 20
	webSearchPerMinute     = 30
	userRateWindow         = time.Minute
)

type userLimitKey struct {
	userID string
	group  string
}

// 用户额度跨 Service 共用；每组仅保存最近一分钟内已放行的请求。
type userLimiter struct {
	mu        sync.Mutex
	requests  map[userLimitKey][]time.Time
	lastSweep time.Time
	now       func() time.Time
}

var globalUserLimiter = &userLimiter{now: time.Now}

func (l *userLimiter) allow(userID, group string, limit int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cutoff := now.Add(-userRateWindow)
	if now.Sub(l.lastSweep) >= userRateWindow {
		for key, stamps := range l.requests {
			if !stamps[len(stamps)-1].After(cutoff) {
				delete(l.requests, key)
			}
		}
		l.lastSweep = now
	}
	key := userLimitKey{userID: userID, group: group}
	stamps := l.requests[key]
	expired := 0
	for expired < len(stamps) && !stamps[expired].After(cutoff) {
		expired++
	}
	stamps = stamps[:copy(stamps, stamps[expired:])]
	if len(stamps) >= limit {
		return failure("rate_limited")
	}
	if l.requests == nil {
		l.requests = make(map[userLimitKey][]time.Time)
	}
	l.requests[key] = append(stamps, now)
	return nil
}

// 一个许可串行安排所有 MusicBrainz 请求；时钟和等待通道供包内测试替换。
type requestLimiter struct {
	gate  chan struct{}
	last  time.Time
	now   func() time.Time
	after func(time.Duration) <-chan time.Time
}

func newRequestLimiter() *requestLimiter {
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return &requestLimiter{gate: gate, now: time.Now, after: time.After}
}

var globalMusicLimiter = newRequestLimiter()

func (l *requestLimiter) wait(ctx context.Context) error {
	deadline := l.now().Add(10 * time.Second)
	timeout := l.after(10 * time.Second)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timeout:
		return failure("rate_limited")
	case <-l.gate:
	}
	defer func() { l.gate <- struct{}{} }()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !l.now().Before(deadline) {
		return failure("rate_limited")
	}
	delay := l.last.Add(time.Second).Sub(l.now())
	if delay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return failure("rate_limited")
		case <-l.after(delay):
		}
	}
	if !l.now().Before(deadline) {
		return failure("rate_limited")
	}
	l.last = l.now()
	return nil
}
