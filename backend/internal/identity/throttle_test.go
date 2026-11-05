package identity

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type throttleClock struct{ now time.Time }

func newThrottleClock() *throttleClock {
	return &throttleClock{now: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)}
}

func (c *throttleClock) read() time.Time         { return c.now }
func (c *throttleClock) advance(d time.Duration) { c.now = c.now.Add(d) }

func beginThrottle(t *testing.T, limiter *LoginThrottle, username, source string, fingerprint [8]byte) *Attempt {
	t.Helper()
	attempt, err := limiter.Begin(username, source, fingerprint)
	if err != nil || attempt == nil {
		t.Fatalf("开始尝试 %q/%q：attempt=%v err=%v", username, source, attempt, err)
	}
	return attempt
}

func requireThrottle(t *testing.T, attempt *Attempt, err error, scope string, wait time.Duration) *ThrottledError {
	t.Helper()
	var throttled *ThrottledError
	if attempt != nil || !errors.As(err, &throttled) {
		t.Fatalf("应拒绝且不返回尝试：attempt=%v err=%v", attempt, err)
	}
	if throttled.Scope != scope || throttled.RetryAfter != wait {
		t.Fatalf("限速结果：%+v，期望 %s/%s", throttled, scope, wait)
	}
	return throttled
}

func TestLoginThrottleThresholdBackoffAndRejectedAttemptsDoNotCount(t *testing.T) {
	clock := newThrottleClock()
	limiter := NewLoginThrottle(clock.read)
	fingerprint := [8]byte{1}
	for failure := 1; failure <= 4; failure++ {
		attempt := beginThrottle(t, limiter, "curator", "source-a", fingerprint)
		if attempt.Failures() != failure || attempt.LockedFor() != 0 {
			t.Fatalf("第 %d 次不应锁定：%d/%s", failure, attempt.Failures(), attempt.LockedFor())
		}
	}
	for index, wait := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 15 * time.Minute, 15 * time.Minute} {
		attempt := beginThrottle(t, limiter, "curator", "source-a", fingerprint)
		if attempt.Failures() != index+5 || attempt.LockedFor() != wait {
			t.Fatalf("第 %d 次：%d/%s，期望 %s", index+5, attempt.Failures(), attempt.LockedFor(), wait)
		}
		// 多次拒绝既不加账号次数，也不加来源额度或延长等待。
		clock.advance(wait - time.Nanosecond)
		for rejected := 0; rejected < 35; rejected++ {
			next, err := limiter.Begin("curator", "source-a", fingerprint)
			requireThrottle(t, next, err, "account", time.Nanosecond)
		}
		clock.advance(time.Nanosecond)
	}
}

func TestLoginThrottleSuccessClearsOnlyThisCombination(t *testing.T) {
	limiter := NewLoginThrottle(newThrottleClock().read)
	for i := 0; i < 4; i++ {
		beginThrottle(t, limiter, "curator", "source-a", [8]byte{})
		beginThrottle(t, limiter, "curator", "source-b", [8]byte{})
	}
	success := beginThrottle(t, limiter, "curator", "source-a", [8]byte{})
	success.Succeeded()
	success.Succeeded()
	for failure := 1; failure <= 4; failure++ {
		attempt := beginThrottle(t, limiter, "curator", "source-a", [8]byte{})
		if attempt.Failures() != failure || attempt.LockedFor() != 0 {
			t.Fatalf("成功后未清零：%d/%s", attempt.Failures(), attempt.LockedFor())
		}
	}
	other := beginThrottle(t, limiter, "curator", "source-b", [8]byte{})
	if other.Failures() != 5 || other.LockedFor() != time.Minute {
		t.Fatal("成功不应清除另一来源")
	}
	a, err := limiter.Begin("curator", "source-b", [8]byte{})
	requireThrottle(t, a, err, "account", time.Minute)
}

