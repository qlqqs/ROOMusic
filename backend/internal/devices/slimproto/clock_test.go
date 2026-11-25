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
	observe := func(i int, offset, wantOffset time.Duration) {
		t.Helper()
		at := epoch.Add(time.Duration(i) * time.Second)
		got := clock.Observe(uint32(i*1000), at.Add(offset))
		if want := at.Add(wantOffset); !got.Equal(want) {
			t.Fatalf("样本 %d 估计偏移 %v，期望 %v", i, got.Sub(at), wantOffset)
		}
	}
	ms := time.Millisecond
	observe(0, 8*ms, 8*ms)
	observe(1, 25*ms, 8*ms)
	observe(2, 2*ms, 2*ms)
	observe(3, 2*ms, 2*ms)
	// 保留低延迟样本，等它移出五十条样本的窗口才上调。
	for i := 4; i < 53; i++ {
		observe(i, 25*ms, 2*ms)
	}
	observe(53, 25*ms, 25*ms)
	observe(54, 4*ms, 4*ms)
}

func TestSlimProtoClockEstimatorTracksSmallDrift(t *testing.T) {
	var clock ClockEstimator
	epoch := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock.Observe(0, epoch)
	for i := 1; i <= 10; i++ {
		at := epoch.Add(time.Duration(i) * time.Second)
		offset := 4 * time.Millisecond
		if i == 1 {
			offset = 2 * time.Millisecond
		}
		want := at
		if i == 10 {
			want = at.Add(2 * time.Millisecond)
		}
		if got := clock.Observe(uint32(i*1000), at.Add(offset)); !got.Equal(want) {
			t.Fatalf("样本 %d 估计偏移 %v，期望 %v", i, got.Sub(at), want.Sub(at))
		}
	}
}

func TestSlimProtoClockEstimatorWrap(t *testing.T) {
	var clock ClockEstimator
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock.Observe(0xfffffffe, now)
	for _, sample := range []struct {
		jiffies uint32
		delta   time.Duration
	}{{0xffffffff, time.Millisecond}, {0, 2 * time.Millisecond}, {6, 8 * time.Millisecond}} {
		want := now.Add(sample.delta)
		if got := clock.Observe(sample.jiffies, want.Add(5*time.Millisecond)); !got.Equal(want) {
			t.Fatalf("jiffies %#x 的估计时间 %v，期望 %v", sample.jiffies, got, want)
		}
	}
}

func TestSlimProtoClockTarget(t *testing.T) {
	var clock ClockEstimator
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got, ok := clock.Target(now, now.Add(time.Second)); ok {
		t.Fatalf("未取样便生成了目标 %d", got)
	}
	clock.Observe(0xfffffffe, now)
	for _, tc := range []struct {
		name  string
		delay time.Duration
		want  uint32
		valid bool
	}{
		{"过去", -time.Millisecond, 0, false},
		{"当前", 0, 0, false},
		{"回绕前", time.Millisecond, 0xffffffff, true},
		{"跳过立即播放的零值", 2 * time.Millisecond, 1, true},
		{"回绕后", 5 * time.Millisecond, 3, true},
		{"恰好十秒", 10 * time.Second, 9998, true},
		{"超过十秒", 10*time.Second + time.Millisecond, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := clock.Target(now, now.Add(tc.delay))
			if got != tc.want || ok != tc.valid {
				t.Fatalf("Target(%v) = (%d, %t)，期望 (%d, %t)", tc.delay, got, ok, tc.want, tc.valid)
			}
		})
	}
	var zeroAtDeadline ClockEstimator
	zeroAtDeadline.Observe(0xffffd8f0, now)
	if got, ok := zeroAtDeadline.Target(now, now.Add(10*time.Second)); ok {
		t.Fatalf("十秒边界的零值改写后越界，仍生成目标 %d", got)
	}
}
