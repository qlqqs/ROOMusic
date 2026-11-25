package app

import (
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
)

func TestSubsonicBrowseAlbumLists(t *testing.T) {
	f := newSubsonicBrowseFixture(t)
	f.publish(t)

	for _, tc := range []struct {
		name, kind string
		params     []string
		want       []string
	}{
		{"Newest", "newest", nil, []string{subsonicMaple, subsonicCue, subsonicBlue, subsonicZebra}},
		{"Name", "alphabeticalByName", nil, []string{subsonicCue, subsonicBlue, subsonicMaple, subsonicZebra}},
		{"Artist", "alphabeticalByArtist", nil, []string{subsonicZebra, subsonicBlue, subsonicCue, subsonicMaple}},
		{"AscendingYear", "byYear", []string{"fromYear", "1998", "toYear", "2005"}, []string{subsonicZebra, subsonicBlue, subsonicCue}},
		{"DescendingYear", "byYear", []string{"fromYear", "2010", "toYear", "2001"}, []string{subsonicMaple, subsonicCue, subsonicBlue}},
		{"Rock", "byGenre", []string{"genre", "Rock"}, []string{subsonicMaple, subsonicZebra}},
		{"OnlyOtherVersionGenre", "byGenre", []string{"genre", "Metal"}, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := append([]string{"type", tc.kind}, tc.params...)
			requireSubsonicAlbumOrder(t, f, tc.want, base...)
			for i := 0; i <= len(tc.want); i++ {
				params := append(slices.Clone(base), "size", "1", "offset", strconv.Itoa(i))
				requireSubsonicAlbumOrder(t, f, tc.want[i:min(i+1, len(tc.want))], params...)
			}
		})
	}

	t.Run("ArtistSortKey", func(t *testing.T) {
		mustExec(t, f.app, `UPDATE artist_profiles SET sort_key='aardvark' WHERE artist_id=$1`, f.artists["Zenith Group"])
		requireSubsonicAlbumOrder(t, f, []string{subsonicMaple, subsonicZebra, subsonicBlue, subsonicCue}, "type", "alphabeticalByArtist")
	})

	t.Run("Random", func(t *testing.T) {
		list := subsonicBrowseResult[protocol.AlbumList](t, f, "json", "getAlbumList2", "albumList2", "type", "random", "size", "4")
		var ids []string
		for _, album := range list.Albums {
			ids = append(ids, album.ID)
		}
		slices.Sort(ids)
		want := []string{f.albums[subsonicCue].group, f.albums[subsonicBlue].group,
			f.albums[subsonicMaple].group, f.albums[subsonicZebra].group}
		slices.Sort(want)
		if !slices.Equal(ids, want) {
			t.Errorf("随机专辑 ID = %v，期望 %v", ids, want)
		}
		page := subsonicBrowseResult[protocol.AlbumList](t, f, "json", "getAlbumList2", "albumList2",
			"type", "random", "size", "2", "offset", "3")
		if len(page.Albums) != 1 || !slices.Contains(want, page.Albums[0].ID) {
			t.Errorf("随机末页只有一张可见专辑，实际 %+v", page)
		}
		requireSubsonicSeededAlbumPages(t, f)
	})
	t.Run("InvalidParameters", func(t *testing.T) {
		for _, fields := range [][]string{
			{}, {"type", "unknown"}, {"type", "byGenre"}, {"type", "byYear", "fromYear", "1998"},
			{"type", "byYear", "fromYear", "10000", "toYear", "1999"},
			{"type", "newest", "size", "broken"}, {"type", "newest", "offset", "-1"},
		} {
			t.Run(fmt.Sprint(fields), func(t *testing.T) {
				r := requestSubsonic(f.app, http.MethodGet, "getAlbumList2", f.params("json", fields...))
				requireSubsonicFailure(t, r, http.StatusOK, 10)
			})
		}
	})
	t.Run("PageSizeCap", func(t *testing.T) {
		mustExec(t, f.app, `WITH source AS (SELECT source_root_id FROM tracks WHERE id=$1::uuid),
 fixture AS MATERIALIZED (
 SELECT i,gen_random_uuid() AS gid,gen_random_uuid() AS rid,gen_random_uuid() AS mid FROM generate_series(1,501) i),
 groups AS (INSERT INTO release_groups(id) SELECT gid FROM fixture RETURNING id),
 releases AS (INSERT INTO releases(id,group_id,title,artist,source_root_id,relative_directory,candidate_kind)
 SELECT rid,gid,'Limit Album '||lpad(i::text,3,'0'),'Limit Artist',source_root_id,'limit-'||i,'ordinary_directory'
 FROM fixture JOIN groups ON groups.id=gid CROSS JOIN source RETURNING id),
 media AS (INSERT INTO media(id,release_id,position,title)
 SELECT mid,rid,1,'Disc' FROM fixture JOIN releases ON releases.id=rid RETURNING id)
 INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at)
 SELECT gen_random_uuid(),mid,1,'Limit Song','Limit Artist',1,source_root_id,'limit-'||i||'.flac','present',NOW()
 FROM fixture JOIN media ON media.id=mid CROSS JOIN source`, f.tracks["Overture"])

		list := subsonicBrowseResult[protocol.AlbumList](t, f, "json", "getAlbumList2", "albumList2",
			"type", "alphabeticalByName", "size", "501", "offset", "2")
		if len(list.Albums) != 500 || list.Albums[0].Name != "Limit Album 001" || list.Albums[499].Name != "Limit Album 500" {
			t.Fatalf("size=501、offset=2 的结果数 = %d，期望从 Limit Album 001 起的 500 个结果", len(list.Albums))
		}
		next := subsonicBrowseResult[protocol.AlbumList](t, f, "json", "getAlbumList2", "albumList2",
			"type", "alphabeticalByName", "size", "1", "offset", "502")
		if len(next.Albums) != 1 || next.Albums[0].Name != "Limit Album 501" {
			t.Errorf("500 项之后的第一页不符：%+v", next)
		}
	})
}

