package app

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

// 同一实体同时由发行标题、曲目标题及两条 credit 命中，计数须先去重。
func insertSearchPaginationFixture(t *testing.T, app *integrationTestApplication) {
	t.Helper()
	root := searchRoot(t, app)
	mustExec(t, app, `INSERT INTO release_groups(id) SELECT ('91000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid FROM generate_series(1,1002) i`)
	mustExec(t, app, `INSERT INTO releases(id,group_id,title,artist,source_root_id,relative_directory,candidate_anchor,candidate_kind)
 SELECT ('92000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,('91000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,
 '共有 '||CASE WHEN i<=999 THEN '九百 ' ELSE '' END||CASE WHEN i<=1000 THEN '千条 ' ELSE '' END||CASE WHEN i<=1001 THEN '千一 ' ELSE '' END||lpad(i::text,4,'0'),
 '分页艺人',$1::uuid,'分页/'||i,'分页:'||i,'ordinary_directory' FROM generate_series(1,1002) i`, root)
	mustExec(t, app, `INSERT INTO media(id,release_id,position,title) SELECT ('93000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,('92000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,1,'介质' FROM generate_series(1,1002) i`)
	mustExec(t, app, `INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind)
 SELECT ('94000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,('93000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,1,r.title,'分页艺人',1,$1::uuid,'分页/'||i||'.flac','present',NOW(),'flac_vorbis_comment'
 FROM generate_series(1,1002) i JOIN releases r ON r.id=('92000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid`, root)
	mustExec(t, app, `INSERT INTO track_credits(track_id,role,name,position) SELECT id,role,'共有',1 FROM tracks CROSS JOIN (VALUES ('composer'),('performer')) roles(role)`)
}

