package httpapi

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestClientErrorLimiterRollingWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiter := reportLimiter{reports: make(map[string][]time.Time)}
		const user = "synthetic-member"
		if !limiter.allow(user) {
			t.Fatal("首条上报被拒绝")
		}
		time.Sleep(time.Minute)
		for i := range 19 {
			if !limiter.allow(user) {
				t.Fatalf("第 %d 条上报被拒绝", i+2)
			}
		}

		time.Sleep(9*time.Minute - time.Nanosecond)
		if limiter.allow(user) {
			t.Fatal("首条上报尚未满 10 分钟时，第 21 条上报应被拒绝")
		}
		time.Sleep(time.Nanosecond)
		if !limiter.allow(user) || limiter.allow(user) {
			t.Fatal("首条上报过期时应只恢复一条额度")
		}

		time.Sleep(time.Minute)
		for i := range 19 {
			if !limiter.allow(user) {
				t.Fatalf("其余上报过期后，第 %d 条恢复的额度不可用", i+1)
			}
		}
		if limiter.allow(user) {
			t.Fatal("恢复后的窗口内仍应限制为 20 条")
		}
	})
}
