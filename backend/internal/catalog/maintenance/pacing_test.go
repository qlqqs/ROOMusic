package maintenance

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBatchYieldUsesMeasuredDurationWithinBounds(t *testing.T) {
	for _, tc := range []struct {
		name           string
		duration, want time.Duration
	}{
		{"unknown", 0, 50 * time.Millisecond},
		{"invalid", -time.Second, 50 * time.Millisecond},
		{"fast_skip", time.Nanosecond, 10 * time.Millisecond},
		{"short", 20 * time.Millisecond, 10 * time.Millisecond},
		{"below_lower_boundary", 100*time.Millisecond - time.Nanosecond, 10 * time.Millisecond},
		{"lower_boundary", 100 * time.Millisecond, 10 * time.Millisecond},
		{"above_lower_boundary", 100*time.Millisecond + 10*time.Nanosecond, 10*time.Millisecond + time.Nanosecond},
		{"normal", 150 * time.Millisecond, 15 * time.Millisecond},
		{"fractional", 150*time.Millisecond + 10*time.Nanosecond, 15*time.Millisecond + time.Nanosecond},
		{"below_long_batch_boundary", 200*time.Millisecond - time.Nanosecond, 20*time.Millisecond - time.Nanosecond},
		{"long_batch_boundary", 200 * time.Millisecond, 50 * time.Millisecond},
		{"above_long_batch_boundary", 200*time.Millisecond + time.Nanosecond, 50 * time.Millisecond},
		{"slow_batch_or_gate_wait", time.Second, 50 * time.Millisecond},
		{"commit_spike", 3667 * time.Millisecond, 50 * time.Millisecond},
		{"largest_duration", time.Duration(1<<63 - 1), 50 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := batchYield(tc.duration); got != tc.want {
				t.Fatalf("duration=%s yield=%s want=%s", tc.duration, got, tc.want)
			}
		})
	}
}

func TestBatchYieldIsPositiveMonotoneAndRecoversAfterSlowBatch(t *testing.T) {
	previous := time.Duration(0)
	for elapsed := time.Nanosecond; elapsed < time.Second; elapsed += time.Millisecond {
		got := batchYield(elapsed)
		if got < minBatchYield || got > maxBatchYield || got < previous {
			t.Fatalf("耗时变长却更少让出或突破边界：elapsed=%s previous=%s yield=%s", elapsed, previous, got)
		}
		previous = got
	}
	// 一次锁等待或提交尖峰不把后续短批永久固定在最大让出；只依据当前实测，不依赖subject数。
	for _, tc := range []struct{ elapsed, want time.Duration }{
		{time.Second, 50 * time.Millisecond},
		{150 * time.Millisecond, 15 * time.Millisecond},
		{80 * time.Millisecond, 10 * time.Millisecond},
		{20 * time.Millisecond, 10 * time.Millisecond},
	} {
		if got := batchYield(tc.elapsed); got != tc.want {
			t.Fatalf("尖峰后让出未恢复：%s want=%s", got, tc.want)
		}
	}
}

func TestBatchYieldWaitDoesNotBusyLoop(t *testing.T) {
	yield := batchYield(time.Nanosecond)
	started := time.Now()
	if err := wait(context.Background(), yield); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < yield {
		t.Fatalf("正时长让出提前结束：elapsed=%s yield=%s", elapsed, yield)
	}
}

func TestBatchYieldWaitIsContextCancelable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := wait(ctx, batchYield(time.Second)); !errors.Is(err, context.Canceled) {
		t.Fatalf("已取消的context未结束让出：%v", err)
	}

	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	started, done := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		done <- wait(ctx, time.Hour)
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("让出取消返回错误：%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("取消后让出未退出")
	}
}
