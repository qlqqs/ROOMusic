package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

const ratingsURL = personalBase + "/ratings"

func TestPostgreSQLRatingWebAndSubsonic(t *testing.T) {
	app := newSubsonicTestApplication(t)
	const username = "rating-listener"
	f := newPersonalFixtureForUser(t, app, seedSubsonicMember(t, app, username), 2)
	artist, unratedArtist := uuid.New(), uuid.New()
	mustExec(t, app, `INSERT INTO artists(id,initial_name) VALUES($1,'评分艺人'),($2,'未评分艺人')`, artist, unratedArtist)
	mustExec(t, app, `UPDATE catalog_projection_state SET status='ready',manifest_sealed=true,published_generation=target_generation,
		published_textnorm_version=textnorm_version,published_search_projection_version=search_projection_version WHERE capability='artists'`)
	service := personal.NewService(app.database)
	for _, tc := range []struct {
		kind, collection, input, key, rated, unrated string
	}{
		{"track", "tracks", "track_ids", "track_id", f.tracks[0], f.tracks[1]},
		{"album", "albums", "group_ids", "group_id", f.groups[0], f.groups[1]},
		{"artist", "artists", "artist_ids", "artist_id", artist, unratedArtist},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			path := ratingsURL + "/" + tc.collection + "/" + tc.rated
			webLookup := func(want any) {
				t.Helper()
				input := map[string]any{tc.input: []string{tc.unrated, strings.ToUpper(tc.rated), tc.rated, tc.unrated}}
				got := personalBody(t, requireRatingHTTP(t, f.call(http.MethodPost, ratingsURL+"/lookup", input), 200))
				expected := map[string]any{"tracks": []any{}, "albums": []any{}, "artists": []any{}}
				unrated := map[string]any{tc.key: tc.unrated, "rating": nil}
				rated := map[string]any{tc.key: tc.rated, "rating": want}
				if tc.kind == "artist" {
					unrated["resolved_artist_id"], rated["resolved_artist_id"] = tc.unrated, tc.rated
				}
				expected[tc.collection] = []any{unrated, rated}
				if !reflect.DeepEqual(got, expected) {
					t.Fatalf("网页查询得到 %v，期望 %v", got, expected)
				}
			}
			setSubsonic := func(rating string) {
				t.Helper()
				params := subsonicParameters(username, subsonicPassword, "plain")
				params.Set("id", tc.rated)
				params.Set("rating", rating)
				requireSubsonicOK(t, requestSubsonic(app, http.MethodPost, "setRating.view", params))
			}
			checkWebMutation := func(response *httptest.ResponseRecorder, want any) {
				t.Helper()
				body := personalBody(t, requireRatingHTTP(t, response, 200))
				expected := map[string]any{"target_kind": tc.kind, "target_id": tc.rated, "rating": want, "rated_at": nil}
				if tc.kind == "artist" {
					expected["resolved_artist_id"] = tc.rated
				}
				if want != nil {
					raw, _ := body["rated_at"].(string)
					if _, err := time.Parse(time.RFC3339Nano, raw); err != nil || !strings.HasSuffix(raw, "Z") {
						t.Fatalf("rated_at 应为 UTC RFC3339 时间，得到 %q", raw)
					}
					expected["rated_at"] = raw
				}
				if !reflect.DeepEqual(body, expected) {
					t.Fatalf("网页写入得到 %v，期望 %v", body, expected)
				}
			}
			serviceLookup := func(want *int) {
				t.Helper()
				ids := map[string][]string{tc.input: {tc.rated}}
				got, err := service.LookupRatings(t.Context(), f.user.id, ids["track_ids"], ids["group_ids"], ids["artist_ids"])
				if err != nil {
					t.Fatal(err)
				}
				expected := personal.RatingLookup{Tracks: []personal.TrackRating{}, Albums: []personal.AlbumRating{}, Artists: []personal.ArtistRating{}}
				switch tc.kind {
				case "track":
					expected.Tracks = []personal.TrackRating{{TrackID: tc.rated, Rating: want}}
				case "album":
					expected.Albums = []personal.AlbumRating{{GroupID: tc.rated, Rating: want}}
				case "artist":
					expected.Artists = []personal.ArtistRating{{ArtistID: tc.rated, ResolvedArtistID: &tc.rated, Rating: want}}
				}
				if !reflect.DeepEqual(got, expected) {
					t.Fatalf("网页写入后评分服务查询不符，得到 %+v，期望 %+v", got, expected)
				}
			}

			webLookup(nil)
			checkWebMutation(f.call(http.MethodPut, path, map[string]int{"rating": 5}), float64(5))
			webLookup(float64(5))
			setSubsonic("4")
			webLookup(float64(4))
			checkWebMutation(f.call(http.MethodPut, path, map[string]int{"rating": 2}), float64(2))
			two := 2
			serviceLookup(&two)
			checkWebMutation(f.call(http.MethodDelete, path, nil), nil)
			serviceLookup(nil)
			setSubsonic("4")
			setSubsonic("0")
			webLookup(nil)
		})
	}
}

