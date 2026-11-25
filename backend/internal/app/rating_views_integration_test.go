package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/personal/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func decodeRatingPage[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	requireRatingHTTP(t, response, http.StatusOK)
	var page T
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	return page
}

func TestPostgreSQLRatingListTracksHTTP(t *testing.T) {
	f := newPersonalFixture(t, 5)
	other := f.app.createUser(t, "user")
	five, four, two, foreign := f.tracks[0], f.tracks[1], f.tracks[2], f.tracks[3]
	path := ratingsURL + "/tracks"
	at := time.Now().UTC().Truncate(time.Microsecond).Add(-3 * time.Hour)
	set := func(t *testing.T, id string, rating int) {
		t.Helper()
		requireRatingHTTP(t, f.call(http.MethodPut, path+"/"+id, map[string]int{"rating": rating}), http.StatusOK)
	}
	for i, rating := range []int{5, 4, 2} {
		set(t, f.tracks[i], rating)
		mustExec(t, f.app, `UPDATE rating_tracks SET rated_at=$3 WHERE user_id=$1 AND track_id=$2`, f.user.id, f.tracks[i], at.Add(time.Duration(i)*time.Hour))
	}
	requireRatingHTTP(t, f.app.request(t, http.MethodPut, path+"/"+foreign, map[string]int{"rating": 5}, &other, map[string]string{"X-Request-ID": sampleRequestID}), http.StatusOK)

	check := func(t *testing.T, query string, page, size int, total int64, ids ...string) personal.RatedTracksPage {
		t.Helper()
		response := f.call(http.MethodGet, path+query, nil)
		got := decodeRatingPage[personal.RatedTracksPage](t, response)
		wantPagination := personal.Pagination{Page: page, PageSize: size, Total: total}
		if body := personalBody(t, response); len(body) != 2 || got.Items == nil || got.Pagination != wantPagination || len(got.Items) != len(ids) {
			t.Fatalf("分页结果 %s，期望分页 %+v 和曲目 %v", response.Body, wantPagination, ids)
		}
		for i, id := range ids {
			if got.Items[i].Track.TrackID != id {
				t.Fatalf("第 %d 行为 %s，期望 %s（查询 %q）", i, got.Items[i].Track.TrackID, id, query)
			}
		}
		return got
	}
	t.Run("默认列表", func(t *testing.T) {
		got := check(t, "", 1, 50, 3, five, four, two)
		for i, rating := range []int{5, 4, 2} {
			if got.Items[i].Rating != rating || !got.Items[i].RatedAt.Equal(at.Add(time.Duration(i)*time.Hour)) {
				t.Fatalf("第 %d 行的星级或时间不正确：%+v", i, got.Items[i])
			}
		}
	})
	for i, ids := range [][]string{{five, four, two}, {five, four, two}, {five, four}, {five, four}, {five}} {
		t.Run(fmt.Sprintf("最低%d星", i+1), func(t *testing.T) {
			check(t, fmt.Sprintf("?min_rating=%d", i+1), 1, 50, int64(len(ids)), ids...)
		})
	}
	t.Run("最近评分", func(t *testing.T) {
		check(t, "?sort=recent", 1, 50, 3, two, four, five)
		check(t, "?min_rating=4&sort=recent", 1, 50, 2, four, five)
	})
	t.Run("分页及越界空页", func(t *testing.T) {
		check(t, "?min_rating=4&page_size=1", 1, 1, 2, five)
		check(t, "?min_rating=4&page=2&page_size=1", 2, 1, 2, four)
		check(t, "?min_rating=4&page=3&page_size=1", 3, 1, 2)
	})
	t.Run("评分写入后移位与筛除", func(t *testing.T) {
		set(t, two, 5)
		check(t, "?min_rating=4", 1, 50, 3, two, five, four)
		set(t, five, 1)
		check(t, "?min_rating=4", 1, 50, 2, two, four)
		requireRatingHTTP(t, f.call(http.MethodDelete, path+"/"+two, nil), http.StatusOK)
		check(t, "?min_rating=4", 1, 50, 1, four)
	})
}

