package sources

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestLimiterPriority(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		now := time.Now()
		// 每个等待者有自己的唤醒通道，使后台的时钟先响，排除 goroutine 调度顺序的影响。
		ticks := make(chan chan time.Time, 4)
		after := func(d time.Duration) <-chan time.Time {
			if d == 10*time.Second {
				return nil
			}
			tick := make(chan time.Time, 1)
			ticks <- tick
			return tick
		}
		l := NewLimiter(time.Second, func() time.Time { return now }, after)
		if err := l.acquire(ctx, true); err != nil {
			t.Fatal(err)
		}
		l.release(true)
		queue := func(background bool) <-chan error {
			done := make(chan error, 1)
			go func() { done <- l.acquire(ctx, background) }()
			synctest.Wait()
			return done
		}
		bg := queue(true)
		bgTick := <-ticks
		chat := queue(false)
		chatTick := <-ticks
		now = now.Add(time.Second)
		bgTick <- now
		synctest.Wait()
		select {
		case err := <-bg:
			t.Fatalf("对话仍排队时后台抢先取得许可：%v", err)
		default:
		}
		chatTick <- now
		if err := <-chat; err != nil {
			t.Fatal(err)
		}
		l.release(true)
		synctest.Wait()
		secondTick := <-ticks
		now = now.Add(time.Second)
		secondTick <- now
		if err := <-bg; err != nil {
			t.Fatal(err)
		}
		l.release(true)
	})
}

func TestLimiterWaitDeadlineAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		l := NewLimiter(time.Second, time.Now, time.After)
		if err := l.acquire(ctx, true); err != nil {
			t.Fatal(err)
		}
		type waitResult struct {
			err     error
			elapsed time.Duration
		}
		chat := make(chan waitResult, 1)
		bg := make(chan error, 1)
		go func() {
			start := time.Now()
			err := l.acquire(ctx, false)
			chat <- waitResult{err, time.Since(start)}
		}()
		go func() { bg <- l.acquire(ctx, true) }()
		synctest.Wait()
		time.Sleep(10*time.Second - time.Nanosecond)
		select {
		case err := <-chat:
			t.Fatalf("未到十秒就结束排队：%v", err)
		default:
		}
		time.Sleep(2 * time.Nanosecond)
		if got := <-chat; !errors.Is(got.err, ErrRateLimited) || got.elapsed != 10*time.Second {
			t.Fatalf("界面应在十秒时返回限流，实际等待 %v，错误 %v", got.elapsed, got.err)
		}
		time.Sleep(time.Hour)
		select {
		case err := <-bg:
			t.Fatalf("后台自行结束排队：%v", err)
		default:
		}
		cancel()
		if err := <-bg; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		l.release(false)
	})
}

func TestLimiterFourQueuePrioritiesAndIndependentSources(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		shared := NewLimiter(time.Second, time.Now, time.After)
		other := NewLimiter(time.Second, time.Now, time.After)
		policy := NewQueuePolicy(time.Now)
		for _, limiter := range []*Limiter{shared, other} {
			if err := limiter.acquire(ctx, true); err != nil {
				t.Fatal(err)
			}
		}
		type permit struct {
			name string
			err  error
		}
		granted := make(chan permit, 5)
		for _, tc := range []struct {
			name       string
			limiter    *Limiter
			priority   QueuePriority
			background bool
		}{
			{"艺人资料", shared, QueueArtistMedia, true},
			{"本地齐全识别", shared, QueueLocalComplete, true},
			{"普通识别", shared, QueueNormal, true},
			{"界面", shared, QueueNormal, false},
			{"另一来源", other, QueueArtistMedia, true},
		} {
			callCtx := ctx
			if tc.background {
				callCtx = policy.Context(ctx, tc.priority, nil)
			}
			go func() { granted <- permit{tc.name, tc.limiter.acquire(callCtx, tc.background)} }()
			synctest.Wait()
		}
		requirePermit := func(want string) {
			t.Helper()
			synctest.Wait()
			select {
			case got := <-granted:
				if got.err != nil || got.name != want {
					t.Fatalf("取得许可 %+v，期望 %s", got, want)
				}
			default:
				t.Fatalf("%s 未取得许可", want)
			}
		}
		other.release(false)
		requirePermit("另一来源")
		// 留住另一来源的许可，验证本来源无需等待该请求结束。
		for _, name := range []string{"界面", "普通识别", "本地齐全识别", "艺人资料"} {
			shared.release(false)
			requirePermit(name)
		}
		shared.release(false)
		other.release(false)
	})
}
