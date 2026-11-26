package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
)

func TestPostgreSQL整理重启复用响应及已提交写入(t *testing.T) {
	f := newProgramFixture(t)
	_, remote := f.tagged(t)
	f.enable(t)
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/mb/release/"+remote.ID {
			t.Errorf("意外的 MusicBrainz 请求 %s", r.URL)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":%q,"title":"Album A","date":"2020","artist-credit":[{"name":"Artist A","artist":{"id":"a","name":"Artist A"}}],
			"label-info":[{"catalog-number":"CAT-7","label":{"name":"合成唱片社"}}],
			"release-group":{"id":%q},"media":[{"position":1,"track-count":1,"tracks":[
			{"id":%q,"position":1,"length":100000,"recording":{"id":%q}}]}]}`,
			remote.ID, remote.Group.ID, remote.Media[0].Tracks[0].ID, remote.Media[0].Tracks[0].RecordingID)
	}))
	t.Cleanup(upstream.Close)

	connect := func(check func(context.Context) (bool, error)) {
		mb := musicbrainz.New(sources.Options{
			BaseURL: upstream.URL + "/mb", DataDirectory: f.application.dataDirectory,
			NewClient: b3HTTPClient(upstream), Limiter: sources.NewLimiter(0, nil, nil),
		})
		f.gate = curation.NewSources(f.application.database,
			curation.SourceRegistration{Declaration: musicbrainz.Declaration(), Releases: mb, Recordings: mb},
		).WithScanCheck(check)
	}
	waitForStop := func(stage string) {
		t.Helper()
		select {
		case <-f.done:
		case <-time.After(10 * time.Second):
			t.Fatalf("未到达%s中断点，MusicBrainz 请求数 %d，状态 %+v", stage, requests.Load(), f.stateFor(t, "release", f.a))
		}
		requireUnattemptedProgramState(t, f.stateFor(t, "release", f.a))
		if n := requests.Load(); n != 1 {
			t.Fatalf("MusicBrainz 请求数 %d，期望 1", n)
		}
	}

	// 第一次中断在响应验证落盘之后、第一笔业务写入之前。
	connect(func(context.Context) (bool, error) {
		files, err := filepath.Glob(filepath.Join(f.application.dataDirectory, "sources", "cache", "musicbrainz", "*", "*.json"))
		if len(files) > 0 {
			f.stop()
			return false, context.Canceled
		}
		return false, err
	})
	f.start(t)
	waitForStop("首次写入前")
	f.assertCount(t, 0, `SELECT count(*) FROM operations WHERE target_id=$1`, f.a)

	// 重建来源和工作者。第二次中断在对应已提交、补字段与结论尚未提交时。
	connect(func(ctx context.Context) (bool, error) {
		var linked bool
		err := f.application.database.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT FROM external_links WHERE object_type='release' AND object_id=$1)`, f.a).Scan(&linked)
		if linked {
			f.stop()
			return false, context.Canceled
		}
		return false, err
	})
	f.start(t)
	waitForStop("对应写入后")
	owner := f.linkOwner(t, f.a)
	f.assertCount(t, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field='label'`, f.a)

	connect(f.scanCheck)
	f.run(t)
	state := f.assertRelease(t, f.a, "matched", "tag_id")
	if state.Attempts != 1 || state.Generation != 1 || f.linkOwner(t, f.a) != owner || requests.Load() != 1 {
		t.Fatalf("恢复后的结论 %+v，对应操作 %s，MusicBrainz 请求数 %d", state, f.linkOwner(t, f.a), requests.Load())
	}
	f.assertCount(t, 1, `SELECT count(*) FROM operations WHERE target_id=$1 AND operation_type='external_link'`, f.a)
	f.assertCount(t, 1, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field='label'`, f.a)
	f.assertCount(t, 1, `SELECT count(*) FROM operations WHERE target_id=$1 AND operation_type='release_auto_correction'`, f.a)
	f.assertCount(t, 0, `SELECT count(*) FROM curation_batches WHERE closed_at IS NULL`)
}

