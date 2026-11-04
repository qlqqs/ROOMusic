package personal

import (
	"context"
	"fmt"
	"math"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

func mergePlaylistStatistics(summary *PlaylistSummary, stats catalog.TrackCollectionStats, expected int64) error {
	if stats.MatchedCount != expected || stats.MissingCount < 0 || stats.MissingCount > expected || stats.UnknownDurationCount < 0 || stats.UnknownDurationCount > expected || stats.KnownDurationSeconds < 0 || math.IsNaN(stats.KnownDurationSeconds) {
		return fmt.Errorf("playlist catalog statistics incomplete or inconsistent")
	}
	if summary.MissingCount > MaxRevision-stats.MissingCount || summary.UnknownDurationCount > MaxRevision-stats.UnknownDurationCount {
		return fmt.Errorf("playlist statistics count overflow")
	}
	summary.MissingCount += stats.MissingCount
	summary.UnknownDurationCount += stats.UnknownDurationCount
	summary.DurationSeconds += stats.KnownDurationSeconds
	if math.IsInf(summary.DurationSeconds, 0) {
		return fmt.Errorf("playlist duration overflow")
	}
	return nil
}

// collectPlaylistCoverGroups 只为尚不足四组的卡片补位置事实；即使没有封面也保留该组。
// 先读小窗口，单一组等退化形状指数增大窗口，仍不超过单批 10,000；统计始终由完整集合计算。
func collectPlaylistCoverGroups(ctx context.Context, source Catalog, summary *PlaylistSummary, ids []string) error {
	for start, size := 0, 32; start < len(ids) && len(summary.CoverGroups) < 4; size = min(size*2, TrackLimit) {
		end := min(start+size, len(ids))
		facts, err := personalTrackFacts(ctx, source, ids[start:end])
		if err != nil {
			return err
		}
		for _, id := range ids[start:end] {
			groupID := facts[id].GroupID
			found := false
			for _, group := range summary.CoverGroups {
				if group.GroupID == groupID {
					found = true
					break
				}
			}
			if !found {
				summary.CoverGroups = append(summary.CoverGroups, CoverGroup{GroupID: groupID})
			}
			if len(summary.CoverGroups) == 4 {
				break
			}
		}
		start = end
	}
	return nil
}
