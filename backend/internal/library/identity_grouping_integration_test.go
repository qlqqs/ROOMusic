package library

import (
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/personal/albumfavorites"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLScanGroupingSplitMergeKeepsRecordsOnOwningUUID(t *testing.T) {
	for _, tc := range []struct {
		name       string
		alphaCount int
	}{
		{"三比一沿用对应与修正", 3},
		{"二比二新 UUID 只保留旧记录", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &mergeScanFixture{libraryFixture: newLibraryFixture(t), t: t, actor: uuid.New()}
			f.root = insertScannerRoot(t, f.libraryFixture, "grouping-split-merge")
			f.service, f.logs = newIncrementalTestService(t, f.libraryFixture)
			f.service.SetAlbumFavoriteMover(albumfavorites.MoveAlbumFavorites)
			f.exec(`INSERT INTO users(id,username,password_hash,role) VALUES($1::uuid,'grouping-actor','test-only','admin')`, f.actor)
			f.exec(`INSERT INTO personal_favorite_state(user_id,revision) VALUES($1::uuid,10)`, f.actor)
			paths := []string{"Shared/A.flac", "Shared/B.flac", "Shared/C.flac", "Shared/D.flac"}
			write := func(split bool) {
				t.Helper()
				for i, path := range paths {
					album, position := "Alpha", i+1
					if split && i >= tc.alphaCount {
						album, position = "Beta", 3
					}
					writeAudio(t, f.root, path, audioFile{seed: byte(i + 1), title: "Track", album: album, track: position})
				}
			}
			write(false)
			f.scan()
			initial := readIncrementalTestTracks(t, f.libraryFixture, f.root)
			old := initial[paths[0]].ReleaseID
			groupOf := func(release string) string {
				t.Helper()
				var group string
				if err := f.database.QueryRow(`SELECT group_id::text FROM releases WHERE id=$1::uuid`, release).Scan(&group); err != nil {
					t.Fatal(err)
				}
				return group
			}
			oldGroup := groupOf(old)
			lastTrack := initial[paths[3]].ID
			f.exec(`INSERT INTO favorite_tracks(user_id,track_id) VALUES($1::uuid,$2::uuid)`, f.actor, lastTrack)
			f.exec(`INSERT INTO favorite_albums(user_id,group_id,saved_title,saved_artist)
				VALUES($1::uuid,$2::uuid,'旧组收藏','合成艺人')`, f.actor, oldGroup)
			aMatch, bMatch := uuid.New(), uuid.New()
			governanceStore := governance.NewStore(f.database)
			linkAndCorrect := func(id, external, title string) {
				t.Helper()
				f.exec(`INSERT INTO external_links(id,source,object_type,object_id,external_id,method,confidence)
					VALUES(gen_random_uuid(),'musicbrainz','release',$1::uuid,$2,'manual','certain')`, id, external)
				state, err := governanceStore.LoadCorrectionState(t.Context(), id)
				if err != nil {
					t.Fatal(err)
				}
				request := governance.CorrectionRequest{ReleaseID: id, ExpectedRevision: state.Revision, Changes: map[string]governance.CorrectionChange{
					"title": {Mode: catalog.CorrectionSet, Text: title},
				}}
				if _, err := operations.Run(t.Context(), f.database, operations.Spec{ActorID: f.actor, Type: operations.TypeReleaseCorrection,
					Key: uuid.New(), Fingerprint: request.Fingerprint(), RequestID: "synthetic-grouping-correction"}, governance.CorrectionExecutor(request)); err != nil {
					t.Fatal(err)
				}
			}
			linkAndCorrect(old, aMatch, "手动甲")
			trackState, err := governanceStore.LoadTrackCorrectionState(t.Context(), lastTrack)
			if err != nil {
				t.Fatal(err)
			}
			trRequest := governance.TrackCorrectionRequest{TrackID: lastTrack, ExpectedRevision: trackState.Revision, Changes: map[string]governance.CorrectionChange{
				"title": {Mode: catalog.CorrectionSet, Text: "手动曲名"},
			}}
			if _, err := operations.Run(t.Context(), f.database, operations.Spec{ActorID: f.actor, Type: governance.TypeTrackCorrection,
				Key: uuid.New(), Fingerprint: trRequest.Fingerprint(), RequestID: "synthetic-track-correction"}, governance.TrackCorrectionExecutor(trRequest)); err != nil {
				t.Fatal(err)
			}
			write(true)
			f.scan()
			split := readIncrementalTestTracks(t, f.libraryFixture, f.root)
			alpha, beta := split[paths[0]].ReleaseID, split[paths[3]].ReleaseID
			if alpha != old || beta == "" || beta == old {
				t.Fatalf("拆分 UUID = %q / %q，期望甲沿用 %q、乙新建", alpha, beta, old)
			}
			for i, path := range paths {
				expected := alpha
				if i >= tc.alphaCount {
					expected = beta
				}
				if split[path].ReleaseID != expected || split[path].ID != initial[path].ID {
					t.Errorf("拆分后 %s = %+v，期望发行 %s、原曲目 %s", path, split[path], expected, initial[path].ID)
				}
			}
			betaGroup := groupOf(beta)
			f.exec(`INSERT INTO favorite_albums(user_id,group_id,saved_title,saved_artist)
				VALUES($1::uuid,$2::uuid,'乙组收藏','合成艺人')`, f.actor, betaGroup)
			linkAndCorrect(beta, bMatch, "手动乙")
			assertVisible := func(id, title, external string) {
				t.Helper()
				evidence, err := catalog.LoadCurationEvidence(t.Context(), f.database, []string{id})
				val, exists := evidence[id]
				if err != nil || !exists || val.Title != title || val.ExternalID != external {
					t.Errorf("发行 %s 有效标题和对应 = %q / %q，期望 %q / %q，错误 %v", id, val.Title, val.ExternalID, title, external, err)
				}
			}
			assertVisible(alpha, "手动甲", aMatch)
			assertVisible(beta, "手动乙", bMatch)
			assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM favorite_albums WHERE user_id=$1::uuid`, 2, f.actor)
			write(false)
			f.scan()
			merged := readIncrementalTestTracks(t, f.libraryFixture, f.root)
			current := merged[paths[0]].ReleaseID
			if tc.alphaCount == 3 {
				if current != alpha {
					t.Fatalf("三比一合并 UUID = %s，期望沿用 %s", current, alpha)
				}
				assertVisible(current, "手动甲", aMatch)
			} else {
				if current == "" || current == alpha || current == beta {
					t.Fatalf("两张各半合并 UUID = %q，期望新建", current)
				}
				assertVisible(current, "Alpha", "")
				assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM release_corrections WHERE release_id=$1::uuid`, 0, current)
			}
			if releases := visibleReleases(t, f.libraryFixture, f.root.ID); len(releases) != 1 || releases[0].ID != current {
				t.Errorf("合并后可见发行 = %+v，期望只有 %s", releases, current)
			}
			for _, path := range paths {
				if merged[path].ReleaseID != current || merged[path].ID != initial[path].ID {
					t.Errorf("合并后 %s = %+v，期望同张发行 %s 和原曲目 %s", path, merged[path], current, initial[path].ID)
				}
			}
			assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM release_corrections WHERE
				(release_id=$1::uuid AND text_value='手动甲' OR release_id=$2::uuid AND text_value='手动乙') AND field='title'`, 2, alpha, beta)
			assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM external_links WHERE object_type='release' AND source='musicbrainz'
				AND (object_id=$1::uuid AND external_id=$3 OR object_id=$2::uuid AND external_id=$4)`, 2, alpha, beta, aMatch, bMatch)
			assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM release_groups WHERE id IN ($1::uuid,$2::uuid)`, 2, oldGroup, betaGroup)
			assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM favorite_albums WHERE user_id=$1::uuid`, 1, f.actor)
			assertDatabaseCount(t, f.libraryFixture, `SELECT revision FROM personal_favorite_state WHERE user_id=$1::uuid`, 11, f.actor)
			assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM favorite_albums WHERE user_id=$1::uuid AND group_id=$2::uuid AND saved_title='旧组收藏'`, 1, f.actor, groupOf(current))
			assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM favorite_tracks ft JOIN track_effective t ON t.id=ft.track_id
				WHERE ft.user_id=$1::uuid AND t.id=$2::uuid AND t.title='手动曲名'`, 1, f.actor, lastTrack)
		})
	}
}

