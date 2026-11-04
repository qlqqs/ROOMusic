package personal

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

func TestMergePlaylistStatisticsRejectsInvalidStats(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stats catalog.TrackCollectionStats
	}{
		{"缺少身份", catalog.TrackCollectionStats{MatchedCount: 1}},
		{"多余身份", catalog.TrackCollectionStats{MatchedCount: 3}},
		{"负匹配数", catalog.TrackCollectionStats{MatchedCount: -1}},
		{"负缺失数", catalog.TrackCollectionStats{MatchedCount: 2, MissingCount: -1}},
		{"缺失数超过匹配数", catalog.TrackCollectionStats{MatchedCount: 2, MissingCount: 3}},
		{"负未知数", catalog.TrackCollectionStats{MatchedCount: 2, UnknownDurationCount: -1}},
		{"未知数超过匹配数", catalog.TrackCollectionStats{MatchedCount: 2, UnknownDurationCount: 3}},
		{"负时长", catalog.TrackCollectionStats{MatchedCount: 2, KnownDurationSeconds: -0.1}},
		{"NaN时长", catalog.TrackCollectionStats{MatchedCount: 2, KnownDurationSeconds: math.NaN()}},
		{"正无穷时长", catalog.TrackCollectionStats{MatchedCount: 2, KnownDurationSeconds: math.Inf(1)}},
		{"负无穷时长", catalog.TrackCollectionStats{MatchedCount: 2, KnownDurationSeconds: math.Inf(-1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var summary PlaylistSummary
			if err := mergePlaylistStatistics(&summary, tc.stats, 2); err == nil {
				t.Fatalf("无效统计未被拒绝: %+v", tc.stats)
			}
		})
	}
}

func TestMergePlaylistStatisticsAccumulatesBatches(t *testing.T) {
	summary := PlaylistSummary{CoverGroups: []CoverGroup{{GroupID: "保留的组"}}}
	for _, stats := range []catalog.TrackCollectionStats{
		{MatchedCount: 3, MissingCount: 2, UnknownDurationCount: 1, KnownDurationSeconds: 0.1},
		{MatchedCount: 2, MissingCount: 1, UnknownDurationCount: 1, KnownDurationSeconds: 0.2},
		{},
	} {
		if err := mergePlaylistStatistics(&summary, stats, stats.MatchedCount); err != nil {
			t.Fatal(err)
		}
	}
	// 批间浮点加法允许舍入误差，不要求与逐项累加逐位相同；缺失曲目的已知时长仍计入。
	if summary.MissingCount != 3 || summary.UnknownDurationCount != 2 || math.Abs(summary.DurationSeconds-0.3) > 1e-12 {
		t.Fatalf("跨批统计错误: %+v", summary)
	}
	if len(summary.CoverGroups) != 1 || summary.CoverGroups[0].GroupID != "保留的组" {
		t.Fatalf("合并统计改变了封面组: %+v", summary.CoverGroups)
	}
}

func TestMergePlaylistStatisticsRejectsCrossBatchOverflow(t *testing.T) {
	for _, tc := range []struct {
		name    string
		initial PlaylistSummary
		first   catalog.TrackCollectionStats
		second  catalog.TrackCollectionStats
	}{
		{
			name:    "缺失数安全整数边界",
			initial: PlaylistSummary{MissingCount: MaxRevision - 1},
			first:   catalog.TrackCollectionStats{MatchedCount: 1, MissingCount: 1},
			second:  catalog.TrackCollectionStats{MatchedCount: 1, MissingCount: 1},
		},
		{
			name:    "未知数安全整数边界",
			initial: PlaylistSummary{UnknownDurationCount: MaxRevision - 1},
			first:   catalog.TrackCollectionStats{MatchedCount: 1, UnknownDurationCount: 1},
			second:  catalog.TrackCollectionStats{MatchedCount: 1, UnknownDurationCount: 1},
		},
		{
			name:   "有限时长跨批溢出",
			first:  catalog.TrackCollectionStats{MatchedCount: 1, KnownDurationSeconds: math.MaxFloat64},
			second: catalog.TrackCollectionStats{MatchedCount: 1, KnownDurationSeconds: math.MaxFloat64},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			summary := tc.initial
			if err := mergePlaylistStatistics(&summary, tc.first, 1); err != nil {
				t.Fatalf("合法边界被拒绝: %v", err)
			}
			if err := mergePlaylistStatistics(&summary, tc.second, 1); err == nil {
				t.Fatalf("跨批溢出未被拒绝: %+v", summary)
			}
		})
	}
}

