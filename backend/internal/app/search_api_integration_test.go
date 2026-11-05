package app

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLSearchAPIAuthenticationAndParameters(t *testing.T) {
	app := newIntegrationTestApplication(t)
	user := app.createUser(t, "user")
	for _, path := range []string{"/api/v1/catalog/status", searchURL("tracks", "爱"), "/api/v1/search/suggestions?q=x"} {
		requireAPIError(t, app.request(t, http.MethodGet, path, nil, nil, nil), 401, "unauthorized")
	}
	for _, mode := range []string{"legacy", "normalized"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "normalized" {
				publishSearch(t, app)
			}
			for _, prefix := range []string{"/api/v1/search?type=tracks&q=", "/api/v1/search/suggestions?q="} {
				for _, query := range []string{strings.Repeat("a", 201), strings.Repeat("爱", 67), string([]byte{0xff}), "a\x00b", "a\x1fb", "a\x7fb"} {
					requireAPIError(t, app.request(t, http.MethodGet, prefix+url.QueryEscape(query), nil, &user, nil), 400, "invalid_query")
				}
				for _, query := range []string{strings.Repeat("a", 200), strings.Repeat("爱", 66) + "aa", "  " + strings.Repeat("a", 200) + "  ", "爱", "\t \n\r\u00a0", "a\tb"} {
					searchResponse(t, app, &user, prefix+url.QueryEscape(query))
				}
			}
			for _, tc := range []struct{ path, code string }{
				{"/api/v1/search?q=x", "invalid_search_type"},
				{"/api/v1/search?type=unknown&q=x", "invalid_search_type"},
				{"/api/v1/search?type=artist&q=x", "invalid_search_type"},
				{"/api/v1/search?type=tracks&page=0", "invalid_pagination"},
				{"/api/v1/search?type=tracks&page=-1", "invalid_pagination"},
				{"/api/v1/search?type=tracks&page=9223372036854775807&page_size=100", "invalid_pagination"},
				{"/api/v1/search?type=tracks&page_size=0", "invalid_pagination"},
				{"/api/v1/search?type=tracks&page_size=101", "invalid_pagination"},
				{"/api/v1/search?type=tracks&page_size=no", "invalid_pagination"},
				{"/api/v1/search/suggestions?q=x&limit=0", "invalid_limit"},
				{"/api/v1/search/suggestions?q=x&limit=9", "invalid_limit"},
				{"/api/v1/search/suggestions?q=x&limit=no", "invalid_limit"},
			} {
				requireAPIError(t, app.request(t, http.MethodGet, tc.path, nil, &user, nil), 400, tc.code)
			}
			availableTypes := []string{"albums", "tracks"}
			if mode == "legacy" {
				requireAPIError(t, app.request(t, http.MethodGet, searchURL("artists", "x"), nil, &user, nil), 503, "artist_catalog_preparing")
			} else {
				availableTypes = []string{"artists", "albums", "tracks"}
				artists := searchRead[catalog.SearchArtistItem](t, app, &user, searchURL("artists", "x"))
				if artists.Type != "artists" || artists.Query != "x" || artists.CatalogState.ArtistsState != "ready" || artists.Items == nil || len(artists.Items) != 0 || artists.Pagination != (catalog.SearchPagination{Page: 1, PageSize: 50, TotalRelation: "eq"}) {
					t.Fatalf("就绪艺人搜索：%+v", artists)
				}
			}
			page := searchRead[catalog.SearchTrackItem](t, app, &user, "/api/v1/search?type=tracks&type=artists&q=%20%09&page=1&page=0&page_size=100&page_size=101")
			if page.Pagination != (catalog.SearchPagination{Page: 1, PageSize: 100, TotalRelation: "eq"}) || page.Query != "" || page.CatalogState.SearchMode != mode {
				t.Fatalf("空查询/首值策略：%+v", page)
			}
			response := searchResponse(t, app, &user, "/api/v1/search/suggestions?q=%20%09&limit=8&limit=0")
			var suggestions catalog.SearchSuggestions
			if err := json.Unmarshal(response.Body.Bytes(), &suggestions); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(suggestions.AvailableTypes, availableTypes) || suggestions.Artists.Items == nil || suggestions.Albums.Items == nil || suggestions.Tracks.Items == nil || len(suggestions.Artists.Items)+len(suggestions.Albums.Items)+len(suggestions.Tracks.Items) != 0 || suggestions.Artists.HasMore || suggestions.Albums.HasMore || suggestions.Tracks.HasMore {
				t.Fatalf("空建议：%s", response.Body.String())
			}
		})
	}
}