func TestPostgreSQLRatingListUnavailableMediaHTTP(t *testing.T) {
	f := newPersonalFixture(t, 4)
	missing := f.tracks[0]
	mustExec(t, f.app, `UPDATE releases SET title='已保存的合成专辑',artist='已保存的合成艺人' WHERE group_id=$1`, f.groups[0])
	for i, rating := range []int{5, 4, 5, 2} {
		for _, target := range []string{"/tracks/" + f.tracks[i], "/albums/" + f.groups[i]} {
			requireRatingHTTP(t, f.call(http.MethodPut, ratingsURL+target, map[string]int{"rating": rating}), http.StatusOK)
		}
	}
	// 固定评分时间，让同分项和最近评分的顺序都可断言。
	for i, hour := range []int{0, 3, 2, 4} {
		at := time.Date(2026, 11, 20, hour, 0, 0, 0, time.UTC)
		mustExec(t, f.app, `UPDATE rating_tracks SET rated_at=$2 WHERE track_id=$1`, f.tracks[i], at)
		mustExec(t, f.app, `UPDATE rating_albums SET rated_at=$2 WHERE group_id=$1`, f.groups[i], at)
	}
	mustExec(t, f.app, `UPDATE tracks SET source_status='missing' WHERE id=$1`, missing)
	mustExec(t, f.app, `UPDATE releases SET title='改名后的不可用专辑',artist='改名后的艺人' WHERE group_id=$1`, f.groups[0])

	tracks := decodeRatingPage[personal.RatedTracksPage](t, f.call(http.MethodGet, ratingsURL+"/tracks?min_rating=5", nil))
	if len(tracks.Items) != 2 || tracks.Items[0].Track.TrackID != f.tracks[2] || !tracks.Items[0].Track.Present ||
		tracks.Items[1].Track.TrackID != missing || tracks.Items[1].Track.Present || tracks.Items[1].Rating != 5 {
		t.Fatalf("缺失曲目仍应列在五星结果中：%+v", tracks)
	}

	for _, tc := range []struct {
		sort                   string
		albumOrder, queueOrder []int
	}{
		{"rating", []int{2, 0, 1}, []int{2, 1}},
		{"recent", []int{1, 2, 0}, []int{1, 2}},
	} {
		t.Run(tc.sort, func(t *testing.T) {
			response := f.call(http.MethodGet, ratingsURL+"/albums?min_rating=4&sort="+tc.sort, nil)
			albums := decodeRatingPage[personal.RatedAlbumsPage](t, response)
			if len(albums.Items) != 3 || albums.Pagination.Total != 3 || len(personalBody(t, response)) != 2 {
				t.Fatalf("专辑列表响应错误：%s", response.Body)
			}
			for i, index := range tc.albumOrder {
				item := albums.Items[i]
				if item.Album.GroupID != f.groups[index] || item.Album.Available != (index != 0) {
					t.Fatalf("专辑顺序或可用性错误：第 %d 行 %+v", i, item)
				}
				if index == 0 && (item.Album.Title != "已保存的合成专辑" || item.Album.Artist != "已保存的合成艺人" || item.Album.Representative != nil || item.Rating != 5) {
					t.Fatalf("不可用专辑应显示保存的名称：%+v", item)
				}
			}
			response = f.call(http.MethodGet, ratingsURL+"/tracks/queue?min_rating=4&sort="+tc.sort, nil)
			queue := decodeRatingPage[personal.QueueTracks](t, response)
			body := personalBody(t, response)
			if queue.Total != 3 || queue.MissingCount != 1 || queue.EligibleCount != 2 || queue.ReturnedCount != 2 ||
				queue.OmittedCount != 0 || queue.Limit != 10000 || queue.Truncated || len(body) != 8 ||
				len(queue.Items) != 2 {
				t.Fatalf("筛选后的曲目队列错误：%s", response.Body)
			}
			for i, index := range tc.queueOrder {
				if item := queue.Items[i]; item.TrackID != f.tracks[index] || !item.Present {
					t.Fatalf("队列顺序或可用性错误：第 %d 行 %+v", i, item)
				}
			}
		})
	}
}