type playlistStatisticsFactsCatalog struct {
	Catalog
	facts func(context.Context, []string) (map[string]catalog.TrackFact, error)
}

func (c playlistStatisticsFactsCatalog) LoadTrackFacts(ctx context.Context, ids []string) (map[string]catalog.TrackFact, error) {
	return c.facts(ctx, ids)
}

// 生成唯一曲目身份；事实全部 missing，不附带封面，防止只选择可播放或有封面的组。
func playlistStatisticsCoverFixture(t *testing.T, count int, groupAt func(int) string) ([]string, Catalog, *[]int) {
	t.Helper()
	ids := make([]string, count)
	facts := make(map[string]catalog.TrackFact, count)
	for i := range ids {
		ids[i] = fmt.Sprintf("10000000-0000-4000-8000-%012d", i+1)
		facts[ids[i]] = catalog.TrackFact{TrackID: ids[i], GroupID: groupAt(i), Present: false}
	}
	var sizes []int
	visited := 0
	source := playlistStatisticsFactsCatalog{facts: func(_ context.Context, batch []string) (map[string]catalog.TrackFact, error) {
		t.Helper()
		if len(batch) == 0 || len(batch) > TrackLimit || visited+len(batch) > len(ids) {
			t.Fatalf("事实窗口越界: visited=%d size=%d total=%d", visited, len(batch), len(ids))
		}
		if !reflect.DeepEqual(batch, ids[visited:visited+len(batch)]) {
			t.Fatalf("事实窗口重读、跳项或乱序: visited=%d size=%d", visited, len(batch))
		}
		sizes = append(sizes, len(batch))
		visited += len(batch)
		result := make(map[string]catalog.TrackFact, len(batch))
		// 故意反序填入 map；封面选择必须依赖输入顺序，不能依赖 map 遍历顺序。
		for i := len(batch) - 1; i >= 0; i-- {
			result[batch[i]] = facts[batch[i]]
		}
		return result, nil
	}}
	return ids, source, &sizes
}

