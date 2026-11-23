package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/playback/fingerprint"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/acoustid"
	"github.com/qlqq/roomusic/backend/internal/sources/coverartarchive"
)

func b3ProgramApp(t *testing.T, tool *fingerprint.Fingerprinter, server *httptest.Server) (*integrationTestApplication, *programClock, integrationTestUser) {
	t.Helper()
	clock := newProgramClock()
	source := func(path string) sources.Options {
		return sources.Options{BaseURL: server.URL + path, NewClient: b3HTTPClient(server), Limiter: sources.NewLimiter(0, nil, nil)}
	}
	app := newIntegrationTestApplication(t, WithCurationProgram(true, clock.options()), WithFingerprinter(tool), WithAcoustID(b3AcoustOptions(server)),
		WithCurationSources(source("/mb"), coverartarchive.Options{BaseURL: server.URL + "/covers", NewClient: b3HTTPClient(server), Limiter: sources.NewLimiter(0, nil, nil), ManifestLimiter: sources.NewLimiter(0, nil, nil)}, source("/lyrics")))
	t.Cleanup(func() {
		app.shutdown()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := app.app.WaitForCurationProgram(ctx); err != nil {
			t.Error(err)
		}
	})
	clock.wait(t)
	return app, clock, app.createUser(t, "admin")
}

func b3Enable(t *testing.T, app *integrationTestApplication, admin integrationTestUser) {
	t.Helper()
	if _, err := curation.NewWriter(app.database, app.dataDirectory, nil).SetSettings(t.Context(), admin.id, true); err != nil {
		t.Fatal(err)
	}
}

func b3RequireCount(t *testing.T, app *integrationTestApplication, want int, query string, args ...any) {
	t.Helper()
	if count := countRows(t, app, query, args...); count != want {
		t.Errorf("查询 %s 得到 %d，期望 %d", query, count, want)
	}
}

func b3WorkState(t *testing.T, app *integrationTestApplication, subject, target string) curation.WorkState {
	t.Helper()
	rows, err := curation.LoadWorkStates(t.Context(), app.database, subject, []string{target})
	if err != nil {
		t.Fatal(err)
	}
	return rows[target]
}

func b3RequireReleaseResult(t *testing.T, app *integrationTestApplication, release, status, method, fingerprint string, conflict string) {
	t.Helper()
	state := b3WorkState(t, app, "release", release)
	var result struct {
		Method, Fingerprint string
		Conflicts           []string
	}
	if err := json.Unmarshal(state.Result, &result); err != nil {
		t.Fatalf("发行状态=%+v，解码结果=%v", state, err)
	}
	if state.Status != status || result.Method != method || result.Fingerprint != fingerprint ||
		(conflict == "" && len(result.Conflicts) != 0) ||
		(conflict != "" && !reflect.DeepEqual(result.Conflicts, []string{conflict})) {
		t.Fatalf("状态=%q 结果=%s，期望 %q 方法=%q 指纹=%q 冲突=%q", state.Status, state.Result, status, method, fingerprint, conflict)
	}
}

type b3RemoteRelease struct {
	ID, Group  string
	Recordings [2]string
}

func b3NewRemoteRelease() b3RemoteRelease {
	return b3RemoteRelease{ID: uuid.New(), Group: uuid.New(), Recordings: [2]string{uuid.New(), uuid.New()}}
}

func (x b3RemoteRelease) writeMusicBrainz(w http.ResponseWriter) {
	fmt.Fprintf(w, `{"id":%q,"title":"合成正式专辑","date":"2024-02-01",
		"artist-credit":[{"name":"合成正式艺人","artist":{"id":"b3-artist","name":"合成正式艺人"}}],
		"release-group":{"id":%q},
		"media":[{"position":1,"track-count":2,"tracks":[
		{"id":"b3-track-1","position":1,"length":12000,"recording":{"id":%q}},
		{"id":"b3-track-2","position":2,"length":16000,"recording":{"id":%q}}]}]}`, x.ID, x.Group, x.Recordings[0], x.Recordings[1])
}

