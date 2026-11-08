package workpool

import "context"

// Map 以至多 workers 个工作者计算结果，返回顺序与输入相同。
// workers 必须为正数；fn 应响应 ctx 取消，返回前会等待已开始的任务退出。
func Map[I, O any](ctx context.Context, workers int, items []I, fn func(context.Context, I) O) ([]O, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	results := make([]O, len(items))
	if len(items) == 0 {
		return results, nil
	}
	workers = min(workers, len(items))
	next := 0
	pool := NewOrdered(ctx, workers, 2*workers, func(value O) error {
		results[next] = value
		next++
		return nil
	})
	defer pool.Close()
	for _, item := range items {
		if err := pool.Submit(func(ctx context.Context) O { return fn(ctx, item) }); err != nil {
			return nil, err
		}
	}
	if err := pool.Drain(); err != nil {
		return nil, err
	}
	return results, nil
}
