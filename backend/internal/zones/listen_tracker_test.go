package zones

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/zones/queue"
)

// 规则来源：9344074 的 frontend/src/features/player/engine/listen-tracker.test.ts。
// 服务端没有 mediaDuration 输入：以目录显示时长为准；未知时长保持 240 秒。
func listenRuleFixture(duration *float64) (*Service, *zoneRuntime) {
	next := 0
	service := &Service{options: Options{NewID: func() string { next++; return fmt.Sprintf("visit-%d", next) }}}
	zone := &zoneRuntime{
		persisted:    PersistedZone{Queue: queue.State{Entries: []queue.Entry{{EntryID: "entry-1", TrackID: "track-1"}, {EntryID: "entry-2", TrackID: "track-2"}}}},
		cursorTracks: map[string]catalog.TrackSummary{"track-1": {TrackID: "track-1", DurationSeconds: duration}, "track-2": {TrackID: "track-2", DurationSeconds: duration}},
	}
	return service, zone
}
func listenDuration(n float64) *float64 { return &n }

func TestZonesListenThreshold(t *testing.T) {
	for _, tc := range []struct {
		name     string
		duration *float64
		want     float64
	}{
		{"短曲", listenDuration(60), 30}, {"上限之前", listenDuration(479), 239.5}, {"上限边界", listenDuration(480), 240}, {"长曲", listenDuration(3600), 240},
		{"未知", nil, 240}, {"零", listenDuration(0), 240}, {"负数", listenDuration(-3), 240}, {"NaN", listenDuration(math.NaN()), 240}, {"无穷", listenDuration(math.Inf(1)), 240},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, z := listenRuleFixture(tc.duration)
			s.beginVisit(z, "entry-1", true)
			if z.visit.threshold != tc.want {
				t.Fatalf("门槛 = %v，期望 %v", z.visit.threshold, tc.want)
			}
		})
	}
}

type listenRuleStep struct {
	action                         string
	position, elapsed, accumulated float64
	record                         bool
}

func TestZonesListenSamplingRules(t *testing.T) {
	for _, tc := range []struct {
		name     string
		duration *float64
		baseline bool
		steps    []listenRuleStep
	}{
		{"过半恰好一次", listenDuration(200), true, []listenRuleStep{
			{"sample", 50, 50, 50, false}, {"sample", 99, 49, 99, false}, {"sample", 100.5, 1.5, 100.5, true}, {"sample", 160.5, 60, 100.5, false}}},
		{"目录已知时长权威", listenDuration(200), true, []listenRuleStep{{"sample", 60, 60, 60, false}, {"sample", 100, 40, 100, true}}},
		{"未知时长保持240秒", nil, true, []listenRuleStep{{"sample", 239, 239, 239, false}, {"sample", 240, 1, 240, true}}},
		{"静音不影响位置累计", listenDuration(20), true, []listenRuleStep{{"sample", 10, 10, 10, true}}},
		{"定位重置基准不累计跳变", listenDuration(200), true, []listenRuleStep{{"sample", 40, 40, 40, false}, {"reset", 190, 0, 40, false}, {"sample", 195, 5, 45, false}, {"sample", 245, 50, 95, false}, {"sample", 250, 5, 100, true}}},
		{"无定位事件的跳变与一秒容差", listenDuration(200), true, []listenRuleStep{{"sample", 150, .25, 0, false}, {"sample", 151.2, .25, 1.2, false}, {"sample", 201.2, 50, 51.2, false}}},
		{"后退不算", listenDuration(100), true, []listenRuleStep{{"reset", 80, 0, 0, false}, {"sample", 120, 40, 40, false}, {"sample", 60, 1, 40, false}, {"sample", 65, 5, 45, false}, {"sample", 70, 5, 50, true}}},
		{"后台稀疏采样照常累计", listenDuration(200), true, []listenRuleStep{{"sample", 30, 30, 30, false}, {"sample", 60, 30, 60, false}, {"sample", 90, 30, 90, false}, {"sample", 120, 30, 120, true}}},
		{"墙钟倒退不算", listenDuration(60), true, []listenRuleStep{{"sample", 10, 10, 10, false}, {"sample", 40, -60, 10, false}, {"sample", 50, 10, 20, false}, {"sample", 60, 10, 30, true}}},
		{"暂停或缓冲等待不算", listenDuration(100), true, []listenRuleStep{{"sample", 30, 30, 30, false}, {"reset", 30, 600, 30, false}, {"sample", 40, 10, 40, false}, {"sample", 50, 10, 50, true}}},
		{"第一次采样只建基准", listenDuration(100), false, []listenRuleStep{{"sample", 500, 500, 0, false}, {"sample", 540, 40, 40, false}, {"sample", 550, 10, 50, true}}},
		{"非有限位置忽略", listenDuration(200), true, []listenRuleStep{{"sample", math.NaN(), 0, 0, false}, {"sample", math.Inf(1), 0, 0, false}, {"sample", 10, 10, 10, false}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, z := listenRuleFixture(tc.duration)
			s.beginVisit(z, "entry-1", true)
			now := time.Unix(1000000, 0)
			if tc.baseline {
				z.visit.sample(0, now)
			}
			records := 0
			for i, step := range tc.steps {
				now = now.Add(time.Duration(step.elapsed * float64(time.Second)))
				if step.action == "reset" {
					suspendVisit(z)
				}
				got := z.visit.sample(step.position, now)
				if got != step.record || math.Abs(z.visit.accumulated-step.accumulated) > 1e-8 {
					t.Fatalf("步骤 %d：record=%v accumulated=%v，期望 %v/%v", i, got, z.visit.accumulated, step.record, step.accumulated)
				}
				if got {
					if z.visit.trackID != "track-1" || z.visit.pendingSeconds == nil || math.Abs(*z.visit.pendingSeconds-step.accumulated) > 1e-8 {
						t.Fatalf("计次载荷错误：%+v", z.visit)
					}
					z.visit.recorded = true
					records++
				}
			}
			if records > 1 {
				t.Fatalf("同一访问记录 %d 次", records)
			}
		})
	}
}

