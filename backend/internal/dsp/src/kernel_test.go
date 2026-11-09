package src

import (
	"fmt"
	"math"
	"math/cmplx"
	"sync"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/dsp/internal/fft"
)

// 频响网格与倒谱生成网格独立；本机对照也复用此验收口径。
type kernelMetrics struct {
	dc, passDB, stopDB, delay, minus6, minus3, stopStart float64
}

func measureKernel(t *testing.T, h []float64, q Quality, l, m int) kernelMetrics {
	t.Helper()
	n := 1 << 20
	for n < 16*len(h) {
		n *= 2
	}
	work := make([]complex128, n)
	dc, moment := 0.0, 0.0
	for i, v := range h {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatal("核包含非有限值")
		}
		dc += v
		moment += float64(i) * v
		work[i] = complex(v, 0)
	}
	fft.New(n).Forward(work)
	passEnd, stopBegin, stopDB := 0.9, 1.06, 180.0
	if q == Smooth {
		cutoff := 1.0
		if l == 147 {
			cutoff = 0.91875 * 20 / float64(m)
		}
		passEnd, stopBegin, stopDB = 0.9*cutoff, cutoff+0.6, 135.5
	} else if q == Stage2 {
		passEnd, stopBegin, stopDB = 0.113, 0.99, 160
	}
	result := kernelMetrics{dc: dc, delay: moment / dc / float64(l), minus6: math.NaN(), minus3: math.NaN()}
	maxStop := 0.0
	previousDB := 0.0
	step := 2 * float64(max(l, m)) / float64(n)
	for k := 0; k <= n/2; k++ {
		x := float64(k) * step
		magnitude := cmplx.Abs(work[k]) / math.Abs(dc)
		db := 20 * math.Log10(magnitude)
		if x <= passEnd && (q != Precise || x < passEnd) {
			result.passDB = max(result.passDB, math.Abs(db))
		}
		if x >= stopBegin {
			maxStop = max(maxStop, magnitude)
		}
		if db > -stopDB {
			result.stopStart = x + step
		}
		if k > 0 {
			if math.IsNaN(result.minus6) && db <= -6 {
				result.minus6 = (float64(k-1) + (-6-previousDB)/(db-previousDB)) * step
			}
			if math.IsNaN(result.minus3) && db <= -3 {
				result.minus3 = (float64(k-1) + (-3-previousDB)/(db-previousDB)) * step
			}
		}
		previousDB = db
	}
	result.stopDB = -20 * math.Log10(maxStop)
	return result
}

func assertLinearKernel(t *testing.T, h []float64, l int, delay float64) {
	t.Helper()
	for i := 0; i < len(h)/2; i++ {
		if math.Abs(h[i]-h[len(h)-1-i]) > 1e-14 {
			t.Fatalf("线性相位核不对称，抽头 %d", i)
		}
	}
	want := float64(len(h)-1) / (2 * float64(l))
	if math.Abs(delay-want) > 1e-9 {
		t.Errorf("群延迟 = %.12g，期望 %.12g 输入样本", delay, want)
	}
}

func TestKernelSpecifications(t *testing.T) {
	for _, phase := range []Phase{Linear, Minimum} {
		for _, tc := range []struct {
			name               string
			quality            Quality
			l, m, taps         int
			pass, stop, cutoff float64
		}{
			{"smooth_2", Smooth, 2, 1, 20, 2.5, 135.5, 1},
			{"precise_160_147", Precise, 160, 147, 166, 0.003, 180, 0.9524},
			{"2s_2", Stage2, 2, 1, 19, 0.002, 160, 0.4758},
		} {
			t.Run(fmt.Sprintf("phase_%d/%s", phase, tc.name), func(t *testing.T) {
				h := Kernel(phase, tc.quality, tc.l, tc.m)
				if len(h) != tc.l*tc.taps+1 {
					t.Fatalf("核长 = %d，期望 %d", len(h), tc.l*tc.taps+1)
				}
				got := measureKernel(t, h, tc.quality, tc.l, tc.m)
				if math.Abs(got.dc-1) > 1e-12 {
					t.Errorf("直流和 = %.15g，期望 1", got.dc)
				}
				if got.passDB > tc.pass {
					t.Errorf("通带偏差 %.8f dB > %.8f dB", got.passDB, tc.pass)
				}
				if got.stopDB < tc.stop {
					t.Errorf("阻带衰减 %.4f dB < %.4f dB", got.stopDB, tc.stop)
				}
				if math.IsNaN(got.minus6) || math.Abs(got.minus6-tc.cutoff) > 0.002 {
					t.Errorf("−6 dB 点 %.8f，期望 %.8f ± 0.002", got.minus6, tc.cutoff)
				}
				if phase == Linear {
					assertLinearKernel(t, h, tc.l, got.delay)
					if tc.quality == Smooth {
						center := len(h) / 2
						if h[center] != 0.5 {
							t.Errorf("半带中心抽头 = %.17g", h[center])
						}
						for i, v := range h {
							if i != center && (i-center)%2 == 0 && v != 0 {
								t.Errorf("半带偶数偏移 %d 非零", i-center)
							}
						}
					}
				} else if got.delay <= 0 || got.delay >= float64(tc.taps)/2 {
					t.Errorf("最小相位低频群延迟 %.8f 未前移", got.delay)
				}
			})
		}
	}
}

func TestKernelConcurrentCacheAndCopies(t *testing.T) {
	// 只隔离本测试的冷键，结束后恢复；不清空全局缓存，也不与其他测试并行。
	key := kernelKey{Minimum, Smooth, ratio{3, 2}}
	previous, existed := kernels.LoadAndDelete(key)
	t.Cleanup(func() {
		kernels.Delete(key)
		if existed {
			kernels.Store(key, previous)
		}
	})
	const count = 8
	var ready, done sync.WaitGroup
	ready.Add(count)
	done.Add(count)
	start := make(chan struct{})
	results := make([][]float64, count)
	for i := range results {
		go func(i int) {
			defer done.Done()
			ready.Done()
			<-start
			l, m := 3, 2
			if i%3 == 1 {
				l, m = 6, 4
			} else if i%3 == 2 {
				l, m = 2, 3
			}
			results[i] = cachedKernel(Minimum, Smooth, l, m)
		}(i)
	}
	ready.Wait()
	close(start)
	done.Wait()
	for i, h := range results {
		if len(h) != 61 || &h[0] != &results[0][0] {
			t.Fatalf("请求 %d 未复用唯一初始化的缓存数组", i)
		}
	}
	value, exists := kernels.Load(key)
	if !exists {
		t.Fatal("首次请求后缓存键缺失")
	}
	entry := value.(*kernelEntry)
	if &entry.h[0] != &results[0][0] {
		t.Fatal("返回的不是缓存条目的数组")
	}
	ran := false
	entry.once.Do(func() { ran = true })
	if ran {
		t.Fatal("首次并发请求未完成一次性初始化")
	}
	first := Kernel(Minimum, Smooth, 3, 2)
	saved := first[0]
	first[0] = saved + 1
	second := Kernel(Minimum, Smooth, 2, 3)
	if second[0] != saved || results[0][0] != saved {
		t.Fatal("公开返回副本的修改污染了缓存")
	}
	if &first[0] == &second[0] || &second[0] == &results[0][0] {
		t.Fatal("公开 Kernel 返回了共享存储")
	}
}
