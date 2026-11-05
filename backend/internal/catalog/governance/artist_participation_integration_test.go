package governance

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLArtistParticipationReleaseAndTrackUnion(t *testing.T) {
	f := newArtistFixture(t)
	for i := 0; i < 3; i++ {
		status := "present"
		if i == 0 {
			status = "missing"
		}
		id := f.track(f.first, "发行参与曲目", status, 100.0)
		f.exec(`UPDATE tracks SET artist='Beta' WHERE id=$1::uuid`, id)
	}
	// 第一张的重复发行角色、直接曲目署名只计一次；第二张只通过作曲署名参与。
	f.exec(`INSERT INTO track_credits(id,track_id,role,name,position) VALUES($1::uuid,$2::uuid,'composer','Alpha',1)`, uuid.New(), f.trackB)
	f.refresh(f.first.releaseID, f.second.releaseID)
	releases, count, err := affectedArtistReleases(context.Background(), f.database, []string{f.alpha})
	if err != nil || count != 5 || len(releases) != 2 {
		t.Fatalf("participation count=%d releases=%v error=%v", count, releases, err)
	}
	aliases := ArtistSplitScope{Mode: "aliases", NameKeyIDs: []string{f.keyAlpha}}
	aliasPreview, err := PreviewArtistSplit(context.Background(), f.database, f.alpha, aliases)
	if err != nil || aliasPreview.Scope.TrackCount != 5 {
		t.Fatalf("alias scope %+v error=%v", aliasPreview, err)
	}
	for _, item := range []struct {
		group string
		count int
	}{{f.first.groupID, 4}, {f.second.groupID, 1}} {
		preview, err := PreviewArtistSplit(context.Background(), f.database, f.alpha, ArtistSplitScope{Mode: "albums", NameKeyIDs: []string{f.keyAlpha}, GroupIDs: []string{item.group}})
		if err != nil || preview.Scope.TrackCount != item.count {
			t.Fatalf("album scope %+v expected=%d error=%v", preview, item.count, err)
		}
	}
	// 只有发行级参与的新曲目不新增 Alpha 的 credit 行，也必须让旧预览失效。
	added := f.track(f.first, "无曲目署名的新曲目", "present", 100.0)
	f.exec(`UPDATE tracks SET artist='' WHERE id=$1::uuid`, added)
	changed, err := PreviewArtistSplit(context.Background(), f.database, f.alpha, aliases)
	if err != nil || changed.Scope.TrackCount != 6 || changed.Basis == aliasPreview.Basis {
		t.Fatalf("release-only new track did not change basis: %+v %v", changed, err)
	}
	split := ArtistSplitRequest{ArtistID: f.alpha, ExpectedRevision: 1, ArtistSplitScope: aliases, Basis: aliasPreview.Basis, Target: ArtistSplitTarget{Kind: "new"}}
	_, err = f.run(operations.TypeArtistSplit, uuid.New(), split.Fingerprint(), f.governor.SplitExecutor(split))
	assertArtistFailure(t, err, "artist_scope_changed")
	merge := ArtistMergeRequest{ArtistID: f.beta, ExpectedRevision: 1, SourceArtistID: f.alpha, SourceExpectedRevision: 1}
	result, err := f.run(operations.TypeArtistMerge, uuid.New(), merge.Fingerprint(), f.governor.MergeExecutor(merge))
	if err != nil {
		t.Fatal(err)
	}
	record, err := operations.Load(context.Background(), f.database, result.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Changes struct {
			TrackCount int `json:"track_count"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(record.After, &state); err != nil || state.Changes.TrackCount != 6 {
		t.Fatalf("merge source count=%d error=%v", state.Changes.TrackCount, err)
	}
}

func TestPostgreSQLArtistReleaseOnlyParticipationBudgetBoundary(t *testing.T) {
	f := newArtistFixture(t)
	f.exec(`UPDATE tracks SET artist='Beta' WHERE id=$1::uuid`, f.trackA)
	f.refresh(f.first.releaseID)
	var directlyCredited int
	if err := f.database.QueryRow(`SELECT count(*) FROM artist_effective_credits WHERE artist_id=$1::uuid AND track_id IS NOT NULL`, f.alpha).Scan(&directlyCredited); err != nil || directlyCredited != 0 {
		t.Fatalf("fixture is not release-only: %d %v", directlyCredited, err)
	}
	// 一次集合写入建立真实的 100,000 边界，不缩小产品预算；其中 missing 也属于治理影响范围。
	f.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind)
 SELECT gen_random_uuid(),$1::uuid,n+100,'边界曲目','',1,$2::uuid,'artist-budget/'||n||'.flac',
 CASE WHEN n%2=0 THEN 'missing' ELSE 'present' END,NOW(),'physical' FROM generate_series(1,$3::integer) n`, f.first.mediumID, f.rootID, maxArtistTracks-1)
	_, count, err := affectedArtistReleases(context.Background(), f.database, []string{f.alpha})
	if err != nil || count != maxArtistTracks {
		t.Fatalf("exact boundary count=%d error=%v", count, err)
	}
	aliases := ArtistSplitScope{Mode: "aliases", NameKeyIDs: []string{f.keyAlpha}}
	albums := ArtistSplitScope{Mode: "albums", NameKeyIDs: []string{f.keyAlpha}, GroupIDs: []string{f.first.groupID}}
	preview, err := PreviewArtistSplit(context.Background(), f.database, f.alpha, aliases)
	if err != nil || preview.Scope.TrackCount != maxArtistTracks {
		t.Fatalf("exact alias boundary count=%d error=%v", preview.Scope.TrackCount, err)
	}
	albumPreview, err := PreviewArtistSplit(context.Background(), f.database, f.alpha, albums)
	if err != nil || albumPreview.Scope.TrackCount != maxArtistTracks {
		t.Fatalf("exact album boundary count=%d error=%v", albumPreview.Scope.TrackCount, err)
	}
	extra := f.track(f.first, "超过上限", "missing", 100.0)
	f.exec(`UPDATE tracks SET artist='' WHERE id=$1::uuid`, extra)
	_, _, err = affectedArtistReleases(context.Background(), f.database, []string{f.alpha})
	assertArtistFailure(t, err, "artist_scope_too_large")
	for _, scope := range []ArtistSplitScope{aliases, albums} {
		_, err := PreviewArtistSplit(context.Background(), f.database, f.alpha, scope)
		assertArtistFailure(t, err, "artist_scope_too_large")
	}
	before := f.intentSnapshot()
	name := "不得写入的新名"
	rename := ArtistRenameRequest{ArtistID: f.alpha, ExpectedRevision: 1, Mode: "set", Name: &name}
	merge := ArtistMergeRequest{ArtistID: f.beta, ExpectedRevision: 1, SourceArtistID: f.alpha, SourceExpectedRevision: 1}
	split := ArtistSplitRequest{ArtistID: f.alpha, ExpectedRevision: 1, ArtistSplitScope: aliases, Basis: preview.Basis, Target: ArtistSplitTarget{Kind: "new"}}
	for kind, execute := range map[string]operations.Executor{operations.TypeArtistRename: f.governor.RenameExecutor(rename), operations.TypeArtistMerge: f.governor.MergeExecutor(merge), operations.TypeArtistSplit: f.governor.SplitExecutor(split)} {
		_, err := f.run(kind, uuid.New(), kind, execute)
		assertArtistFailure(t, err, "artist_scope_too_large")
	}
	if f.count() != 0 || f.intentSnapshot() != before || len(f.port.calls) != 0 {
		t.Fatal("over-budget participation produced partial writes")
	}
}
