package app

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
)

func subsonicPersonalRequest(a *integrationTestApplication, username, method, endpoint string, fields url.Values) *httptest.ResponseRecorder {
	params := subsonicParameters(username, subsonicPassword, "token")
	for key, values := range fields {
		params[key] = values
	}
	return requestSubsonic(a, method, endpoint, params)
}

func decodeSubsonicPersonalWeb[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	personalOK(t, response, http.StatusOK)
	var body T
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析网页个人数据响应：%v，正文 %s", err, response.Body)
	}
	return body
}

type subsonicFavoriteTimes struct {
	tracks, albums, artists map[string]string
}

func newSubsonicFavoriteTimes() subsonicFavoriteTimes {
	return subsonicFavoriteTimes{map[string]string{}, map[string]string{}, map[string]string{}}
}

func webSubsonicFavorites(t *testing.T, f *subsonicBrowseFixture) (subsonicFavoriteTimes, int64) {
	t.Helper()
	get := func(kind string) *httptest.ResponseRecorder {
		return f.app.request(t, http.MethodGet, favoritesURL+"/"+kind, nil, &f.admin, nil)
	}
	tracks := decodeSubsonicPersonalWeb[personal.FavoriteTracksPage](t, get("tracks"))
	albums := decodeSubsonicPersonalWeb[personal.FavoriteAlbumsPage](t, get("albums"))
	artists := decodeSubsonicPersonalWeb[personal.FavoriteArtistsPage](t, get("artists"))
	if tracks.Revision != albums.Revision || tracks.Revision != artists.Revision {
		t.Fatalf("三种收藏列表的版本不同：%d、%d、%d", tracks.Revision, albums.Revision, artists.Revision)
	}
	out := newSubsonicFavoriteTimes()
	for _, item := range tracks.Items {
		out.tracks[item.Track.TrackID] = item.CreatedAt.Format(time.RFC3339Nano)
	}
	for _, item := range albums.Items {
		out.albums[item.Album.GroupID] = item.CreatedAt.Format(time.RFC3339Nano)
	}
	for _, item := range artists.Items {
		out.artists[item.Artist.ID] = item.CreatedAt.Format(time.RFC3339Nano)
	}
	if int64(len(out.tracks)) != tracks.Pagination.Total || int64(len(out.albums)) != albums.Pagination.Total ||
		int64(len(out.artists)) != artists.Pagination.Total || len(artists.Items) != len(out.artists) {
		t.Fatalf("网页收藏总数或艺人去重有误：曲目 %+v，专辑 %+v，艺人 %+v", tracks, albums, artists)
	}
	return out, tracks.Revision
}

func requireSubsonicStarred2(t *testing.T, f *subsonicBrowseFixture, want subsonicFavoriteTimes) {
	t.Helper()
	for _, format := range []string{"json", "xml"} {
		list := subsonicBrowseResult[protocol.SearchResult](t, f, format, "getStarred2", "starred2")
		got := newSubsonicFavoriteTimes()
		for _, artist := range list.Artists {
			got.artists[artist.ID] = artist.Starred
		}
		for _, album := range list.Albums {
			got.albums[album.ID] = album.Starred
		}
		for _, track := range list.Songs {
			got.tracks[track.ID] = track.Starred
		}
		if !reflect.DeepEqual(got, want) || len(list.Artists) != len(want.artists) ||
			len(list.Albums) != len(want.albums) || len(list.Songs) != len(want.tracks) {
			t.Fatalf("%s getStarred2 = %+v，期望 %+v", format, got, want)
		}
	}
}

func requireSubsonicFavoriteIDs(t *testing.T, got map[string]string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("收藏列表为 %v，期望 ID %v", got, want)
	}
	for _, id := range want {
		if _, err := time.Parse(time.RFC3339Nano, got[id]); err != nil {
			t.Fatalf("收藏 %s 缺少有效时间，列表为 %v", id, got)
		}
	}
}

