package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestArtistI2AlbumParameterValidation(t *testing.T) {
	for _, tc := range []struct{ query, code string }{
		{"", ""}, {"participation=any", ""}, {"participation=", "invalid_artist_participation"}, {"participation=album_artist", "invalid_artist_participation"},
		{"participation=any&participation=any", "invalid_artist_participation"}, {"q=a&q=b", "invalid_query"}, {"q=&q=", "invalid_query"},
		{"q=%01", "invalid_query"}, {"q=%FF", "invalid_query"}, {"q=" + strings.Repeat("a", 200), ""}, {"q=" + strings.Repeat("a", 201), "invalid_query"},
		{"q=" + url.QueryEscape(strings.Repeat("中", 66)+"ab"), ""}, {"q=" + url.QueryEscape(strings.Repeat("中", 67)), "invalid_query"},
		{"q=" + url.QueryEscape(" \t%_~ \n"), ""},
	} {
		r := httptest.NewRequest("GET", "/api/v1/artists/id/albums?"+tc.query, nil)
		w := httptest.NewRecorder()
		option, ok := parseArtistAlbumOptions(w, r)
		if ok != (tc.code == "") || (!ok && (w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`))) {
			t.Fatalf("%s: %+v %d %s", tc.query, option, w.Code, w.Body.String())
		}
		if ok && strings.Contains(tc.query, "%25") && option.Query != "%_~" {
			t.Fatalf("未修剪搜索边界: %q", option.Query)
		}
	}
}

func TestPostgreSQLArtistI2I6HTTP(t *testing.T) {
	ctx := context.Background()
	db, err := postgres.Open(ctx, postgrestest.NewSchema(t, "roomusic_artist_i2_i6_"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(statement string, args ...any) {
		t.Helper()
		if _, err := db.Exec(statement, args...); err != nil {
			t.Fatal(err)
		}
	}
	user, root, group, release, medium, track := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	token := identity.GenerateSessionToken()
	exec(`INSERT INTO users(id,username,password_hash,role) VALUES($1::uuid,'member','x','user')`, user)
	exec(`INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2::uuid,now()+interval '1 hour')`, identity.HashSessionToken(token), user)
	h := New(db, identity.NewGuard(db, false, ""), t.TempDir(), nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/artists", h.listArtists)
	mux.HandleFunc("GET /api/v1/artists/{id}/albums", h.artistAlbums)
	request := func(path string, loggedIn bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		if loggedIn {
			r.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: token})
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	checkError := func(path string, loggedIn bool, status int, code string) {
		t.Helper()
		w := request(path, loggedIn)
		if w.Code != status || !strings.Contains(w.Body.String(), `"code":"`+code+`"`) || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		if code == "artist_catalog_preparing" && w.Header().Get("Retry-After") != "3" {
			t.Fatalf("整理响应缺少Retry-After: %v", w.Header())
		}
	}
	base := "/api/v1/artists/" + uuid.New() + "/albums"
	for _, path := range []string{base + "?participation=any", "/api/v1/artists?role=album_artist", "/api/v1/artists?role=release_artist"} {
		checkError(path, false, 401, "unauthorized")
		checkError(path, true, 503, "artist_catalog_preparing")
	}
	for _, tc := range []struct{ query, code string }{
		{"participation=all", "invalid_artist_participation"}, {"participation=", "invalid_artist_participation"}, {"participation=any&participation=any", "invalid_artist_participation"},
		{"q=x&q=x", "invalid_query"}, {"q=%00", "invalid_query"}, {"q=%FF", "invalid_query"}, {"q=" + strings.Repeat("x", 201), "invalid_query"},
		{"page=0", "invalid_pagination"}, {"page_size=101", "invalid_pagination"}, {"page_size=0", "invalid_pagination"},
	} {
		checkError(base+"?"+tc.query, true, 400, tc.code)
	}
	checkError("/api/v1/artists/not-uuid/albums?participation=any", true, 400, "invalid_id")
	checkError("/api/v1/artists?role=album_artist&role=release_artist", true, 400, "invalid_artist_role")
	checkError(base+"?participation=bad", false, 401, "unauthorized")

	exec(`INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,'/library','active',1)`, root)
	exec(`INSERT INTO release_groups(id) VALUES($1::uuid)`, group)
	exec(`INSERT INTO releases(id,group_id,title,artist,album_artist,source_root_id,relative_directory,candidate_anchor,candidate_kind) VALUES($1::uuid,$2::uuid,'专辑%_~','发行独有','专辑独有',$3::uuid,'album','album-anchor','ordinary_directory')`, release, group, root)
	exec(`INSERT INTO media(id,release_id,position,title) VALUES($1::uuid,$2::uuid,1,'碟')`, medium, release)
	exec(`INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind) VALUES($1::uuid,$2::uuid,1,'曲目','曲目独有',1,$3::uuid,'album/track.flac','present',now(),'physical')`, track, medium, root)
	exec(`INSERT INTO track_credits(track_id,role,name,position) VALUES($1::uuid,'composer','作曲独有',1)`, track)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := catalog.AcquireArtistWriteGate(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err := artistgraph.Refresh(ctx, tx, []string{release}); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE catalog_projection_state SET status='ready',manifest_sealed=true,published_generation=target_generation,published_textnorm_version=textnorm_version,published_search_projection_version=search_projection_version WHERE capability='artists'`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	find := func(name string) string {
		t.Helper()
		var id string
		if err := db.QueryRow(`SELECT initial_artist_id::text FROM artist_name_keys WHERE normalized_key=$1`, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	composer, album, releaseArtist := find("作曲独有"), find("专辑独有"), find("发行独有")
	composerPath := "/api/v1/artists/" + composer + "/albums"
	albums := func(path string) catalog.ArtistPage[catalog.ArtistAlbum] {
		t.Helper()
		w := request(path, true)
		var page catalog.ArtistPage[catalog.ArtistAlbum]
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil {
			t.Fatalf("普通成员读取失败 %s: %d %s", path, w.Code, w.Body.String())
		}
		return page
	}
	if got := albums(composerPath); got.Pagination.Total != 0 || got.Items == nil || len(got.Items) != 0 {
		t.Fatalf("默认不应包含作曲: %+v", got)
	}
	for _, query := range []string{"", "&q=%25", "&q=_", "&q=~", "&q=%25_~", "&page_size=100"} {
		got := albums(composerPath + "?participation=any" + query)
		if got.Pagination.Total != 1 || len(got.Items) != 1 || got.Items[0].Group.ID != group || got.Items[0].CreditedReleaseID != release || !reflect.DeepEqual(got.Items[0].Roles, []string{"composer"}) {
			t.Fatalf("全角色参与响应错误: %+v", got)
		}
	}
	for _, query := range []string{"&page=2&page_size=1", "&q=" + strings.Repeat("a", 200), "&q=" + url.QueryEscape(strings.Repeat("中", 66)+"ab")} {
		got := albums(composerPath + "?participation=any" + query)
		if got.Items == nil || len(got.Items) != 0 {
			t.Fatalf("空页应为空数组: %+v", got)
		}
	}
	w := request("/api/v1/artists/"+album+"/albums", true)
	if w.Code != 200 || strings.Contains(w.Body.String(), `"roles":`) {
		t.Fatalf("旧响应改变: %d %s", w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		role string
		ids  []string
	}{{"album_artist", []string{album, releaseArtist}}, {"release_artist", []string{releaseArtist}}} {
		w := request("/api/v1/artists?role="+tc.role, true)
		var page catalog.ArtistPage[catalog.ArtistSummary]
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Pagination.Total != int64(len(tc.ids)) || len(page.Items) != len(tc.ids) {
			t.Fatalf("角色筛选: %d %s", w.Code, w.Body.String())
		}
		actual := map[string]bool{}
		for _, artist := range page.Items {
			actual[artist.ID] = true
		}
		for _, id := range tc.ids {
			if !actual[id] {
				t.Fatalf("角色身份不符: %+v", page)
			}
		}
	}
	checkError(base+"?participation=any", true, 404, "not_found")
}
