package library

import (
	"fmt"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/personal/albumfavorites"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type mergeScanFixture struct {
	*libraryFixture
	t                                *testing.T
	root                             registeredRoot
	service                          *Service
	logs                             *incrementalTestLogs
	actor, target, source, operation string
}

func newMergeScanFixture(t *testing.T, a, b int) *mergeScanFixture {
	t.Helper()
	f := &mergeScanFixture{libraryFixture: newLibraryFixture(t), t: t, actor: uuid.New()}
	f.root = insertScannerRoot(t, f.libraryFixture, "合并扫描")
	f.service, f.logs = newIncrementalTestService(t, f.libraryFixture)
	f.service.SetAlbumFavoriteMover(albumfavorites.MoveAlbumFavorites)
	f.exec(`INSERT INTO users(id,username,password_hash,role) VALUES($1,'测试管理员','synthetic','admin')`, f.actor)
	for n := 1; n <= a; n++ {
		f.write("A", 1, n)
	}
	for n := 1; n <= b; n++ {
		f.write("B", 1, n)
	}
	f.scan()
	ids := releaseIDsByTitle(t, f.libraryFixture, f.root.ID)
	if len(ids) != 2 || ids["A"] == "" || ids["B"] == "" {
		t.Fatalf("首次扫描的发行=%v", ids)
	}
	f.target, f.source = ids["A"], ids["B"]
	return f
}
func (f *mergeScanFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.database.Exec(query, args...); err != nil {
		f.t.Fatal(err)
	}
}
func (f *mergeScanFixture) write(album string, disc, track int) {
	f.t.Helper()
	path := fmt.Sprintf("%s/CD%d/%02d.flac", album, disc, track)
	writeAudio(f.t, f.root, path, audioFile{seed: byte(int(album[0]-'A')*50 + disc*10 + track), title: fmt.Sprintf("%s-%d-%d", album, disc, track), album: album, track: track})
}
func (f *mergeScanFixture) scan() incrementalTestSummary {
	f.t.Helper()
	_, stats := runIncrementalTestScan(f.t, f.service, f.logs, f.root)
	return stats
}
func (f *mergeScanFixture) request() governance.ReleaseMergeRequest {
	f.t.Helper()
	p, err := governance.NewStore(f.database).PreviewReleaseMerge(f.t.Context(), f.target, f.source)
	if err != nil {
		f.t.Fatal(err)
	}
	return governance.ReleaseMergeRequest{TargetID: f.target, SourceID: f.source, Token: p.Token}
}
func (f *mergeScanFixture) runMerge(req governance.ReleaseMergeRequest) (operations.Result, error) {
	return operations.Run(f.t.Context(), f.database, operations.Spec{ActorID: f.actor, Type: operations.TypeReleaseMerge, Key: uuid.New(), Fingerprint: req.Fingerprint(), RequestID: "merge-scan"}, governance.ReleaseMergeExecutor(req))
}
func (f *mergeScanFixture) merge() {
	f.t.Helper()
	op, err := f.runMerge(f.request())
	if err != nil {
		f.t.Fatal(err)
	}
	f.operation = op.OperationID
}
func (f *mergeScanFixture) revert() operations.Result {
	f.t.Helper()
	kind := governance.ReleaseMergeOperations(operations.TypeReleaseMerge, curation.NewWriter(f.database, "", nil))
	op, err := operations.Run(f.t.Context(), f.database, operations.Spec{ActorID: f.actor, Type: operations.TypeRevert, Key: uuid.New(), Fingerprint: f.operation, RequestID: "revert-scan"}, operations.RevertExecutor(kind, f.operation))
	if err != nil {
		f.t.Fatal(err)
	}
	return op
}
func (f *mergeScanFixture) checkMerged(tracks, releases int) {
	f.t.Helper()
	got := visibleReleases(f.t, f.libraryFixture, f.root.ID)
	if len(got) != 1 || got[0].ID != f.target || got[0].Title != "A" || got[0].Tracks != tracks {
		f.t.Fatalf("重扫后的可见发行=%+v，期望 A 共 %d 首", got, tracks)
	}
	assertDatabaseCount(f.t, f.libraryFixture, `SELECT count(*) FROM releases WHERE source_root_id=$1`, releases, f.root.ID)
	assertDatabaseCount(f.t, f.libraryFixture, `SELECT count(*) FROM release_merges WHERE target_release_id=$1 AND source_release_id=$2 AND operation_id=$3`, 1, f.target, f.source, f.operation)
}

