package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/maintenance"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type searchPage[T any] struct {
	Query        string                   `json:"query"`
	Type         string                   `json:"type"`
	CatalogState catalog.CatalogState     `json:"catalog_state"`
	Items        []T                      `json:"items"`
	Pagination   catalog.SearchPagination `json:"pagination"`
}

type searchReleaseFixture struct {
	releaseID, groupID string
	trackIDs           []string
}

func searchRoot(t *testing.T, app *integrationTestApplication) string {
	t.Helper()
	id := uuid.New()
	mustExec(t, app, `INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'active',1)`, id, app.allowedRoot)
	return id
}

func insertSearchRelease(t *testing.T, app *integrationTestApplication, root string, spec groupedRelease) searchReleaseFixture {
	t.Helper()
	release, group := insertGroupedRelease(t, app, root, spec)
	rows, err := app.database.Query(`SELECT t.id::text FROM tracks t JOIN media m ON m.id=t.medium_id WHERE m.release_id=$1::uuid ORDER BY t.position,t.id`, release)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := searchReleaseFixture{releaseID: release, groupID: group}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		result.trackIDs = append(result.trackIDs, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

// 测试只调用公开维护入口，等待真实发布，不直接伪造 ready 或写派生表。
func publishSearch(t *testing.T, app *integrationTestApplication) catalog.CatalogState {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	done := make(chan error, 1)
	go func() { done <- maintenance.Run(ctx, app.database, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
	finished := false
	defer func() {
		cancel()
		if !finished {
			select {
			case err := <-done:
				if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("停止搜索后台：%v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("搜索后台未按时退出")
			}
		}
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := catalog.NewStore(app.database).LoadCatalogState(ctx)
		if err != nil {
			t.Fatalf("读取目录状态：%v", err)
		}
		if state.SearchMode == "normalized" {
			if state.ArtistsState != "unavailable" {
				t.Fatalf("④不应启用艺人：%+v", state)
			}
			return state
		}
		select {
		case err := <-done:
			finished = true
			if err != nil {
				t.Fatalf("搜索后台退出：%v", err)
			}
			// Run 允许发布成功后返回；仍以公开状态确认发布结果。
			state, err = catalog.NewStore(app.database).LoadCatalogState(ctx)
			if err != nil || state.SearchMode != "normalized" {
				t.Fatalf("后台退出但未发布：%+v，%v", state, err)
			}
			return state
		case <-ctx.Done():
			t.Fatalf("等待搜索发布超时：%+v", state)
		case <-ticker.C:
		}
	}
}

func searchURL(kind, query string) string {
	return "/api/v1/search?type=" + kind + "&q=" + url.QueryEscape(query)
}

func searchResponse(t *testing.T, app *integrationTestApplication, user *integrationTestUser, path string) *httptest.ResponseRecorder {
	t.Helper()
	response := app.request(t, http.MethodGet, path, nil, user, map[string]string{"X-Request-ID": sampleRequestID})
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s: HTTP %d: %s", path, response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("搜索响应必须 no-store：%v", response.Header())
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("搜索响应必须 JSON：%v", response.Header())
	}
	return response
}

func searchRead[T any](t *testing.T, app *integrationTestApplication, user *integrationTestUser, path string) searchPage[T] {
	t.Helper()
	response := searchResponse(t, app, user, path)
	var page searchPage[T]
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Items == nil {
		t.Fatalf("items 必须为空数组而非 null：%s", response.Body.String())
	}
	return page
}

func requireSearchTracks(t *testing.T, app *integrationTestApplication, user *integrationTestUser, query string, want ...string) searchPage[catalog.SearchTrackItem] {
	t.Helper()
	page := searchRead[catalog.SearchTrackItem](t, app, user, searchURL("tracks", query))
	got := map[string]bool{}
	for _, item := range page.Items {
		if !item.Present {
			t.Fatalf("搜索返回 missing：%+v", item)
		}
		if got[item.TrackID] {
			t.Fatalf("曲目重复：%s", item.TrackID)
		}
		got[item.TrackID] = true
	}
	expected := map[string]bool{}
	for _, id := range want {
		expected[id] = true
	}
	if !reflect.DeepEqual(got, expected) || page.Pagination.Total != int64(len(want)) || page.Pagination.TotalRelation != "eq" || page.Pagination.HasMore {
		t.Fatalf("查询 %q：曲目 %v / 分页 %+v，期望 %v", query, got, page.Pagination, expected)
	}
	return page
}

func requireSearchAlbums(t *testing.T, app *integrationTestApplication, user *integrationTestUser, query string, want ...string) searchPage[catalog.SearchAlbumItem] {
	t.Helper()
	page := searchRead[catalog.SearchAlbumItem](t, app, user, searchURL("albums", query))
	got := map[string]bool{}
	for _, item := range page.Items {
		if got[item.Group.ID] {
			t.Fatalf("专辑未去重：%s", item.Group.ID)
		}
		got[item.Group.ID] = true
	}
	expected := map[string]bool{}
	for _, id := range want {
		expected[id] = true
	}
	if !reflect.DeepEqual(got, expected) || page.Pagination.Total != int64(len(want)) || page.Pagination.TotalRelation != "eq" || page.Pagination.HasMore {
		t.Fatalf("查询 %q：专辑 %v / 分页 %+v，期望 %v", query, got, page.Pagination, expected)
	}
	return page
}
