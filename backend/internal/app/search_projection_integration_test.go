package app

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

func TestPostgreSQLSearchTrackSummaryNullabilityAndSafety(t *testing.T) {
	app := newIntegrationTestApplication(t)
	user := app.createUser(t, "user")
	fixture := insertSearchRelease(t, app, searchRoot(t, app), groupedRelease{title: "安全专辑", artist: "展示艺人", tracks: []string{"present", "present"}})
	mustExec(t, app, `UPDATE tracks SET title=CASE WHEN id=$1::uuid THEN '完整音频' ELSE '未知音频' END,source_kind='flac_vorbis_comment' WHERE medium_id IN (SELECT id FROM media WHERE release_id=$2::uuid)`, fixture.trackIDs[0], fixture.releaseID)
	mustExec(t, app, `UPDATE tracks SET duration_seconds=183.5,codec='flac',bit_depth=24,sample_rate=96000,channels=2,bitrate=2400000 WHERE id=$1::uuid`, fixture.trackIDs[0])
	for _, mode := range []string{"legacy", "normalized"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "normalized" {
				publishSearch(t, app)
			}
			var artistID string
			if mode == "normalized" {
				artistID = requireSearchArtistByName(t, app, &user, "展示艺人", []string{"release_artist", "track_artist"}).ID
			}
			for index, title := range []string{"完整音频", "未知音频"} {
				response := searchResponse(t, app, &user, searchURL("tracks", title))
				var raw struct {
					Items []map[string]json.RawMessage `json:"items"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
					t.Fatal(err)
				}
				if len(raw.Items) != 1 {
					t.Fatalf("安全摘要：%s", response.Body.String())
				}
				item := raw.Items[0]
				requireSearchKeys(t, item, "track_id", "title", "artist", "artist_refs", "present", "duration_seconds", "group_id", "release", "source", "match")
				var source, release, match map[string]json.RawMessage
				for _, target := range []struct {
					key   string
					value *map[string]json.RawMessage
				}{{"source", &source}, {"release", &release}, {"match", &match}} {
					if err := json.Unmarshal(item[target.key], target.value); err != nil {
						t.Fatal(err)
					}
				}
				requireSearchKeys(t, source, "source_kind", "codec", "bit_depth", "sample_rate", "channels", "bitrate")
				requireSearchKeys(t, release, "id", "title", "artist", "artist_refs", "album_artist_refs", "album_artist", "year", "source_type", "media_type", "medium_count", "track_count", "attention_count", "artwork")
				requireSearchKeys(t, match, "kind", "field", "role", "original", "artist_id", "alias", "release_id", "track_id")
				for _, key := range []string{"role", "artist_id", "alias"} {
					if string(match[key]) != "null" {
						t.Errorf("%s 必须显式 null：%s", key, match[key])
					}
				}
				for _, key := range []string{"year", "source_type", "media_type", "artwork"} {
					if string(release[key]) != "null" {
						t.Errorf("%s 必须显式 null：%s", key, release[key])
					}
				}
				var decoded searchPage[catalog.SearchTrackItem]
				if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
					t.Fatal(err)
				}
				track := decoded.Items[0]
				requireSearchArtistRefs(t, track.ArtistRefs, artistID, "展示艺人")
				requireSearchArtistRefs(t, track.Release.ArtistRefs, artistID, "展示艺人")
				requireSearchArtistRefs(t, track.Release.AlbumArtistRefs, "", "")
				if track.TrackID != fixture.trackIDs[index] || track.Release.ID != fixture.releaseID || track.GroupID != fixture.groupID || !track.Present || track.Source.SourceKind != "flac_vorbis_comment" {
					t.Fatalf("TrackSummary 关系不完整：%+v", track)
				}
				if index == 0 {
					if track.DurationSeconds == nil || *track.DurationSeconds != 183.5 || track.Source.Codec == nil || *track.Source.Codec != "flac" || track.Source.BitDepth == nil || *track.Source.BitDepth != 24 || track.Source.SampleRate == nil || *track.Source.SampleRate != 96000 || track.Source.Channels == nil || *track.Source.Channels != 2 || track.Source.Bitrate == nil || *track.Source.Bitrate != 2400000 {
						t.Fatalf("可播放事实丢失：%+v", track)
					}
				} else {
					if string(item["duration_seconds"]) != "null" {
						t.Errorf("未知时长不是 null：%s", item["duration_seconds"])
					}
					for _, key := range []string{"codec", "bit_depth", "sample_rate", "channels", "bitrate"} {
						if string(source[key]) != "null" {
							t.Errorf("未知 %s 不是 null：%s", key, source[key])
						}
					}
				}
				assertSearchSafe(t, app, response.Body.String())
			}
		})
	}
}

func requireSearchKeys(t *testing.T, value map[string]json.RawMessage, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if _, ok := value[key]; !ok {
			t.Errorf("缺少必传字段 %q：%v", key, value)
		}
	}
}

func assertSearchSafe(t *testing.T, app *integrationTestApplication, body string) {
	t.Helper()
	for _, forbidden := range []string{app.allowedRoot, app.dataDirectory, `"relative_path"`, `"relative_directory"`, `"source_root_id"`, `"source_identity"`, `"content_fingerprint"`, `"cue_sheet_path"`, `"cue_parent_relative_path"`, `"observed_at"`, `"normalized_value"`, `"projection_generation"`, `"build_id"`, `"input_revision"`} {
		if strings.Contains(body, forbidden) {
			t.Errorf("搜索泄露内部字段 %q：%s", forbidden, body)
		}
	}
}

func TestPostgreSQLSearchRankingGroupRepresentativeAndLegacyRoutes(t *testing.T) {
	app := newIntegrationTestApplication(t)
	user := app.createUser(t, "user")
	root := searchRoot(t, app)
	contains := insertSearchRelease(t, app, root, groupedRelease{title: "A Tune inside", artist: "Artist", tracks: []string{"present"}})
	prefix := insertSearchRelease(t, app, root, groupedRelease{title: "Tune extended", artist: "Artist", tracks: []string{"present"}})
	exact := insertSearchRelease(t, app, root, groupedRelease{title: "Tune", artist: "Artist", tracks: []string{"present"}})
	representative := insertSearchRelease(t, app, root, groupedRelease{title: "代表版本", artist: "Artist", year: 1980, tracks: []string{"present"}})
	matched := insertSearchRelease(t, app, root, groupedRelease{groupID: representative.groupID, title: "非代表命中", artist: "Artist", year: 2000, tracks: []string{"present"}})
	var beforeReleases releaseListResponse
	var beforeGroups releaseGroupListResponse
	getJSON(t, app, &user, "/api/v1/releases", &beforeReleases)
	getJSON(t, app, &user, "/api/v1/release-groups", &beforeGroups)
	var legacy releaseListResponse
	getJSON(t, app, &user, "/api/v1/releases?q=Tune", &legacy)
	if len(legacy.Items) != 3 || legacy.Items[0].ID != contains.releaseID {
		t.Fatalf("旧搜索排序：%+v", legacy)
	}
	before := searchRead[catalog.SearchTrackItem](t, app, &user, searchURL("tracks", "Tune"))
	if before.CatalogState.SearchMode != "legacy" || before.Items[0].TrackID != contains.trackIDs[0] {
		t.Fatalf("未发布必须完整回退：%+v", before)
	}
	publishSearch(t, app)
	albums := searchRead[catalog.SearchAlbumItem](t, app, &user, searchURL("albums", "Tune"))
	if len(albums.Items) != 3 {
		t.Fatalf("相关性结果：%+v", albums)
	}
	for index, want := range []searchReleaseFixture{exact, prefix, contains} {
		if albums.Items[index].Group.ID != want.groupID || albums.Items[index].Match.Kind != []string{"exact", "prefix", "contains"}[index] {
			t.Errorf("相关性第 %d 项：%+v", index, albums.Items[index])
		}
	}
	var releases releaseListResponse
	var groups releaseGroupListResponse
	getJSON(t, app, &user, "/api/v1/releases?q=Tune", &releases)
	getJSON(t, app, &user, "/api/v1/release-groups?q=Tune", &groups)
	if releases.Pagination.Total != 3 || groups.Pagination.Total != 3 {
		t.Fatalf("旧接口必须精确计数：%+v %+v", releases.Pagination, groups.Pagination)
	}
	for index, want := range []searchReleaseFixture{exact, prefix, contains} {
		if releases.Items[index].ID != want.releaseID || groups.Items[index].ID != want.groupID {
			t.Errorf("旧两路由未按相关性排序：%+v %+v", releases.Items, groups.Items)
		}
	}
	var afterReleases releaseListResponse
	var afterGroups releaseGroupListResponse
	getJSON(t, app, &user, "/api/v1/releases", &afterReleases)
	getJSON(t, app, &user, "/api/v1/release-groups", &afterGroups)
	artistID := requireSearchArtistByName(t, app, &user, "Artist", []string{"release_artist", "track_artist"}).ID
	// 独立验证 refs 后只归一这两个新增字段；其余摘要、数量、顺序与代表版本仍完整比较。
	for _, phase := range []struct {
		releases *releaseListResponse
		groups   *releaseGroupListResponse
		artistID string
	}{{&beforeReleases, &beforeGroups, ""}, {&afterReleases, &afterGroups, artistID}} {
		for i := range phase.releases.Items {
			item := &phase.releases.Items[i]
			requireSearchArtistRefs(t, item.ArtistRefs, phase.artistID, "Artist")
			requireSearchArtistRefs(t, item.AlbumArtistRefs, "", "")
			item.ArtistRefs, item.AlbumArtistRefs = nil, nil
		}
		for i := range phase.groups.Items {
			item := &phase.groups.Items[i].Representative
			requireSearchArtistRefs(t, item.ArtistRefs, phase.artistID, "Artist")
			requireSearchArtistRefs(t, item.AlbumArtistRefs, "", "")
			item.ArtistRefs, item.AlbumArtistRefs = nil, nil
		}
	}
	if !reflect.DeepEqual(beforeReleases, afterReleases) || !reflect.DeepEqual(beforeGroups, afterGroups) {
		t.Fatal("发布改变了无 q 的目录结果或排序")
	}
	page := requireSearchAlbums(t, app, &user, "非代表命中", representative.groupID)
	item := page.Items[0]
	if item.MatchedReleaseID != matched.releaseID || item.Group.Representative.ID != representative.releaseID || item.Group.ReleaseCount != 2 {
		t.Fatalf("命中版本替换了代表：%+v", item)
	}
	// 指定默认版本后仍由既有代表规则决定卡片，而非搜索命中的版本。
	admin := app.createUser(t, "admin")
	response := app.request(t, http.MethodPost, "/api/v1/release-groups/"+representative.groupID+"/default", map[string]any{"expected_revision": 1, "release_id": matched.releaseID}, &admin, idempotencyHeader("20000000-0000-4000-8000-000000000001"))
	if response.Code != 200 {
		t.Fatalf("设置默认版本：%d %s", response.Code, response.Body.String())
	}
	page = requireSearchAlbums(t, app, &user, "代表版本", representative.groupID)
	if page.Items[0].Group.Representative.ID != matched.releaseID || page.Items[0].MatchedReleaseID != representative.releaseID {
		t.Fatalf("默认版本与命中版本混淆：%+v", page.Items[0])
	}
}

func TestPostgreSQLSearchNormalizationPairsAndLiteralWildcards(t *testing.T) {
	app := newIntegrationTestApplication(t)
	user := app.createUser(t, "user")
	root := searchRoot(t, app)
	pairs := [][2]string{{"周杰倫", "周杰伦"}, {"ＡＢＣ", "abc"}, {"Beyoncé", "beyonce"}, {"さユり", "サユリ"}, {"Straße", "STRASSE"}, {"ガ", "が"}, {"ﾊﾟ", "ぱ"}, {"A\u00a0 B", "a b"}}
	expected := make([][]string, len(pairs))
	for i, pair := range pairs {
		for _, title := range pair {
			fixture := insertSearchRelease(t, app, root, groupedRelease{title: title, artist: "隔离艺人", tracks: []string{"present"}})
			expected[i] = append(expected[i], fixture.groupID)
		}
	}
	literals := map[string]string{}
	for _, title := range []string{"100% Tune", "100X Tune", "under_score", "underXscore", "tilde~name", "tildeXname", "-", "か", "は", "ば", "炼", "链"} {
		fixture := insertSearchRelease(t, app, root, groupedRelease{title: title, artist: "隔离艺人", tracks: []string{"present"}})
		literals[title] = fixture.groupID
	}
	publishSearch(t, app)
	for i, pair := range pairs {
		for _, query := range pair {
			requireSearchAlbums(t, app, &user, query, expected[i]...)
		}
	}
	for _, tc := range []struct{ query, title string }{{"%", "100% Tune"}, {"_", "under_score"}, {"~", "tilde~name"}, {"-", "-"}, {"か", "か"}, {"は", "は"}, {"ば", "ば"}, {"炼", "炼"}, {"链", "链"}} {
		requireSearchAlbums(t, app, &user, tc.query, literals[tc.title])
	}
}

func TestPostgreSQLSearchUnsafeProjectionFailsClosed(t *testing.T) {
	app := newIntegrationTestApplication(t)
	user := app.createUser(t, "user")
	fixture := insertSearchRelease(t, app, searchRoot(t, app), groupedRelease{title: "Unsafe", artist: "Artist", tracks: []string{"present"}})
	mustExec(t, app, `INSERT INTO release_artworks(release_id,content_hash,mime_type,width,height,storage_key,source_type) VALUES($1::uuid,$2,'image/webp',10,10,'/srv/private/cover.webp','embedded')`, fixture.releaseID, strings.Repeat("a", 64))
	for _, mode := range []string{"legacy", "normalized"} {
		if mode == "normalized" {
			publishSearch(t, app)
		}
		for _, path := range []string{searchURL("albums", "Unsafe"), searchURL("tracks", "Unsafe"), "/api/v1/search/suggestions?q=Unsafe"} {
			response := app.request(t, http.MethodGet, path, nil, &user, nil)
			requireAPIError(t, response, 503, "database_unavailable")
			assertSearchSafe(t, app, response.Body.String())
			for _, secret := range []string{"/srv/private", "SELECT ", "release_artworks", "storage_key"} {
				if strings.Contains(response.Body.String(), secret) {
					t.Fatalf("错误泄露：%s", response.Body.String())
				}
			}
		}
	}
}

func requireSearchArtistRefs(t *testing.T, refs []catalog.ArtistRef, artistID, name string) {
	t.Helper()
	want := []catalog.ArtistRef{}
	if artistID != "" {
		want = append(want, catalog.ArtistRef{ArtistID: artistID, Name: name, DisplayName: name})
	}
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("艺人引用：%+v，期望非 null 数组 %+v", refs, want)
	}
}
