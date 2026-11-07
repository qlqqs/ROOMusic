package governance

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

const (
	artistProjectionScan       = "ZuluScan"
	artistProjectionCorrection = "AlphaCorrection"
	artistProjectionManual     = "PinnedArtist"
	artistProjectionTrack      = "TrackSolo"
)

// 复用旧修正夹具，图的建立也经过真实修正命令，不用直接 Refresh 掩盖遗漏的调用点。
// 预先拆出发行 scope 再合并两个名字，留下非空的绑定、scope、redirect 和人工主名意图。
type artistCorrectionProjection struct {
	*correctionFixture
	artists                         *artistFixture
	artistID, trackArtistID, intent string
}

func newArtistCorrectionProjection(t *testing.T, f *correctionFixture, releases ...placementRelease) *artistCorrectionProjection {
	t.Helper()
	p := &artistCorrectionProjection{correctionFixture: f}
	p.artists = &artistFixture{effectiveFixture: f.effectiveFixture, port: &artistResolutionTestPort{}}
	p.artists.governor = NewArtistGovernance(p.artists.port)
	for _, r := range releases {
		f.exec(`UPDATE releases SET artist=$2,album_artist=$2 WHERE id=$1::uuid`, r.releaseID, artistProjectionScan)
		f.exec(`UPDATE tracks SET artist=$2 WHERE medium_id=$1::uuid`, r.mediumID, artistProjectionTrack)
		f.mustCorrect(r.releaseID, f.revisionOf(r.releaseID), map[string]CorrectionChange{"genre": {Mode: catalog.CorrectionSet, Genres: []string{"图回归初始化"}}})
	}
	first := releases[0]
	p.correct(first, catalog.CorrectionSet)
	p.correct(first, catalog.CorrectionClear)
	// 这些小夹具的所有发行均已由上述真实命令写入投影，可以发布读取能力。
	f.exec(`UPDATE catalog_projection_state SET status='ready',manifest_sealed=true,published_generation=target_generation,
 published_textnorm_version=textnorm_version,published_search_projection_version=search_projection_version WHERE capability IN ('artists','search')`)
	var scanID, scanKey, correctionID string
	if err := f.database.QueryRow(`SELECT initial_artist_id::text,id::text FROM artist_name_keys WHERE normalized_key='zuluscan'`).Scan(&scanID, &scanKey); err != nil {
		t.Fatal(err)
	}
	if err := f.database.QueryRow(`SELECT initial_artist_id::text FROM artist_name_keys WHERE normalized_key='alphacorrection'`).Scan(&correctionID); err != nil {
		t.Fatal(err)
	}
	if err := f.database.QueryRow(`SELECT initial_artist_id::text FROM artist_name_keys WHERE normalized_key='tracksolo'`).Scan(&p.trackArtistID); err != nil {
		t.Fatal(err)
	}
	scope := ArtistSplitScope{Mode: "albums", NameKeyIDs: []string{scanKey}, GroupIDs: []string{first.groupID}}
	preview, err := PreviewArtistSplit(context.Background(), f.database, scanID, scope)
	if err != nil {
		t.Fatal(err)
	}
	name := artistProjectionManual
	split := ArtistSplitRequest{ArtistID: scanID, ExpectedRevision: p.artists.revision(scanID), ArtistSplitScope: scope, Basis: preview.Basis, Target: ArtistSplitTarget{Kind: "new", DisplayName: &name}}
	result, err := p.artists.run(operations.TypeArtistSplit, uuid.New(), split.Fingerprint(), p.artists.governor.SplitExecutor(split))
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
	}
	if err := json.Unmarshal(result.Response, &response); err != nil || response.Target.ID == "" {
		t.Fatalf("split response: %s, %v", result.Response, err)
	}
	p.artistID = response.Target.ID
	for _, sourceID := range []string{scanID, correctionID} {
		request := ArtistMergeRequest{ArtistID: p.artistID, ExpectedRevision: p.artists.revision(p.artistID), SourceArtistID: sourceID, SourceExpectedRevision: p.artists.revision(sourceID)}
		if _, err := p.artists.run(operations.TypeArtistMerge, uuid.New(), request.Fingerprint(), p.artists.governor.MergeExecutor(request)); err != nil {
			t.Fatal(err)
		}
	}
	p.intent = p.artists.intentSnapshot()
	return p
}

