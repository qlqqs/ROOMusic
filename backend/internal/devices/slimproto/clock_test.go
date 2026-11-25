package slimproto

import (
	"testing"
	"time"
)

func TestSlimProtoJiffiesUnwrap(t *testing.T) {
	var clock Jiffies
	for _, sample := range []struct {
		raw  uint32
		want uint64
	}{
		{0xfffffffe, 0xfffffffe}, {0xffffffff, 0xffffffff}, {0, 0x100000000},
		{9, 0x100000009}, {9, 0x100000009}, {0xffffffff, 0x1ffffffff}, {2, 0x200000002},
	} {
		if got := clock.Unwrap(sample.raw); got != sample.want {
			t.Errorf("Unwrap(%#x) = %#x，期望 %#x", sample.raw, got, sample.want)
		}
	}
}

func TestSlimProtoClockEstimatorWindow(t *testing.T) {
	var clock ClockEstimator
	epoch := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i <= 69; i++ {
		elapsed := time.Duration(i) * time.Second
		latency := 25 * time.Millisecond
		if i == 0 {
			latency = 2 * time.Millisecond
		} else if i == 14 {
			latency = 8 * time.Millisecond
		}
		received := epoch.Add(elapsed + latency)
		got := clock.Observe(uint32(elapsed.Milliseconds()), received)
		want := received
		// 第 39 秒重算仍保留最小偏移，第 69 秒重算时 2 ms 和 8 ms 的样本已移出窗口。
		if i >= 9 && i < 69 {
			want = epoch.Add(elapsed + 2*time.Millisecond)
		}
		if !got.Equal(want) {
			t.Fatalf("样本 %d 估计时间 %v，期望 %v", i, got, want)
		}
	}
}