func TestPostgreSQLRatingListQueryAndRoutingHTTP(t *testing.T) {
	f := newPersonalFixture(t, 1)
	for _, endpoint := range []string{"tracks", "albums", "artists", "tracks/queue"} {
		t.Run(endpoint, func(t *testing.T) {
			path := ratingsURL + "/" + endpoint
			requireAPIError(t, requireRatingHTTP(t, personalRaw(f.app, http.MethodGet, path, "", nil, nil), 401), 401, "unauthorized")
			type queryError struct{ query, code string }
			cases := []queryError{
				{"min_rating=0", "invalid_rating"},
				{"min_rating=-1", "invalid_rating"},
				{"min_rating=6", "invalid_rating"},
				{"min_rating=2.5", "invalid_rating"},
				{"min_rating=abc", "invalid_rating"},
				{"min_rating=", "invalid_rating"},
				{"sort=highest", "invalid_sort"},
				{"sort=", "invalid_sort"},
				{"min_rating=4&min_rating=5", "invalid_query"},
				{"sort=rating&sort=recent", "invalid_query"},
				{"revision=0", "invalid_query"},
				{"min_rating=%ZZ", "invalid_query"},
			}
			if endpoint == "tracks/queue" {
				cases = append(cases, queryError{"page=1", "invalid_query"}, queryError{"page_size=50", "invalid_query"})
			} else {
				cases = append(cases, []queryError{
					{"page=0", "invalid_pagination"},
					{"page=", "invalid_pagination"},
					{"page=abc", "invalid_pagination"},
					{"page=9223372036854775807&page_size=100", "invalid_pagination"},
					{"page_size=0", "invalid_pagination"},
					{"page_size=", "invalid_pagination"},
					{"page_size=101", "invalid_pagination"},
					{"page=1&page=2", "invalid_query"},
					{"page_size=1&page_size=2", "invalid_query"},
				}...)
			}
			for _, tc := range cases {
				t.Run(tc.query, func(t *testing.T) {
					response := f.call(http.MethodGet, path+"?"+tc.query, nil)
					requireAPIError(t, requireRatingHTTP(t, response, 400), 400, tc.code)
				})
			}
		})
	}
	for _, method := range []string{http.MethodPut, http.MethodDelete, http.MethodPost, http.MethodPatch, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			response := f.call(method, ratingsURL+"/tracks/queue", nil)
			requireAPIError(t, requireRatingHTTP(t, response, 405), 405, "method_not_allowed")
			if got := response.Header().Get("Allow"); got != "GET, HEAD" {
				t.Fatalf("队列的 Allow=%q", got)
			}
		})
	}
}

func TestPostgreSQLRatingToolsWebAndSubsonic(t *testing.T) {
	app := newSubsonicTestApplication(t)
	const username = "rating-tools-listener"
	f := newPersonalFixtureForUser(t, app, seedSubsonicMember(t, app, username), 2)
	registry := tool.NewRegistry()
	for _, def := range stewardtools.StewardTools(personal.NewService(app.database)) {
		if err := registry.Register(def); err != nil {
			t.Fatal(err)
		}
	}
	invoke := func(name string, input any) json.RawMessage {
		t.Helper()
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		got, err := registry.Invoke(t.Context(), tool.Caller{User: identity.User{ID: f.user.id, Role: "user"}}, name, raw)
		if err != nil {
			t.Fatal(err)
		}
		return got.Output
	}
	listAlbum := func(want ...string) {
		t.Helper()
		input := map[string]any{"kind": "album", "min_rating": 5, "page_size": 50}
		toolResult := invoke("list_ratings", input)
		web := f.call(http.MethodGet, ratingsURL+"/albums?min_rating=5&page_size=50", nil)
		got := decodeRatingPage[personal.RatedAlbumsPage](t, web)
		var ids []string
		for _, item := range got.Items {
			ids = append(ids, item.Album.GroupID)
		}
		if !slices.Equal(ids, want) {
			t.Fatalf("网页专辑评分的列表 %v，期望 %v", ids, want)
		}
		var toolBody map[string]any
		if err := json.Unmarshal(toolResult, &toolBody); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(toolBody, personalBody(t, web)) {
			t.Fatalf("管家与网页列表不同：\n工具 %s\n网页 %s", toolResult, web.Body)
		}
	}
	subsonicSet := func(rating string) {
		t.Helper()
		params := subsonicParameters(username, subsonicPassword, "plain")
		params.Set("id", f.groups[0])
		params.Set("rating", rating)
		requireSubsonicOK(t, requestSubsonic(app, http.MethodPost, "setRating.view", params))
	}
	setTool := func(rating int) {
		t.Helper()
		invoke("set_rating", map[string]any{"kind": "album", "target_id": f.groups[0], "rating": rating})
	}
	setTool(5)
	listAlbum(f.groups[0])
	// Subsonic 重报同评分时保留评分时间，证明它读写的是管家刚设置的同一条记录。
	before := decodeRatingPage[personal.RatedAlbumsPage](t, f.call(http.MethodGet, ratingsURL+"/albums", nil))
	subsonicSet("5")
	after := decodeRatingPage[personal.RatedAlbumsPage](t, f.call(http.MethodGet, ratingsURL+"/albums", nil))
	if len(before.Items) != 1 || len(after.Items) != 1 || !before.Items[0].RatedAt.Equal(after.Items[0].RatedAt) {
		t.Fatalf("Subsonic 重报评分改变了管家的评分记录：前 %+v，后 %+v", before, after)
	}
	setTool(0)
	listAlbum()
	subsonicSet("5")
	listAlbum(f.groups[0])
	subsonicSet("0")
	listAlbum()
}
