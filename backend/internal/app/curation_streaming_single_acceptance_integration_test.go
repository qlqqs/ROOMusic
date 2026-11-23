package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLCurationStreamingSingleEnrichment(t *testing.T) {
	tool := b3RealFingerprinter(t)
	const code = "USABC2300002"
	for _, sample := range []struct{ name, method string }{
		{"acoustid", "acoustid"}, {"isrc_same_title", "isrc"}, {"isrc_placeholder", "isrc"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			method := sample.method
			recording := uuid.New()
			const title, artist = "海上的灯", "合成歌手"
			official := fmt.Sprintf(`{"id":%q,"title":%q,"length":12000,"artist-credit":[{"name":%q}]}`,
				recording, title, artist)
			mux := http.NewServeMux()
			if method == "isrc" {
				mux.HandleFunc("GET /mb/isrc/"+code, func(w http.ResponseWriter, r *http.Request) {
					fmt.Fprintf(w, `{"isrc":%q,"recordings":[%s]}`, code, official)
				})
			} else {
				mux.HandleFunc("POST /v2/lookup", func(w http.ResponseWriter, r *http.Request) {
					form := b3LookupForm(t, r)
					if form.Get("fingerprint") == "" || form.Get("duration") != "12" {
						t.Error("AcoustID 查询应包含合成音频的指纹和 12 秒时长")
					}
					fmt.Fprintf(w, `{"status":"ok","results":[{"id":"hit","score":0.98,"recordings":[{"id":%q,"duration":12}]}]}`, recording)
				})
				mux.HandleFunc("GET /mb/recording/"+recording, func(w http.ResponseWriter, r *http.Request) {
					fmt.Fprint(w, official)
				})
			}
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("意外的外部请求 %s %s", r.Method, r.URL.Path)
				http.NotFound(w, r)
			})
			server := httptest.NewServer(mux)
			t.Cleanup(server.Close)
			app, clock, admin := b3ProgramApp(t, tool, server)
			release, tracks := b3ReleaseTracks(t, app, b3Root(t, app), "未知专辑", "未知艺术家", 1)
			track := tracks[0]
			mustExec(t, app, `UPDATE releases SET candidate_kind='loose_unknown' WHERE id=$1`, release)
			localTitle := "未知曲目"
			if sample.name == "isrc_same_title" {
				localTitle = title
			} else {
				scan := uuid.New()
				mustExec(t, app, `INSERT INTO scan_runs(id,status,started_at,finished_at) VALUES($1,'succeeded',now(),now())`, scan)
				mustExec(t, app, `INSERT INTO track_observations(track_id,scan_run_id,field_name,source_kind)
					VALUES($1,$2,'title','default_fallback')`, track, scan)
			}
			mustExec(t, app, `UPDATE tracks SET title=$2,duration_seconds=12 WHERE id=$1`, track, localTitle)
			wantFingerprint := "not_reached"
			if method == "isrc" {
				programTag(t, app, track, "isrc", code)
			} else {
				wantFingerprint = "used"
				const file = "single.flac"
				b3GenerateAudio(t, filepath.Join(app.allowedRoot, file), b3LowTone)
				b3TrackSnapshot(t, app, track, file, 12)
				b3SetHTTPKey(t, app, &admin, "application", b3AppKey)
				b3SetHTTPKey(t, app, &admin, "user", b3UserKey)
			}

			resp := app.request(t, "PUT", curationBase+"/sources/lrclib", map[string]bool{"enabled": false}, &admin, nil)
			if resp.Code != http.StatusOK {
				t.Fatal(resp.Code, resp.Body.String())
			}
			b3Enable(t, app, admin)
			clock.advance(t, time.Minute)
			if sample.name == "isrc_placeholder" {
				b3RequireReleaseResult(t, app, release, "uncertain", method, wantFingerprint, "")
				b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1
					AND kind='recording_match' AND status='waiting_model' AND candidates->0->>'external_id'=$2`, release, recording)
				b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links`)
				b3RequireCount(t, app, 0, `SELECT count(*) FROM track_auto_values`)
				b3RequireCount(t, app, 1, `SELECT count(*) FROM track_effective WHERE id=$1 AND title=$2 AND artist='未知艺术家'`, track, localTitle)
				return
			}
			b3RequireCount(t, app, 1, `SELECT count(*) FROM track_auto_values WHERE track_id=$1 AND field='artist' AND text_value=$2`, track, artist)
			b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE object_type='track'
				AND object_id=$1 AND source='musicbrainz' AND method=$2 AND external_id=$3`, track, method, recording)
			b3RequireCount(t, app, 1, `SELECT count(*) FROM track_effective WHERE id=$1 AND title=$2 AND artist=$3`, track, title, artist)
			b3RequireReleaseResult(t, app, release, "matched", method, wantFingerprint, "")
		})
	}
}
