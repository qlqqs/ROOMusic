package app

import (
	"reflect"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQL整理同名跨组用四个识别请求定案(t *testing.T) {
	artist := metricArtist{ID: uuid.New(), Name: "试音艺人"}
	s := cMatchAlbum("同名的三张专辑", artist.Name, 5, artist.ID)
	s.External.Artists = []metricArtist{artist}
	want := s.External.Releases[0].ID
	for range 2 {
		other := cMatchAlbum(s.Local.Album, artist.Name, 5, artist.ID).External.Releases[0]
		other.Tracks[0].Length += 20000
		s.External.Releases = append(s.External.Releases, other)
	}

	clock := newProgramClock()
	app, mock, _ := newCurationMockApplication(t, writeMetricCatalog(t, []metricSample{s}), clock)
	admin := app.createUser(t, "admin")
	b3Root(t, app)
	writeMetricSample(t, app.allowedRoot, s)
	scanAndConfirmCuration(t, app, &admin)
	publishSearch(t, app)
	entry := metricScannedEntry(t, app, s)
	b3Enable(t, app, admin)
	clock.advance(t, time.Minute)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1 AND external_id=$2`, entry.release, want)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_pending_items WHERE kind='release_match' AND target_id=$1`, entry.release)

	// 新艺人允许读取一次；其余 MusicBrainz 请求应只有一次搜索及每个候选各一次详情。
	const prefix = "/musicbrainz/ws/2/"
	requests := map[string]int{}
	artistReads := 0
	for _, req := range cMatchRequests(t, mock) {
		if req.Service != "musicbrainz" {
			continue
		}
		if req.Path == prefix+"artist/"+artist.ID {
			artistReads++
			continue
		}
		requests[req.Path]++
	}
	expected := map[string]int{prefix + "release": 1}
	for _, release := range s.External.Releases {
		expected[prefix+"release/"+release.ID] = 1
	}
	if artistReads > 1 || !reflect.DeepEqual(requests, expected) {
		t.Fatalf("同名跨组实际请求 %v，期望 %v，另读取艺人 %d 次", requests, expected, artistReads)
	}
}

func TestPostgreSQL整理唯一候选撤销后不调用模型(t *testing.T) {
	artist := metricArtist{ID: uuid.New(), Name: "试音艺人"}
	s := cMatchAlbum("撤销后保持原样", artist.Name, 5, artist.ID)
	s.External.Artists = []metricArtist{artist}
	remote := s.External.Releases[0].ID
	s.Local.Tags = map[string]string{"MUSICBRAINZ_ALBUMID": remote}

	clock, endpoint := newProgramClock(), newMetricsModel(t)
	clock.now.Store(time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond).UnixNano())
	app, _, upstream := newCurationMockApplication(t, writeMetricCatalog(t, []metricSample{s}), clock, patrolModelOptions(clock))
	admin := app.createUser(t, "admin")
	b3Root(t, app)
	writeMetricSample(t, app.allowedRoot, s)
	scanAndConfirmCuration(t, app, &admin)
	publishSearch(t, app)
	entry := metricScannedEntry(t, app, s)
	endpoint.configure(t, app, &admin, map[string]metricsModelTarget{entry.release: {SampleID: s.ID, ReleaseID: remote}}, upstream)
	setPatrolTestSource(t, app, &admin, "web_search", false)
	b3Enable(t, app, admin)
	metricDrain(t, app, &admin, clock, endpoint, entry)
	before := endpoint.usage()
	if len(before) == 0 {
		t.Fatal("首次整理应实际调用巡检模型")
	}
	operation := programString(t, app, `SELECT operation_id FROM external_links WHERE object_type='release' AND object_id=$1 AND external_id=$2`, entry.release, remote)
	cMatchFixture(app, admin).undo(t, operation)
	metricDrain(t, app, &admin, clock, endpoint, entry)
	if state := b3WorkState(t, app, "release", entry.release); state.Status != "no_usable_candidates" {
		t.Fatalf("撤销唯一候选后的状态 %+v", state)
	}
	b3RequireCount(t, app, 0, `SELECT count(*) FROM curation_pending_items WHERE kind='release_match' AND target_id=$1`, entry.release)
	b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1`, entry.release)
	if after := endpoint.usage(); !reflect.DeepEqual(after, before) {
		t.Errorf("无可用候选时产生了模型用量：%v → %v", before, after)
	}
}
