package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
	"github.com/qlqq/roomusic/backend/internal/steward/web"
)

func registerTestSource(f *curationFixture, client *musicbrainz.Client) *curation.Sources {
	return curation.NewSources(f.application.database, curation.SourceRegistration{Declaration: musicbrainz.Declaration(), Artists: client, Releases: client, Recordings: client})
}

func TestPostgreSQLCurationSourcesGate(t *testing.T) {
	f := newCurationFixture(t)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		const artist = `{"id":"11111111-1111-3111-8111-111111111111","name":"合成艺人"}`
		if strings.HasSuffix(r.URL.Path, "/artist") {
			io.WriteString(w, `{"artists":[`+artist+`]}`)
		} else {
			io.WriteString(w, artist)
		}
	}))
	defer server.Close()
	client := musicbrainz.New(musicbrainz.Options{DataDirectory: t.TempDir(), BaseURL: server.URL, Limiter: sources.NewLimiter(0, nil, nil), NewClient: func(egress.Options) (*http.Client, error) { return server.Client(), nil }})
	gate := registerTestSource(f, client)
	finder, ok := gate.ArtistFinder("musicbrainz")
	if !ok {
		t.Fatal("缺少艺人查找能力")
	}
	releases, registeredReleases := gate.ReleaseFinder("musicbrainz")
	recordings, registeredRecordings := gate.RecordingFinder("musicbrainz")
	if !registeredReleases || !registeredRecordings {
		t.Fatal("缺少发行或录音能力")
	}
	ctx := t.Context()
	for range 2 {
		artists, err := client.SearchArtists(ctx, "已有缓存")
		if err != nil || len(artists) != 1 {
			t.Fatalf("预热缓存：%v %v", artists, err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("预热缓存出站 %d 次", hits.Load())
	}

	tools := web.New(nil, nil, nil, nil).WithMusic(client).StewardTools()
	check := func(reason, code string) {
		t.Helper()
		before := hits.Load()
		for _, q := range []string{"已有缓存", "冷查询"} {
			artists, err := finder.SearchArtists(ctx, q)
			requireCurationProblem(t, err, code, reason)
			if len(artists) != 0 {
				t.Fatalf("拒绝时仍返回 %v", artists)
			}
		}
		release, err := releases.ReleaseByID(ctx, uuid.New())
		requireCurationProblem(t, err, code, reason)
		tracks, err := recordings.RecordingsByISRC(ctx, "USABC2300001")
		requireCurationProblem(t, err, code, reason)
		if release.ID != "" || len(tracks) != 0 {
			t.Fatalf("拒绝时仍返回发行或录音：%+v %+v", release, tracks)
		}
		if n := hits.Load() - before; n != 0 {
			t.Fatalf("拒绝时出站 %d 次", n)
		}
		for name, raw := range map[string]string{
			"search_music_metadata": `{"entity":"artist","query":"` + uuid.New() + `"}`,
			"get_music_metadata":    `{"entity":"artist","mbid":"` + uuid.New() + `"}`,
		} {
			i := slices.IndexFunc(tools, func(d tool.Definition) bool { return d.Name == name })
			if i < 0 {
				t.Fatal(name)
			}
			out, err := tools[i].Handler(ctx, tool.Caller{User: identity.User{ID: f.ordinary.id}}, json.RawMessage(raw))
			if err != nil || !strings.Contains(string(out), "合成艺人") {
				t.Fatalf("%s：%s %v", name, out, err)
			}
		}
		if n := hits.Load() - before; n != 2 {
			t.Fatalf("两个对话工具合计出站 %d 次，期望 2", n)
		}
	}
	check("开关", "curation_disabled")
	f.enable(t)
	toggle := func(enabled bool) {
		t.Helper()
		if _, err := gate.SetEnabled(ctx, "musicbrainz", f.admin.id, enabled); err != nil {
			t.Fatal(err)
		}
	}
	toggle(false)
	check("外部源", "source_disabled")
	toggle(true)
	mustExec(t, f.application, `UPDATE users SET disabled_at=now() WHERE id=$1`, f.admin.id)
	check("开启者", "curation_disabled")
	mustExec(t, f.application, `UPDATE users SET disabled_at=NULL,role='user' WHERE id=$1`, f.admin.id)
	check("开启者", "curation_disabled")
}

func TestPostgreSQLCurationSourcesGateDuringWait(t *testing.T) {
	for _, stage := range []string{"queue", "retry"} {
		t.Run(stage, func(t *testing.T) {
			f := newCurationFixture(t)
			f.enable(t)
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if hits.Add(1) == 1 && stage == "retry" {
					w.WriteHeader(503)
					return
				}
				io.WriteString(w, `{"artists":[{"id":"result"}]}`)
			}))
			defer server.Close()
			var elapsed atomic.Int64
			now := func() time.Time { return time.Unix(0, elapsed.Load()) }
			waiting := make(chan time.Duration, 1)
			resume := make(chan time.Time)
			after := func(d time.Duration) <-chan time.Time { waiting <- d; return resume }
			opts := musicbrainz.Options{BaseURL: server.URL, Limiter: sources.NewLimiter(0, now, nil), NewClient: func(egress.Options) (*http.Client, error) { return server.Client(), nil }}
			if stage == "queue" {
				opts.Limiter = sources.NewLimiter(time.Second, now, after)
			} else {
				opts.After = after
			}
			client := musicbrainz.New(opts)
			gate := registerTestSource(f, client)
			finder, ok := gate.ArtistFinder("musicbrainz")
			if !ok {
				t.Fatal("缺少艺人查找能力")
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if stage == "queue" {
				if _, err := finder.SearchArtists(ctx, "占用上一秒"); err != nil {
					t.Fatal(err)
				}
			}
			done := make(chan error, 1)
			go func() { _, err := finder.SearchArtists(ctx, "等待时关闭"); done <- err }()
			select {
			case d := <-waiting:
				elapsed.Add(int64(d))
			case err := <-done:
				t.Fatalf("尚未等待即返回 %v", err)
			}
			if _, err := f.writer.SetSettings(ctx, f.admin.id, false); err != nil {
				t.Fatal(err)
			}
			close(resume)
			err := <-done
			if n := hits.Load(); n != 1 {
				t.Errorf("关掉开关后又收到 %d 次请求", n-1)
			}
			requireCurationProblem(t, err, "curation_disabled", "开关")
		})
	}
}