func TestPostgreSQLSearchBoundedCountsPaginationAndSuggestions(t *testing.T) {
	app := newIntegrationTestApplication(t)
	user := app.createUser(t, "user")
	insertSearchPaginationFixture(t, app)
	for _, mode := range []string{"legacy", "normalized"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "normalized" {
				publishSearch(t, app)
			}
			for _, kind := range []string{"albums", "tracks"} {
				for _, tc := range []struct {
					query              string
					page, total, count int
					relation           string
					more               bool
				}{
					{"九百", 1, 999, 50, "eq", true},
					{"九百", 20, 999, 49, "eq", false},
					{"千条", 1, 1000, 50, "eq", true},
					{"千条", 20, 1000, 50, "eq", false},
					{"千条", 21, 1000, 0, "eq", false},
					{"千一", 1, 1000, 50, "gte", true},
					{"千一", 20, 1000, 50, "gte", true},
					{"千一", 21, 1000, 1, "gte", false},
					{"千一", 22, 1000, 0, "gte", false},
					{"共有", 21, 1000, 2, "gte", false},
				} {
					path := searchURL(kind, tc.query) + fmt.Sprintf("&page=%d&page_size=50", tc.page)
					page := searchRead[json.RawMessage](t, app, &user, path)
					want := catalog.SearchPagination{Page: tc.page, PageSize: 50, Total: int64(tc.total), TotalRelation: tc.relation, HasMore: tc.more}
					if page.Pagination != want || len(page.Items) != tc.count {
						t.Fatalf("%s: 分页 %+v / 项数 %d，期望 %+v / %d", path, page.Pagination, len(page.Items), want, tc.count)
					}
					seen := map[string]bool{}
					for _, raw := range page.Items {
						var item struct {
							TrackID string `json:"track_id"`
							Group   struct {
								ID string `json:"id"`
							} `json:"group"`
						}
						if err := json.Unmarshal(raw, &item); err != nil {
							t.Fatal(err)
						}
						id := item.TrackID + item.Group.ID
						if id == "" || seen[id] {
							t.Fatalf("分页中有空身份或重复实体：%s", raw)
						}
						seen[id] = true
					}
				}
			}
			availableTypes := []string{"albums", "tracks"}
			var sharedArtist catalog.ArtistSummary
			artistCount := 0
			if mode == "normalized" {
				availableTypes = []string{"artists", "albums", "tracks"}
				artistCount = 1
				sharedArtist = requireSearchArtistByName(t, app, &user, "共有", []string{"composer", "performer"})
				if sharedArtist.AlbumCount != 1002 || sharedArtist.TrackCount != 1002 {
					t.Fatalf("艺人跨角色参与未去重：%+v", sharedArtist)
				}
			}
			for _, limit := range []int{1, 5, 8} {
				suffix := ""
				if limit != 5 {
					suffix = fmt.Sprintf("&limit=%d", limit)
				}
				response := searchResponse(t, app, &user, "/api/v1/search/suggestions?q="+url.QueryEscape("共有")+suffix)
				var suggestions catalog.SearchSuggestions
				if err := json.Unmarshal(response.Body.Bytes(), &suggestions); err != nil {
					t.Fatal(err)
				}
				if len(suggestions.Albums.Items) != limit || len(suggestions.Tracks.Items) != limit || !suggestions.Albums.HasMore || !suggestions.Tracks.HasMore || suggestions.Artists.Items == nil || len(suggestions.Artists.Items) != artistCount || !reflect.DeepEqual(suggestions.AvailableTypes, availableTypes) || suggestions.Artists.HasMore {
					t.Fatalf("建议上限：%s", response.Body.String())
				}
				if mode == "normalized" {
					item := suggestions.Artists.Items[0]
					if !reflect.DeepEqual(item.Artist, sharedArtist) || item.Match.Kind != "exact" || item.Match.Field != "artist_name" || item.Match.Original != "共有" || item.Match.ArtistID == nil || *item.Match.ArtistID != sharedArtist.ID || item.Match.Role != nil || item.Match.Alias != nil || item.Match.ReleaseID != nil || item.Match.TrackID != nil {
						t.Fatalf("建议艺人身份/命中不符：%+v", item)
					}
				}
				var raw map[string]map[string]json.RawMessage
				var envelope map[string]json.RawMessage
				if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				raw = map[string]map[string]json.RawMessage{}
				for _, section := range []string{"artists", "albums", "tracks"} {
					var fields map[string]json.RawMessage
					if err := json.Unmarshal(envelope[section], &fields); err != nil {
						t.Fatal(err)
					}
					raw[section] = fields
					if _, exists := fields["total"]; exists {
						t.Fatalf("建议不应执行/返回总计数：%s", response.Body.String())
					}
				}
			}
			var releases releaseListResponse
			var groups releaseGroupListResponse
			getJSON(t, app, &user, "/api/v1/releases?q="+url.QueryEscape("分页艺人")+"&page=21", &releases)
			getJSON(t, app, &user, "/api/v1/release-groups?q="+url.QueryEscape("分页艺人")+"&page=21", &groups)
			if releases.Pagination.Total != 1002 || groups.Pagination.Total != 1002 || len(releases.Items) != 2 || len(groups.Items) != 2 {
				t.Fatalf("旧路由精确总数/21页被截断：%+v %+v", releases.Pagination, groups.Pagination)
			}
		})
	}
}

func TestPostgreSQLSearchLargestLegalOffsetRemainsEmptyPage(t *testing.T) {
	app := newIntegrationTestApplication(t)
	user := app.createUser(t, "user")
	root := searchRoot(t, app)
	for _, title := range []string{"tone", "tone prefix", "inside tone"} {
		insertSearchRelease(t, app, root, groupedRelease{title: title, artist: "合成艺人", tracks: []string{"present"}})
	}
	maximum := int(^uint(0) >> 1)
	for _, mode := range []string{"legacy", "normalized"} {
		if mode == "normalized" {
			publishSearch(t, app)
		}
		for _, size := range []int{1, 2, 100} {
			page := maximum
			if size > 1 {
				page = maximum/size + 1
			}
			for _, prefix := range []string{"/api/v1/search?type=albums&", "/api/v1/search?type=tracks&", "/api/v1/releases?", "/api/v1/release-groups?"} {
				path := prefix + fmt.Sprintf("q=tone&page=%d&page_size=%d", page, size)
				response := searchResponse(t, app, &user, path)
				var got struct {
					Items      []json.RawMessage        `json:"items"`
					Pagination catalog.SearchPagination `json:"pagination"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(prefix, "/api/v1/search?") && got.Pagination.TotalRelation != "eq" {
					t.Fatalf("极大合法页计数关系错误：%s", response.Body.String())
				}
				if len(got.Items) != 0 || got.Pagination.Page != page || got.Pagination.PageSize != size || got.Pagination.Total != 3 || got.Pagination.HasMore {
					t.Fatalf("%s %s 极大合法页被改写：%s", mode, path, response.Body.String())
				}
			}
		}
	}
}