func TestPostgreSQLSearchNormalizationFieldsAndVisibility(t *testing.T) {
	app := newIntegrationTestApplication(t)
	user := app.createUser(t, "user")
	root := searchRoot(t, app)
	fixture := insertSearchRelease(t, app, root, groupedRelease{title: "ＡＢＣ 愛", artist: "Beyoncé", tracks: []string{"present", "present", "missing"}})
	mustExec(t, app, `UPDATE releases SET album_artist='さユり',label='Étiquette 唱片',catalog_number='ＳＲＣＬ－１２３４' WHERE id=$1::uuid`, fixture.releaseID)
	mustExec(t, app, `UPDATE tracks SET title='Ｓｕｍｍｅｒ',artist='曲目藝人',duration_seconds=183.5,source_kind='flac_vorbis_comment',codec='flac',bit_depth=24,sample_rate=96000,channels=2 WHERE id=$1::uuid`, fixture.trackIDs[0])
	mustExec(t, app, `UPDATE tracks SET title='无关曲目',artist='无关歌手' WHERE id=$1::uuid`, fixture.trackIDs[1])
	mustExec(t, app, `UPDATE tracks SET title='消失标题',artist='消失艺人' WHERE id=$1::uuid`, fixture.trackIDs[2])
	for _, credit := range []struct{ role, name string }{{"composer", "作曲周杰倫"}, {"conductor", "指揮さユり"}, {"performer", "演奏Beyoncé"}, {"producer", "製作ＡＢＣ"}} {
		mustExec(t, app, `INSERT INTO track_credits(track_id,role,name,position) VALUES($1::uuid,$2,$3,1)`, fixture.trackIDs[0], credit.role, credit.name)
	}
	mustExec(t, app, `INSERT INTO track_credits(track_id,role,name,position) VALUES($1::uuid,'composer','消失作曲者',1)`, fixture.trackIDs[2])
	hidden := insertSearchRelease(t, app, root, groupedRelease{title: "隐形发行", artist: "隐形艺人", tracks: []string{"missing"}})
	publishSearch(t, app)
	for _, tc := range []struct {
		query, field, role, original string
		own                          bool
	}{
		{"abc 爱", "release_title", "", "ＡＢＣ 愛", false},
		{"BEYONCE", "release_artist", "release_artist", "Beyoncé", false},
		{"サユリ", "album_artist", "album_artist", "さユり", false},
		{"etiquette 唱片", "label", "", "Étiquette 唱片", false},
		{"srcl 1234", "catalog_number", "", "ＳＲＣＬ－１２３４", false},
		{"srcl1234", "catalog_number", "", "ＳＲＣＬ－１２３４", false},
		{"summer", "track_title", "", "Ｓｕｍｍｅｒ", true},
		{"曲目艺人", "track_artist", "track_artist", "曲目藝人", true},
		{"作曲周杰伦", "credit", "composer", "作曲周杰倫", true},
		{"指挥サユリ", "credit", "conductor", "指揮さユり", true},
		{"演奏beyonce", "credit", "performer", "演奏Beyoncé", true},
		{"制作abc", "credit", "producer", "製作ＡＢＣ", true},
	} {
		t.Run(tc.query, func(t *testing.T) {
			want := fixture.trackIDs[:2]
			if tc.own {
				want = fixture.trackIDs[:1]
			}
			tracks := requireSearchTracks(t, app, &user, tc.query, want...)
			albums := requireSearchAlbums(t, app, &user, tc.query, fixture.groupID)
			matches := []catalog.SearchMatch{albums.Items[0].Match}
			for _, item := range tracks.Items {
				matches = append(matches, item.Match)
			}
			var artistID string
			if tc.role != "" {
				artistID = requireSearchArtistByName(t, app, &user, tc.original, []string{tc.role}).ID
			}
			for _, match := range matches {
				if match.Kind != "exact" || match.Field != tc.field || match.Original != tc.original || match.Alias != nil || match.ReleaseID == nil || *match.ReleaseID != fixture.releaseID {
					t.Fatalf("命中说明不保留原文/关系：%+v", match)
				}
				if artistID == "" {
					if match.ArtistID != nil || match.Role != nil {
						t.Fatalf("非署名命中不得带艺人身份或角色：%+v", match)
					}
				} else if match.ArtistID == nil || *match.ArtistID != artistID || match.Role == nil || *match.Role != tc.role {
					t.Fatalf("署名命中身份或角色不符：%+v，期望 %s/%s", match, artistID, tc.role)
				}
				if !tc.own && match.TrackID != nil {
					t.Fatalf("发行级命中不得伪造曲目来源：%+v", match)
				}
				if tc.own && (match.TrackID == nil || *match.TrackID != fixture.trackIDs[0]) {
					t.Fatalf("曲目级命中来源：%+v", match)
				}
			}
		})
	}
	for _, query := range []string{"消失标题", "消失艺人", "消失作曲者", "隐形发行", "隐形艺人"} {
		requireSearchTracks(t, app, &user, query)
		requireSearchAlbums(t, app, &user, query)
	}
	if hidden.groupID == fixture.groupID {
		t.Fatal("夹具未隔离")
	}
	requireSearchTracks(t, app, &user, "爱", fixture.trackIDs[:2]...)
	// 状态检查发生在查询时，发布之后的 missing 不能继续命中旧索引。
	mustExec(t, app, `UPDATE tracks SET source_status='missing' WHERE id=$1::uuid`, fixture.trackIDs[0])
	requireSearchTracks(t, app, &user, "作曲周杰伦")
	requireSearchAlbums(t, app, &user, "作曲周杰伦")
	requireSearchTracks(t, app, &user, "etiquette 唱片", fixture.trackIDs[1])
}

// 从独立艺人读取接口核验身份，避免只检查搜索返回了某个非空 UUID。
func requireSearchArtistByName(t *testing.T, app *integrationTestApplication, user *integrationTestUser, name string, roles []string) catalog.ArtistSummary {
	t.Helper()
	var page catalog.ArtistPage[catalog.ArtistSummary]
	getJSON(t, app, user, "/api/v1/artists?q="+url.QueryEscape(name), &page)
	var matches []catalog.ArtistSummary
	for _, artist := range page.Items {
		if artist.Name == name {
			matches = append(matches, artist)
		}
	}
	if len(matches) != 1 || !uuid.Valid(matches[0].ID) || !matches[0].Available || !reflect.DeepEqual(matches[0].Roles, roles) {
		t.Fatalf("艺人身份 %q/%v 不符：%+v", name, roles, page)
	}
	var detail catalog.ArtistDetail
	getJSON(t, app, user, "/api/v1/artists/"+matches[0].ID, &detail)
	if !reflect.DeepEqual(detail.Artist, matches[0]) || detail.RedirectArtistID != nil {
		t.Fatalf("艺人详情身份不符：%+v", detail)
	}
	return matches[0]
}
