package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
)

func TestPostgreSQLCurationCPaused(t *testing.T) {
	f := newCurationFixture(t)
	app := f.application
	path := curationBase + "/settings"
	check := func(body map[string]bool, enabled, paused bool) {
		t.Helper()
		var got curation.Settings
		if body != nil {
			response := app.request(t, "PUT", path, body, &f.admin, nil)
			if response.Code != 200 {
				t.Fatalf("设置 %v：%d %s", body, response.Code, response.Body)
			}
			if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil || got.Enabled != enabled || got.Paused != paused {
				t.Fatalf("设置 %v 返回 %+v，期望 enabled=%t paused=%t，错误=%v", body, got, enabled, paused, err)
			}
		}
		got = curation.Settings{}
		getJSON(t, app, &f.admin, path, &got)
		if got.Enabled != enabled || got.Paused != paused {
			t.Fatalf("读取设置=%+v，期望 enabled=%t paused=%t", got, enabled, paused)
		}
	}
	check(nil, false, false)
	requireAPIError(t, app.request(t, "GET", path, nil, &f.ordinary, nil), 403, "forbidden")
	requireAPIError(t, app.request(t, "PUT", path, map[string]bool{"paused": true}, &f.ordinary, nil), 403, "forbidden")
	requireAPIError(t, app.request(t, "PUT", path, map[string]bool{}, &f.admin, nil), 400, "invalid_input")
	check(map[string]bool{"paused": true}, false, true)
	check(map[string]bool{"enabled": true}, true, false)
	check(map[string]bool{"paused": true}, true, true)

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		io.WriteString(w, `{"artists":[{"id":"11111111-1111-3111-8111-111111111111","name":"合成艺人"}]}`)
	}))
	t.Cleanup(server.Close)
	client := musicbrainz.New(musicbrainz.Options{
		BaseURL: server.URL, DataDirectory: t.TempDir(), Limiter: sources.NewLimiter(0, nil, nil),
		NewClient: func(egress.Options) (*http.Client, error) { return server.Client(), nil },
	})
	var scanning atomic.Bool
	gate := registerTestSource(f, client).WithScanCheck(func(context.Context) (bool, error) { return scanning.Load(), nil })
	finder, ok := gate.ArtistFinder("musicbrainz")
	if !ok {
		t.Fatal("缺少 MusicBrainz 艺人查找能力")
	}
	blocked := func(code string) {
		t.Helper()
		got, err := finder.SearchArtists(t.Context(), "合成艺人")
		curationCRequireProblem(t, err, 409, code)
		if len(got) != 0 || hits.Load() != 0 {
			t.Fatalf("%s：结果=%+v，出站=%d，期望零结果、零出站", code, got, hits.Load())
		}
	}
	blocked("curation_paused")
	check(map[string]bool{"paused": false}, true, false)
	scanning.Store(true)
	blocked("scan_running")
	scanning.Store(false)
	if got, err := finder.SearchArtists(t.Context(), "合成艺人"); err != nil || len(got) != 1 || hits.Load() != 1 {
		t.Fatalf("闸门放行时结果=%+v，出站=%d，错误=%v", got, hits.Load(), err)
	}
	check(map[string]bool{"enabled": false, "paused": true}, false, true)
	check(map[string]bool{"enabled": true, "paused": true}, true, false)
	check(map[string]bool{"enabled": true, "paused": true}, true, true)
}