func (p *artistCorrectionProjection) correct(r placementRelease, mode string) CorrectionResult {
	p.t.Helper()
	change := CorrectionChange{Mode: mode}
	if mode == catalog.CorrectionSet {
		change.Text = artistProjectionCorrection
	}
	return p.mustCorrect(r.releaseID, p.revisionOf(r.releaseID), map[string]CorrectionChange{"album_artist": change})
}

func (p *artistCorrectionProjection) assertProfile(auto string) {
	p.t.Helper()
	var actualAuto, display string
	if err := p.database.QueryRow(`SELECT auto_display_name,effective_display_name FROM artist_profiles WHERE artist_id=$1::uuid`, p.artistID).Scan(&actualAuto, &display); err != nil {
		p.t.Fatal(err)
	}
	if actualAuto != auto || display != artistProjectionManual {
		p.t.Fatalf("profile = %q/%q, want %q/%q", actualAuto, display, auto, artistProjectionManual)
	}
	if got := p.artists.intentSnapshot(); got != p.intent {
		p.t.Fatal("发行修正或认领改写了既有艺人意图或人工版本")
	}
	result, err := p.store.Search(context.Background(), catalog.SearchParams{Type: "artists", Query: artistProjectionManual, Page: 1, PageSize: 10})
	if err != nil {
		p.t.Fatal(err)
	}
	items, ok := result.Items.([]catalog.SearchArtistItem)
	if !ok || len(items) != 1 || items[0].Artist.ID != p.artistID || items[0].Artist.Name != artistProjectionManual {
		p.t.Fatalf("artist search: %+v", result.Items)
	}
}

