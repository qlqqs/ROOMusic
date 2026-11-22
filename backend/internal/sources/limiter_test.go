package sources

import (
	"context"
	"testing"
	"time"
)

// 原 TestC8MusicRateLimitUserAgentAndFailures 中的排队超时断言。
func TestC8MusicRateLimitQueueTimeout(t *testing.T) {
	limiter := NewLimiter(time.Second, nil, func(time.Duration) <-chan time.Time { c := make(chan time.Time, 1); c <- time.Now(); return c })
	limiter.busy = true
	if err := limiter.acquire(context.Background(), false); err != ErrRateLimited {
		t.Fatalf("错误=%v，期望 %s", err, "rate_limited")
	}
}
