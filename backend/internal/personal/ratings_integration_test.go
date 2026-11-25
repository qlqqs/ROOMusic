package personal

import (
	"reflect"
	"testing"
	"time"
)

func TestPostgreSQLRatingHighestRatedAlbums(t *testing.T) {
	f := newPersonalFixture(t)
	listener, other := f.user(t), f.user(t)
	olderHigh := f.release(t, "较早评的五星专辑").groupID
	newerHigh := f.release(t, "较新评的五星专辑").groupID
	low := f.release(t, "最新评的三星专辑").groupID
	otherOnly := f.release(t, "只有另一位用户评分的专辑").groupID
	f.release(t, "无人评分的专辑")

	earliest := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	old := RatedAlbum{GroupID: olderHigh, Rating: 5, RatedAt: earliest}
	newer := RatedAlbum{GroupID: newerHigh, Rating: 5, RatedAt: earliest.Add(time.Hour)}
	latest := RatedAlbum{GroupID: low, Rating: 3, RatedAt: earliest.Add(2 * time.Hour)}
	private := RatedAlbum{GroupID: otherOnly, Rating: 5, RatedAt: earliest.Add(3 * time.Hour)}
	otherRating := RatedAlbum{GroupID: olderHigh, Rating: 2, RatedAt: earliest.Add(4 * time.Hour)}
	set := func(user string, rating RatedAlbum) {
		t.Helper()
		if _, err := f.service.SetRating(t.Context(), user, "album", rating.GroupID, rating.Rating); err != nil {
			t.Fatalf("设置专辑评分 %s：%v", rating.GroupID, err)
		}
		f.exec(t, `UPDATE rating_albums SET rated_at=$3 WHERE user_id=$1 AND group_id=$2`, user, rating.GroupID, rating.RatedAt)
	}
	for _, rating := range []RatedAlbum{latest, newer, old} {
		set(listener, rating)
	}
	set(other, private)
	set(other, otherRating)

	for _, tc := range []struct {
		name, user   string
		offset, size int
		want         []RatedAlbum
	}{
		{"full", listener, 0, 10, []RatedAlbum{newer, old, latest}},
		{"first-page", listener, 0, 2, []RatedAlbum{newer, old}},
		{"last-page", listener, 2, 2, []RatedAlbum{latest}},
		{"past-end", listener, 3, 2, []RatedAlbum{}},
		{"other-user", other, 0, 10, []RatedAlbum{private, otherRating}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.service.HighestRatedAlbums(t.Context(), tc.user, tc.offset, tc.size)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("最高评分查询得到 %+v，错误 %v，期望 %+v", got, err, tc.want)
			}
		})
	}
}

func TestPostgreSQLRatingArtistMergeAndSplit(t *testing.T) {
	f := newPersonalFixture(t)
	user := f.user(t)
	a, b := f.artist(t, "甲"), f.artist(t, "乙")
	f.artistsReady(t)
	set := func(id string, rating int) RatingResult {
		t.Helper()
		result, err := f.service.SetRating(t.Context(), user, "artist", id, rating)
		if err != nil {
			t.Fatalf("设置艺人 %s 的评分：%v", id, err)
		}
		return result
	}
	check := func(resolvedA string, ratingA, ratingB int) {
		t.Helper()
		got, err := f.service.LookupRatings(t.Context(), user, nil, nil, []string{a, b})
		want := []ArtistRating{
			{ArtistID: a, ResolvedArtistID: &resolvedA, Rating: &ratingA},
			{ArtistID: b, ResolvedArtistID: &b, Rating: &ratingB},
		}
		if err != nil || !reflect.DeepEqual(got.Artists, want) {
			t.Fatalf("艺人评分查询得到 %+v，错误 %v，期望解析为 %s/%s、评分为 %d/%d", got.Artists, err, resolvedA, b, ratingA, ratingB)
		}
	}

	set(a, 2)
	set(b, 5)
	first := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	f.exec(t, `UPDATE rating_artists SET rated_at=$3 WHERE user_id=$1 AND artist_id=$2`, user, a, first.Add(time.Hour))
	f.exec(t, `UPDATE rating_artists SET rated_at=$3 WHERE user_id=$1 AND artist_id=$2`, user, b, first)
	check(a, 2, 5)

	f.artistRoute(t, a, b)
	check(b, 2, 2)
	result := set(a, 4)
	if result.ResolvedArtistID == nil || *result.ResolvedArtistID != b {
		t.Fatalf("经原锚点修改合并艺人时未解析到 %s", b)
	}
	check(b, 4, 4)

	f.artistRoute(t, a, a)
	check(a, 4, 4)
	if revision := f.count(t, `SELECT revision FROM personal_favorite_state WHERE user_id=$1`, user); revision != 0 {
		t.Fatalf("只有艺人评分的用户收藏版本变成 %d，期望 0", revision)
	}
}