func requireSubsonicAlbumOrder(t *testing.T, f *subsonicBrowseFixture, want []string, fields ...string) {
	t.Helper()
	r := requestSubsonic(f.app, http.MethodGet, "getAlbumList2", f.params("json", fields...))
	list := subsonicBrowseJSON[protocol.AlbumList](t, r, "albumList2")
	var got []string
	for _, a := range list.Albums {
		got = append(got, a.Name)
		if a.ID != f.albums[a.Name].group {
			t.Errorf("专辑 %q 的 ID = %q，期望 %q", a.Name, a.ID, f.albums[a.Name].group)
		}
	}
	if list.Albums == nil || !slices.Equal(got, want) {
		t.Errorf("参数 %v 的专辑顺序 = %v，期望 %v（响应 %s）", fields, got, want, r.Body)
	}
}

func requireSubsonicSeededAlbumPages(t *testing.T, f *subsonicBrowseFixture) {
	t.Helper()
	store := catalog.NewStore(f.app.database)
	params := catalog.BrowseAlbumParams{
		BrowsePage: catalog.BrowsePage{Limit: 4}, Sort: catalog.BrowseAlbumSortRandom, Seed: 2718,
	}
	all, err := store.ListBrowseAlbums(t.Context(), params)
	if err != nil || len(all) != 4 {
		t.Fatalf("固定种子的随机专辑数 = %d，期望 4：%v", len(all), err)
	}
	params.BrowsePage = catalog.BrowsePage{Limit: 2, Offset: 1}
	page, err := store.ListBrowseAlbums(t.Context(), params)
	if err != nil || len(page) != 2 || page[0].ID != all[1].ID || page[1].ID != all[2].ID {
		t.Errorf("同种子的随机分页不符：%v（%v），完整顺序 %v", page, err, all)
	}
}