func TestPostgreSQLRatingSubsonicRejectsWithoutWriting(t *testing.T) {
	app := newSubsonicTestApplication(t)
	const username = "rating-listener"
	f := newPersonalFixtureForUser(t, app, seedSubsonicMember(t, app, username), 1)
	track := f.tracks[0]
	requireRatingHTTP(t, f.call(http.MethodPut, ratingsURL+"/tracks/"+track, map[string]int{"rating": 3}), 200)
	snapshot := func() string {
		t.Helper()
		var state string
		err := app.database.QueryRow(`SELECT jsonb_build_object(
			'tracks',(SELECT jsonb_agg(to_jsonb(r) ORDER BY user_id,track_id) FROM rating_tracks r),
			'albums',(SELECT jsonb_agg(to_jsonb(r) ORDER BY user_id,group_id) FROM rating_albums r),
			'artists',(SELECT jsonb_agg(to_jsonb(r) ORDER BY user_id,artist_id) FROM rating_artists r),
			'revision',(SELECT revision FROM personal_favorite_state WHERE user_id=$1))::text`, f.user.id).Scan(&state)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	before := snapshot()
	unknown := uuid.New()
	for _, tc := range []struct {
		name, id, rating string
		code             int
	}{
		{"missing-id", "", "4", 10},
		{"missing-rating", track, "", 10},
		{"non-integer", track, "abc", 10},
		{"fraction", track, "2.5", 10},
		{"above-range", track, "6", 0},
		{"below-range", track, "-1", 0},
		{"unknown-target", unknown, "4", 70},
		{"clear-unknown-target", unknown, "0", 70},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := subsonicParameters(username, subsonicPassword, "plain")
			if tc.id != "" {
				params.Set("id", tc.id)
			}
			if tc.rating != "" {
				params.Set("rating", tc.rating)
			}
			requireSubsonicFailure(t, requestSubsonic(app, http.MethodGet, "setRating.view", params), http.StatusOK, tc.code)
			if after := snapshot(); after != before {
				t.Fatalf("失败请求改变了评分或收藏版本，写入前 %s，写入后 %s", before, after)
			}
		})
	}
}

