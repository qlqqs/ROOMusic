package maintenance

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestArtistReleaseWorkloadSelection(t *testing.T) {
	complete := func(tracks, names int) artistReleaseCost {
		return artistReleaseCost{Tracks: tracks, Names: names, EstimateComplete: true}
	}
	for _, tc := range []struct {
		name       string
		budget     artistReleaseBudget
		costs      []artistReleaseCost
		wantCount  int
		wantTracks int
		wantNames  int
		oversized  bool
	}{
		{"起步不再打十发行hot批", initialArtistReleaseBudget(), []artistReleaseCost{complete(10, 62), complete(10, 62)}, 1, 10, 62, false},
		{"上限也不能把missing百轨发行合成千轨", artistReleaseBudget{50, 256}, []artistReleaseCost{complete(100, 200), complete(100, 200), complete(100, 200)}, 1, 100, 200, true},
		{"署名预算先于轨数耗尽", artistReleaseBudget{50, 256}, []artistReleaseCost{complete(10, 62), complete(10, 62), complete(10, 62), complete(10, 62), complete(10, 62)}, 4, 40, 248, false},
		{"单轨百署名不能被当成一条便宜记录", initialArtistReleaseBudget(), []artistReleaseCost{complete(1, 100), complete(1, 1)}, 1, 1, 100, true},
		{"零轨零署名仍完成", artistReleaseBudget{1, 1}, []artistReleaseCost{complete(0, 0), complete(0, 0), complete(0, 0)}, 3, 0, 0, false},
		{"恰好达到预算可以提交", artistReleaseBudget{10, 64}, []artistReleaseCost{complete(4, 30), complete(6, 34)}, 2, 10, 64, false},
		{"不完整估计首项仍完整执行", artistReleaseBudget{10, 64}, []artistReleaseCost{{Tracks: 100, EstimateComplete: false}, complete(1, 1)}, 1, 100, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var candidates []artistReleaseCandidate
			costs := map[string]artistReleaseCost{}
			for i, cost := range tc.costs {
				id := fmt.Sprint(i)
				candidates = append(candidates, artistReleaseCandidate{ID: id, Stale: true})
				costs[id] = cost
			}
			selected, err := selectArtistReleaseWork(candidates, tc.budget, func(id string, remaining artistReleaseBudget) (artistReleaseCost, error) {
				if remaining.Tracks < 0 || remaining.Names < 0 {
					t.Fatalf("向后续估计传了负预算：%+v", remaining)
				}
				return costs[id], nil
			})
			if err != nil || len(selected.IDs) != tc.wantCount || len(selected.Refresh) != tc.wantCount || selected.Tracks != tc.wantTracks || selected.Names != tc.wantNames || selected.Oversized != tc.oversized {
				t.Fatalf("批次选择错误：%+v err=%v", selected, err)
			}
			for i, id := range selected.IDs {
				if id != candidates[i].ID {
					t.Fatal("没有保持清单连续前缀")
				}
			}
		})
	}
}

func TestArtistReleaseWorkloadNeverStarvesLargeSubject(t *testing.T) {
	pending := []artistReleaseCandidate{{"small-before", true}, {"large", true}, {"small-after", true}}
	costs := map[string]artistReleaseCost{
		"small-before": {Tracks: 6, Names: 20, EstimateComplete: true},
		"large":        {Tracks: 10000, Names: 0, EstimateComplete: false},
		"small-after":  {Tracks: 1, Names: 1, EstimateComplete: true},
	}
	var done []string
	for batch := 0; len(pending) > 0 && batch < 3; batch++ {
		selected, err := selectArtistReleaseWork(pending, initialArtistReleaseBudget(), func(id string, _ artistReleaseBudget) (artistReleaseCost, error) { return costs[id], nil })
		if err != nil || len(selected.Refresh) != 1 || selected.Oversized != (batch == 1) {
			t.Fatalf("第%d批跳过/饿死大subject：%+v %v", batch, selected, err)
		}
		done = append(done, selected.IDs...)
		pending = pending[len(selected.IDs):]
	}
	if len(pending) != 0 || !reflect.DeepEqual(done, []string{"small-before", "large", "small-after"}) {
		t.Fatalf("不能跳过昂贵subject去处理后续便宜对象：%v", done)
	}
}