func b3AcoustHit(remote b3RemoteRelease, index int) string {
	return fmt.Sprintf(`{"id":%q,"duration":%d,"releases":[{"id":%q,"title":"合成正式专辑","track_count":2,"medium_count":1}]}`,
		remote.Recordings[index], []int{12, 16}[index], remote.ID)
}

func TestPostgreSQLB3AcoustIDProgram(t *testing.T) {
	tool := b3RealFingerprinter(t)
	for _, tc := range []struct {
		name, invalidKey string
		code             int
		ambiguous        bool
	}{
		{name: "占位专辑对上"},
		{name: "待定项带分数", ambiguous: true},
		{name: "应用密钥无效仍搜索", invalidKey: "application", code: 4},
		{name: "用户密钥无效仍搜索", invalidKey: "user", code: 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remote, alternate := b3NewRemoteRelease(), b3NewRemoteRelease()
			var mu sync.Mutex
			hits := map[string]int{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				hits[r.URL.Path]++
				mu.Unlock()
				switch r.URL.Path {
				case "/v2/lookup":
					form := b3LookupForm(t, r)
					if form.Get("fingerprint") == "" || form.Get("client") != b3AppKey || form.Get("user") != b3UserKey {
						t.Error("受控 AcoustID 未收到指纹和两把合成密钥")
					}
					if tc.code != 0 {
						w.WriteHeader(http.StatusBadRequest)
						fmt.Fprintf(w, `{"status":"error","error":{"code":%d}}`, tc.code)
						return
					}
					var index int
					switch form.Get("duration") {
					case "12":
						index = 0
					case "16":
						index = 1
					default:
						t.Errorf("未识别的整轨时长 %q", form.Get("duration"))
						http.Error(w, "unexpected duration", http.StatusBadRequest)
						return
					}
					recordings := b3AcoustHit(remote, index)
					if tc.ambiguous {
						recordings += "," + b3AcoustHit(alternate, index)
					}
					fmt.Fprintf(w, `{"status":"ok","results":[{"id":"chromaprint-hit","score":0.96,"recordings":[%s]}]}`, recordings)
				case "/mb/release/" + remote.ID:
					remote.writeMusicBrainz(w)
				case "/mb/release/" + alternate.ID:
					alternate.writeMusicBrainz(w)
				case "/mb/release":
					fmt.Fprintf(w, `{"count":1,"releases":[{"id":%q,"title":"合成真实标签","artist-credit":[{"name":"合成艺人"}]}]}`, remote.ID)
				case "/covers/release/" + remote.ID + "/", "/covers/release-group/" + remote.Group + "/", "/lyrics/api/get", "/lyrics/api/search":
					http.NotFound(w, r)
				default:
					t.Errorf("意外请求 %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			app, clock, admin := b3ProgramApp(t, tool, server)
			title, artist := "未知专辑", "未知艺术家"
			if tc.code != 0 {
				title, artist = "合成真实标签", "合成艺人"
			}
			release, tracks := b3ReleaseTracks(t, app, b3Root(t, app), title, artist, 2)
			if tc.code == 0 {
				scan := uuid.New()
				mustExec(t, app, `INSERT INTO scan_runs(id,status,started_at,finished_at) VALUES($1,'succeeded',now(),now())`, scan)
				for i, track := range tracks {
					mustExec(t, app, `UPDATE tracks SET title=$2 WHERE id=$1`, track, fmt.Sprintf("Track %d", i+1))
					mustExec(t, app, `INSERT INTO track_observations(track_id,scan_run_id,field_name,source_kind) VALUES($1,$2,'title','default_fallback')`, track, scan)
				}
				evidence, err := catalog.LoadCurationEvidence(t.Context(), app.database, []string{release})
				local := evidence[release]
				if err != nil || !local.TitlePlaceholder || !local.ArtistPlaceholder || len(local.Tracks) != 2 ||
					slices.ContainsFunc(local.Tracks, func(track catalog.CurationTrack) bool { return !track.TitlePlaceholder || !track.ArtistPlaceholder }) {
					t.Fatalf("占位标签证据=%+v，错误=%v", local, err)
				}
			}
			for i, tone := range []string{b3LowTone, b3HighTone} {
				file := fmt.Sprintf("program-%d.flac", i)
				b3GenerateAudio(t, filepath.Join(app.allowedRoot, file), tone)
				b3TrackSnapshot(t, app, tracks[i], file, []float64{12, 16}[i])
			}
			b3SetHTTPKey(t, app, &admin, "application", b3AppKey)
			b3SetHTTPKey(t, app, &admin, "user", b3UserKey)
			b3Enable(t, app, admin)
			clock.advance(t, time.Minute)

			mu.Lock()
			lookups, searches := hits["/v2/lookup"], hits["/mb/release"]
			mu.Unlock()
			item := b3SourceList(t, app, &admin)["acoustid"]
			switch {
			case tc.code != 0:
				b3RequireReleaseResult(t, app, release, "uncertain", "search", "unavailable", "search_only")
				b3Reason(t, item, "key_invalid")
				for _, key := range item.Keys {
					want := "unchecked"
					if key.Name == tc.invalidKey {
						want = "invalid"
					}
					if key.Status != want {
						t.Errorf("%s 状态=%s，期望 %s", key.Name, key.Status, want)
					}
				}
				if lookups != 1 || searches != 1 {
					t.Errorf("AcoustID 请求=%d，搜索请求=%d，均期望 1", lookups, searches)
				}
			case tc.ambiguous:
				b3RequireReleaseResult(t, app, release, "uncertain", "acoustid", "used", "ambiguous")
				raw := programString(t, app, `SELECT candidates FROM curation_pending_items WHERE target_id=$1 AND status='waiting_model'`, release)
				var candidates []program.Candidate
				if err := json.Unmarshal([]byte(raw), &candidates); err != nil || len(candidates) != 2 {
					t.Fatalf("待定项候选=%s，错误=%v", raw, err)
				}
				want := map[string]bool{remote.ID: true, alternate.ID: true}
				for _, candidate := range candidates {
					if !want[candidate.ExternalID] || candidate.Method != "acoustid" || candidate.Score != 0.96 {
						t.Errorf("候选=%+v，期望依据 acoustid、分数 0.96", candidate)
					}
					delete(want, candidate.ExternalID)
				}
			default:
				b3RequireReleaseResult(t, app, release, "matched", "acoustid", "used", "")
				b3Reason(t, item, "")
				for _, key := range item.Keys {
					if key.Status != "valid" {
						t.Errorf("%s 密钥状态=%s，期望 valid", key.Name, key.Status)
					}
				}
				b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1 AND source='musicbrainz' AND method='acoustid' AND external_id=$2`, release, remote.ID)
				for i, track := range tracks {
					b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE object_type='track' AND object_id=$1 AND source='musicbrainz' AND method='acoustid' AND external_id=$2`, track, remote.Recordings[i])
				}
				b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1`, release)
				var detail struct {
					Title       string
					AlbumArtist string `json:"album_artist"`
					Year        int
					AutoFields  []string `json:"auto_fields"`
				}
				getJSON(t, app, &admin, "/api/v1/releases/"+release, &detail)
				if detail.Title != "合成正式专辑" || detail.AlbumArtist != "合成正式艺人" || detail.Year != 2024 ||
					!slices.Equal(detail.AutoFields, []string{"title", "album_artist", "year"}) {
					t.Errorf("发行详情的自动补全字段=%+v", detail)
				}
			}
			if tc.code == 0 && (lookups != 2 || searches != 0) {
				t.Errorf("AcoustID 请求=%d，搜索请求=%d，期望 2 和 0", lookups, searches)
			}
		})
	}
}

func TestPostgreSQLB3FingerprintToolUnavailable(t *testing.T) {
	ffmpeg := b3RequireTool(t, "ROOMUSIC_TEST_FFMPEG")
	b3RequireTool(t, "ROOMUSIC_TEST_FPCALC")
	for _, fail := range []string{"不存在", "自检失败"} {
		t.Run(fail, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fpcalc")
			if fail == "自检失败" {
				script := "#!/bin/sh\nif [ \"$1\" = -version ]; then printf 'fpcalc version 1.6.2\\n'; exit 0; fi\nexit 1\n"
				if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			tool, err := OpenFingerprinter(t.Context(), config.Server{FFmpegPath: ffmpeg, FPCalcPath: path}, logger)
			if err != nil || tool != nil {
				t.Fatalf("指纹工具=%v，启动错误=%v", tool, err)
			}
			remote := b3NewRemoteRelease()
			png := externalFixturePNG(t, 4, 4)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/mb/release/" + remote.ID:
					remote.writeMusicBrainz(w)
				case "/covers/release/" + remote.ID + "/":
					io.WriteString(w, `{"images":[{"id":17,"front":true}]}`)
				case "/covers/release/" + remote.ID + "/17-1200":
					w.Header().Set("Content-Type", "image/png")
					w.Write(png)
				case "/lyrics/api/get":
					q := r.URL.Query()
					fmt.Fprintf(w, `{"id":8,"trackName":%q,"artistName":%q,"duration":%s,"syncedLyrics":"[00:01.00]第一行"}`,
						q.Get("track_name"), q.Get("artist_name"), q.Get("duration"))
				default:
					t.Errorf("意外出站 %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			app, clock, admin := b3ProgramApp(t, tool, server)
			release, tracks := b3ReleaseTracks(t, app, b3Root(t, app), "合成正式专辑", "合成正式艺人", 2)
			for i, track := range tracks {
				mustExec(t, app, `UPDATE tracks SET duration_seconds=$2 WHERE id=$1`, track, []int{12, 16}[i])
				programTag(t, app, track, "mb_release", remote.ID)
			}
			b3Enable(t, app, admin)
			items := b3SourceList(t, app, &admin)
			b3Reason(t, items["acoustid"], "fingerprint_unavailable")
			for _, enabled := range []bool{false, true} {
				response := app.request(t, http.MethodPut, curationBase+"/sources/acoustid", map[string]bool{"enabled": enabled}, &admin, nil)
				if response.Code != http.StatusOK {
					t.Fatalf("切换指纹源返回 HTTP %d %s", response.Code, response.Body)
				}
				reason := "source_disabled"
				if enabled {
					reason = "fingerprint_unavailable"
				}
				b3Reason(t, b3SourceList(t, app, &admin)["acoustid"], reason)
			}
			for _, provider := range []string{"musicbrainz", "coverartarchive", "lrclib"} {
				b3Reason(t, items[provider], "")
			}
			clock.advance(t, time.Minute)
			b3RequireReleaseResult(t, app, release, "matched", "tag_id", "not_reached", "")
			if got := b3WorkState(t, app, "artwork", release); got.Status != "done" {
				t.Errorf("封面状态=%s，期望 done", got.Status)
			}
			stored, _ := externalArtworkFile(t, app, png)
			requireExternalArtworkHTTP(t, app, &admin, stored, png)
			for _, track := range tracks {
				if got := b3WorkState(t, app, "lyrics", track); got.Status != "done" {
					t.Errorf("歌词状态=%s，期望 done", got.Status)
				}
				requireExternalLyricsHTTP(t, app, &admin, track, "online", "synced", []map[string]any{{"start_ms": 1000, "text": "第一行"}})
			}
		})
	}
}

func TestPostgreSQLB3LegacyWorkReopenedOnce(t *testing.T) {
	tool := b3RealFingerprinter(t)
	var mu sync.Mutex
	hits := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		switch r.URL.Path {
		case "/v2/lookup":
			form := b3LookupForm(t, r)
			if form.Get("fingerprint") == "" || form.Get("duration") != "12" {
				t.Error("升级后的查询未携带合成音频指纹与时长")
			}
			io.WriteString(w, b3EmptyLookup)
		case "/mb/release":
			io.WriteString(w, `{"count":0,"releases":[]}`)
		default:
			t.Errorf("意外出站 %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	app, clock, admin := b3ProgramApp(t, tool, server)
	root := b3Root(t, app)
	for _, filename := range []string{"upgrade-placeholder.flac", "upgrade-search.flac"} {
		b3GenerateAudio(t, filepath.Join(app.allowedRoot, filename), b3LowTone)
	}
	type seededState struct {
		state curation.WorkState
		redo  bool
	}
	var old []seededState
	seed := func(subject, target, digest, status, method, conflict string, redo bool) {
		t.Helper()
		shape := map[string]any{}
		if subject == "release" {
			shape["method"] = method
			conflicts := []string{}
			if conflict != "" {
				conflicts = append(conflicts, conflict)
			}
			shape["conflicts"] = conflicts
		}
		data, err := json.Marshal(shape)
		if err != nil {
			t.Fatal(err)
		}
		state := curation.WorkState{Subject: subject, TargetID: target, RuleVersion: program.StoredRuleVersion,
			InputDigest: digest, Status: status, Result: data, Attempts: 6, Generation: 7}
		if err := curation.SaveWorkState(t.Context(), app.database, state); err != nil {
			t.Fatal(err)
		}
		old = append(old, seededState{b3WorkState(t, app, subject, target), redo})
	}
	for _, tc := range []struct {
		status, method, conflict string
		redo                     bool
	}{
		{"no_match", "", "insufficient_evidence", true},
		{"uncertain", "search", "search_only", true},
		{"uncertain", "", "track_count", false},
		{"uncertain", "tag_id", "duration_mismatch", false},
		{"uncertain", "barcode", "barcode_mismatch", false},
		{"uncertain", "disc_id", "disc_fuzzy", false},
		{"matched", "search", "", false},
	} {
		title, artist := "升级夹具 "+tc.status+" "+tc.method+" "+tc.conflict, "合成艺人"
		if tc.conflict == "insufficient_evidence" {
			title, artist = "未知专辑", "未知艺术家"
		}
		release, tracks := b3ReleaseTracks(t, app, root, title, artist, 1)
		mustExec(t, app, `UPDATE tracks SET duration_seconds=12 WHERE id=$1`, tracks[0])
		if tc.redo {
			filename := "upgrade-search.flac"
			if tc.conflict == "insufficient_evidence" {
				filename = "upgrade-placeholder.flac"
			}
			b3TrackSnapshot(t, app, tracks[0], filename, 12)
		}
		items, err := catalog.LoadCurationEvidence(t.Context(), app.database, []string{release})
		evidence, found := items[release]
		if err != nil || !found {
			t.Fatalf("读取旧版证据 found=%t 错误=%v", found, err)
		}
		seed("release", release, program.ReleaseDigest(evidence), tc.status, tc.method, tc.conflict, tc.redo)
		seed("artwork", release, program.ArtworkDigest(evidence), "done", "", "", false)
		seed("lyrics", tracks[0], program.LyricsDigest(evidence, evidence.Tracks[0]), "done", "", "", false)
	}
	check := func(available bool) {
		t.Helper()
		for _, previous := range old {
			before := previous.state
			after := b3WorkState(t, app, before.Subject, before.TargetID)
			if available && previous.redo {
				conflict := ""
				var oldResult struct{ Conflicts []string }
				if err := json.Unmarshal(before.Result, &oldResult); err != nil {
					t.Fatal(err)
				}
				method := "search"
				if slices.Contains(oldResult.Conflicts, "insufficient_evidence") {
					method, conflict = "", "insufficient_evidence"
					b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1`, before.TargetID)
				}
				b3RequireReleaseResult(t, app, before.TargetID, "no_match", method, "used", conflict)
				if after.Generation != before.Generation+1 || after.Attempts != before.Attempts+1 {
					t.Errorf("%s 代次/尝试=%d/%d，期望 %d/%d", after.TargetID, after.Generation, after.Attempts, before.Generation+1, before.Attempts+1)
				}
			} else if !reflect.DeepEqual(before, after) {
				t.Errorf("%s %s 的旧状态被重做，代次 %d → %d", before.Subject, before.TargetID, before.Generation, after.Generation)
			}
		}
	}
	b3Enable(t, app, admin)
	clock.advance(t, time.Minute)
	check(false)
	b3SetHTTPKey(t, app, &admin, "application", b3AppKey)
	clock.advance(t, time.Minute)
	check(false)
	mu.Lock()
	baseline := len(hits)
	mu.Unlock()
	if baseline != 0 {
		t.Errorf("密钥不全时已有 %d 种外部请求", baseline)
	}
	b3SetHTTPKey(t, app, &admin, "user", b3UserKey)
	clock.advance(t, time.Minute)
	check(true)
	clock.advance(t, 7*time.Hour)
	check(true)
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(hits, map[string]int{"/v2/lookup": 1, "/mb/release": 1}) {
		t.Errorf("重开后外部请求计数=%v，期望一次指纹查询（相同样本共用缓存）与一次文本搜索", hits)
	}
}

