package app

import (
	"context"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/coverartarchive"
	"github.com/qlqq/roomusic/backend/internal/sources/lrclib"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
	"sync/atomic"
	"testing"
	"time"
)

// 工作者进入 After 时将控制权交给测试，只有测试推进时钟才会开始下一次轮询。
type programClock struct {
	now     atomic.Int64
	waiting chan time.Duration
	tick    chan time.Time
}

func newProgramClock() *programClock {
	c := &programClock{waiting: make(chan time.Duration, 1), tick: make(chan time.Time, 1)}
	c.now.Store(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC).UnixNano())
	return c
}
func (c *programClock) options() program.Options {
	return program.Options{Now: func() time.Time { return time.Unix(0, c.now.Load()).UTC() }, After: func(d time.Duration) <-chan time.Time { c.waiting <- d; return c.tick }, PollInterval: time.Minute, ScanInterval: 30 * time.Second, FullPassInterval: 6 * time.Hour}
}
func (c *programClock) wait(t *testing.T) time.Duration {
	t.Helper()
	select {
	case d := <-c.waiting:
		return d
	case <-time.After(10 * time.Second):
		t.Fatal("工作者未进入下一次等待")
		return 0
	}
}
func (c *programClock) advance(t *testing.T, d time.Duration) {
	t.Helper()
	c.tick <- time.Unix(0, c.now.Add(int64(d)))
	c.wait(t)
}

// 每种能力只返回夹具中显式登记的候选。计数只在闸门放行之后增加。
type programSource struct {
	calls                    map[string]int
	releases                 map[string]sources.Release
	barcode, catalog, search map[string][]sources.Release
	recordings               map[string]sources.Recording
	isrc                     map[string][]sources.Recording
	disc                     sources.DiscMatch
	cover                    sources.CoverFile
	hook                     func(string) error
}

func newProgramSource() *programSource {
	return &programSource{calls: map[string]int{}, releases: map[string]sources.Release{}, barcode: map[string][]sources.Release{}, catalog: map[string][]sources.Release{}, search: map[string][]sources.Release{}, recordings: map[string]sources.Recording{}, isrc: map[string][]sources.Recording{}}
}
func (s *programSource) call(method string) error {
	s.calls[method]++
	if s.hook != nil {
		return s.hook(method)
	}
	return nil
}
func (s *programSource) ReleaseByID(_ context.Context, id string) (sources.Release, error) {
	return s.releases[id], s.call("ReleaseByID")
}
func (s *programSource) ReleasesByBarcode(_ context.Context, b string) ([]sources.Release, error) {
	return s.barcode[b], s.call("ReleasesByBarcode")
}
func (s *programSource) ReleasesByCatalog(_ context.Context, n, l string) ([]sources.Release, error) {
	return s.catalog[n+"/"+l], s.call("ReleasesByCatalog")
}
func (s *programSource) ReleasesByDisc(context.Context, sources.DiscTOC) (sources.DiscMatch, error) {
	return s.disc, s.call("ReleasesByDisc")
}
func (s *programSource) SearchReleases(_ context.Context, q sources.ReleaseQuery) ([]sources.Release, error) {
	return s.search[q.Title], s.call("SearchReleases")
}
func (s *programSource) RecordingByID(_ context.Context, id string) (sources.Recording, error) {
	return s.recordings[id], s.call("RecordingByID")
}
func (s *programSource) RecordingsByISRC(_ context.Context, id string) ([]sources.Recording, error) {
	return s.isrc[id], s.call("RecordingsByISRC")
}
func (s *programSource) SearchRecordings(context.Context, sources.RecordingQuery) ([]sources.Recording, error) {
	return nil, s.call("SearchRecordings")
}
func (s *programSource) Covers(context.Context, sources.CoverTarget) ([]sources.CoverImage, error) {
	var images []sources.CoverImage
	if s.cover.Image.ID != "" {
		images = []sources.CoverImage{s.cover.Image}
	}
	return images, s.call("Covers")
}
func (s *programSource) FrontCover(context.Context, sources.CoverTarget) (sources.CoverFile, error) {
	return s.cover, s.call("FrontCover")
}
func (s *programSource) LyricsFor(_ context.Context, q sources.LyricsQuery) (sources.Lyrics, error) {
	return sources.Lyrics{ID: "7", Title: q.Title, Artist: q.Artist, DurationSeconds: float64(q.DurationSeconds), Plain: "合成歌词"}, s.call("LyricsFor")
}
func (s *programSource) SearchLyrics(context.Context, sources.LyricsQuery) ([]sources.Lyrics, error) {
	return nil, s.call("SearchLyrics")
}