func TestPostgreSQLCurationSourcesSettings(t *testing.T) {
	f := newCurationFixture(t)
	path := curationBase + "/sources"
	toggle := func(enabled bool) {
		t.Helper()
		r := f.application.request(t, "PUT", path+"/musicbrainz", map[string]bool{"enabled": enabled}, &f.admin, nil)
		if r.Code != 200 {
			t.Fatalf("PUT %d %s", r.Code, r.Body.String())
		}
	}
	check := func(enabled, active bool, reason string) {
		t.Helper()
		var list struct{ Items []curation.SourceSettings }
		getJSON(t, f.application, &f.admin, path, &list)
		if len(list.Items) != 1 {
			t.Fatal(list)
		}
		item := list.Items[0]
		if item.ID != "musicbrainz" || item.Enabled != enabled || item.Active != active || (reason == "" && item.InactiveReason != nil) || (reason != "" && (item.InactiveReason == nil || *item.InactiveReason != reason)) {
			t.Fatalf("期望 enabled=%t active=%t reason=%q，实际 %+v", enabled, active, reason, item)
		}
	}
	check(true, false, "curation_disabled")
	f.enable(t)
	check(true, true, "")
	toggle(false)
	check(false, false, "source_disabled")
	toggle(true)
	check(true, true, "")
	if _, err := f.writer.SetSettings(t.Context(), f.admin.id, false); err != nil {
		t.Fatal(err)
	}
	check(true, false, "curation_disabled")
	requireAPIError(t, f.application.request(t, "GET", path, nil, &f.ordinary, nil), 403, "forbidden")
	requireAPIError(t, f.application.request(t, "PUT", path+"/musicbrainz", map[string]bool{"enabled": false}, &f.ordinary, nil), 403, "forbidden")
	requireAPIError(t, f.application.request(t, "PUT", path+"/unknown", map[string]bool{"enabled": false}, &f.admin, nil), 404, "not_found")
}

