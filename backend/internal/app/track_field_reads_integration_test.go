package app

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	catalogtools "github.com/qlqq/roomusic/backend/internal/catalog/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
	"github.com/qlqq/roomusic/backend/internal/zones"
	"github.com/qlqq/roomusic/backend/internal/zones/zonestest"
)

func TestPostgreSQLTrackFieldReads(t *testing.T) {
	t.Run("旧式搜索", func(t *testing.T) {
		f := newTrackCorrectionFixture(t, "present")
		track := f.trackIDs[0]
		requireTrackSearchValues(t, f, "legacy", scanTrackTitle, scanTrackArtist)
		f.patchTrack(t, track, 1, map[string]any{"title": setChange(fixedTrackTitle), "artist": setChange(fixedTrackArtist)})
		requireTrackSearchValues(t, f, "legacy", fixedTrackTitle, fixedTrackArtist)
	})

	f := newTrackCorrectionFixture(t, "present")
	app, user, track := f.app, &f.member, f.trackIDs[0]
	ctx := context.Background()
	store := catalog.NewStore(app.database)

	favorite := app.request(t, http.MethodPut, favoritesURL+"/tracks/"+track, map[string]any{}, user, nil)
	personalOK(t, favorite, 200)
	created := app.request(t, http.MethodPost, playlistsURL, map[string]any{"name": "合成歌单", "track_ids": []string{track}}, user, idempotencyHeader(uuid.New()))
	playlist := personalState(t, personalOK(t, created, 201)).ID
	requirePlayRecorded(t, postPlay(t, app, user, uuid.New(), track, 150.0))

	zoneService := zones.NewService(app.database, zones.Options{Renderer: zonestest.NewRenderer(zonestest.Options{})})
	t.Cleanup(zoneService.Close)
	device, err := zoneService.RegisterDevice(ctx, user.id, zones.RegisterDeviceInput{Name: "合成设备", Kind: "browser", Volume: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zoneService.ClaimOutput(ctx, user.id, device.Device.DeviceID, zones.ClaimOutputInput{Protocol: 1, Capabilities: zonesSampleCapabilities()}); err != nil {
		t.Fatal(err)
	}
	zoneID := device.Zone.ZoneID
	play := zones.Command{Command: "play_tracks", TrackIDs: []string{track}}
	started, err := zoneService.Command(ctx, user.id, zoneID, play)
	if err != nil || started.Zone.Current == nil || started.Zone.Stream == nil {
		t.Fatalf("启动播放：%+v，%v", started, err)
	}

	registry := tool.NewRegistry()
	for _, definition := range catalogtools.StewardTools(store, governance.NewStore(app.database)) {
		if err := registry.Register(definition); err != nil {
			t.Fatal(err)
		}
	}
	publishSearch(t, app)
	assertReads := func(t *testing.T, title, artist string, fields []string) {
		t.Helper()
		checkFields := func(t *testing.T, source, id, gotTitle, gotArtist string) {
			t.Helper()
			if id != track || gotTitle != title || gotArtist != artist {
				t.Errorf("%s 读取到 %q/%q/%q，期望 %q/%q/%q", source, id, gotTitle, gotArtist, track, title, artist)
			}
		}
		checkSummary := func(t *testing.T, source string, item catalog.TrackSummary) {
			t.Helper()
			checkFields(t, source, item.TrackID, item.Title, item.Artist)
		}
		t.Run("目录详情摘要与放置", func(t *testing.T) {
			var detail catalog.ReleaseDetail
			getJSON(t, app, user, "/api/v1/releases/"+f.releaseID, &detail)
			if len(detail.Media) != 1 || len(detail.Media[0].Tracks) != 1 {
				t.Fatalf("发行详情没有预期的曲目：%+v", detail.Media)
			}
			row := detail.Media[0].Tracks[0]
			checkFields(t, "发行详情", row.ID, row.Title, row.Artist)
			if !reflect.DeepEqual(row.CorrectedFields, fields) || row.AutoFields != nil {
				t.Errorf("曲目标记为人工 %v、自动 %v，期望人工 %v 且省略自动", row.CorrectedFields, row.AutoFields, fields)
			}
			var summaries struct {
				Items []catalog.TrackSummary `json:"items"`
			}
			response := app.request(t, http.MethodPost, "/api/v1/tracks/summaries", map[string][]string{"track_ids": {track}}, user, nil)
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &summaries) != nil || len(summaries.Items) != 1 {
				t.Fatalf("曲目摘要返回 HTTP %d：%s", response.Code, response.Body.String())
			}
			checkSummary(t, "摘要", summaries.Items[0])
			placements, err := store.LoadTrackPlacements(ctx, []string{track})
			if got := placements[track]; err != nil || got.TrackID != track || got.GroupID != f.groupID || got.Title != title {
				t.Errorf("曲目放置为 %+v，期望标题 %q，错误 %v", got, title, err)
			}
		})
		t.Run("搜索与艺人曲目", func(t *testing.T) {
			requireTrackSearchValues(t, f, "normalized", title, artist)
			artistID := effectiveTrackArtistID(t, app, track, artist)
			var artistTracks catalog.ArtistPage[catalog.TrackSummary]
			getJSON(t, app, user, "/api/v1/artists/"+artistID+"/tracks?role=track_artist", &artistTracks)
			if len(artistTracks.Items) != 1 {
				t.Fatalf("艺人曲目：%+v", artistTracks)
			}
			checkSummary(t, "艺人曲目", artistTracks.Items[0])
		})
		t.Run("历史收藏与歌单", func(t *testing.T) {
			history := historyTracksOf(t, app, user, "")
			recent := recentReleasesOf(t, app, user)
			var favorites personal.FavoriteTracksPage
			getJSON(t, app, user, favoritesURL+"/tracks", &favorites)
			var items personal.PlaylistItemsPage
			getJSON(t, app, user, playlistsURL+"/"+playlist+"/items", &items)
			if len(history.Items) != 1 || len(recent.Items) != 1 || len(favorites.Items) != 1 || len(items.Items) != 1 {
				t.Fatalf("历史 %d、最近发行 %d、收藏 %d、歌单 %d，分别期望 1 条", len(history.Items), len(recent.Items), len(favorites.Items), len(items.Items))
			}
			checkSummary(t, "历史", history.Items[0].TrackSummary)
			if last := recent.Items[0].LastTrack; last.ID != track || last.Title != title {
				t.Errorf("最近发行的曲目为 %+v，期望 %q", last, title)
			}
			checkSummary(t, "收藏", favorites.Items[0].Track)
			checkSummary(t, "歌单", items.Items[0].Track)
		})
		t.Run("队列与播放", func(t *testing.T) {
			queue, err := zoneService.ReadQueue(ctx, user.id, zoneID)
			if err != nil || len(queue.Entries) != 1 {
				t.Fatalf("播放队列为 %+v，错误 %v", queue, err)
			}
			checkSummary(t, "播放队列", queue.Entries[0].Track)
			snapshot, err := zoneService.Snapshot(ctx, user.id)
			if err != nil || len(snapshot.Zones) != 1 || snapshot.Zones[0].Current == nil {
				t.Fatalf("播放状态为 %+v，错误 %v", snapshot, err)
			}
			checkSummary(t, "当前播放", snapshot.Zones[0].Current.Track)
		})
		t.Run("管家 get_track", func(t *testing.T) {
			result, err := registry.Invoke(ctx, tool.Caller{User: identity.User{ID: user.id, Role: "user"}}, "get_track", json.RawMessage(`{"id":"`+track+`"}`))
			if err != nil {
				t.Fatal(err)
			}
			var value struct {
				catalog.TrackSummary
				Detail *catalog.ReleaseTrack `json:"detail"`
			}
			if err := json.Unmarshal(result.Output, &value); err != nil || value.Detail == nil {
				t.Fatalf("get_track 返回 %s，错误 %v", result.Output, err)
			}
			checkSummary(t, "管家摘要", value.TrackSummary)
			checkFields(t, "管家详情", value.Detail.ID, value.Detail.Title, value.Detail.Artist)
		})
	}
	t.Run("扫描值", func(t *testing.T) { assertReads(t, scanTrackTitle, scanTrackArtist, nil) })

	// 直接放入自动值，使人工优先的读取用例独立于自动执行器。
	mustExec(t, app, `WITH automatic AS (
		INSERT INTO operations(id,actor_id,operation_type,target_type,target_id,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
		VALUES(gen_random_uuid(),$2::uuid,'track_auto_correction','release',$3::uuid,gen_random_uuid(),'auto',1,2,'{}','{}','{}','fixture') RETURNING id
	) INSERT INTO track_auto_values(track_id,field,mode,text_value,operation_id)
	SELECT $1::uuid,field,'set',text_value,automatic.id FROM automatic
	CROSS JOIN (VALUES ('title','自动中曲'),('artist','自动演奏者')) AS fixture(field,text_value)`, track, f.admin.id, f.releaseID)
	f.patchTrack(t, track, 1, map[string]any{"title": setChange(fixedTrackTitle), "artist": setChange(fixedTrackArtist)})
	t.Run("人工值优先于自动值", func(t *testing.T) { assertReads(t, fixedTrackTitle, fixedTrackArtist, []string{"title", "artist"}) })
}

func requireTrackSearchValues(t *testing.T, f trackCorrectionFixture, mode, title, artist string) {
	t.Helper()
	page := requireSearchTracks(t, f.app, &f.member, title, f.trackIDs[0])
	if item := page.Items[0]; page.CatalogState.SearchMode != mode || item.Title != title || item.Artist != artist {
		t.Fatalf("搜索模式为 %q，曲目为 %q/%q，期望 %q 模式与 %q/%q", page.CatalogState.SearchMode, item.Title, item.Artist, mode, title, artist)
	}
}