func TestPostgreSQLRatingWebValidation(t *testing.T) {
	f := newPersonalFixture(t, 1)
	trackURL := ratingsURL + "/tracks/" + f.tracks[0]
	lookupURL := ratingsURL + "/lookup"
	artist := uuid.New()
	mustExec(t, f.app, `INSERT INTO artists(id,initial_name) VALUES($1,'待整理艺人')`, artist)
	mustExec(t, f.app, `UPDATE catalog_projection_state SET status='preparing',manifest_sealed=false WHERE capability='artists'`)

	for _, route := range []struct{ method, path, body string }{
		{http.MethodPut, trackURL, `{"rating":4}`},
		{http.MethodDelete, trackURL, ""},
		{http.MethodPost, lookupURL, `{}`},
	} {
		t.Run(route.method, func(t *testing.T) {
			requireAPIError(t, requireRatingHTTP(t, personalRaw(f.app, route.method, route.path, route.body, nil, nil), 401), 401, "unauthorized")
			badOrigin := map[string]string{"Origin": "https://other.example"}
			requireAPIError(t, requireRatingHTTP(t, personalRaw(f.app, route.method, route.path, route.body, &f.user, badOrigin), 403), 403, "origin_forbidden")
		})
	}
	for _, tc := range []struct {
		name, method, path, body, code string
		status                         int
	}{
		{"invalid-json", http.MethodPut, trackURL, `{"rating":`, "invalid_json", 400},
		{"missing-rating", http.MethodPut, trackURL, `{}`, "invalid_input", 400},
		{"null-rating", http.MethodPut, trackURL, `{"rating":null}`, "invalid_input", 400},
		{"fraction", http.MethodPut, trackURL, `{"rating":3.5}`, "invalid_input", 400},
		{"zero", http.MethodPut, trackURL, `{"rating":0}`, "invalid_input", 400},
		{"above-range", http.MethodPut, trackURL, `{"rating":6}`, "invalid_input", 400},
		{"duplicate-field", http.MethodPut, trackURL, `{"rating":2,"rating":3}`, "invalid_input", 400},
		{"delete-with-rating", http.MethodDelete, trackURL, `{"rating":3}`, "invalid_input", 400},
		{"missing-target", http.MethodPut, ratingsURL + "/tracks/" + uuid.New(), `{"rating":4}`, "not_found", 404},
		{"artist-write-preparing", http.MethodPut, ratingsURL + "/artists/" + artist, `{"rating":4}`, "artist_catalog_not_ready", 409},
		{"artist-lookup-preparing", http.MethodPost, lookupURL, fmt.Sprintf(`{"artist_ids":[%q]}`, artist), "artist_catalog_preparing", 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := personalRaw(f.app, tc.method, tc.path, tc.body, &f.user, nil)
			requireAPIError(t, requireRatingHTTP(t, response, tc.status), tc.status, tc.code)
			if tc.status == http.StatusServiceUnavailable && response.Header().Get("Retry-After") != "3" {
				t.Fatalf("艺人目录准备态缺少 Retry-After，响应头 %v", response.Header())
			}
		})
	}

	for _, tc := range []struct {
		field string
		limit int
		id    string
	}{
		{"track_ids", 10000, f.tracks[0]},
		{"group_ids", 1000, f.groups[0]},
		{"artist_ids", 1000, artist},
	} {
		t.Run(tc.field+"-limit", func(t *testing.T) {
			input := func(n int) map[string][]string {
				return map[string][]string{tc.field: slices.Repeat([]string{tc.id}, n)}
			}
			response := f.call(http.MethodPost, lookupURL, input(tc.limit+1))
			requireAPIError(t, requireRatingHTTP(t, response, 400), 400, "invalid_input")
			if tc.field == "artist_ids" {
				response = f.call(http.MethodPost, lookupURL, input(tc.limit))
				requireAPIError(t, requireRatingHTTP(t, response, 503), 503, "artist_catalog_preparing")
			} else {
				requireRatingHTTP(t, f.call(http.MethodPost, lookupURL, input(tc.limit)), 200)
			}
		})
	}

	if got := countRows(t, f.app, `SELECT (SELECT count(*) FROM rating_tracks) + (SELECT count(*) FROM rating_albums) + (SELECT count(*) FROM rating_artists)`); got != 0 {
		t.Fatalf("网页拒绝的请求留下了 %d 条评分", got)
	}
}

func TestPostgreSQLRatingKeepsFavoritePaginationRevision(t *testing.T) {
	f := newPersonalFixture(t, 2)
	for _, id := range f.tracks {
		personalOK(t, f.call(http.MethodPut, favoritesURL+"/tracks/"+id, nil), 200)
	}
	page := func(query string) personal.FavoriteTracksPage {
		t.Helper()
		response := personalOK(t, f.call(http.MethodGet, favoritesURL+"/tracks"+query, nil), 200)
		var got personal.FavoriteTracksPage
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Items) != 1 || got.Pagination.Total != 2 {
			t.Fatalf("收藏分页错误，响应 %s", response.Body)
		}
		return got
	}
	first := page("?page=1&page_size=1")
	path := ratingsURL + "/tracks/" + f.tracks[0]
	requireRatingHTTP(t, f.call(http.MethodPut, path, map[string]int{"rating": 4}), 200)
	second := page(fmt.Sprintf("?page=2&page_size=1&revision=%d", first.Revision))
	gotIDs := []string{first.Items[0].Track.TrackID, second.Items[0].Track.TrackID}
	if second.Revision != first.Revision || !slices.Equal(slices.Sorted(slices.Values(gotIDs)), slices.Sorted(slices.Values(f.tracks))) {
		t.Fatalf("评分后收藏翻页错误，第一页 %+v，第二页 %+v", first, second)
	}
}

func requireRatingHTTP(t *testing.T, response *httptest.ResponseRecorder, status int) *httptest.ResponseRecorder {
	t.Helper()
	response = personalOK(t, response, status)
	if got := response.Header().Get("X-Request-ID"); got != sampleRequestID {
		t.Fatalf("评分响应 X-Request-ID 为 %q，期望 %q", got, sampleRequestID)
	}
	return response
}