func TestLoginThrottleSourceIsolationAndExactUsername(t *testing.T) {
	limiter := NewLoginThrottle(newThrottleClock().read)
	for i := 0; i < 5; i++ {
		beginThrottle(t, limiter, "curator", "source-a", [8]byte{})
	}
	a, err := limiter.Begin("curator", "source-a", [8]byte{})
	requireThrottle(t, a, err, "account", time.Minute)
	for _, pair := range [][2]string{{"curator", "source-b"}, {"Curator", "source-a"}, {" curator", "source-a"}, {"curator ", "source-a"}} {
		if beginThrottle(t, limiter, pair[0], pair[1], [8]byte{}).Failures() != 1 {
			t.Fatal("来源或原样用户名没有隔离")
		}
	}
}

func TestLoginThrottleSourceLimitPauseAndRejectedAttemptsDoNotCount(t *testing.T) {
	clock := newThrottleClock()
	limiter := NewLoginThrottle(clock.read)
	for i := 0; i < 30; i++ {
		beginThrottle(t, limiter, fmt.Sprint(i), "source-a", [8]byte{})
	}
	a, err := limiter.Begin("next", "source-a", [8]byte{})
	if !requireThrottle(t, a, err, "source", time.Minute).Started {
		t.Fatal("第 31 次应开始来源暂停")
	}
	beginThrottle(t, limiter, "next", "source-b", [8]byte{})
	clock.advance(30 * time.Second)
	for i := 0; i < 35; i++ {
		a, err = limiter.Begin("next", "source-a", [8]byte{})
		if requireThrottle(t, a, err, "source", 30*time.Second).Started {
			t.Fatal("重复拒绝不应重新开始暂停")
		}
	}
	clock.advance(30 * time.Second)
	if beginThrottle(t, limiter, "next", "source-a", [8]byte{}).Failures() != 1 {
		t.Fatal("被拒绝的尝试被计数")
	}
	if len(limiter.sources["source-a"].stamps) != 1 {
		t.Fatal("暂停期间不应增加来源额度")
	}
}

func TestLoginThrottleSourceSlidingWindowBoundary(t *testing.T) {
	for _, elapsed := range []time.Duration{time.Minute - time.Nanosecond, time.Minute, 61 * time.Second} {
		t.Run(elapsed.String(), func(t *testing.T) {
			clock := newThrottleClock()
			limiter := NewLoginThrottle(clock.read)
			beginThrottle(t, limiter, "first", "source", [8]byte{})
			clock.advance(time.Second)
			for i := 1; i < 30; i++ {
				beginThrottle(t, limiter, fmt.Sprint(i), "source", [8]byte{})
			}
			clock.advance(elapsed - time.Second)
			a, err := limiter.Begin("next", "source", [8]byte{})
			if elapsed < time.Minute {
				requireThrottle(t, a, err, "source", time.Minute)
				return
			}
			if err != nil || a == nil {
				t.Fatalf("满一分钟的时间戳应过期：%v", err)
			}
			want := 30
			if elapsed == 61*time.Second {
				want = 1
			}
			if got := len(limiter.sources["source"].stamps); got != want {
				t.Fatalf("窗口保留 %d，期望 %d", got, want)
			}
		})
	}
}

func TestLoginThrottleSuccessRemovesOnlyItsOwnSameTimeStamp(t *testing.T) {
	limiter := NewLoginThrottle(newThrottleClock().read)
	attempts := make([]*Attempt, 30)
	for i := range attempts {
		attempts[i] = beginThrottle(t, limiter, fmt.Sprint(i), "source", [8]byte{})
	}
	attempts[10].Succeeded()
	attempts[10].Succeeded()
	beginThrottle(t, limiter, "replacement", "source", [8]byte{})
	a, err := limiter.Begin("overflow", "source", [8]byte{})
	requireThrottle(t, a, err, "source", time.Minute)
	for _, stamp := range limiter.sources["source"].stamps {
		if stamp.id == attempts[10].stampID {
			t.Fatal("成功的同时间戳尝试仍占来源额度")
		}
	}
}

func TestLoginThrottleConcurrentAttemptsPrecount(t *testing.T) {
	limiter := NewLoginThrottle(newThrottleClock().read)
	start := make(chan struct{})
	results := make(chan error, 16)
	var group sync.WaitGroup
	for i := 0; i < 16; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, err := limiter.Begin("curator", "source", [8]byte{})
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	allowed, denied := 0, 0
	for err := range results {
		if err == nil {
			allowed++
			continue
		}
		requireThrottle(t, nil, err, "account", time.Minute)
		denied++
	}
	if allowed != 5 || denied != 11 {
		t.Fatalf("在途预计数：通过 %d、拒绝 %d", allowed, denied)
	}
}

