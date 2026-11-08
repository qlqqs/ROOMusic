package workpool

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestMapReturnsInputOrderWithBoundedConcurrency(t *testing.T) {
	release := make(chan struct{})
	started := make(chan int, 3)
	var active, peak atomic.Int32
	type result struct {
		values []int
		err    error
	}
	done := make(chan result, 1)
	go func() {
		values, err := Map(context.Background(), 3, []int{0, 1, 2, 3, 4, 5, 6}, func(_ context.Context, value int) int {
			current := active.Add(1)
			defer active.Add(-1)
			for previous := peak.Load(); current > previous; previous = peak.Load() {
				if peak.CompareAndSwap(previous, current) {
					break
				}
			}
			if value < 3 {
				started <- value
				<-release
			}
			return value * value
		})
		done <- result{values, err}
	}()
	for range 3 {
		await(t, started)
	}
	close(release)
	got := await(t, done)
	if got.err != nil || !reflect.DeepEqual(got.values, []int{0, 1, 4, 9, 16, 25, 36}) {
		t.Fatalf("Map=%v err=%v", got.values, got.err)
	}
	if peak.Load() != 3 || active.Load() != 0 {
		t.Fatalf("并发上限或退出错误：peak=%d active=%d", peak.Load(), active.Load())
	}
}

func TestMapCancellationWaitsForActiveWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 2)
	var active, calls atomic.Int32
	done := make(chan error, 1)
	go func() {
		_, err := Map(ctx, 2, []int{1, 2, 3, 4, 5, 6}, func(ctx context.Context, value int) int {
			calls.Add(1)
			active.Add(1)
			defer active.Add(-1)
			started <- struct{}{}
			<-ctx.Done()
			return value
		})
		done <- err
	}()
	await(t, started)
	await(t, started)
	cancel()
	if err := await(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("Map=%v", err)
	}
	if active.Load() != 0 || calls.Load() != 2 {
		t.Fatalf("取消后仍有工作：active=%d calls=%d", active.Load(), calls.Load())
	}
	values, err := Map(ctx, 2, []int{1}, func(context.Context, int) int { t.Error("已取消仍启动任务"); return 1 })
	if values != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("预先取消 Map=%v %v", values, err)
	}
}
