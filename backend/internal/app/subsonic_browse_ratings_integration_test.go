package app

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
)

func putSubsonicBrowseRating(t *testing.T, f *subsonicBrowseFixture, user *integrationTestUser, kind, id string, rating int) {
	t.Helper()
	response := f.app.request(t, http.MethodPut, ratingsURL+"/"+kind+"/"+id, map[string]int{"rating": rating}, user,
		map[string]string{"X-Request-ID": sampleRequestID})
	body := personalBody(t, requireRatingHTTP(t, response, http.StatusOK))
	if body["target_id"] != id || body["rating"] != float64(rating) {
		t.Fatalf("网页评分写入不符：%s", response.Body)
	}
}

func readSubsonicRatingResponse(t *testing.T, f *subsonicBrowseFixture, username, format, endpoint string, fields ...string) (protocol.Response, int) {
	t.Helper()
	params := f.params(format, fields...)
	params.Set("u", username)
	response := requestSubsonic(f.app, http.MethodGet, endpoint, params)
	var envelope protocol.Response
	switch format {
	case "json":
		requireSubsonicOK(t, response)
		var doc struct {
			Response protocol.Response `json:"subsonic-response"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		return doc.Response, bytes.Count(response.Body.Bytes(), []byte(`"userRating"`))
	case "xml":
		requireSubsonicHTTP(t, response, http.StatusOK, "application/xml; charset=utf-8")
		if err := xml.Unmarshal(response.Body.Bytes(), &envelope); err != nil || envelope.Status != "ok" || envelope.Error != nil {
			t.Fatalf("XML 响应失败：%s （%v）", response.Body, err)
		}
		return envelope, bytes.Count(response.Body.Bytes(), []byte(` userRating="`))
	default:
		t.Fatalf("未知测试格式 %q", format)
		return protocol.Response{}, 0
	}
}

func requireSubsonicEntityRating(t *testing.T, id string, rating *int, want int) {
	t.Helper()
	if want == 0 {
		if rating != nil {
			t.Errorf("实体 %s 未评分，响应却带 %d 星", id, *rating)
		}
	} else if rating == nil {
		t.Errorf("实体 %s 缺少 userRating，期望 %d", id, want)
	} else if *rating != want {
		t.Errorf("实体 %s 的 userRating = %d，期望 %d", id, *rating, want)
	}
}

func TestSubsonicBrowseRatings(t *testing.T) {
	f := newSubsonicBrowseFixture(t)
	f.publish(t)
	const listener = "subsonic-rating-listener"
	createSubsonicMember(t, f.app, &f.admin, listener)

	artistID := f.artists["Atlas Collective"]
	groupID := f.albums[subsonicZebra].group
	trackID := f.tracks["Overture"]

	type ratings struct {
		artists []protocol.Artist
		albums  []protocol.Album
		tracks  []protocol.Song
	}
	check := func(t *testing.T, username string, stars int) {
		t.Helper()
		for _, format := range []string{"json", "xml"} {
			t.Run(format, func(t *testing.T) {
				for _, tc := range []struct {
					endpoint     string
					params       []string
					pick         func(protocol.Response) ratings
					wantEntities int
				}{
					{"getSong", []string{"id", trackID}, func(p protocol.Response) ratings {
						if p.Song == nil {
							return ratings{}
						}
						return ratings{tracks: []protocol.Song{*p.Song}}
					}, 1},
					{"getAlbum", []string{"id", groupID}, func(p protocol.Response) ratings {
						if p.Album == nil {
							return ratings{}
						}
						return ratings{albums: []protocol.Album{*p.Album}, tracks: p.Album.Songs}
					}, 2},
					{"getArtist", []string{"id", artistID}, func(p protocol.Response) ratings {
						if p.Artist == nil {
							return ratings{}
						}
						return ratings{artists: []protocol.Artist{*p.Artist}, albums: p.Artist.Albums}
					}, 2},
					{"getArtists", nil, func(p protocol.Response) ratings {
						var artists []protocol.Artist
						if p.Artists != nil {
							for _, index := range p.Artists.Indexes {
								artists = append(artists, index.Artists...)
							}
						}
						return ratings{artists: artists}
					}, 1},
					{"getAlbumList2", []string{"type", "alphabeticalByName"}, func(p protocol.Response) ratings {
						if p.AlbumList2 == nil {
							return ratings{}
						}
						return ratings{albums: p.AlbumList2.Albums}
					}, 1},
					{"search3", []string{"query", "Atlas"}, func(p protocol.Response) ratings {
						if p.SearchResult3 == nil {
							return ratings{}
						}
						return ratings{artists: p.SearchResult3.Artists, albums: p.SearchResult3.Albums, tracks: p.SearchResult3.Songs}
					}, 3},
					{"getRandomSongs", []string{"genre", "Rock", "fromYear", "1998", "toYear", "1998", "size", "10"}, func(p protocol.Response) ratings {
						if p.RandomSongs == nil {
							return ratings{}
						}
						return ratings{tracks: p.RandomSongs.Songs}
					}, 1},
				} {
					t.Run(tc.endpoint, func(t *testing.T) {
						envelope, ratingFields := readSubsonicRatingResponse(t, f, username, format, tc.endpoint, tc.params...)
						got := tc.pick(envelope)
						found := 0
						checkEntity := func(id, ratedID string, rating *int) {
							want := 0
							if id == ratedID {
								found++
								want = stars
							}
							requireSubsonicEntityRating(t, id, rating, want)
						}
						for _, a := range got.artists {
							checkEntity(a.ID, artistID, a.UserRating)
						}
						for _, a := range got.albums {
							checkEntity(a.ID, groupID, a.UserRating)
						}
						for _, s := range got.tracks {
							checkEntity(s.ID, trackID, s.UserRating)
						}
						if found != tc.wantEntities {
							t.Errorf("响应包含 %d 个目标实体，期望 %d", found, tc.wantEntities)
						}
						wantFields := 0
						if stars > 0 {
							wantFields = tc.wantEntities
						}
						if ratingFields != wantFields {
							t.Errorf("实际编码 %d 个 userRating 字段，期望 %d", ratingFields, wantFields)
						}
					})
				}
			})
		}
	}

	t.Run("BeforeRating", func(t *testing.T) { check(t, subsonicAdmin, 0) })
	putSubsonicBrowseRating(t, f, &f.admin, "tracks", trackID, 4)
	putSubsonicBrowseRating(t, f, &f.admin, "albums", groupID, 4)
	putSubsonicBrowseRating(t, f, &f.admin, "artists", artistID, 4)
	t.Run("Owner", func(t *testing.T) { check(t, subsonicAdmin, 4) })
	t.Run("OtherUser", func(t *testing.T) { check(t, listener, 0) })
}

func TestSubsonicBrowseMergedArtistRating(t *testing.T) {
	f := newSubsonicBrowseFixture(t)
	f.publish(t)
	target, source := f.artists["Atlas Collective"], f.artists["Cedar Band"]

	putSubsonicBrowseRating(t, f, &f.admin, "artists", target, 5)
	putSubsonicBrowseRating(t, f, &f.admin, "artists", source, 3)
	first := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	mustExec(t, f.app, `UPDATE rating_artists SET rated_at=$3 WHERE user_id=$1 AND artist_id=$2`, f.admin.id, target, first)
	mustExec(t, f.app, `UPDATE rating_artists SET rated_at=$3 WHERE user_id=$1 AND artist_id=$2`, f.admin.id, source, first.Add(time.Hour))

	merged := artistMediaCommand(t, f.app, &f.admin, http.MethodPost, target, "/merge", uuid.New(), map[string]any{
		"expected_revision":        readArtistMedia(t, f.app, &f.admin, target).Revision,
		"source_artist_id":         source,
		"source_expected_revision": readArtistMedia(t, f.app, &f.admin, source).Revision,
	})
	if merged.Code != http.StatusOK {
		t.Fatalf("通过网页合并艺人：HTTP %d %s", merged.Code, merged.Body)
	}
	for _, format := range []string{"json", "xml"} {
		t.Run(format, func(t *testing.T) {
			for _, id := range []string{target, source} {
				p, ratingFields := readSubsonicRatingResponse(t, f, subsonicAdmin, format, "getArtist", "id", id)
				if p.Artist == nil || p.Artist.ID != target || ratingFields != 1 {
					t.Fatalf("艺人 %s 应解析至 %s，且响应应包含一个评分：%+v", id, target, p.Artist)
				}
				requireSubsonicEntityRating(t, p.Artist.ID, p.Artist.UserRating, 3)
			}
		})
	}
}

func TestSubsonicBrowseHighestRatedAlbums(t *testing.T) {
	f := newSubsonicBrowseFixture(t)
	const listener = "subsonic-highest-listener"
	otherID := createSubsonicMember(t, f.app, &f.admin, listener)
	other := loginPasswordUser(t, f.app, listener, subsonicPassword, "user")
	if other.id != otherID {
		t.Fatal("另一账号的网页登录身份不符")
	}

	type ratedAlbum struct {
		id    string
		stars int
	}
	zebra := ratedAlbum{f.albums[subsonicZebra].group, 5}
	maple := ratedAlbum{f.albums[subsonicMaple].group, 5}
	cue := ratedAlbum{f.albums[subsonicCue].group, 4}
	blue := ratedAlbum{f.albums[subsonicBlue].group, 5}
	check := func(t *testing.T, username, format string, offset, size int, want ...ratedAlbum) {
		t.Helper()
		p, ratingFields := readSubsonicRatingResponse(t, f, username, format, "getAlbumList2",
			"type", "highest", "offset", strconv.Itoa(offset), "size", strconv.Itoa(size))
		if p.AlbumList2 == nil {
			t.Fatal("响应缺少 albumList2 对象")
		}
		if len(p.AlbumList2.Albums) != len(want) || (format == "json" && p.AlbumList2.Albums == nil) {
			t.Fatalf("highest 的 offset=%d size=%d 返回 %+v，期望 %v", offset, size, p.AlbumList2.Albums, want)
		}
		if ratingFields != len(want) {
			t.Errorf("实际编码 %d 个 userRating 字段，期望 %d", ratingFields, len(want))
		}
		for i, album := range p.AlbumList2.Albums {
			if album.ID != want[i].id {
				t.Errorf("第 %d 张专辑 ID = %s，期望 %s", i+1, album.ID, want[i].id)
			}
			requireSubsonicEntityRating(t, album.ID, album.UserRating, want[i].stars)
		}
	}
	t.Run("UnratedLibrary", func(t *testing.T) {
		for _, format := range []string{"json", "xml"} {
			check(t, subsonicAdmin, format, 0, 10)
		}
	})

	base := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	for i, album := range []ratedAlbum{zebra, maple, blue, cue} {
		putSubsonicBrowseRating(t, f, &f.admin, "albums", album.id, album.stars)
		mustExec(t, f.app, `UPDATE rating_albums SET rated_at=$3 WHERE user_id=$1 AND group_id=$2`,
			f.admin.id, album.id, base.Add(time.Duration(i)*time.Hour))
	}
	// 在同分两张专辑之间插入 105 个已失去可见版本的组，迫使 offset=1、size=2 跨过多个评分批次。
	mustExec(t, f.app, `WITH groups AS (
 INSERT INTO release_groups(id) SELECT gen_random_uuid() FROM generate_series(1,105) RETURNING id
 )
 INSERT INTO rating_albums(user_id,group_id,rating,rated_at)
 SELECT $1,id,5,$2 FROM groups`, f.admin.id, base.Add(30*time.Minute))
	mustExec(t, f.app, `UPDATE tracks SET source_status='missing' WHERE id=$1`, f.tracks["Blue Song"])

	otherCue := ratedAlbum{cue.id, 2}
	putSubsonicBrowseRating(t, f, &other, "albums", otherCue.id, otherCue.stars)
	for _, format := range []string{"json", "xml"} {
		t.Run(format, func(t *testing.T) {
			t.Run("Order", func(t *testing.T) { check(t, subsonicAdmin, format, 0, 3, maple, zebra, cue) })
			t.Run("CrossBatchOffset", func(t *testing.T) { check(t, subsonicAdmin, format, 1, 2, zebra, cue) })
			t.Run("LastPage", func(t *testing.T) { check(t, subsonicAdmin, format, 2, 2, cue) })
			t.Run("OutsidePage", func(t *testing.T) { check(t, subsonicAdmin, format, 3, 2) })
			t.Run("ZeroSize", func(t *testing.T) { check(t, subsonicAdmin, format, 0, 0) })
			t.Run("OtherUser", func(t *testing.T) { check(t, listener, format, 0, 10, otherCue) })
		})
	}
}