type programFixture struct {
	*curationFixture
	source *programSource
	gate   *curation.Sources
	clock  *programClock
	scan   atomic.Bool
	worker *program.Worker
	stop   context.CancelFunc
	done   chan struct{}
}

func newProgramFixture(t *testing.T) *programFixture {
	t.Helper()
	f := &programFixture{curationFixture: newCurationFixture(t), source: newProgramSource(), clock: newProgramClock()}
	// 留下一张可见发行，其他形态由用例逐个加入。
	mustExec(t, f.application, `UPDATE tracks SET source_status='missing' WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, f.b)
	f.gate = curation.NewSources(f.application.database,
		curation.SourceRegistration{Declaration: musicbrainz.Declaration(), Releases: f.source, Recordings: f.source},
		curation.SourceRegistration{Declaration: lrclib.Declaration(), Lyrics: f.source},
		curation.SourceRegistration{Declaration: coverartarchive.Declaration(), Covers: f.source}).WithScanCheck(f.scanCheck)
	return f
}
func (f *programFixture) scanCheck(context.Context) (bool, error) { return f.scan.Load(), nil }
func (f *programFixture) start(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	f.stop = cancel
	f.done = done
	f.worker = program.NewWorker(f.application.database, f.writer, f.gate, f.scanCheck, nil, f.clock.options())
	go func() { defer close(done); f.worker.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("工作者未退出")
		}
	})
}
func (f *programFixture) run(t *testing.T) { t.Helper(); f.start(t); f.clock.wait(t) }
func (f *programFixture) stateFor(t *testing.T, subject, id string) curation.WorkState {
	t.Helper()
	states, err := curation.LoadWorkStates(t.Context(), f.application.database, subject, []string{id})
	if err != nil {
		t.Fatal(err)
	}
	return states[id]
}
func programTag(t *testing.T, app *integrationTestApplication, track, kind, value string) {
	t.Helper()
	mustExec(t, app, `INSERT INTO track_identifiers(track_id,kind,value,ordinal) VALUES($1,$2,$3,1)`, track, kind, value)
}
func programAlbum(title, artist string) sources.Release {
	ms := int64(100000)
	return sources.Release{ID: uuid.New(), Title: title, Artists: []sources.Credit{{Name: artist}}, Group: sources.Group{ID: uuid.New()}, Date: "2020-03-01", Labels: []sources.Label{{Name: "唱片社", CatalogNumber: "CAT-7"}}, Barcode: "123456789012", Media: []sources.Medium{{Position: 1, Format: "CD", Tracks: []sources.Track{{Position: 1, ID: uuid.New(), RecordingID: uuid.New(), LengthMS: &ms}}}}}
}
func (f *programFixture) tagged(t *testing.T) (string, sources.Release) {
	t.Helper()
	track, _ := externalTrack(t, f.application, f.a)
	r := programAlbum("Album A", "Artist A")
	f.source.releases[r.ID] = r
	programTag(t, f.application, track, "mb_release", r.ID)
	return track, r
}
func programString(t *testing.T, app *integrationTestApplication, query string, args ...any) string {
	t.Helper()
	var value string
	if err := app.database.QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return value
}
func (f *programFixture) sourceEnabled(t *testing.T, id string, enabled bool) {
	t.Helper()
	if _, err := f.gate.SetEnabled(t.Context(), id, f.admin.id, enabled); err != nil {
		t.Fatal(err)
	}
}
func (f *programFixture) linkOwner(t *testing.T, id string) string {
	t.Helper()
	return programString(t, f.application, `SELECT operation_id FROM external_links WHERE object_type='release' AND object_id=$1`, id)
}
func (f *programFixture) assertRelease(t *testing.T, id, status, method string) curation.WorkState {
	t.Helper()
	s := f.stateFor(t, "release", id)
	if s.Status != status || s.RuleVersion != program.StoredRuleVersion {
		t.Fatalf("发行 %s 期望 %s，状态 %+v", id, status, s)
	}
	if got := programString(t, f.application, `SELECT result->>'method' FROM curation_work_states WHERE subject='release' AND target_id=$1`, id); got != method {
		t.Fatalf("发行 %s 方法 %q，期望 %q", id, got, method)
	}
	return s
}
func (f *programFixture) assertCount(t *testing.T, want int, query string, args ...any) {
	t.Helper()
	if n := countRows(t, f.application, query, args...); n != want {
		t.Fatalf("%s: 得到 %d，期望 %d", query, n, want)
	}
}
