package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
)

func TestPostgreSQL整理进行中新专辑优先(t *testing.T) {
	const first, stock, newAlbum = "首张存量专辑", "次张存量专辑", "新入库专辑"
	f := newProgramFixture(t)
	app := f.application
	admin := &f.admin
	mustExec(t, app, `UPDATE tracks SET source_status='missing'`)
	firstRequest, releaseFirst := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var order []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		for _, title := range []string{first, stock, newAlbum} {
			if !strings.Contains(query, title) {
				continue
			}
			mu.Lock()
			isNew := !slices.Contains(order, title)
			if isNew {
				order = append(order, title)
			}
			mu.Unlock()
			if isNew && title == first {
				close(firstRequest)
				select {
				case <-releaseFirst:
				case <-r.Context().Done():
					return
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"count":0,"releases":[],"release-groups":[],"recordings":[],"artists":[]}`)
	}))
	t.Cleanup(server.Close)
	unblock := sync.OnceFunc(func() { close(releaseFirst) })
	t.Cleanup(unblock)
	mb := musicbrainz.New(sources.Options{
		BaseURL: server.URL + "/mb", DataDirectory: app.dataDirectory, NewClient: b3HTTPClient(server),
		Limiter: sources.NewLimiter(0, nil, nil),
	})
	f.gate = curation.NewSources(app.database,
		curation.SourceRegistration{Declaration: musicbrainz.Declaration(), Releases: mb, Recordings: mb},
	).WithScanCheck(f.scanCheck)
	// 扫描让路由既有用例覆盖。这里让首项等待扫描完成，以核对下一次领取。
	add := func(title string, seed byte) {
		t.Helper()
		writeSyntheticTrack(t, filepath.Join(app.allowedRoot, title, "01.flac"), seed,
			"TITLE=合成曲名", "ARTIST=合成艺人", "ALBUM="+title, "TRACKNUMBER=1")
	}
	id := func(title string) string {
		t.Helper()
		return programString(t, app, `SELECT id FROM releases WHERE relative_directory=$1`, title)
	}
	add(first, 51)
	add(stock, 52)
	runScan(t, app, admin)
	mustExec(t, app, `UPDATE releases SET created_at=now()-interval '1 day' WHERE id=$1`, id(stock))
	mustExec(t, app, `UPDATE releases SET curation_changed_at=now()-interval '60 seconds' WHERE id IN ($1,$2)`, id(first), id(stock))
	runScan(t, app, admin)
	f.enable(t)
	f.start(t)
	select {
	case <-firstRequest:
	case <-time.After(10 * time.Second):
		t.Fatal("第一张存量专辑未进入识别")
	}
	add(newAlbum, 53)
	runScan(t, app, admin)
	confirmProgramRelease(t, app, admin, id(newAlbum))
	unblock()
	f.clock.wait(t)
	mu.Lock()
	got := slices.Clone(order)
	mu.Unlock()
	if want := []string{first, newAlbum, stock}; !slices.Equal(got, want) {
		t.Fatalf("首项出站顺序 %v，期望 %v", got, want)
	}
	for _, title := range got {
		if s := b3WorkState(t, app, "release", id(title)); s.Status != "unlisted" || s.Attempts != 1 {
			t.Fatalf("%s 应处理一次并结案：%+v", title, s)
		}
	}
}

func TestPostgreSQL整理安静窗口只发一次确认扫描(t *testing.T) {
	const album = "正在拷贝的专辑"
	clock := newProgramClock()
	app, mock, _ := newCurationMockApplication(t, "", clock)
	admin := app.createUser(t, "admin")
	b3Root(t, app)
	b3Enable(t, app, admin)
	dir := filepath.Join(app.allowedRoot, album)
	writeSyntheticTrack(t, filepath.Join(dir, "01.flac"), 61,
		"TITLE=已拷贝的曲目", "ARTIST=合成艺人", "ALBUM="+album, "TRACKNUMBER=1")
	partial := filepath.Join(dir, "02.flac")
	mark := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	requireCurationOK(t, os.WriteFile(partial, []byte("fLaC"), 0o644))
	requireCurationOK(t, os.Chtimes(partial, mark, mark))
	runScan(t, app, &admin)
	requirePending := func() {
		t.Helper()
		var p curation.Progress
		getJSON(t, app, &admin, curationBase+"/progress", &p)
		q := p.Queues[0]
		if q.ID != "release" || q.Remaining != 1 || q.Ready != 0 ||
			q.PauseReason == nil || *q.PauseReason != "waiting_for_stability" || q.ETASeconds != nil ||
			p.Releases.Total != 1 || p.Releases.Processed != 0 || len(metricCounters(t, mock)) != 0 {
			t.Fatalf("尚在拷贝时的识别队列 %+v，发行进度 %+v，出站 %v", q, p.Releases, metricCounters(t, mock))
		}
	}
	requireScans := func(want int) {
		t.Helper()
		b3RequireCount(t, app, want, `SELECT count(*) FROM scan_runs WHERE trigger_kind='scheduled' AND scan_mode='incremental'`)
		b3RequireCount(t, app, want, `SELECT count(*) FROM scan_runs WHERE trigger_kind='scheduled'`)
	}
	scanDue := func() {
		t.Helper()
		mustExec(t, app, `UPDATE releases SET curation_changed_at=now()-interval '60 seconds'
			WHERE relative_directory=$1 AND NOT curation_confirmed`, album)
		clock.advance(t, time.Minute)
		scan := programString(t, app, `SELECT id FROM scan_runs WHERE trigger_kind='scheduled' ORDER BY started_at DESC LIMIT 1`)
		if status := waitForScanToFinish(t, app, scan); status != "succeeded" {
			t.Fatalf("确认扫描结束于 %s", status)
		}
		clock.advance(t, time.Minute)
	}

	clock.advance(t, time.Minute)
	requireScans(0)
	requirePending()
	scanDue()
	requireScans(1)
	requirePending()
	clock.advance(t, time.Minute)
	requireScans(1)

	// 完成第二首的拷贝，沿用同一份旧 mtime。成员变化需要新的静默窗口。
	writeSyntheticTrack(t, partial, 62, "TITLE=拷贝完成的曲目", "ARTIST=合成艺人", "ALBUM="+album, "TRACKNUMBER=2")
	requireCurationOK(t, os.Chtimes(partial, mark, mark))
	runScan(t, app, &admin)
	clock.advance(t, time.Minute)
	requireScans(1)
	requirePending()
	scanDue()
	requireScans(2)
	clock.advance(t, time.Minute)
	requireScans(2)
	id := programString(t, app, `SELECT r.id FROM releases r JOIN media m ON m.release_id=r.id
		JOIN tracks t ON t.medium_id=m.id WHERE t.relative_path=$1 AND t.source_status='present'`, album+"/02.flac")
	if s := b3WorkState(t, app, "release", id); s.Status != "unlisted" || s.Attempts != 1 {
		t.Fatalf("稳定确认后应进入识别：%+v", s)
	}
	if len(metricCounters(t, mock)) == 0 {
		t.Fatal("稳定确认后未查询受控来源")
	}
}