func TestZonesListenVisits(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		entry                string
		force, end, newVisit bool
		recorded             bool
	}{
		{"暂停后继续", "entry-1", false, false, false, false},
		{"定位或续流后继续", "entry-1", false, false, false, false},
		{"换条目重新开始", "entry-2", true, false, true, false},
		{"continue遇到新条目", "entry-2", false, false, true, false},
		{"同条目循环重新开始", "entry-1", true, false, true, true},
		{"访问结束后继续新建", "entry-1", false, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, z := listenRuleFixture(listenDuration(100))
			s.beginVisit(z, "entry-1", true)
			at := time.Unix(1000000, 0)
			z.visit.sample(0, at)
			z.visit.sample(30, at.Add(30*time.Second))
			records := 0
			if tc.recorded {
				if !z.visit.sample(50, at.Add(50*time.Second)) {
					t.Fatal("循环前第一轮未到计次门槛")
				}
				z.visit.recorded = true
				records++
			}
			id := z.visit.id
			suspendVisit(z)
			if tc.end {
				z.visit = nil
				if z.visit.sample(1000, at.Add(time.Hour)) {
					t.Fatal("无访问仍产生记录")
				}
			}
			s.beginVisit(z, tc.entry, tc.force)
			if (z.visit.id != id) != tc.newVisit || z.visit.entryID != tc.entry {
				t.Fatalf("访问身份错误：%+v", z.visit)
			}
			accumulated := 30.0
			if tc.newVisit {
				accumulated = 0
			}
			if z.visit.baseline || z.visit.accumulated != accumulated {
				t.Fatalf("续流基准或累计错误：%+v", z.visit)
			}
			at = at.Add(time.Hour)
			z.visit.sample(0, at)
			before := 50 - accumulated - 1
			if z.visit.sample(before, at.Add(time.Duration(before)*time.Second)) {
				t.Fatal("门槛前提前计次")
			}
			if !z.visit.sample(before+1, at.Add(time.Duration(before+1)*time.Second)) || *z.visit.pendingSeconds != 50 {
				t.Fatal("门槛未计次")
			}
			z.visit.recorded = true
			records++
			if tc.recorded && records != 2 {
				t.Fatalf("同条目两轮访问应各计一次，实际 %d 次", records)
			}
			key := z.visit.id
			suspendVisit(z)
			s.beginVisit(z, tc.entry, false)
			if z.visit.id != key || !z.visit.recorded || z.visit.sample(100, at.Add(100*time.Second)) {
				t.Fatal("已记录访问续流后重复计次")
			}
		})
	}
	t.Run("没有访问不计次", func(t *testing.T) {
		var visit *listenVisit
		if visit.sample(1000, time.Unix(1000000, 0)) {
			t.Fatal("空访问产生记录")
		}
	})
	t.Run("没有访问时continue创建", func(t *testing.T) {
		s, z := listenRuleFixture(listenDuration(100))
		s.beginVisit(z, "entry-1", false)
		if z.visit == nil || z.visit.accumulated != 0 || z.visit.baseline {
			t.Fatal("continue 未新建空累计访问")
		}
	})
}

func TestZonesListenPendingRecordFrozen(t *testing.T) {
	s, z := listenRuleFixture(listenDuration(100))
	s.beginVisit(z, "entry-1", true)
	at := time.Unix(1000000, 0)
	z.visit.sample(0, at)
	if !z.visit.sample(50.5, at.Add(50500*time.Millisecond)) {
		t.Fatal("到门槛未准备计次")
	}
	key, track, seconds := z.visit.id, z.visit.trackID, *z.visit.pendingSeconds
	// 第一次提交失败，不置 recorded；续流后重试的键、曲目和秒数都不变。
	suspendVisit(z)
	s.beginVisit(z, "entry-1", false)
	for _, position := range []float64{60, 80, 100} {
		if !z.visit.sample(position, at.Add(time.Duration(position)*time.Second)) || z.visit.id != key || z.visit.trackID != track || *z.visit.pendingSeconds != seconds {
			t.Fatalf("重试载荷变化：%+v", z.visit)
		}
	}
	z.visit.recorded = true
	if z.visit.sample(120, at.Add(120*time.Second)) {
		t.Fatal("成功后仍重试")
	}
}
