package maintenance

import "time"

const (
	minBatchYield = 10 * time.Millisecond
	maxBatchYield = 50 * time.Millisecond
)

// batchYield 只决定提交并释放 CATP 后的计划让出，不参与选批或持锁计时。
// duration 沿用整批实测值（含锁等待与 COMMIT），长批/较长争用仍让出 50ms；
// 小于 200ms 的短批按耗时的十分之一让出且至少 10ms；达到 200ms 保留 50ms 长批保护。
// 修复清单等没有有效批耗时的路径保守等待 50ms。
func batchYield(duration time.Duration) time.Duration {
	if duration <= 0 || duration >= 200*time.Millisecond {
		return maxBatchYield
	}
	return min(maxBatchYield, max(minBatchYield, duration/10))
}
