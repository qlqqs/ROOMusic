package releasesql

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

func TestArtistRefsReadyUsesCurrentVersions(t *testing.T) {
	// 同时核对目标与已发布版本，避免只更新一侧而始终隐藏已就绪的艺人引用。
	for _, tc := range []struct {
		column  string
		version int
	}{
		{"textnorm_version", textnorm.Version},
		{"published_textnorm_version", textnorm.Version},
		{"search_projection_version", artistgraph.Version},
		{"published_search_projection_version", artistgraph.Version},
	} {
		t.Run(tc.column, func(t *testing.T) {
			pattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(tc.column) + `\s*=\s*([0-9]+)\b`)
			matches := pattern.FindAllStringSubmatch(ArtistRefsReady, -1)
			if len(matches) != 1 || matches[0][1] != strconv.Itoa(tc.version) {
				t.Fatalf("版本条件 %s 应唯一使用当前版本 %d，实际 SQL：%s", tc.column, tc.version, ArtistRefsReady)
			}
		})
	}

	// 消费投影须沿用同一个门闩，不能另存旧版 SQL 副本。
	for name, projection := range map[string]string{
		"发行艺人":   releaseArtistRefsProjection,
		"专辑艺人":   releaseAlbumArtistRefsProjection,
		"发行摘要":   ReleaseSummaryProjection,
		"曲目艺人":   TrackArtistRefsProjection,
		"曲目署名":   TrackCreditArtistRefsProjection,
		"发行署名":   ReleaseCreditArtistRefsProjection,
		"批量发行摘要": BatchSummaryArtistRefsCTE,
		"批量曲目艺人": SelectedTrackArtistRefsCTE,
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(projection, ArtistRefsReady) {
				t.Fatalf("%s没有使用当前艺人引用就绪条件", name)
			}
		})
	}
}