func TestPostgreSQLB3FingerprintGatePauseAndScan(t *testing.T) {
	b3RequireTool(t, "ROOMUSIC_TEST_FPCALC")
	for _, tc := range []struct{ code, workerState string }{
		{"curation_paused", "paused"},
		{"scan_running", "waiting_for_scan"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			f := newProgramFixture(t)
			f.enable(t)
			// 旧摘要使发行进入本轮候选，闸门应原样保留这条记录。
			if err := curation.SaveWorkState(t.Context(), f.application.database, curation.WorkState{
				Subject: "release", TargetID: f.a, RuleVersion: program.StoredRuleVersion, InputDigest: "before-change",
				Status: "no_match", Generation: 8, Result: json.RawMessage(`{"method":"search","fingerprint":"used"}`),
			}); err != nil {
				t.Fatal(err)
			}
			before := f.stateFor(t, "release", f.a)
			var gateScans int
			gate := curation.NewSources(f.application.database,
				curation.SourceRegistration{Declaration: acoustid.Declaration()},
			).WithScanCheck(func(context.Context) (bool, error) {
				gateScans++
				return tc.code == "scan_running", nil
			})
			// 工作循环刚检查过总设置与扫描。使变化只由 AcoustID 闸门观察到。
			worker := program.NewWorker(f.application.database, f.writer, gate, func(ctx context.Context) (bool, error) {
				if tc.code == "curation_paused" {
					_, err := f.writer.SetPaused(ctx, f.admin.id, true)
					return false, err
				}
				return false, nil
			}, nil, f.clock.options())
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			go func() { defer close(done); worker.Run(ctx) }()
			t.Cleanup(func() {
				cancel()
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					t.Error("工作者未退出")
				}
			})
			f.clock.wait(t)
			if got := worker.State(); got != tc.workerState {
				t.Errorf("工作者状态=%s，期望 %s", got, tc.workerState)
			}
			if tc.code == "scan_running" && gateScans != 1 {
				t.Errorf("扫描检查次数=%d，期望 1", gateScans)
			}
			curationCRequireProblem(t, gate.Check(t.Context(), "acoustid"), http.StatusConflict, tc.code)
			if after := f.stateFor(t, "release", f.a); !reflect.DeepEqual(before, after) {
				t.Errorf("闸门拒绝时发行状态被修改，之前=%+v 之后=%+v", before, after)
			}
		})
	}
}