func TestPostgreSQL整理重启后界面成功仍按原期限探测(t *testing.T) {
	f := newProgramFixture(t)
	_, remote := f.tagged(t)
	f.enable(t)
	var available atomic.Bool
	var backgroundReads, uiReads atomic.Int32
	uiID := "30000000-0000-4000-8000-000000000002"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mb/release/" + remote.ID:
			backgroundReads.Add(1)
			if !available.Load() {
				w.Header().Set("Retry-After", "245")
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			fmt.Fprintf(w, `{"id":%q,"title":"Album A","artist-credit":[{"name":"Artist A","artist":{"id":"a","name":"Artist A"}}],
				"release-group":{"id":%q},"media":[{"position":1,"track-count":1,"tracks":[
				{"id":%q,"position":1,"length":100000,"recording":{"id":%q}}]}]}`,
				remote.ID, remote.Group.ID, remote.Media[0].Tracks[0].ID, remote.Media[0].Tracks[0].RecordingID)
		case "/mb/release/" + uiID:
			uiReads.Add(1)
			fmt.Fprintf(w, `{"id":%q,"title":"界面预览专辑","artist-credit":[{"name":"合成艺人","artist":{"id":"ui","name":"合成艺人"}}],
				"release-group":{"id":%q},"media":[{"position":1,"track-count":1,"tracks":[
				{"id":%q,"position":1,"length":100000,"recording":{"id":%q}}]}]}`,
				uiID, remote.Group.ID, remote.Media[0].Tracks[0].ID, remote.Media[0].Tracks[0].RecordingID)
		default:
			t.Errorf("意外请求 %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	reconnect := func() *musicbrainz.Client {
		mb := musicbrainz.New(sources.Options{BaseURL: upstream.URL + "/mb", Now: f.clock.options().Now,
			NewClient: b3HTTPClient(upstream), Limiter: sources.NewLimiter(0, nil, nil)})
		f.gate = curation.NewSources(f.application.database,
			curation.SourceRegistration{Declaration: musicbrainz.Declaration(), Releases: mb, Recordings: mb},
		).WithScanCheck(f.scanCheck)
		return mb
	}
	reconnect()
	f.run(t)
	until := f.clock.options().Now().Add(245 * time.Second)
	requireWaiting := func() {
		t.Helper()
		state := f.stateFor(t, "release", f.a)
		queues, err := f.worker.QueueProgress(t.Context())
		requireCurationOK(t, err)
		q := queues[0]
		if state.Status != "try_later" || state.RetryCount != 0 || state.NextAttemptAt == nil || !state.NextAttemptAt.Equal(until) ||
			q.RetryAt == nil || !q.RetryAt.Equal(until) || q.Source == nil || *q.Source != "musicbrainz" || backgroundReads.Load() != 1 {
			t.Fatalf("原期限 %s 前应只有首个后台失败，专辑 %+v，队列 %+v，后台请求 %d", until, state, q, backgroundReads.Load())
		}
	}
	requireWaiting()
	f.stop()
	select {
	case <-f.done:
	case <-time.After(10 * time.Second):
		t.Fatal("首个工作者未退出")
	}

	mb := reconnect()
	f.run(t)
	available.Store(true)
	release, err := mb.ReleaseByID(t.Context(), uiID)
	if err != nil || release.ID != uiID || uiReads.Load() != 1 {
		t.Fatalf("界面请求应真实成功一次，得到 %q，错误 %v，请求 %d", release.ID, err, uiReads.Load())
	}
	requireWaiting()
	f.clock.advance(t, 244*time.Second)
	requireWaiting()
	f.clock.advance(t, time.Second)
	state := f.assertRelease(t, f.a, "matched", "tag_id")
	if state.NextAttemptAt != nil || state.RetryCount != 0 || state.Attempts != 1 || backgroundReads.Load() != 2 {
		t.Fatalf("原期限到达后应由第 2 个后台请求恢复并完成，状态 %+v，后台请求 %d", state, backgroundReads.Load())
	}
}
