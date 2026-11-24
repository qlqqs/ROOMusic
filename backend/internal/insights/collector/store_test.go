package collector

import (
	"testing"
	"time"
)

func TestInsightsCollectorSummaryWindowBoundaries(t *testing.T) {
	store := newInsightsCollector(t)
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		id, version string
		age         time.Duration
	}{
		{"a1000000-0000-4000-8000-000000000001", "1.0.0", 7 * 24 * time.Hour},
		{"a1000000-0000-4000-8000-000000000002", "2.0.0", 7*24*time.Hour + time.Nanosecond},
		{"a1000000-0000-4000-8000-000000000003", "3.0.0", 30 * 24 * time.Hour},
		{"a1000000-0000-4000-8000-000000000004", "4.0.0", 30*24*time.Hour + time.Nanosecond},
	} {
		p := insightsHeartbeat(tc.id)
		p.Version = tc.version
		if err := store.put(p, now.Add(-tc.age)); err != nil {
			t.Fatal(err)
		}
	}
	summary := store.Summary(now)
	if summary.Active7d != 1 || summary.Active30d != 3 || len(summary.Versions) != 3 ||
		summary.Versions["1.0.0"] != 1 || summary.Versions["2.0.0"] != 1 || summary.Versions["3.0.0"] != 1 {
		t.Fatalf("7/30 天窗口边界统计错误：%+v", summary)
	}
}

func TestInsightsCollectorPersistenceAndPrune(t *testing.T) {
	store := newInsightsCollector(t)
	now := time.Now().UTC().Add(time.Hour)
	current := insightsHeartbeat(collectorFixtureID)
	first := now.Add(-3 * 24 * time.Hour)
	last := now.Add(-2 * 24 * time.Hour)
	if err := store.put(current, first); err != nil {
		t.Fatal(err)
	}
	current.Version = "2.0.0"
	if err := store.put(current, last); err != nil {
		t.Fatal(err)
	}
	late := current
	late.Version = "1.0.0"
	if err := store.put(late, last.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}

	const atCutoff = "a1000000-0000-4000-8000-000000000002"
	const expired = "a1000000-0000-4000-8000-000000000003"
	cutoff := now.Add(-90 * 24 * time.Hour)
	if err := store.put(insightsHeartbeat(atCutoff), cutoff); err != nil {
		t.Fatal(err)
	}
	if err := store.put(insightsHeartbeat(expired), cutoff.Add(-time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	if err := store.Prune(now); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.instances[expired]; ok {
		t.Fatal("90 天前的记录没有删除")
	}
	if _, ok := store.instances[atCutoff]; !ok {
		t.Fatal("恰好 90 天的边界记录被删除")
	}

	// 模拟进程停机期间有记录超过保留期，启动时需要清理。
	if err := store.put(insightsHeartbeat(expired), cutoff.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for restart := range 2 {
		reopened, err := Open(store.dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(reopened.instances) != 2 {
			t.Fatalf("第 %d 次打开后有 %d 条记录，期望 2 条", restart+1, len(reopened.instances))
		}
		if _, ok := reopened.instances[atCutoff]; !ok {
			t.Fatal("重启后丢失保留期内的旧记录")
		}
		rec := reopened.instances[current.InstanceID]
		if !rec.FirstSeen.Equal(first) || !rec.LastSeen.Equal(last) || rec.Payload != current {
			t.Fatalf("重启后最新心跳、首次或最后时间错误：%+v", rec)
		}
	}
}
