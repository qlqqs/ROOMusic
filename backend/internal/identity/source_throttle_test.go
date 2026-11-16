package identity

import (
	"fmt"
	"testing"
	"time"
)

func TestSourceThrottleThresholdPauseAndRecovery(t *testing.T) {
	clock := newThrottleClock()
	limiter := NewSourceThrottle(clock.read)
	for i := 1; i < 30; i++ {
		if err := limiter.Failed("source"); err != nil {
			t.Fatalf("第 %d 次提前限速：%v", i, err)
		}
		for range 3 {
			if err := limiter.Check("source"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !requireThrottle(t, nil, limiter.Failed("source"), "source", time.Minute).Started {
		t.Fatal("第 30 次必须开始暂停")
	}
	clock.advance(30 * time.Second)
	for range 35 {
		for _, err := range []error{limiter.Check("source"), limiter.Failed("source")} {
			if requireThrottle(t, nil, err, "source", 30*time.Second).Started {
				t.Fatal("暂停不能重复开始")
			}
		}
	}
	if err := limiter.Check("other"); err != nil {
		t.Fatal(err)
	}
	clock.advance(30 * time.Second)
	if err := limiter.Check("source"); err != nil {
		t.Fatalf("暂停到期未恢复：%v", err)
	}
	for range 29 {
		if err := limiter.Failed("source"); err != nil {
			t.Fatal("拒绝请求污染下一窗口")
		}
	}
	if !requireThrottle(t, nil, limiter.Failed("source"), "source", time.Minute).Started {
		t.Fatal("新窗口应重新告警")
	}
}

func TestSourceThrottleLoginStateIndependent(t *testing.T) {
	clock := newThrottleClock()
	source, login := NewSourceThrottle(clock.read), NewLoginThrottle(clock.read)
	for range 30 {
		_ = source.Failed("mcp-source")
	}
	beginThrottle(t, login, "member", "mcp-source", [8]byte{}).Succeeded()
	for i := 0; i < 30; i++ {
		beginThrottle(t, login, fmt.Sprint(i), "login-source", [8]byte{})
	}
	attempt, err := login.Begin("overflow", "login-source", [8]byte{})
	requireThrottle(t, attempt, err, "source", time.Minute)
	if err := source.Check("login-source"); err != nil {
		t.Fatal("登录限速影响 MCP")
	}
	if err := source.Failed("login-source"); err != nil {
		t.Fatal(err)
	}
	requireThrottle(t, nil, source.Check("mcp-source"), "source", time.Minute)
}