func TestPostgreSQLCurationSourcesLinks(t *testing.T) {
	flow := newClaimFlow(t)
	ctx := t.Context()
	db := flow.application.database
	old, _ := flow.releaseIDs(t, "a")
	initial := curation.Link{Source: "musicbrainz", ObjectType: "release", ObjectID: old, ExternalID: "fixture-one", Method: "barcode", Confidence: "medium"}
	put := func(link curation.Link, want *curation.Link) {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		previous, err := curation.PutLink(ctx, tx, link)
		if err != nil || (want == nil && previous != nil) || (want != nil && (previous == nil || *previous != *want)) {
			t.Fatalf("PutLink 旧行=%+v，期望 %+v，错误 %v", previous, want, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	put(initial, nil)
	flow.correct(t, old, "我的合成发行")
	current := initial
	current.ExternalID = "fixture-two"
	current.Method = "toc"
	current.Confidence = "high"
	if err := db.QueryRowContext(ctx, "SELECT operation_id::text FROM release_corrections WHERE release_id=$1 AND field='title'", old).Scan(&current.OperationID); err != nil {
		t.Fatal(err)
	}
	put(current, &initial)
	check := func() {
		t.Helper()
		links, err := curation.LinksOf(ctx, db, "release", []string{old})
		if err != nil || !slices.Equal(links, []curation.Link{current}) {
			t.Fatalf("对象读取=%+v %v", links, err)
		}
		for id, want := range map[string][]string{initial.ExternalID: nil, current.ExternalID: {old}} {
			ids, err := curation.ObjectsLinkedTo(ctx, db, current.Source, current.ObjectType, id)
			if err != nil || !slices.Equal(ids, want) {
				t.Fatalf("反查 %s=%v %v", id, ids, err)
			}
		}
	}
	check()
	// 用改路径接口登记移动后的合成库，再从新位置扫描同一发行。
	var rootID string
	var revision int64
	if err := db.QueryRowContext(ctx, "SELECT id::text,revision FROM library_roots WHERE path=$1", flow.library).Scan(&rootID, &revision); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(filepath.Dir(flow.library), "moved")
	if err := os.Rename(flow.library, moved); err != nil {
		t.Fatal(err)
	}
	response := relocateRootRequest(t, flow.application, flow.admin, rootID, moved, revision, "move-fixture")
	requireRelocatedRoot(t, response, rootID, moved, "active", revision+1, []string{})
	flow.library = moved
	runScan(t, flow.application, &flow.admin)
	check()
	// 在同一发行内改文件名并重扫，确认身份沿用。
	dir := filepath.Join(flow.library, "a")
	if err := os.Rename(filepath.Join(dir, trackName(1)), filepath.Join(dir, "renamed.flac")); err != nil {
		t.Fatal(err)
	}
	runScan(t, flow.application, &flow.admin)
	if visible, _ := flow.releaseIDs(t, "a"); visible != old {
		t.Fatalf("改名后发行=%s，原来为 %s", visible, old)
	}
	check()
	if err := os.Remove(filepath.Join(dir, "renamed.flac")); err != nil {
		t.Fatal(err)
	}
	flow.writeSet(t, "a", 100, "Replacement")
	for n := 1; n <= 3; n++ {
		future := time.Now().Add(time.Minute)
		if err := os.Chtimes(filepath.Join(dir, trackName(n)), future, future); err != nil {
			t.Fatal(err)
		}
	}
	runScan(t, flow.application, &flow.admin)
	fresh, hidden := flow.releaseIDs(t, "a")
	if fresh == "" || hidden != old || fresh == old {
		t.Fatalf("重扫后新旧发行=%q, %q", fresh, hidden)
	}
	check()
	preview := flow.get(t, claimPreviewPath(fresh, old))
	flow.post(t, claimPath(fresh), claimBody(int64(preview["revision"].(float64)), int64(preview["source_revision"].(float64)), old, preview["token"].(string)))
	check()
}
