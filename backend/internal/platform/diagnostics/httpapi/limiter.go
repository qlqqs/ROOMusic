package httpapi

import (
	"sync"
	"time"
)

type reportLimiter struct {
	mu        sync.Mutex
	reports   map[string][]time.Time
	lastSweep time.Time
}

func (l *reportLimiter) allow(userID string) bool {
	const window = 10 * time.Minute
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-window)
	if now.Sub(l.lastSweep) >= window {
		for id, stamps := range l.reports {
			if !stamps[len(stamps)-1].After(cutoff) {
				delete(l.reports, id)
			}
		}
		l.lastSweep = now
	}
	stamps := l.reports[userID]
	for len(stamps) > 0 && !stamps[0].After(cutoff) {
		stamps = stamps[1:]
	}
	if len(stamps) >= 20 {
		return false
	}
	l.reports[userID] = append(stamps, now)
	return true
}