func TestPostgreSQLCurationCProgress(t *testing.T) {
	f := newCurationFixture(t)
	app := f.application
	db := app.database
	// 接口执行的语句由 pgx 缓存在连接上。复用同一个连接读取 SQL 和检查计划。
	db.SetMaxOpenConns(1)
	newVisible := func(title string) string {
		t.Helper()
		id, _ := insertGroupedRelease(t, app, f.rootID, groupedRelease{title: title, artist: "Artist", tracks: []string{"present"}})
		return id
	}
	noMatch := newVisible("无匹配")
	tryLater := newVisible("稍后重试")
	outdated := newVisible("旧规则")
	unprocessed := newVisible("未处理")
	const version = 102
	for _, row := range []struct {
		id, subject, status string
		version             int16
	}{
		{f.a, "release", "matched", version},
		{f.b, "release", "uncertain", version},
		{noMatch, "release", "no_match", version},
		{tryLater, "release", "try_later", version},
		{outdated, "release", "matched", version - 1},
		{unprocessed, "artwork", "done", version},
		{f.hidden, "release", "matched", version},
	} {
		err := curation.SaveWorkState(t.Context(), db, curation.WorkState{
			Subject: row.subject, TargetID: row.id, RuleVersion: row.version, Status: row.status, InputDigest: strings.Repeat("a", 64),
		})
		if err != nil {
			t.Fatalf("写工作状态 %+v：%v", row, err)
		}
	}
	for _, id := range []string{f.a, f.b, f.hidden} {
		err := curation.UpsertPendingItem(t.Context(), db, curation.PendingItem{
			TargetType: "release", TargetID: id, Kind: "release_match", RuleVersion: version,
			InputDigest: strings.Repeat("b", 64), Evidence: json.RawMessage(`{}`),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := curation.ResolvePendingItems(t.Context(), db, f.b, "release_match", "matched"); err != nil {
		t.Fatal(err)
	}
	path := curationBase + "/progress"
	requireAPIError(t, app.request(t, "GET", path, nil, &f.ordinary, nil), 403, "forbidden")
	var got curation.Progress
	getJSON(t, app, &f.admin, path, &got)
	want := curation.Progress{State: "unavailable", RuleVersion: version,
		Releases: curation.ReleaseProgress{Total: 6, Processed: 3, Matched: 1, Uncertain: 1, NoMatch: 1, TryLater: 1},
		Pending:  curation.PendingProgress{WaitingModel: 1}}
	for i, id := range [...]string{"release", "artwork", "lyrics", "artist_media", "fingerprint"} {
		want.Queues[i] = curation.QueueProgress{ID: id, State: "unavailable"}
	}
	if got != want {
		t.Fatalf("进度=%+v，期望 %+v", got, want)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var query string
	if err := tx.QueryRowContext(t.Context(), `SELECT statement FROM pg_prepared_statements
 WHERE strpos(statement,'curation_work_states')>0 AND strpos(statement,'count(*) FILTER')>0 AND NOT starts_with(statement,'SELECT statement')`).Scan(&query); err != nil {
		t.Fatalf("读取进度实际 SQL：%v", err)
	}
	if _, err := tx.ExecContext(t.Context(), `SET LOCAL enable_seqscan=off`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.QueryContext(t.Context(), `EXPLAIN (COSTS OFF) `+query, version)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// 当前规则和保留旧对应通过 OR 联接，规划器可从主题复合主键进入。
	if !strings.Contains(plan.String(), "curation_work_states_progress_idx") &&
		!strings.Contains(plan.String(), "curation_work_states_pkey") {
		t.Errorf("进度计数未使用工作状态索引：\n%s", plan.String())
	}
}

func TestPostgreSQLCurationCSuppressions(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	track, _ := externalTrack(t, f.application, f.a)
	otherTrack, _ := externalTrack(t, f.application, f.b)
	for _, input := range []struct {
		release, group, track string
	}{{f.a, f.groupA, track}, {f.b, f.groupB, otherTrack}} {
		req := curationCLinkRequest(input.release, "",
			curationCLink("release", input.release, "fixture-release"),
			curationCLink("release_group", input.group, "fixture-group"),
			curationCLink("track", input.track, "fixture-track"))
		op := curationCApplyLinks(t, f, req).OperationID
		revertExternal(t, f, op, input.release, 1)
	}
	want := map[string]string{"release": f.a, "release_group": f.groupA, "track": track}
	for _, page := range []string{"1", "2", "3", "4"} {
		var got struct {
			Items      []curation.Suppression
			Pagination struct{ Total int }
		}
		path := curationBase + "/suppressions?release_id=" + f.a + "&page_size=1&page=" + page
		getJSON(t, f.application, &f.admin, path, &got)
		if got.Pagination.Total != 3 {
			t.Fatalf("第 %s 页总数=%d，期望 3", page, got.Pagination.Total)
		}
		if page == "4" {
			if len(got.Items) != 0 {
				t.Fatalf("第 4 页=%+v，期望为空", got.Items)
			}
			continue
		}
		if len(got.Items) != 1 {
			t.Fatalf("第 %s 页返回 %+v，期望 1 条", page, got.Items)
		}
		marker := got.Items[0]
		if want[marker.TargetType] != marker.TargetID || marker.Subject != "external_link" {
			t.Fatalf("第 %s 页返回非目标发行或重复标记 %+v", page, marker)
		}
		delete(want, marker.TargetType)
	}
	if len(want) != 0 {
		t.Fatalf("标记列表缺少 %v", want)
	}
}
