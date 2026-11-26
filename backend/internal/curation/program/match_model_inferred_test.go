package program

import (
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

func Test模型整理的标题必须通过曲数和时长核对(t *testing.T) {
	for _, tc := range []struct {
		name, conflict  string
		tracks, seconds int
	}{
		{"完全吻合", "", 5, 100},
		{"同曲数但时长不同", "duration_mismatch", 5, 110},
		{"同时长但曲数不同", "track_count", 4, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, remote := b3Album(5)
			e.LocalTitle, e.Title, e.TitlePlaceholder = e.Title, "未知专辑", true
			e.Authenticity = map[string]metadata.Authenticity{"title": metadata.Placeholder, "artist": metadata.Real}
			e.FieldSources = map[string]string{"title": "display_fallback", "artist": "tag"}
			e.Tracks[0].DurationSeconds = tc.seconds
			remote.Complete = true
			remote.Media[0].Tracks = remote.Media[0].Tracks[:tc.tracks]
			finder := &pendingSearchFinder{completePage: true, b3MatchFinder: b3MatchFinder{search: []sources.Release{remote}}}
			result, err := Match(t.Context(), e, finder, finder)
			if err != nil || finder.searches != 1 {
				t.Fatalf("预期执行一次完整的文字搜索，实际 %d 次，错误 %v", finder.searches, err)
			}
			if tc.conflict == "" {
				if result.Status != "matched" || result.Rule != "structure" || result.Release == nil || result.Release.ID != remote.ID {
					t.Fatalf("应由版本结构定案：%+v", result)
				}
				return
			}
			wantMatch(t, result, "uncertain", tc.conflict)
			if result.Release != nil || result.GroupID != "" || len(result.Pairs) != 0 {
				t.Fatalf("存在结构反证却有外部对应：%+v", result)
			}
		})
	}
}