func TestSubsonicBrowsePersonalAlbumLists(t *testing.T) {
	f := newSubsonicBrowseFixture(t)
	otherID := createSubsonicMember(t, f.app, &f.admin, "subsonic-other-listener")
	other := loginPasswordUser(t, f.app, "subsonic-other-listener", subsonicPassword, "user")
	for i, name := range []string{subsonicZebra, subsonicBlue, subsonicMaple} {
		group := f.albums[name].group
		r := f.app.request(t, http.MethodPut, favoritesURL+"/albums/"+group, nil, &f.admin, nil)
		if r.Code != http.StatusOK {
			t.Fatalf("收藏 %q：HTTP %d %s", name, r.Code, r.Body)
		}
		mustExec(t, f.app, `UPDATE favorite_albums SET created_at=$3 WHERE user_id=$1 AND group_id=$2`,
			f.admin.id, group, time.Date(2026, 11, 1+i, 0, 0, 0, 0, time.UTC))
	}
	r := f.app.request(t, http.MethodPut, favoritesURL+"/albums/"+f.albums[subsonicCue].group, nil, &other, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("另一账号收藏：HTTP %d %s", r.Code, r.Body)
	}

	for i, title := range []string{"Overture", "Finale", "Overture", "Maple Song", "Maple Song", "Blue Song"} {
		mustExec(t, f.app, `INSERT INTO play_history(user_id,track_id,idempotency_key,played_seconds,played_at)
 VALUES ($1,$2,gen_random_uuid(),60,$3)`, f.admin.id, f.tracks[title], time.Date(2026, 11, 1+i, 0, 0, 0, 0, time.UTC))
	}
	mustExec(t, f.app, `INSERT INTO play_history(user_id,track_id,idempotency_key,played_seconds,played_at)
 SELECT $1,$2,gen_random_uuid(),60,NOW() FROM generate_series(1,10)`, otherID, f.tracks["Cue One"])

	for _, tc := range []struct {
		kind string
		want []string
	}{
		{"starred", []string{subsonicMaple, subsonicBlue, subsonicZebra}},
		{"recent", []string{subsonicBlue, subsonicMaple, subsonicZebra}},
		{"frequent", []string{subsonicZebra, subsonicMaple, subsonicBlue}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			requireSubsonicAlbumOrder(t, f, tc.want, "type", tc.kind)
			requireSubsonicAlbumOrder(t, f, tc.want[1:2], "type", tc.kind, "size", "1", "offset", "1")
			requireSubsonicAlbumOrder(t, f, nil, "type", tc.kind, "size", "1", "offset", "3")
			params := f.params("json", "type", tc.kind)
			params.Set("u", "subsonic-other-listener")
			otherPage := requestSubsonic(f.app, http.MethodGet, "getAlbumList2", params)
			list := subsonicBrowseJSON[protocol.AlbumList](t, otherPage, "albumList2")
			if len(list.Albums) != 1 || list.Albums[0].ID != f.albums[subsonicCue].group {
				t.Errorf("另一账号的 %s 列表不符：%s", tc.kind, otherPage.Body)
			}
		})
	}
	t.Run("UnavailableBeforeOffset", func(t *testing.T) {
		mustExec(t, f.app, `UPDATE tracks SET source_status='missing' WHERE id=$1`, f.tracks["Blue Song"])
		for _, kind := range []string{"starred", "recent"} {
			requireSubsonicAlbumOrder(t, f, []string{subsonicZebra}, "type", kind, "size", "1", "offset", "1")
		}
	})
}

func TestSubsonicBrowseSearchPagination(t *testing.T) {
	f := newSubsonicBrowseFixture(t)
	t.Run("Preparing", func(t *testing.T) {
		r := requestSubsonic(f.app, http.MethodGet, "search3", f.params("json", "query", "Maple"))
		got := subsonicBrowseJSON[protocol.SearchResult](t, r, "searchResult3")
		if got.Artists == nil || len(got.Artists) != 0 || len(got.Albums) != 1 || len(got.Songs) != 1 ||
			got.Albums[0].ID != f.albums[subsonicMaple].group || got.Songs[0].ID != f.tracks["Maple Song"] {
			t.Fatalf("艺人索引未就绪时三类搜索结果不符：%s", r.Body)
		}
	})
	f.publish(t)

	t.Run("IndependentOffsets", func(t *testing.T) {
		search := func(fields ...string) protocol.SearchResult {
			t.Helper()
			return subsonicBrowseResult[protocol.SearchResult](t, f, "json", "search3", "searchResult3", append([]string{"query", ""}, fields...)...)
		}
		all := search()
		if len(all.Artists) != 6 || len(all.Albums) != 4 || len(all.Songs) != 7 {
			t.Fatalf("空查询三类数量不符：艺人 %d、专辑 %d、曲目 %d", len(all.Artists), len(all.Albums), len(all.Songs))
		}
		page := search("artistCount", "2", "artistOffset", "1", "albumCount", "1", "albumOffset", "2", "songCount", "3", "songOffset", "4")
		if !reflect.DeepEqual(page.Artists, all.Artists[1:3]) || !reflect.DeepEqual(page.Albums, all.Albums[2:3]) ||
			!reflect.DeepEqual(page.Songs, all.Songs[4:7]) {
			t.Errorf("三栏独立分页不符：%+v", page)
		}
		empty := search("artistCount", "0", "albumOffset", "50", "songOffset", "50")
		if empty.Artists == nil || empty.Albums == nil || empty.Songs == nil ||
			len(empty.Artists)+len(empty.Albums)+len(empty.Songs) != 0 {
			t.Errorf("零条数与超范围偏移应编码为空数组：%+v", empty)
		}
	})
	t.Run("IndexedMatches", func(t *testing.T) {
		all := subsonicBrowseResult[protocol.SearchResult](t, f, "json", "search3", "searchResult3", "query", "Record")
		if len(all.Albums) != 3 || all.Albums[0].ID != f.albums[subsonicBlue].group ||
			all.Albums[1].ID != f.albums[subsonicMaple].group || all.Albums[2].ID != f.albums[subsonicZebra].group {
			t.Fatalf("文本匹配的专辑顺序不符：%+v", all.Albums)
		}
		page := subsonicBrowseResult[protocol.SearchResult](t, f, "json", "search3", "searchResult3",
			"query", "Record", "albumOffset", "1", "albumCount", "1", "artistCount", "0", "songCount", "0")
		if len(page.Albums) != 1 || page.Albums[0].ID != f.albums[subsonicMaple].group ||
			len(page.Artists) != 0 || len(page.Songs) != 0 {
			t.Errorf("文本查询的独立偏移不符：%+v", page)
		}
		artist := subsonicBrowseResult[protocol.SearchResult](t, f, "json", "search3", "searchResult3", "query", "Beacon")
		if len(artist.Artists) != 1 || artist.Artists[0].ID != f.artists["Beacon Soloist"] {
			t.Errorf("艺人搜索不符：%+v", artist.Artists)
		}
		song := subsonicBrowseResult[protocol.SearchResult](t, f, "json", "search3", "searchResult3", "query", "Duet")
		if len(song.Songs) != 1 || song.Songs[0].ID != f.tracks["Duet & Guests"] {
			t.Errorf("曲目搜索不符：%+v", song.Songs)
		}
	})
	t.Run("InvalidParameters", func(t *testing.T) {
		for _, fields := range [][]string{
			{}, {"query", "Maple", "artistCount", "-1"}, {"query", "", "songOffset", "broken"},
		} {
			r := requestSubsonic(f.app, http.MethodGet, "search3", f.params("json", fields...))
			requireSubsonicFailure(t, r, http.StatusOK, 10)
		}
	})
}