func TestPostgreSQLReleaseMergeRescanKeeps(t *testing.T) {
	f := newMergeScanFixture(t, 3, 3)
	original := presentTrackIDs(t, f.libraryFixture, f.root.ID)
	f.merge()
	requireScanStatus(t, runBorrowFullScan(t, f.libraryFixture, f.service, f.root), "succeeded")
	f.checkMerged(6, 2)
	stats := f.scan()
	if stats.CandidatesSkipped != 2 || stats.CandidatesWritten != 0 {
		t.Fatalf("两侧均无修改，跳过=%d 写入=%d", stats.CandidatesSkipped, stats.CandidatesWritten)
	}
	f.write("B", 1, 4)
	f.scan()
	f.checkMerged(7, 2)
	f.write("B", 2, 1)
	f.write("B", 3, 1)
	f.scan()
	f.checkMerged(9, 2)
	assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM media WHERE release_id=$1 AND origin_release_id=$2 AND position=origin_position+1 AND origin_position IN (1,2,3)`, 3, f.target, f.source)
	moveFixture(t, f.root, "B/CD1/04.flac", "../removed.flac")
	f.scan()
	f.checkMerged(8, 2)
	moveFixture(t, f.root, "B", "Renamed")
	f.scan()
	f.checkMerged(8, 2)
	ids := presentTrackIDs(t, f.libraryFixture, f.root.ID)
	for n := 1; n <= 3; n++ {
		sourcePath := fmt.Sprintf("B/CD1/%02d.flac", n)
		requireTrackAt(t, ids, fmt.Sprintf("Renamed/CD1/%02d.flac", n), original[sourcePath])
		path := fmt.Sprintf("A/CD1/%02d.flac", n)
		requireTrackAt(t, ids, path, original[path])
	}
	undo := f.revert()
	assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM curation_suppressions WHERE target_type='release' AND subject='release_merge'
      AND target_id=least($1::uuid,$2::uuid) AND value->>'release_id'=greatest($1::uuid,$2::uuid)::text
      AND value->>'title'=(SELECT title FROM releases WHERE id=greatest($1::uuid,$2::uuid))
      AND value_hash=encode(sha256(convert_to(greatest($1::uuid,$2::uuid)::text,'UTF8')),'hex') AND operation_id=$3`, 1, f.source, f.target, undo.OperationID)
	assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM media WHERE release_id=$1 AND origin_release_id IS NULL AND origin_position IS NULL AND position IN(1,2,3)`, 3, f.source)
}

func TestPostgreSQLReleaseMergeTargetGainsDisc(t *testing.T) {
	f := newMergeScanFixture(t, 3, 2)
	f.merge()
	f.write("A", 2, 1)
	f.scan()
	f.checkMerged(6, 2)
	assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM media WHERE release_id=$1 AND position=2 AND origin_release_id IS NULL`, 1, f.target)
	assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM media WHERE release_id=$1 AND position=3 AND origin_release_id=$2 AND origin_position=1`, 1, f.target, f.source)
}

func TestPostgreSQLReleaseMergeGroupMoves(t *testing.T) {
	f := newMergeScanFixture(t, 2, 4)
	f.merge()
	// 在 B 的组留下收藏，用完整重扫检验“是否腾空”，再让 C 的文件真正移入 B。
	f.exec(`INSERT INTO personal_favorite_state(user_id,revision) VALUES($1,10)`, f.actor)
	f.exec(`INSERT INTO favorite_albums(user_id,group_id,saved_title) SELECT $1,group_id,'B' FROM releases WHERE id=$2`, f.actor, f.source)
	requireScanStatus(t, runBorrowFullScan(t, f.libraryFixture, f.service, f.root), "succeeded")
	assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM favorite_albums f JOIN releases r ON r.group_id=f.group_id WHERE user_id=$1 AND r.id=$2`, 1, f.actor, f.source)
	f.write("C", 1, 1)
	f.scan()
	f.exec(`INSERT INTO favorite_albums(user_id,group_id,saved_title) SELECT $1,group_id,'C' FROM releases WHERE title='C'`, f.actor)
	moveFixture(t, f.root, "C/CD1/01.flac", "B/CD1/05.flac")
	f.scan()
	f.checkMerged(7, 3)
	assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM favorite_albums f JOIN releases r ON r.group_id=f.group_id WHERE user_id=$1 AND r.id=$2 AND saved_title='C'`, 1, f.actor, f.target)
	assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM personal_favorite_state WHERE user_id=$1 AND revision=11`, 1, f.actor)
}

