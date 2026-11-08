package workpool

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func await[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("等待工作池事件超时")
		var zero T
		return zero
	}
}

func TestOrderedAppliesSubmissionOrderAndFlushKeepsWorkers(t *testing.T) {
	first := make(chan struct{})
	secondDone := make(chan struct{})
	var got []int
	pool := NewOrdered(context.Background(), 2, 4, func(value int) error { got = append(got, value); return nil })
	defer pool.Close()
	if err := pool.Submit(func(ctx context.Context) int {
		select {
		case <-first:
		case <-ctx.Done():
		}
		return 1
	}); err != nil {
		t.Fatal(err)
	}
	if err := pool.Ready(2); err != nil {
		t.Fatal(err)
	}
	if err := pool.Submit(func(context.Context) int { close(secondDone); return 3 }); err != nil {
		t.Fatal(err)
	}
	await(t, secondDone)
	if len(got) != 0 {
		t.Fatalf("头部尚未完成却提前应用：%v", got)
	}
	close(first)
	if err := pool.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := pool.Submit(func(context.Context) int { return 4 }); err != nil {
		t.Fatal(err)
	}
	if err := pool.Drain(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []int{1, 2, 3, 4}) {
		t.Fatalf("应用顺序=%v", got)
	}
}

func TestOrderedFullWindowBlocksSubmission(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	full := make(chan struct{})
	thirdStarted := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		pool := NewOrdered(ctx, 2, 2, func(int) error { return nil })
		defer pool.Close()
		for range 2 {
			if err := pool.Submit(func(ctx context.Context) int {
				select {
				case <-release:
				case <-ctx.Done():
				}
				return 1
			}); err != nil {
				done <- err
				return
			}
		}
		close(full)
		if err := pool.Submit(func(context.Context) int { close(thirdStarted); return 3 }); err != nil {
			done <- err
			return
		}
		done <- pool.Drain()
	}()
	await(t, full)
	select {
	case <-thirdStarted:
		t.Fatal("窗口满时仍启动第三个任务")
	case err := <-done:
		t.Fatalf("窗口满时提前返回：%v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	if err := await(t, done); err != nil {
		t.Fatal(err)
	}
	await(t, thirdStarted)
}

func TestOrderedApplyErrorStopsAndPreservesFirstError(t *testing.T) {
	want := errors.New("应用失败")
	var applied, started int
	pool := NewOrdered(context.Background(), 2, 4, func(int) error { applied++; return want })
	defer pool.Close()
	if err := pool.Ready(1); !errors.Is(err, want) {
		t.Fatalf("Ready=%v", err)
	}
	if err := pool.Submit(func(context.Context) int { started++; return 2 }); !errors.Is(err, want) {
		t.Fatalf("Submit=%v", err)
	}
	if err := pool.Ready(3); !errors.Is(err, want) {
		t.Fatalf("后续 Ready=%v", err)
	}
	if err := pool.Drain(); !errors.Is(err, want) {
		t.Fatalf("Drain=%v", err)
	}
	if applied != 1 || started != 0 {
		t.Fatalf("失败后继续工作：applied=%d started=%d", applied, started)
	}
}

func TestOrderedCancellationDiscardsResultsAndWaitsForWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 2)
	var active atomic.Int32
	applied := 0
	pool := NewOrdered(ctx, 2, 4, func(int) error { applied++; return nil })
	defer pool.Close()
	for range 2 {
		if err := pool.Submit(func(ctx context.Context) int {
			active.Add(1)
			defer active.Add(-1)
			started <- struct{}{}
			<-ctx.Done()
			return 1
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.Ready(3); err != nil {
		t.Fatal(err)
	}
	await(t, started)
	await(t, started)
	cancel()
	if err := pool.Drain(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Drain=%v", err)
	}
	if active.Load() != 0 || applied != 0 {
		t.Fatalf("取消后未停止：active=%d applied=%d", active.Load(), applied)
	}
}
