package web

import (
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