func TestPostgreSQLSubsonicPersonalFavorites(t *testing.T) {
	f := newSubsonicBrowseFixture(t)
	f.publish(t)
	first, second, webTrack := f.tracks["Overture"], f.tracks["Finale"], f.tracks["Blue Song"]
	album, webAlbum := f.albums[subsonicZebra].group, f.albums[subsonicBlue].group
	source, target := f.artists["Cedar Band"], f.artists["Atlas Collective"]
	write := func(endpoint string, values url.Values) {
		t.Helper()
		requireSubsonicOK(t, subsonicPersonalRequest(f.app, subsonicAdmin, http.MethodPost, endpoint, values))
	}

	batch := url.Values{"id": {first, second}, "albumId": {album}, "artistId": {source}}
	write("star", batch)
	initial, revision := webSubsonicFavorites(t, f)
	requireSubsonicFavoriteIDs(t, initial.tracks, first, second)
	requireSubsonicFavoriteIDs(t, initial.albums, album)
	requireSubsonicFavoriteIDs(t, initial.artists, source)
	if revision != 1 {
		t.Fatalf("一次批量收藏后版本为 %d，期望 1", revision)
	}
	requireSubsonicStarred2(t, f, initial)

	write("star", batch)
	repeated, repeatedRevision := webSubsonicFavorites(t, f)
	if !reflect.DeepEqual(repeated, initial) || repeatedRevision != revision {
		t.Fatalf("重复收藏改变了收藏或版本：%+v（%d）→ %+v（%d）", initial, revision, repeated, repeatedRevision)
	}
	requireSubsonicStarred2(t, f, initial)

	for _, favorite := range []struct{ kind, id string }{
		{"tracks", webTrack}, {"albums", webAlbum}, {"artists", target},
	} {
		personalOK(t, f.app.request(t, http.MethodPut, favoritesURL+"/"+favorite.kind+"/"+favorite.id, nil, &f.admin, nil), http.StatusOK)
	}
	web, webRevision := webSubsonicFavorites(t, f)
	requireSubsonicFavoriteIDs(t, web.tracks, first, second, webTrack)
	requireSubsonicFavoriteIDs(t, web.albums, album, webAlbum)
	requireSubsonicFavoriteIDs(t, web.artists, source, target)
	requireSubsonicStarred2(t, f, web)
	song := subsonicBrowseResult[protocol.Song](t, f, "json", "getSong", "song", "id", webTrack)
	browsedAlbum := subsonicBrowseResult[protocol.Album](t, f, "json", "getAlbum", "album", "id", webAlbum)
	artist := subsonicBrowseResult[protocol.Artist](t, f, "json", "getArtist", "artist", "id", target)
	if song.ID != webTrack || song.Starred != web.tracks[webTrack] ||
		browsedAlbum.ID != webAlbum || browsedAlbum.Starred != web.albums[webAlbum] ||
		artist.ID != target || artist.Starred != web.artists[target] {
		t.Fatalf("浏览响应的收藏字段与网页不同：%+v / %+v / %+v", song, browsedAlbum, artist)
	}

	merged := artistMediaCommand(t, f.app, &f.admin, http.MethodPost, target, "/merge", uuid.New(), map[string]any{
		"expected_revision":        readArtistMedia(t, f.app, &f.admin, target).Revision,
		"source_artist_id":         source,
		"source_expected_revision": readArtistMedia(t, f.app, &f.admin, source).Revision,
	})
	if merged.Code != http.StatusOK {
		t.Fatalf("网页合并艺人：HTTP %d %s", merged.Code, merged.Body)
	}
	var sourceTime time.Time
	if err := f.app.database.QueryRowContext(t.Context(),
		`SELECT created_at FROM artist_favorites WHERE user_id=$1 AND artist_id=$2 AND resolved_artist_id=$3`,
		f.admin.id, source, target).Scan(&sourceTime); err != nil || sourceTime.UTC().Format(time.RFC3339Nano) != initial.artists[source] {
		t.Fatalf("合并后原锚点和收藏时间未保留：%v，%v", sourceTime, err)
	}
	mergedFavorites, mergedRevision := webSubsonicFavorites(t, f)
	sourceAt, _ := time.Parse(time.RFC3339Nano, web.artists[source])
	targetAt, _ := time.Parse(time.RFC3339Nano, web.artists[target])
	wantMerged := web
	wantMerged.artists = map[string]string{target: targetAt.Format(time.RFC3339Nano)}
	if sourceAt.After(targetAt) {
		wantMerged.artists[target] = web.artists[source]
	}
	if mergedRevision <= webRevision || !reflect.DeepEqual(mergedFavorites, wantMerged) {
		t.Fatalf("合并后的收藏为 %+v（版本 %d），期望 %+v（版本大于 %d）", mergedFavorites, mergedRevision, wantMerged, webRevision)
	}
	requireSubsonicStarred2(t, f, wantMerged)
	for _, id := range []string{source, target} {
		got := subsonicBrowseResult[protocol.Artist](t, f, "json", "getArtist", "artist", "id", id)
		if got.ID != target || got.Starred != wantMerged.artists[target] {
			t.Fatalf("艺人 %s 合并后的收藏响应为 %+v", id, got)
		}
	}

	write("star", url.Values{"artistId": {source, target}})
	same, sameRevision := webSubsonicFavorites(t, f)
	if !reflect.DeepEqual(same, wantMerged) || sameRevision != mergedRevision {
		t.Fatalf("重复收藏等价艺人改变了收藏或版本：%+v（%d）", same, sameRevision)
	}
	write("unstar", url.Values{"artistId": {source}, "id": {first}, "albumId": {album}})
	remaining, afterRevision := webSubsonicFavorites(t, f)
	wantRemaining := subsonicFavoriteTimes{maps.Clone(web.tracks), maps.Clone(web.albums), map[string]string{}}
	delete(wantRemaining.tracks, first)
	delete(wantRemaining.albums, album)
	if !reflect.DeepEqual(remaining, wantRemaining) || afterRevision != mergedRevision+1 {
		t.Fatalf("取消收藏后为 %+v（版本 %d），期望 %+v（版本 %d）", remaining, afterRevision, wantRemaining, mergedRevision+1)
	}
	requireSubsonicStarred2(t, f, remaining)

	write("star", url.Values{"artistId": {source, target}})
	anchors := countRows(t, f.app, `SELECT count(*) FROM artist_favorites WHERE user_id=$1`, f.admin.id)
	firstAnchor := countRows(t, f.app,
		`SELECT count(*) FROM artist_favorites WHERE user_id=$1 AND artist_id=$2 AND resolved_artist_id=$3`, f.admin.id, source, target)
	if anchors != 1 || firstAnchor != 1 {
		t.Fatalf("同时收藏两个等价 ID 后有 %d 个锚点，其中 %d 个指向请求中第一项", anchors, firstAnchor)
	}
}

