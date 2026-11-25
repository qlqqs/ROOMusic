package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
)

func webSubsonicPlaylistItems(t *testing.T, f *personalFixture, id string) personal.PlaylistItemsPage {
	t.Helper()
	return decodeSubsonicPersonalWeb[personal.PlaylistItemsPage](t, f.call(http.MethodGet, playlistsURL+"/"+id+"/items", nil))
}

func requireSubsonicPlaylist(t *testing.T, p protocol.Playlist, id, name, username string, tracks []string) {
	t.Helper()
	if p.ID != id || p.Name != name || p.Owner != username || p.Public || p.CoverArt != "pl-"+id ||
		p.Created == "" || p.Changed == "" || p.SongCount != int64(len(tracks)) {
		t.Fatalf("Subsonic 歌单属性为 %+v，期望 %s / %s / %s，曲目数 %d", p, id, name, username, len(tracks))
	}
	got := make([]string, 0, len(p.Entries))
	for _, entry := range p.Entries {
		got = append(got, entry.ID)
	}
	if !slices.Equal(got, tracks) {
		t.Fatalf("Subsonic 歌单曲序为 %v，期望 %v", got, tracks)
	}
}

func requireSubsonicWebPlaylist(t *testing.T, f *personalFixture, id, name string, revision int64, tracks []string) personal.PlaylistItemsPage {
	t.Helper()
	state := f.state(id)
	if state.ID != id || state.Name != name || state.Revision != revision || state.TrackCount != int64(len(tracks)) {
		t.Fatalf("网页歌单为 %+v，期望 %s / %s，版本 %d，曲目数 %d", state, id, name, revision, len(tracks))
	}
	page := webSubsonicPlaylistItems(t, f, id)
	got := make([]string, 0, len(page.Items))
	for i, entry := range page.Items {
		if entry.Position != int64(i+1) {
			t.Fatalf("网页歌单第 %d 项的位置为 %d", i+1, entry.Position)
		}
		got = append(got, entry.Track.TrackID)
	}
	if page.Playlist != state || page.Pagination.Total != int64(len(tracks)) || !slices.Equal(got, tracks) {
		t.Fatalf("网页歌单曲序为 %v（总数 %d），期望 %v，条目页歌单为 %+v", got, page.Pagination.Total, tracks, page.Playlist)
	}
	return page
}

func TestPostgreSQLSubsonicPersonalPlaylists(t *testing.T) {
	browse := newSubsonicBrowseFixture(t)
	f := &personalFixture{t: t, app: browse.app, user: browse.admin}
	a, b, c, d := browse.tracks["Overture"], browse.tracks["Duet & Guests"], browse.tracks["Finale"], browse.tracks["Maple Song"]
	write := func(endpoint string, fields url.Values) *httptest.ResponseRecorder {
		return subsonicPersonalRequest(f.app, subsonicAdmin, http.MethodPost, endpoint, fields)
	}

	check := func(id, name string, revision int64, tracks ...string) personal.PlaylistItemsPage {
		t.Helper()
		p := subsonicBrowseResult[protocol.Playlist](t, browse, "json", "getPlaylist", "playlist", "id", id)
		requireSubsonicPlaylist(t, p, id, name, subsonicAdmin, tracks)
		return requireSubsonicWebPlaylist(t, f, id, name, revision, tracks)
	}

	const name = "合成歌单"
	initial := url.Values{"name": {name}, "songId": {a, b}, "public": {"true"}}
	create := func(fields url.Values) protocol.Playlist {
		t.Helper()
		p := subsonicBrowseJSON[protocol.Playlist](t, write("createPlaylist", fields), "playlist")
		if !uuid.Valid(p.ID) {
			t.Fatalf("新建歌单返回无效 ID：%+v", p)
		}
		return p
	}

	empty := create(url.Values{"name": {"空歌单"}})
	requireSubsonicPlaylist(t, empty, empty.ID, "空歌单", subsonicAdmin, nil)
	check(empty.ID, "空歌单", 1)
	first, second := create(initial), create(initial)
	if first.ID == second.ID {
		t.Fatal("两次同名同曲目新建返回了同一个歌单")
	}
	for _, p := range []protocol.Playlist{first, second} {
		requireSubsonicPlaylist(t, p, p.ID, name, subsonicAdmin, []string{a, b})
		check(p.ID, name, 1, a, b)
	}

	cover := subsonicPersonalRequest(f.app, subsonicAdmin, http.MethodGet, "getCoverArt", url.Values{"id": {first.CoverArt}, "size": {"128"}})
	albumCover := subsonicPersonalRequest(f.app, subsonicAdmin, http.MethodGet, "getCoverArt",
		url.Values{"id": {"al-" + browse.albums[subsonicZebra].group}, "size": {"128"}})
	requireSubsonicBrowseImage(t, cover, "png", 128, 64)
	requireSubsonicBrowseImage(t, albumCover, "png", 128, 64)
	if !bytes.Equal(cover.Body.Bytes(), albumCover.Body.Bytes()) {
		t.Fatal("歌单封面与首曲所在专辑的封面不同")
	}

	const renamed = "改名后的歌单"
	requireSubsonicOK(t, write("updatePlaylist", url.Values{"playlistId": {first.ID}, "name": {renamed}}))
	check(first.ID, renamed, 2, a, b)
	requireSubsonicOK(t, write("updatePlaylist", url.Values{"playlistId": {first.ID}, "songIdToAdd": {c, d, a, d}}))
	beforeRemove := check(first.ID, renamed, 3, a, b, c, d)
	requireSubsonicOK(t, write("updatePlaylist", url.Values{"playlistId": {first.ID}, "songIndexToRemove": {"1"}}))
	check(first.ID, renamed, 4, a, c, d)

	replaced := subsonicBrowseJSON[protocol.Playlist](t, write("createPlaylist",
		url.Values{"playlistId": {first.ID}, "songId": {d, a, d}}), "playlist")
	requireSubsonicPlaylist(t, replaced, first.ID, renamed, subsonicAdmin, []string{d, a})
	afterReplace := check(first.ID, renamed, 5, d, a)
	for i, previous := range []personal.PlaylistItem{beforeRemove.Items[3], beforeRemove.Items[0]} {
		if got := afterReplace.Items[i]; got.ID != previous.ID || !got.AddedAt.Equal(previous.AddedAt) {
			t.Fatalf("整体替换改变了保留条目的身份或添加时间：%+v → %+v", previous, got)
		}
	}
	cleared := subsonicBrowseJSON[protocol.Playlist](t, write("createPlaylist", url.Values{"playlistId": {first.ID}}), "playlist")
	requireSubsonicPlaylist(t, cleared, first.ID, renamed, subsonicAdmin, nil)
	check(first.ID, renamed, 6)

	requireSubsonicOK(t, write("deletePlaylist", url.Values{"playlistId": {first.ID}}))
	requireAPIError(t, f.call(http.MethodGet, playlistsURL+"/"+first.ID, nil), http.StatusNotFound, "not_found")
	requireSubsonicFailure(t, subsonicPersonalRequest(f.app, subsonicAdmin, http.MethodGet, "getPlaylist", url.Values{"id": {first.ID}}),
		http.StatusOK, protocol.NotFound)
	recreated := create(initial)
	if recreated.ID == first.ID || recreated.ID == second.ID {
		t.Fatalf("删除后同参数新建复用了旧歌单 ID %s", recreated.ID)
	}
	check(recreated.ID, name, 1, a, b)
	check(second.ID, name, 1, a, b)

	want := []string{empty.ID, second.ID, recreated.ID}
	slices.Sort(want)
	web := decodeSubsonicPersonalWeb[personal.PlaylistsPage](t, f.call(http.MethodGet, playlistsURL, nil))
	var webIDs []string
	for _, item := range web.Items {
		webIDs = append(webIDs, item.ID)
	}
	list := subsonicBrowseResult[protocol.Playlists](t, browse, "json", "getPlaylists", "playlists", "username", subsonicAdmin)
	var subsonicIDs []string
	for _, item := range list.Items {
		if item.Owner != subsonicAdmin || item.Public || item.CoverArt != "pl-"+item.ID || len(item.Entries) != 0 {
			t.Fatalf("Subsonic 歌单列表条目有误：%+v", item)
		}
		subsonicIDs = append(subsonicIDs, item.ID)
	}
	slices.Sort(webIDs)
	slices.Sort(subsonicIDs)
	if !slices.Equal(webIDs, want) || !slices.Equal(subsonicIDs, want) {
		t.Fatalf("网页与 Subsonic 歌单 ID 分别为 %v、%v，期望 %v", webIDs, subsonicIDs, want)
	}
}