func TestLoginThrottleFingerprintResetAndSourcePause(t *testing.T) {
	clock := newThrottleClock()
	limiter := NewLoginThrottle(clock.read)
	for i := 0; i < 5; i++ {
		beginThrottle(t, limiter, "curator", "source", [8]byte{1})
	}
	replacement := beginThrottle(t, limiter, "curator", "source", [8]byte{2})
	if replacement.Failures() != 1 || replacement.LockedFor() != 0 {
		t.Fatal("新指纹应立即解除账号锁")
	}
	for i := 0; i < 24; i++ {
		beginThrottle(t, limiter, fmt.Sprint(i), "source", [8]byte{})
	}
	a, err := limiter.Begin("curator", "source", [8]byte{3})
	requireThrottle(t, a, err, "source", time.Minute)
	clock.advance(time.Minute)
	if beginThrottle(t, limiter, "curator", "source", [8]byte{3}).Failures() != 1 {
		t.Fatal("来源恢复后新密码应可尝试")
	}
}

func TestLoginThrottleForgetsAt24Hours(t *testing.T) {
	clock := newThrottleClock()
	limiter := NewLoginThrottle(clock.read)
	for i := 0; i < 5; i++ {
		beginThrottle(t, limiter, "curator", "source", [8]byte{})
	}
	clock.advance(24*time.Hour - time.Nanosecond)
	if beginThrottle(t, limiter, "curator", "source", [8]byte{}).Failures() != 6 {
		t.Fatal("未满 24 小时不应遗忘")
	}
	clock.advance(24 * time.Hour)
	attempt := beginThrottle(t, limiter, "curator", "source", [8]byte{})
	if attempt.Failures() != 1 || attempt.LockedFor() != 0 {
		t.Fatal("最后一次失败满 24 小时应遗忘")
	}
}

func TestLoginThrottleBoundedAccountsEvictOldestAndSweep(t *testing.T) {
	clock := newThrottleClock()
	limiter := NewLoginThrottle(clock.read)
	oldest, err := limiter.BeginPassword("oldest", [8]byte{})
	if err != nil {
		t.Fatal(err)
	}
	clock.advance(time.Nanosecond)
	for i := 1; i < maxAccounts; i++ {
		if _, err := limiter.BeginPassword(fmt.Sprint(i), [8]byte{}); err != nil {
			t.Fatal(err)
		}
	}
	if len(limiter.accounts) != maxAccounts {
		t.Fatal("账号表未灌满")
	}
	latest, err := limiter.BeginPassword("latest", [8]byte{})
	if err != nil || latest.Failures() != 1 || len(limiter.accounts) != maxAccounts {
		t.Fatalf("满表后应记录新键：%v", err)
	}
	if _, ok := limiter.accounts[oldest.key]; ok {
		t.Fatal("应淘汰最旧账号")
	}
	oldest.Succeeded()
	clock.advance(24 * time.Hour)
	beginThrottle(t, limiter, "sweep", "source", [8]byte{})
	if len(limiter.accounts) != 1 {
		t.Fatalf("过期账号未清理：%d", len(limiter.accounts))
	}
}

func TestLoginThrottleBoundedSourcesEvictOldestAndSweep(t *testing.T) {
	clock := newThrottleClock()
	limiter := NewLoginThrottle(clock.read)
	beginThrottle(t, limiter, "curator", "oldest", [8]byte{})
	clock.advance(time.Nanosecond)
	for i := 1; i < maxSources; i++ {
		beginThrottle(t, limiter, "curator", fmt.Sprint(i), [8]byte{})
	}
	beginThrottle(t, limiter, "curator", "latest", [8]byte{})
	if len(limiter.sources) != maxSources || limiter.sources["latest"] == nil || limiter.sources["oldest"] != nil {
		t.Fatal("来源表应有界并淘汰最旧来源")
	}
	clock.advance(time.Minute)
	beginThrottle(t, limiter, "curator", "sweep", [8]byte{})
	if len(limiter.sources) != 1 {
		t.Fatalf("窗口过期来源未清理：%d", len(limiter.sources))
	}
}

