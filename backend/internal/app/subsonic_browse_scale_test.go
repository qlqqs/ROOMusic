package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/maintenance"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
)

func TestSubsonicBrowseScale(t *testing.T) {
	if os.Getenv("ROOMUSIC_SUBSONIC_BROWSE_SCALE") != "1" {
		t.Skip("通过 ROOMUSIC_SUBSONIC_BROWSE_SCALE=1 单独启用规模验收")
	}
	app := newSubsonicTestApplication(t)
	admin := setupSubsonicAdmin(t, app)
	setSubsonicEnabled(t, app, &admin, true)
	for _, name := range []string{"testdata/search-scale.sql", "testdata/subsonic-browse-scale.sql"} {
		fixture, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, app, string(fixture))
	}
	if got := countRows(t, app, `SELECT count(DISTINCT r.group_id) FROM releases r JOIN media m ON m.release_id=r.id
 JOIN tracks t ON t.medium_id=m.id WHERE t.source_status='present'`); got != 10000 {
		t.Fatalf("可见专辑组数量 = %d，期望 10000", got)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Minute)
	defer cancel()
	start := time.Now()
	if err := maintenance.Run(ctx, app.database, slog.New(slog.NewTextHandler(io.Discard, nil)), maintenance.RunOptions{
		RefreshArtistResolutions: personal.RefreshArtistResolution,
	}); err != nil {
		t.Fatalf("构建生产搜索与艺人投影：%v", err)
	}
	postgrestest.AnalyzeSchema(t, ctx, app.database)
	t.Logf("10000 个可见专辑组，目录投影和 ANALYZE 用时 %s", time.Since(start).Round(time.Millisecond))

	cases := []struct {
		name, endpoint, key, values string
		counts                      map[string]int
	}{
		{"Newest", "getAlbumList2", "albumList2", "type=newest&size=50", map[string]int{"album": 50}},
		{"Alphabetical", "getAlbumList2", "albumList2", "type=alphabeticalByName&size=50", map[string]int{"album": 50}},
		{"Random", "getAlbumList2", "albumList2", "type=random&size=50", map[string]int{"album": 50}},
		{"Search", "search3", "searchResult3", "query=Artist+09000&albumCount=50&artistCount=50&songCount=50",
			map[string]int{"album": -1, "artist": -1, "song": -1}},
		{"FullLibrary", "search3", "searchResult3", "query=&albumCount=50&artistCount=50&songCount=50",
			map[string]int{"album": 50, "artist": 50, "song": 50}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := subsonicParameters(subsonicAdmin, subsonicPassword, "plain")
			values, err := url.ParseQuery(tc.values)
			if err != nil {
				t.Fatal(err)
			}
			for key, value := range values {
				params[key] = value
			}
			request := func() float64 {
				start := time.Now()
				response := requestSubsonic(app, http.MethodGet, tc.endpoint, params)
				ms := float64(time.Since(start)) / float64(time.Millisecond)

				payload, ok := requireSubsonicOK(t, response)[tc.key].(map[string]any)
				if !ok {
					t.Fatalf("缺少 %s：%s", tc.key, response.Body)
				}
				for key, want := range tc.counts {
					items, ok := payload[key].([]any)
					if want == -1 {
						if !ok || len(items) == 0 {
							t.Fatalf("%s 缺少文本查询结果：%s", key, response.Body)
						}
					} else if !ok || len(items) != want {
						t.Fatalf("%s 数量 = %d，期望 %d：%s", key, len(items), want, response.Body)
					}
				}
				return ms
			}
			scaleColdConnections(app.database)
			cold := request()
			for range 5 {
				request()
			}
			samples := make([]float64, 30)
			for i := range samples {
				samples[i] = request()
			}
			stats := scaleStats(cold, samples, 300)
			t.Logf("冷请求 %.2f ms，预热 5 次，样本 %d，p50 %.2f ms，p95 %.2f ms，最大 %.2f ms",
				stats.ColdMS, stats.Samples, stats.MedianMS, stats.P95MS, samples[len(samples)-1])
			if !stats.Pass {
				t.Errorf("p95 %.2f ms 超出 %.0f ms 门槛", stats.P95MS, stats.TargetMS)
			}
		})
	}
}