func TestPostgreSQLSubsonicPersonalFavoriteBatchAtomic(t *testing.T) {
	f := newSubsonicBrowseFixture(t)
	f.publish(t)
	existing := url.Values{
		"id": {f.tracks["Overture"]}, "albumId": {f.albums[subsonicZebra].group}, "artistId": {f.artists["Atlas Collective"]},
	}
	additions := url.Values{
		"id": {f.tracks["Finale"]}, "albumId": {f.albums[subsonicBlue].group}, "artistId": {f.artists["Cedar Band"]},
	}
	requireSubsonicOK(t, subsonicPersonalRequest(f.app, subsonicAdmin, http.MethodPost, "star", existing))
	before, revision := webSubsonicFavorites(t, f)
	for _, action := range []struct {
		name   string
		fields url.Values
	}{{"star", additions}, {"unstar", existing}} {
		for _, key := range []string{"id", "albumId", "artistId"} {
			t.Run(action.name+"/"+key, func(t *testing.T) {
				fields := maps.Clone(action.fields)
				fields[key] = append(slices.Clone(fields[key]), uuid.New())
				response := subsonicPersonalRequest(f.app, subsonicAdmin, http.MethodPost, action.name, fields)
				requireSubsonicFailure(t, response, http.StatusOK, protocol.NotFound)
				after, afterRevision := webSubsonicFavorites(t, f)
				if !reflect.DeepEqual(after, before) || afterRevision != revision {
					t.Fatalf("失败的 %s 请求改变了收藏或版本：%+v（%d）→ %+v（%d）",
						action.name, before, revision, after, afterRevision)
				}
			})
		}
	}
	requireSubsonicStarred2(t, f, before)
}