func TestPostgreSQLSubsonicPersonalPlaylistIsolation(t *testing.T) {
	app := newSubsonicTestApplication(t)
	admin := setupSubsonicAdmin(t, app)
	owner := newPersonalFixtureForUser(t, app, admin, 3)
	const otherName = "subsonic-other-owner"
	createSubsonicMember(t, app, &admin, otherName)
	other := loginPasswordUser(t, app, otherName, subsonicPassword, "user")
	otherWeb := &personalFixture{t: t, app: app, user: other}
	private := owner.create("别人的歌单", owner.tracks[:2])
	own := otherWeb.create("本人的歌单", owner.tracks[2:])
	before := webSubsonicPlaylistItems(t, owner, private.ID)
	receipts := countRows(t, app, `SELECT count(*) FROM personal_playlist_requests`)

	list := subsonicBrowseJSON[protocol.Playlists](t,
		subsonicPersonalRequest(app, otherName, http.MethodGet, "getPlaylists", nil), "playlists")
	if len(list.Items) != 1 || list.Items[0].ID != own.ID || list.Items[0].Owner != otherName {
		t.Fatalf("用户应只看到自己的歌单 %s，实际为 %+v", own.ID, list)
	}
	for _, tc := range []struct {
		endpoint string
		method   string
		fields   url.Values
		code     int
	}{
		{"getPlaylists", http.MethodGet, url.Values{"username": {subsonicAdmin}}, protocol.Forbidden},
		{"getPlaylist", http.MethodGet, url.Values{"id": {private.ID}}, protocol.NotFound},
		{"getCoverArt", http.MethodGet, url.Values{"id": {"pl-" + private.ID}}, protocol.NotFound},
		{"updatePlaylist", http.MethodPost, url.Values{"playlistId": {private.ID}, "name": {"越权改名"},
			"songIdToAdd": owner.tracks[2:], "songIndexToRemove": {"1"}}, protocol.NotFound},
		{"createPlaylist", http.MethodPost, url.Values{"playlistId": {private.ID}, "songId": owner.tracks[2:]}, protocol.NotFound},
		{"deletePlaylist", http.MethodPost, url.Values{"playlistId": {private.ID}}, protocol.NotFound},
	} {
		t.Run(tc.endpoint, func(t *testing.T) {
			requireSubsonicFailure(t, subsonicPersonalRequest(app, otherName, tc.method, tc.endpoint, tc.fields), http.StatusOK, tc.code)
		})
	}
	after := webSubsonicPlaylistItems(t, owner, private.ID)
	if !reflect.DeepEqual(before, after) || countRows(t, app, `SELECT count(*) FROM personal_playlist_requests`) != receipts {
		t.Fatalf("跨用户请求改变了歌单或写入收据：%+v → %+v", before, after)
	}
}