func TestSubsonicBrowseRandomTracks(t *testing.T) {
	f := newSubsonicBrowseFixture(t)
	mustExec(t, f.app, `UPDATE tracks SET source_status='missing' WHERE id=$1`, f.tracks["Finale"])
	for _, tc := range []struct {
		name   string
		params []string
		want   []string
	}{
		{"Genre", []string{"genre", "Rock"}, []string{"Overture", "Duet & Guests", "Maple Song"}},
		{"LowerYear", []string{"genre", "Rock", "fromYear", "1999"}, []string{"Maple Song"}},
		{"UpperYear", []string{"genre", "Rock", "toYear", "1998"}, []string{"Overture", "Duet & Guests"}},
		{"InclusiveRange", []string{"fromYear", "2001", "toYear", "2005"}, []string{"Blue Song", "Cue One", "Cue Two"}},
		{"NoMatch", []string{"genre", "No Such Genre"}, []string{}},
		{"ZeroSize", []string{"size", "0"}, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := subsonicBrowseResult[protocol.RandomSongs](t, f, "json", "getRandomSongs", "randomSongs", tc.params...)
			var ids, want []string
			for _, song := range result.Songs {
				ids = append(ids, song.ID)
			}
			for _, name := range tc.want {
				want = append(want, f.tracks[name])
			}
			slices.Sort(ids)
			slices.Sort(want)
			if result.Songs == nil || !slices.Equal(ids, want) {
				t.Errorf("随机曲目筛选结果 = %v，期望 %v", ids, want)
			}
		})
	}
	t.Run("StableSeed", func(t *testing.T) {
		store := catalog.NewStore(f.app.database)
		from, to := 1998, 2010
		params := catalog.RandomTrackParams{Limit: 3, Seed: 314159, Genre: "Rock", FromYear: &from, ToYear: &to}
		all, err := store.RandomTracks(t.Context(), params)
		if err != nil || len(all) != 3 {
			t.Fatalf("同种子第一轮结果数 = %d，期望 3：%v", len(all), err)
		}
		again, err := store.RandomTracks(t.Context(), params)
		if err != nil || !reflect.DeepEqual(all, again) {
			t.Errorf("相同种子、条件与上限的结果顺序发生变化：%v", err)
		}
		params.Limit = 2
		page, err := store.RandomTracks(t.Context(), params)
		if err != nil || !reflect.DeepEqual(all[:2], page) {
			t.Errorf("缩小上限后的同种子结果应为原序列前两首：%v", err)
		}
	})
	t.Run("InvalidParameters", func(t *testing.T) {
		for _, fields := range [][]string{{"size", "x"}, {"fromYear", "-1"}, {"toYear", "10000"}} {
			r := requestSubsonic(f.app, http.MethodGet, "getRandomSongs", f.params("json", fields...))
			requireSubsonicFailure(t, r, http.StatusOK, 10)
		}
	})
}