func TestArtistReleaseWorkloadSkipsCurrentWithoutEstimating(t *testing.T) {
	candidates := []artistReleaseCandidate{{"online-current", false}, {"deleted", false}, {"large", true}, {"next", true}}
	calls := 0
	selected, err := selectArtistReleaseWork(candidates, initialArtistReleaseBudget(), func(id string, _ artistReleaseBudget) (artistReleaseCost, error) {
		calls++
		if id != "large" {
			t.Fatalf("估计了已完成对象或超大subject之后的对象：%s", id)
		}
		return artistReleaseCost{Tracks: 100, EstimateComplete: false}, nil
	})
	if err != nil || calls != 1 || !reflect.DeepEqual(selected.IDs, []string{"online-current", "deleted", "large"}) || !reflect.DeepEqual(selected.Refresh, []string{"large"}) || !selected.Oversized {
		t.Fatalf("跳过/至少一个完整subject协议错误：%+v %v", selected, err)
	}
}

func TestArtistReleaseWorkloadPassesRemainingBudgetAndFailsClosed(t *testing.T) {
	candidates := []artistReleaseCandidate{{"first", true}, {"second", true}}
	failure := errors.New("输入读取失败")
	calls := 0
	selected, err := selectArtistReleaseWork(candidates, initialArtistReleaseBudget(), func(id string, remaining artistReleaseBudget) (artistReleaseCost, error) {
		calls++
		if id == "first" {
			if remaining != initialArtistReleaseBudget() {
				t.Fatalf("初始预算错误：%+v", remaining)
			}
			return artistReleaseCost{Tracks: 4, Names: 60, EstimateComplete: true}, nil
		}
		if remaining != (artistReleaseBudget{Tracks: 6, Names: 4}) {
			t.Fatalf("未扣除前项实际工作量：%+v", remaining)
		}
		return artistReleaseCost{}, failure
	})
	if calls != 2 || !errors.Is(err, failure) || len(selected.IDs) != 0 {
		t.Fatalf("读取错误被当成便宜/可完成对象：%+v %v", selected, err)
	}
}

func TestArtistReleaseWorkloadSQLScopesInputsAndIncludesMissing(t *testing.T) {
	for _, query := range []string{artistReleaseTrackCountSQL, artistReleaseNameInputsSQL} {
		if strings.Contains(query, "source_status") || strings.Contains(query, "artist_effective") || !strings.Contains(query, "m.release_id=$1::uuid") || !strings.Contains(query, "t.medium_id=m.id OFFSET 0") {
			t.Fatal("工作量必须包含missing且从当前发行的轨索引入口估计")
		}
	}
	for _, source := range []string{"track_observations", "artist_tag_evidence", "track_credits", "release_corrections"} {
		if !strings.Contains(artistReleaseNameInputsSQL, source) {
			t.Fatalf("漏计原输入来源：%s", source)
		}
	}
	if strings.Count(artistReleaseNameInputsSQL, "track_id=t.id") != 3 || !strings.Contains(artistReleaseNameInputsSQL, "LIMIT $2") {
		t.Fatal("署名估计没有局部索引入口或有界原输入读取")
	}
}

func TestArtistReleaseWorkloadBudgetAdaptsWithinEvidenceBounds(t *testing.T) {
	for _, tc := range []struct {
		current artistReleaseBudget
		d       time.Duration
		want    artistReleaseBudget
	}{
		{initialArtistReleaseBudget(), 99 * time.Millisecond, artistReleaseBudget{12, 80}},
		{initialArtistReleaseBudget(), 100 * time.Millisecond, initialArtistReleaseBudget()},
		{initialArtistReleaseBudget(), 200 * time.Millisecond, initialArtistReleaseBudget()},
		{initialArtistReleaseBudget(), 201 * time.Millisecond, artistReleaseBudget{5, 32}},
		{artistReleaseBudget{1, 1}, time.Second, artistReleaseBudget{1, 1}},
		{artistReleaseBudget{50, 256}, time.Millisecond, artistReleaseBudget{50, 256}},
	} {
		if got := adaptArtistReleaseBudget(tc.current, tc.d); got != tc.want {
			t.Fatalf("预算自适应：%+v %s got=%+v want=%+v", tc.current, tc.d, got, tc.want)
		}
	}
}