func (p *artistCorrectionProjection) assertRelease(r placementRelease, name, layer string, visible bool) {
	p.t.Helper()
	rows, err := p.database.Query(`SELECT e.role,n.spelling,e.artist_id::text,c.layer FROM artist_effective_credits e
 JOIN artist_names n ON n.id=e.name_id JOIN artist_credits c ON c.id=e.credit_id WHERE e.release_id=$1::uuid ORDER BY e.role`, r.releaseID)
	if err != nil {
		p.t.Fatal(err)
	}
	type credit struct{ Role, Name, Artist, Layer string }
	actual := []credit{}
	for rows.Next() {
		var c credit
		if err := rows.Scan(&c.Role, &c.Name, &c.Artist, &c.Layer); err != nil {
			rows.Close()
			p.t.Fatal(err)
		}
		actual = append(actual, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		p.t.Fatal(err)
	}
	want := []credit{{"album_artist", name, p.artistID, layer}, {"release_artist", name, p.artistID, layer}}
	if !reflect.DeepEqual(actual, want) {
		p.t.Fatalf("effective credits for %s: %+v, want %+v", r.releaseID, actual, want)
	}
	var scanCount int
	if err := p.database.QueryRow(`SELECT count(*) FROM artist_credits c JOIN artist_names n ON n.id=c.name_id
 WHERE c.release_id=$1::uuid AND c.layer='scan' AND n.spelling=$2`, r.releaseID, artistProjectionScan).Scan(&scanCount); err != nil || scanCount != 2 {
		p.t.Fatalf("scan facts were lost: count=%d, %v", scanCount, err)
	}
	var trackID, title string
	if err := p.database.QueryRow(`SELECT t.id::text,r.title FROM tracks t JOIN releases r ON r.id=$2::uuid WHERE t.medium_id=$1::uuid ORDER BY t.id LIMIT 1`, r.mediumID, r.releaseID).Scan(&trackID, &title); err != nil {
		p.t.Fatal(err)
	}
	summaries, err := p.store.LoadTrackSummaries(context.Background(), []string{trackID})
	if err != nil {
		p.t.Fatal(err)
	}
	summary, ok := summaries[trackID]
	if !ok || summary.Present != visible {
		p.t.Fatalf("track visibility: %+v", summary)
	}
	p.assertRefs(summary.ArtistRefs, artistProjectionTrack, p.trackArtistID, artistProjectionTrack)
	p.assertRefs(summary.Release.ArtistRefs, name, p.artistID, artistProjectionManual)
	p.assertRefs(summary.Release.AlbumArtistRefs, name, p.artistID, artistProjectionManual)
	// 使用公开搜索路径验证补全结果，而不是只查投影表；missing 不应重新出现在搜索中。
	result, err := p.store.Search(context.Background(), catalog.SearchParams{Type: "albums", Query: title, Page: 1, PageSize: 10})
	if err != nil {
		p.t.Fatal(err)
	}
	found := false
	for _, item := range result.Items.([]catalog.SearchAlbumItem) {
		if item.MatchedReleaseID != r.releaseID {
			continue
		}
		found = true
		p.assertRefs(item.Group.Representative.ArtistRefs, name, p.artistID, artistProjectionManual)
		p.assertRefs(item.Group.Representative.AlbumArtistRefs, name, p.artistID, artistProjectionManual)
	}
	if found != visible {
		p.t.Fatalf("album search visible=%v, want %v: %+v", found, visible, result.Items)
	}
	result, err = p.store.Search(context.Background(), catalog.SearchParams{Type: "tracks", Query: artistProjectionTrack, Page: 1, PageSize: 10})
	if err != nil {
		p.t.Fatal(err)
	}
	found = false
	for _, item := range result.Items.([]catalog.SearchTrackItem) {
		if item.TrackID != trackID {
			continue
		}
		found = true
		p.assertRefs(item.ArtistRefs, artistProjectionTrack, p.trackArtistID, artistProjectionTrack)
		p.assertRefs(item.Release.ArtistRefs, name, p.artistID, artistProjectionManual)
		p.assertRefs(item.Release.AlbumArtistRefs, name, p.artistID, artistProjectionManual)
	}
	if found != visible {
		p.t.Fatalf("track search visible=%v, want %v", found, visible)
	}
}

func (p *artistCorrectionProjection) assertRefs(refs []catalog.ArtistRef, name, id, display string) {
	p.t.Helper()
	want := []catalog.ArtistRef{{ArtistID: id, Name: name, DisplayName: display}}
	if !reflect.DeepEqual(refs, want) {
		p.t.Fatalf("refs=%+v, want %+v", refs, want)
	}
}

func TestPostgreSQLArtistCorrectionRefreshesGraphAndSearch(t *testing.T) {
	f := newCorrectionFixture(t)
	release := f.visibleRelease("修正图回归")
	p := newArtistCorrectionProjection(t, f, release)
	p.assertRelease(release, artistProjectionScan, "scan", true)
	p.assertProfile(artistProjectionScan)
	set := p.correct(release, catalog.CorrectionSet)
	p.assertRelease(release, artistProjectionCorrection, "correction", true)
	p.assertProfile(artistProjectionCorrection)
	// 撤销 set 恢复当前扫描层，撤销 clear 则恢复人工层；两条方向都必须刷新图。
	f.mustRevert(CorrectionOperations(), set.OperationID)
	p.assertRelease(release, artistProjectionScan, "scan", true)
	p.assertProfile(artistProjectionScan)
	p.correct(release, catalog.CorrectionSet)
	clear := p.correct(release, catalog.CorrectionClear)
	p.assertRelease(release, artistProjectionScan, "scan", true)
	p.assertProfile(artistProjectionScan)
	f.mustRevert(CorrectionOperations(), clear.OperationID)
	p.assertRelease(release, artistProjectionCorrection, "correction", true)
	p.assertProfile(artistProjectionCorrection)
}