func TestPostgreSQLReleaseMergeFilesMovedTogether(t *testing.T) {
	f := newMergeScanFixture(t, 4, 1)
	oldID := presentTrackIDs(t, f.libraryFixture, f.root.ID)["B/CD1/01.flac"]
	f.merge()
	moveFixture(t, f.root, "B/CD1/01.flac", "A/CD1/05.flac")
	f.scan()
	f.checkMerged(5, 2)
	requireTrackAt(t, presentTrackIDs(t, f.libraryFixture, f.root.ID), "A/CD1/05.flac", oldID)
	assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM media WHERE origin_release_id=$1`, 0, f.source)
	f.revert()
	assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM release_merges`, 0)
	if got := visibleReleases(t, f.libraryFixture, f.root.ID); len(got) != 1 || got[0].ID != f.target || got[0].Tracks != 5 {
		t.Fatalf("空来源撤销后=%+v", got)
	}
}

func TestPostgreSQLReleaseMergeGuardsAfterScan(t *testing.T) {
	f := newMergeScanFixture(t, 2, 3)
	req := f.request()
	f.write("B", 1, 4)
	f.scan()
	_, err := f.runMerge(req)
	if status, code, _ := operations.Problem(err); status != 409 || code != "merge_changed" {
		t.Fatalf("扫描改变成员后执行旧预览=%v", err)
	}
	assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM release_merges`, 0)
	assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM operations WHERE operation_type='release_merge'`, 0)
}

func TestPostgreSQLReleaseMergeFilesMovedTogetherOtherMajorities(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b int
	}{{"来源占多数", 1, 3}, {"双方各半", 2, 2}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMergeScanFixture(t, tc.a, tc.b)
			f.merge()
			// 同一专辑标签确保目录搬到一起后只产生一个候选；保留音频指纹。
			for n := 1; n <= tc.b; n++ {
				old := fmt.Sprintf("B/CD1/%02d.flac", n)
				writeAudio(t, f.root, old, audioFile{seed: byte(60 + n), title: fmt.Sprintf("B-1-%d", n), album: "A", track: n})
				moveFixture(t, f.root, old, fmt.Sprintf("A/CD1/from-B-%02d.flac", n))
			}
			f.scan()
			got := visibleReleases(t, f.libraryFixture, f.root.ID)
			if len(got) != 1 || got[0].Tracks != tc.a+tc.b {
				t.Fatalf("搬到一起的发行=%+v", got)
			}
			if tc.a == tc.b {
				if got[0].ID == f.target || got[0].ID == f.source {
					t.Fatalf("平分应新建发行，得到 %+v", got)
				}
				assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM releases WHERE source_root_id=$1`, 3, f.root.ID)
			} else {
				if got[0].ID != f.target {
					t.Fatalf("B 占多数时显示归属=%s，期望 A", got[0].ID)
				}
				assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM tracks t JOIN media m ON m.id=t.medium_id WHERE m.origin_release_id=$1 AND t.source_status='present'`, tc.a+tc.b, f.source)
			}
			f.revert()
			after := visibleReleases(t, f.libraryFixture, f.root.ID)
			want := got[0].ID
			if tc.b > tc.a {
				want = f.source
			}
			if len(after) != 1 || after[0].ID != want || after[0].Tracks != tc.a+tc.b {
				t.Fatalf("撤销后=%+v，期望可见发行 %s", after, want)
			}
		})
	}
}

func TestPostgreSQLReleaseMergeSourceReplaced(t *testing.T) {
	f := newMergeScanFixture(t, 2, 2)
	f.merge()
	for n := 1; n <= 2; n++ {
		path := fmt.Sprintf("B/CD1/%02d.flac", n)
		writeAudio(t, f.root, path, audioFile{seed: byte(190 + n), title: "替换曲目", album: "替换发行", track: n})
	}
	requireScanStatus(t, runBorrowFullScan(t, f.libraryFixture, f.service, f.root), "succeeded")
	ids := releaseIDsByTitle(t, f.libraryFixture, f.root.ID)
	other := ids["替换发行"]
	if len(ids) != 2 || ids["A"] != f.target || other == "" || other == f.source || other == f.target {
		t.Fatalf("整张替换后的发行=%v", ids)
	}
	assertDatabaseCount(t, f.libraryFixture, `SELECT count(*) FROM release_merges WHERE source_release_id=$1 AND target_release_id=$2`, 1, f.source, f.target)
}
