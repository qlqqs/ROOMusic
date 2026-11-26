package app

import (
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/curation"
)

func TestPostgreSQL整理无标签编号目录中的专辑名与艺人(t *testing.T) {
	const artist, title = "Tori Amos", "Boys for Pele"
	sample := metricSample{ID: "无标签编号目录", Local: metricLocal{
		Directory: "06 - Tori Amos - Boys for Pele", Format: "flac",
		Tracks: []metricLocalTrack{
			{File: "01-01 - Beauty Queen - Horses.flac", Seconds: 368},
			{File: "01-02 - Blood Roses.flac", Seconds: 239},
		},
	}}
	clock, endpoint := newProgramClock(), newMetricsModel(t)
	app, _, upstream := newCurationMockApplication(t, writeMetricCatalog(t, nil), clock, patrolModelOptions(clock))
	admin := app.createUser(t, "admin")
	b3Root(t, app)
	publishSearch(t, app)
	writeMetricSample(t, app.allowedRoot, sample)
	scanAndConfirmCuration(t, app, &admin)
	entry := metricScannedEntry(t, app, sample)
	load := func() catalog.CurationEvidence {
		t.Helper()
		evidence, err := catalog.LoadCurationEvidence(t.Context(), app.database, []string{entry.release})
		requireCurationOK(t, err)
		return evidence[entry.release]
	}
	before := load()
	scannedArtist := before.AlbumArtist
	if scannedArtist == "" {
		scannedArtist = before.Artist
	}
	if before.Title != "Tori Amos - Boys for Pele" || scannedArtist != "06" ||
		before.Authenticity["title"] != metadata.Inferred || before.SelectedArtistAuth() != metadata.Inferred {
		t.Fatalf("无标签扫描应保留目录推断的标题和艺人，实际为 %q、%q、%v", before.Title, scannedArtist, before.Authenticity)
	}
	endpoint.localReply = func(metricsPending) curation.LocalInput { return curation.LocalInput{Title: title, Artist: artist} }
	endpoint.configure(t, app, &admin, map[string]metricsModelTarget{entry.release: {SampleID: sample.ID}}, upstream)
	setPatrolTestSource(t, app, &admin, "musicbrainz", false)
	setPatrolTestSource(t, app, &admin, "web_search", false)
	b3Enable(t, app, admin)
	metricDrain(t, app, &admin, clock, endpoint, entry)
	if usage := endpoint.usage()[sample.ID]["local_cleanup"]; usage.Calls != 1 {
		t.Fatalf("编号目录的本地整理应调用模型一次，实际 %+v", usage)
	}
	b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1 AND kind='local_cleanup'
		AND status='resolved' AND resolution='local_cleanup' AND evidence->'title'->>'requested'='true' AND evidence->'artist'->>'requested'='true'`, entry.release)
	b3RequireCount(t, app, 2, `SELECT count(*) FROM release_auto_values v JOIN operations o ON o.id=v.operation_id
		WHERE release_id=$1 AND ((field='title' AND text_value=$2) OR (field='album_artist' AND text_value=$3))
		AND writer_level=1 AND o.origin->>'mode'='local_cleanup'`, entry.release, title, artist)
	var detail catalog.ReleaseDetail
	getJSON(t, app, &admin, "/api/v1/releases/"+entry.release, &detail)
	if detail.Title != title || detail.AlbumArtist == nil || *detail.AlbumArtist != artist {
		t.Fatalf("整理后的发行详情应显示 %s / %s，实际 %+v", title, artist, detail.ReleaseSummary)
	}
	search := curation.LocalSearchEvidence(load())
	if search.Title != title || search.AlbumArtist != artist ||
		search.FieldSources["title"] != "model_inferred" || search.FieldSources["album_artist"] != "model_inferred" {
		t.Fatalf("识别队列应取得本地整理的标题与艺人：%q、%q、%v", search.Title, search.AlbumArtist, search.FieldSources)
	}
	b3RequireCount(t, app, 0, `SELECT count(*) FROM track_auto_values WHERE track_id=ANY($1::uuid[])`, entry.tracks)
}
