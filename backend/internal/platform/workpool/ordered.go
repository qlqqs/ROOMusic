// Package workpool 提供固定并发、有界在途任务与按提交顺序应用结果的机制。
package workpool

import (
	"context"
	"sync"
)

type orderedJob[T any] struct {
	run    func(context.Context) T
	result chan T
}

// Ordered 按提交顺序在调用方的 goroutine 应用结果；工作者只执行任务。
// Submit、Ready、Flush、Drain 和 Close 必须由同一个 goroutine 调用。
type Ordered[T any] struct {
	ctx     context.Context
	cancel  context.CancelFunc
	workers sync.WaitGroup
	jobs    chan orderedJob[T]
	apply   func(T) error
	slots   []chan T
	head    int
	count   int
	err     error
}

// NewOrdered 创建有序流水线；workers 与 window 必须为正数。
// 调用方必须以 Drain 或 Close 收口，保证全部工作者退出。
func NewOrdered[T any](ctx context.Context, workers, window int, apply func(T) error) *Ordered[T] {
	ctx, cancel := context.WithCancel(ctx)
	pool := &Ordered[T]{
		ctx: ctx, cancel: cancel, jobs: make(chan orderedJob[T], window),
		apply: apply, slots: make([]chan T, window),
	}
	pool.workers.Add(workers)
	for range workers {
		go pool.work()
	}
	return pool
}

func (pool *Ordered[T]) work() {
	defer pool.workers.Done()
	for {
		select {
		case <-pool.ctx.Done():
			return
		case job := <-pool.jobs:
			if pool.ctx.Err() != nil {
				return
			}
			// 每个槽位只接收一次结果；取消后也不阻塞已经开始的任务退出。
			job.result <- job.run(pool.ctx)
		}
	}
}

// Submit 提交一个任务；窗口满时先等待并应用头部结果，再接收新任务。
func (pool *Ordered[T]) Submit(job func(context.Context) T) error {
	if err := pool.makeRoom(); err != nil {
		return err
	}
	result := make(chan T, 1)
	select {
	case <-pool.ctx.Done():
		return pool.stop(pool.ctx.Err())
	case pool.jobs <- orderedJob[T]{run: job, result: result}:
	}
	pool.append(result)
	return pool.applyReady()
}

// Ready 将无需计算的值作为槽位排队，不越过尚未完成的任务。
func (pool *Ordered[T]) Ready(value T) error {
	if err := pool.makeRoom(); err != nil {
		return err
	}
	result := make(chan T, 1)
	result <- value
	pool.append(result)
	return pool.applyReady()
}

func (pool *Ordered[T]) makeRoom() error {
	if err := pool.check(); err != nil {
		return err
	}
	if pool.count == len(pool.slots) {
		return pool.applyHead(true)
	}
	return nil
}

func (pool *Ordered[T]) append(result chan T) {
	pool.slots[(pool.head+pool.count)%len(pool.slots)] = result
	pool.count++
}

func (pool *Ordered[T]) applyReady() error {
	for pool.count > 0 {
		before := pool.count
		if err := pool.applyHead(false); err != nil {
			return err
		}
		if pool.count == before {
			break
		}
	}
	return pool.check()
}

func (pool *Ordered[T]) applyHead(wait bool) error {
	if err := pool.check(); err != nil {
		return err
	}
	var value T
	if wait {
		select {
		case <-pool.ctx.Done():
			return pool.stop(pool.ctx.Err())
		case value = <-pool.slots[pool.head]:
		}
	} else {
		select {
		case value = <-pool.slots[pool.head]:
		default:
			return nil
		}
	}
	pool.slots[pool.head] = nil
	pool.head = (pool.head + 1) % len(pool.slots)
	pool.count--
	if err := pool.check(); err != nil {
		return err
	}
	if err := pool.apply(value); err != nil {
		return pool.stop(err)
	}
	return nil
}

func (pool *Ordered[T]) check() error {
	if pool.err != nil {
		return pool.err
	}
	if err := pool.ctx.Err(); err != nil {
		return pool.stop(err)
	}
	return nil
}

func (pool *Ordered[T]) stop(err error) error {
	if pool.err == nil {
		pool.err = err
	}
	pool.cancel()
	return pool.err
}

// Drain 按序应用剩余结果，返回前停止并等待所有工作者退出。
func (pool *Ordered[T]) Drain() error {
	defer pool.Close()
	return pool.Flush()
}

// Flush 按序应用当前所有槽位，保留工作者供后续提交使用。
func (pool *Ordered[T]) Flush() error {
	for pool.count > 0 {
		if err := pool.applyHead(true); err != nil {
			return err
		}
	}
	return pool.check()
}

// Close 停止接收与执行新任务，等待已开始的任务退出，不再应用结果。
func (pool *Ordered[T]) Close() {
	pool.cancel()
	pool.workers.Wait()
	pool.slots = nil
	pool.jobs = nil
	pool.count = 0
}