func assertPlaylistStatisticsCoverGroups(t *testing.T, summary PlaylistSummary, want []string) {
	t.Helper()
	got := make([]string, len(summary.CoverGroups))
	for i, group := range summary.CoverGroups {
		got[i] = group.GroupID
		if group.Artwork != nil {
			t.Fatalf("无封面组产生了封面: %+v", group)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("封面组顺序或去重错误: got=%v want=%v", got, want)
	}
}

func TestCollectPlaylistCoverGroupsExponentialWindows(t *testing.T) {
	ids, source, sizes := playlistStatisticsCoverFixture(t, 300, func(i int) string {
		switch {
		case i < 31:
			return "A"
		case i == 31:
			return "B"
		case i < 95:
			return []string{"A", "B"}[i%2]
		case i == 95:
			return "C"
		case i == 96:
			return "D"
		default:
			return "不得替换首四组的后续组"
		}
	})
	summary := PlaylistSummary{MissingCount: 300, UnknownDurationCount: 7, DurationSeconds: 12.5}
	if err := collectPlaylistCoverGroups(context.Background(), source, &summary, ids); err != nil {
		t.Fatal(err)
	}
	assertPlaylistStatisticsCoverGroups(t, summary, []string{"A", "B", "C", "D"})
	if !reflect.DeepEqual(*sizes, []int{32, 64, 128}) {
		t.Fatalf("未按指数扩窗或取得四组后仍继续查询: %v", *sizes)
	}
	if summary.MissingCount != 300 || summary.UnknownDurationCount != 7 || summary.DurationSeconds != 12.5 {
		t.Fatalf("补封面重复或覆盖了完整集合统计: %+v", summary)
	}
}

func TestCollectPlaylistCoverGroupsAcrossReferenceBatches(t *testing.T) {
	ids, source, sizes := playlistStatisticsCoverFixture(t, TrackLimit+1, func(i int) string {
		if i == TrackLimit {
			return "D"
		}
		return []string{"A", "B", "C"}[i%3]
	})
	var summary PlaylistSummary
	if err := collectPlaylistCoverGroups(context.Background(), source, &summary, ids[:TrackLimit]); err != nil {
		t.Fatal(err)
	}
	assertPlaylistStatisticsCoverGroups(t, summary, []string{"A", "B", "C"})
	if !reflect.DeepEqual(*sizes, []int{32, 64, 128, 256, 512, 1024, 2048, 4096, 1840}) {
		t.Fatalf("不足四组时未读完第一批或窗口错误: %v", *sizes)
	}
	// 第二批仅一项，必须保留前三组，并把第 10,001 项的组作为第四组。
	if err := collectPlaylistCoverGroups(context.Background(), source, &summary, ids[TrackLimit:]); err != nil {
		t.Fatal(err)
	}
	assertPlaylistStatisticsCoverGroups(t, summary, []string{"A", "B", "C", "D"})
	if len(*sizes) != 10 || (*sizes)[9] != 1 {
		t.Fatalf("第 10,001 项未单独读取: %v", *sizes)
	}
}

func TestCollectPlaylistCoverGroupsRejectsIncompleteFacts(t *testing.T) {
	fault := errors.New("合成目录读取失败")
	for _, tc := range []struct {
		name  string
		facts map[string]catalog.TrackFact
		err   error
	}{
		{"缺少事实", map[string]catalog.TrackFact{}, nil},
		{"身份不匹配", map[string]catalog.TrackFact{"track": {TrackID: "other", GroupID: "A"}}, nil},
		{"读取失败", nil, fault},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := playlistStatisticsFactsCatalog{facts: func(context.Context, []string) (map[string]catalog.TrackFact, error) {
				return tc.facts, tc.err
			}}
			var summary PlaylistSummary
			err := collectPlaylistCoverGroups(context.Background(), source, &summary, []string{"track"})
			if err == nil || (tc.err != nil && !errors.Is(err, tc.err)) {
				t.Fatalf("不完整事实未安全失败或丢失原因: %v", err)
			}
		})
	}
}

func TestMergePlaylistStatisticsMissingIdentityAfterFourCoverGroups(t *testing.T) {
	summary := PlaylistSummary{CoverGroups: []CoverGroup{{GroupID: "A"}, {GroupID: "B"}, {GroupID: "C"}, {GroupID: "D"}}}
	source := playlistStatisticsFactsCatalog{facts: func(context.Context, []string) (map[string]catalog.TrackFact, error) {
		t.Fatal("已有四组不应再读取封面事实")
		return nil, nil
	}}
	// 补封面确实会早停，因此缺失身份必须由独立的统计匹配数检查拒绝。
	// 此处只证明两个纯函数的职责，不代替 projectPlaylists 的事务编排集成测试。
	if err := collectPlaylistCoverGroups(context.Background(), source, &summary, []string{"missing-track"}); err != nil {
		t.Fatal(err)
	}
	if err := mergePlaylistStatistics(&summary, catalog.TrackCollectionStats{}, 1); err == nil {
		t.Fatal("已有四组后，缺失的曲目身份被当作零时长或 missing 静默跳过")
	}
	assertPlaylistStatisticsCoverGroups(t, summary, []string{"A", "B", "C", "D"})
}
