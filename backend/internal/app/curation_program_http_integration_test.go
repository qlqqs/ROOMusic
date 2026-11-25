package app

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/coverartarchive"
)

func TestPostgreSQLCurationProgramEndToEnd(t *testing.T) {
	clock := newProgramClock()
	release, group, recording := uuid.New(), uuid.New(), uuid.New()
	cover := externalFixturePNG(t, 4, 4)
	// 不同前缀分别交给三个适配器，每个请求都核对实际目的路径。
	var mu sync.Mutex
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls[r.URL.Path]++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/mb/release/" + release:
			fmt.Fprintf(w, `{"id":"%s","title":"合成唱片","date":"2020","artist-credit":[{"name":"合成艺人","artist":{"id":"a","name":"合成艺人"}}],"release-group":{"id":"%s"},"media":[{"position":1,"format":"CD","track-count":1,"tracks":[{"id":"t","position":1,"length":100000,"recording":{"id":"%s"}}]}]}`, release, group, recording)
		case "/caa/release/" + release + "/":
			fmt.Fprint(w, `{"images":[{"id":17,"front":true}]}`)
		case "/caa/release/" + release + "/17-1200":
			w.Header().Set("Content-Type", "image/png")
			w.Write(cover)
		case "/lyrics/api/get":
			q := r.URL.Query()
			if q.Get("track_name") != "合成唱片 track 1" || q.Get("artist_name") != "合成艺人" || q.Get("album_name") != "合成唱片" || q.Get("duration") != "100" {
				t.Errorf("歌词参数 %v", q)
			}
			fmt.Fprint(w, `{"id":8,"trackName":"合成唱片 track 1","artistName":"合成艺人","duration":100,"syncedLyrics":"[00:01.00]第一行"}`)
		default:
			t.Errorf("意外出站 %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client := func(egress.Options) (*http.Client, error) { return server.Client(), nil }
	unlimited := func() *sources.Limiter { return sources.NewLimiter(0, nil, nil) }
	app := newIntegrationTestApplication(t, WithCurationProgram(true, clock.options()), WithCurationSources(
		sources.Options{BaseURL: server.URL + "/mb", NewClient: client, Limiter: unlimited()},
		coverartarchive.Options{BaseURL: server.URL + "/caa", NewClient: client, Limiter: unlimited(), ManifestLimiter: unlimited()},
		sources.Options{BaseURL: server.URL + "/lyrics", NewClient: client, Limiter: unlimited()}))
	t.Cleanup(func() {
		app.shutdown()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := app.app.WaitForCurationProgram(ctx); err != nil {
			t.Error(err)
		}
	})
	clock.wait(t)
	admin := app.createUser(t, "admin")
	root := uuid.New()
	mustExec(t, app, `INSERT INTO library_roots(id,path,status,revision) VALUES($1,$2,'active',1)`, root, app.allowedRoot)
	local, _ := insertGroupedRelease(t, app, root, groupedRelease{title: "合成唱片", artist: "合成艺人", tracks: []string{"present"}})
	track, _ := externalTrack(t, app, local)
	programTag(t, app, track, "mb_release", release)
	mustExec(t, app, `UPDATE tracks SET duration_seconds=100 WHERE id=$1`, track)
	var before, after curation.Progress
	getJSON(t, app, &admin, curationBase+"/progress", &before)
	if before.State != "disabled" || before.Releases.Total != 1 || before.Releases.Processed != 0 {
		t.Fatal(before)
	}
	response := app.request(t, "PUT", curationBase+"/settings", map[string]bool{"enabled": true}, &admin, nil)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	clock.advance(t, time.Minute)
	getJSON(t, app, &admin, curationBase+"/progress", &after)
	if after.State != "idle" || after.Releases != (curation.ReleaseProgress{Total: 1, Processed: 1, Matched: 1}) || after.Pending.WaitingModel != 0 || after.Batch != nil || after.LastPassFinishedAt == nil || after.RuleVersion != program.MatchRuleVersion {
		t.Fatal(after)
	}
	mu.Lock()
	gotCalls := maps.Clone(calls)
	mu.Unlock()
	want := map[string]int{"/mb/release/" + release: 1, "/caa/release/" + release + "/": 1, "/caa/release/" + release + "/17-1200": 1, "/lyrics/api/get": 1}
	if !maps.Equal(gotCalls, want) {
		t.Fatalf("HTTP 请求 %v，期望 %v", gotCalls, want)
	}
	var items struct {
		Items []struct {
			ID, Type string
			Changes  json.RawMessage
			Origin   struct{ Source, Provider string }
		}
	}
	getJSON(t, app, &admin, "/api/v1/operations?release_id="+local, &items)
	providers := map[string]string{}
	for _, item := range items.Items {
		if !uuid.Valid(item.ID) || item.Origin.Source != "curation" {
			t.Fatal(item)
		}
		providers[item.Type] = item.Origin.Provider
	}
	if len(items.Items) != 4 || !maps.Equal(providers, map[string]string{"external_link": "musicbrainz", "release_auto_correction": "musicbrainz", "release_external_artwork": "coverartarchive", "track_external_lyrics": "lrclib"}) {
		t.Fatalf("整理记录 %v", providers)
	}
	stored, _ := externalArtworkFile(t, app, cover)
	requireExternalArtworkHTTP(t, app, &admin, stored, cover)
	requireExternalLyricsHTTP(t, app, &admin, track, "online", "synced", []map[string]any{{"start_ms": 1000, "text": "第一行"}})
}