func TestPostgreSQLScanGroupingManualMergeKeepsNewBonusOrigin(t *testing.T) {
	f := newMergeScanFixture(t, 2, 2)
	before := presentTrackIDs(t, f.libraryFixture, f.root.ID)
	f.merge()
	const path = "B/Bonus/Extra.flac"
	writeAudio(t, f.root, path, audioFile{seed: 81, title: "Bonus track", album: "Bonus edition", track: 1})
	f.scan()
	f.checkMerged(5, 2)
	after := presentTrackIDs(t, f.libraryFixture, f.root.ID)
	bonusID := after[path]
	delete(after, path)
	if bonusID == "" || !reflect.DeepEqual(before, after) {
		t.Fatalf("新增附加碟曲目 %q，重扫前后曲目 = %v / %v", bonusID, before, after)
	}
	assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM tracks t JOIN media m ON m.id=t.medium_id
		WHERE t.id=$1::uuid AND m.release_id=$2::uuid AND m.origin_release_id=$3::uuid AND m.origin_position=2
		AND m.position=3 AND m.title='Bonus'`, 1, bonusID, f.target, f.source)
	if stats := f.scan(); stats.CandidatesSkipped != 2 || stats.CandidatesWritten != 0 {
		t.Errorf("人工并入后的附加碟重扫未稳定：%+v", stats)
	}
	f.revert()
	assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM tracks t JOIN media m ON m.id=t.medium_id
		WHERE t.id=$1::uuid AND m.release_id=$2::uuid AND m.origin_release_id IS NULL
		AND m.position=2 AND m.title='Bonus'`, 1, bonusID, f.source)
}