func TestLoginThrottleStaleSuccessCannotClearRecreatedState(t *testing.T) {
	for _, cause := range []string{"fingerprint", "forgotten", "evicted"} {
		t.Run(cause, func(t *testing.T) {
			clock := newThrottleClock()
			limiter := NewLoginThrottle(clock.read)
			old := beginThrottle(t, limiter, "curator", "source", [8]byte{1})
			fingerprint := [8]byte{1}
			switch cause {
			case "fingerprint":
				fingerprint = [8]byte{2}
			case "forgotten":
				clock.advance(24 * time.Hour)
			case "evicted":
				clock.advance(time.Nanosecond)
				for i := 0; i < maxAccounts; i++ {
					if _, err := limiter.BeginPassword(fmt.Sprint(i), [8]byte{}); err != nil {
						t.Fatal(err)
					}
				}
			}
			current := beginThrottle(t, limiter, "curator", "source", fingerprint)
			old.Succeeded()
			next := beginThrottle(t, limiter, "curator", "source", fingerprint)
			if current.Failures() != 1 || next.Failures() != 2 {
				t.Fatal("旧尝试清除了重建状态")
			}
		})
	}
}

func TestLoginThrottlePasswordNamespaceHasNoSourceQuota(t *testing.T) {
	limiter := NewLoginThrottle(newThrottleClock().read)
	login := beginThrottle(t, limiter, "password:member", "", [8]byte{})
	for i := 0; i < 35; i++ {
		a, err := limiter.BeginPassword(fmt.Sprint(i), [8]byte{})
		if err != nil || a.Failures() != 1 {
			t.Fatalf("改密码不应占来源额度：%v", err)
		}
	}
	for i := 1; i <= 5; i++ {
		a, err := limiter.BeginPassword("member", [8]byte{})
		if err != nil || a.Failures() != i {
			t.Fatalf("改密码命名空间应独立：%v", err)
		}
	}
	a, err := limiter.BeginPassword("member", [8]byte{})
	requireThrottle(t, a, err, "account", time.Minute)
	if beginThrottle(t, limiter, "password:member", "", [8]byte{}).Failures() != 2 || login.Failures() != 1 {
		t.Fatal("登录不应与改密码键碰撞，次数快照应稳定")
	}
}

func TestLoginThrottlePasswordSuccessClearsSameFingerprint(t *testing.T) {
	limiter := NewLoginThrottle(newThrottleClock().read)
	for cycle := 0; cycle < 2; cycle++ {
		for failure := 1; failure <= 5; failure++ {
			attempt, err := limiter.BeginPassword("member", [8]byte{1})
			if err != nil || attempt.Failures() != failure {
				t.Fatalf("改密码成功后未清零：failure=%d err=%v", failure, err)
			}
			if failure == 5 {
				attempt.Succeeded()
			}
		}
	}
}

func TestSourceOf(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"192.0.2.5:1234", "192.0.2.5"}, {"192.0.2.5", "192.0.2.5"},
		{"[2001:db8:1:2::1234]:5678", "2001:db8:1:2::/64"}, {"2001:db8:1:2:ffff::1", "2001:db8:1:2::/64"},
		{"[fe80::1234%eth0]:443", "fe80::/64"}, {"fe80::abcd%eth1", "fe80::/64"},
		{"[::ffff:192.0.2.5]:80", "192.0.2.5"}, {"::ffff:192.0.2.5", "192.0.2.5"},
		{"garbage", "unknown"}, {"", "unknown"}, {"proxy.example:80", "unknown"}, {"192.0.2.5:99999", "unknown"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			if got := SourceOf(tc.input); got != tc.want {
				t.Fatalf("SourceOf(%q)=%q，期望 %q", tc.input, got, tc.want)
			}
		})
	}
}

// 直接核对键不存原始用户名，避免超长用户名成为驻留内存。
func TestLoginThrottleHashesUsernameKey(t *testing.T) {
	limiter := NewLoginThrottle(newThrottleClock().read)
	attempt := beginThrottle(t, limiter, "curator", "source", [8]byte{})
	if attempt.key.username != sha256.Sum256([]byte("curator")) {
		t.Fatal("账号键应只保留用户名摘要")
	}
}
