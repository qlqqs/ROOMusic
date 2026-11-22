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
		chat := make(chan error, 1)
		bg := make(chan error, 1)
		go func() { chat <- l.acquire(ctx, false) }()
		go func() { bg <- l.acquire(ctx, true) }()
		synctest.Wait()
		time.Sleep(10*time.Second - time.Nanosecond)
		select {
		case err := <-chat:
			t.Fatalf("未到十秒就结束排队：%v", err)
		default:
		}
		time.Sleep(2 * time.Nanosecond)
		if err := <-chat; !errors.Is(err, ErrRateLimited) {
			t.Fatalf("超过十秒返回 %v", err)
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
