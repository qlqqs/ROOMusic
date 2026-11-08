package library

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// 合成标签先故意冲突，产生两个 box_leaf；仅修改标签后，两组曲目各占一半，必须新建发行。
func TestPostgreSQLDiscFavoriteTransfer(t *testing.T) {
	for _, otherVersion := range []bool{false, true} {
		t.Run(fmt.Sprintf("另一个版本有曲目=%t", otherVersion), func(t *testing.T) {
			f := newLibraryFixture(t)
			root := insertScannerRoot(t, f, "favorite-disc-merge")
			service := scannerTestApplication(f)
			firstPath, secondPath := "A/CD1/01.flac", "A/CD2/01.flac"
			writeAudio(t, root, firstPath, audioFile{seed: 1, title: "One", album: "First", track: 1})
			writeAudio(t, root, secondPath, audioFile{seed: 2, title: "Two", album: "Second", track: 1})
			if otherVersion {
				writeAudio(t, root, "Other/01.flac", audioFile{seed: 3, title: "Other", album: "Other", track: 1})
			}
			requireScanStatus(t, runTestScan(t, f, service, root), "succeeded")
			before := presentTrackIDs(t, f, root.ID)
			oldReleases := releaseIDsByTitle(t, f, root.ID)
			if oldReleases["First"] == "" || oldReleases["Second"] == "" {
				t.Fatalf("缺少合并前的两张发行: %v", oldReleases)
			}
			for _, r := range visibleReleases(t, f, root.ID) {
				if r.Title != "Other" && r.Kind != string(ingest.CandidateBoxLeaf) {
					t.Fatalf("合并前不是 box_leaf: %+v", r)
				}
			}
			groupOf := func(release string) string {
				var group string
				if err := f.database.QueryRow(`SELECT group_id::text FROM releases WHERE id=$1::uuid`, release).Scan(&group); err != nil {
					t.Fatal(err)
				}
				return group
			}
			oldGroups := []string{groupOf(oldReleases["First"]), groupOf(oldReleases["Second"])}
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := f.database.Exec(query, args...); err != nil {
					t.Fatal(err)
				}
			}
			if otherVersion {
				exec(`UPDATE releases SET group_id=$1::uuid WHERE id=$2::uuid`, oldGroups[0], oldReleases["Other"])
			}
			movedUser, mergedUser, unaffectedUser := uuid.New(), uuid.New(), uuid.New()
			for i, user := range []string{movedUser, mergedUser, unaffectedUser} {
				exec(`INSERT INTO users(id,username,password_hash,role) VALUES($1::uuid,$2,'test-only','user')`, user, fmt.Sprintf("合成用户%d", i))
				exec(`INSERT INTO personal_favorite_state(user_id,revision) VALUES($1::uuid,10)`, user)
			}
			for _, user := range []string{movedUser, mergedUser} {
				for i, group := range oldGroups {
					exec(`INSERT INTO favorite_albums(user_id,group_id,saved_title,saved_artist,created_at) VALUES($1::uuid,$2::uuid,$3,'保存艺人','2026-01-01T00:00:00Z'::timestamptz+$4*interval '1 day')`, user, group, fmt.Sprintf("旧收藏%d", i), i)
				}
			}
			// 目标组在候选持久化后才生成；在真实转移入口模拟另一用户已经收藏它。
			mover := personal.NewService(f.database)
			calls, movedRows := 0, 0
			var targetGroup string
			service.SetAlbumFavoriteMover(func(ctx context.Context, tx *sql.Tx, moves map[string]string) (int, error) {
				calls++
				wantMoves := 2 - boolInt(otherVersion)
				if len(moves) != wantMoves {
					t.Errorf("转移映射=%v，期望 %d 个来源组", moves, wantMoves)
				}
				for from, to := range moves {
					if from != oldGroups[0] && from != oldGroups[1] {
						t.Errorf("未知来源组: %s", from)
					}
					if otherVersion && from == oldGroups[0] {
						t.Error("仍有别的版本曲目的组不应被转移")
					}
					if targetGroup != "" && targetGroup != to {
						t.Error("两个旧组没有合到同一目标组")
					}
					targetGroup = to
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO favorite_albums(user_id,group_id,saved_title,saved_artist,created_at) VALUES($1::uuid,$2::uuid,'目标已有收藏','已有艺人','2026-02-01T00:00:00Z')`, mergedUser, targetGroup); err != nil {
					return 0, err
				}
				n, err := mover.MoveAlbumFavorites(ctx, tx, moves)
				movedRows += n
				return n, err
			})
			writeAudio(t, root, firstPath, audioFile{seed: 1, title: "One", album: "Together", track: 1})
			writeAudio(t, root, secondPath, audioFile{seed: 2, title: "Two", album: "Together", track: 1})
			requireScanStatus(t, runTestScan(t, f, service, root), "succeeded")
			after := presentTrackIDs(t, f, root.ID)
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("曲目身份改变: %v → %v", before, after)
			}
			newRelease := releaseIDsByTitle(t, f, root.ID)["Together"]
			assertDatabaseCount(t, f, `SELECT COUNT(*) FROM releases WHERE source_root_id=$1::uuid`, 3+boolInt(otherVersion), root.ID)
			if newRelease == "" || newRelease == oldReleases["First"] || newRelease == oldReleases["Second"] {
				t.Fatalf("未新建合成发行: %q", newRelease)
			}
			if calls != 1 || movedRows != 4-2*boolInt(otherVersion) || targetGroup != groupOf(newRelease) {
				t.Fatalf("转移调用=%d，收藏行=%d，目标=%s", calls, movedRows, targetGroup)
			}
			for _, user := range []string{movedUser, mergedUser} {
				assertDatabaseCount(t, f, `SELECT COUNT(*) FROM favorite_albums WHERE user_id=$1::uuid AND group_id=$2::uuid`, 1, user, targetGroup)
				assertDatabaseCount(t, f, `SELECT revision FROM personal_favorite_state WHERE user_id=$1::uuid`, 11, user)
				assertDatabaseCount(t, f, `SELECT COUNT(*) FROM favorite_albums WHERE user_id=$1::uuid AND group_id=$2::uuid`, boolInt(otherVersion), user, oldGroups[0])
				assertDatabaseCount(t, f, `SELECT COUNT(*) FROM favorite_albums WHERE user_id=$1::uuid AND group_id=$2::uuid`, 0, user, oldGroups[1])
			}
			assertDatabaseCount(t, f, `SELECT revision FROM personal_favorite_state WHERE user_id=$1::uuid`, 10, unaffectedUser)
			// 未收藏目标的用户保留最早被转移的来源快照。
			savedIndex := boolInt(otherVersion)
			assertDatabaseCount(t, f, `SELECT COUNT(*) FROM favorite_albums WHERE user_id=$1::uuid AND group_id=$2::uuid AND saved_title=$3 AND saved_artist='保存艺人' AND created_at='2026-01-01T00:00:00Z'::timestamptz+$4*interval '1 day'`, 1, movedUser, targetGroup, fmt.Sprintf("旧收藏%d", savedIndex), savedIndex)
			assertDatabaseCount(t, f, `SELECT COUNT(*) FROM favorite_albums WHERE user_id=$1::uuid AND group_id=$2::uuid AND saved_title='目标已有收藏' AND saved_artist='已有艺人' AND created_at='2026-02-01T00:00:00Z'`, 1, mergedUser, targetGroup)
			// 同一根再扫一遍没有移动事实，不能再次调用转移、重复增版本或改收藏。
			requireScanStatus(t, runTestScan(t, f, service, root), "succeeded")
			if calls != 1 {
				t.Fatalf("重扫又转移了收藏: %d", calls)
			}
			for _, user := range []string{movedUser, mergedUser} {
				assertDatabaseCount(t, f, `SELECT revision FROM personal_favorite_state WHERE user_id=$1::uuid`, 11, user)
				assertDatabaseCount(t, f, `SELECT COUNT(*) FROM favorite_albums WHERE user_id=$1::uuid`, 1+boolInt(otherVersion), user)
			}
		})
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func TestPostgreSQLDiscParentScopesAndMediumTitle(t *testing.T) {
	f := newLibraryFixture(t)
	root := insertScannerRoot(t, f, "disc-parent-scopes")
	service := scannerTestApplication(f)
	writeScanFixtureFile(t, root, "A/CD1/01.flac", parsertest.FLACWithAudio(t, [16]byte{1}, []byte{1}, "TITLE=One"))
	writeScanFixtureFile(t, root, "A/Scans/Cover.PNG", pngArtworkOfWidth(3))
	writeScanFixtureFile(t, root, "艺人/某精选 CD1/01.flac", parsertest.FLACWithAudio(t, [16]byte{2}, []byte{2}, "TITLE=Two"))
	writeAudio(t, root, "艺人/另一张/01.flac", audioFile{seed: 3, title: "Other", album: "Other", track: 1})
	writeScanFixtureFile(t, root, "艺人/cover.png", pngArtworkOfWidth(9))
	writeScanFixtureFile(t, root, "B/CD1/01.flac", parsertest.FLACWithAudio(t, [16]byte{4}, []byte{4}, "TITLE=Four"))
	writeAudio(t, root, "B/CD1/附录/01.flac", audioFile{seed: 5, title: "Appendix", album: "Appendix", track: 1})
	writeAudio(t, root, "T/Disc 1 - 夏/01.flac", audioFile{seed: 6, title: "Summer", album: "Seasons", track: 1})
	writeAudio(t, root, "T/Disc 2 - 冬/01.flac", audioFile{seed: 7, title: "Winter", album: "Seasons", track: 1})
	requireScanStatus(t, runTestScan(t, f, service, root), "succeeded")
	assertAnchor := func(relativePath, kind, scope, title string) string {
		t.Helper()
		var releaseID, gotKind, gotScope, gotTitle, anchor string
		err := f.database.QueryRow(`SELECT r.id::text,r.candidate_kind,r.relative_directory,r.title,r.candidate_anchor FROM tracks t JOIN media m ON m.id=t.medium_id JOIN releases r ON r.id=m.release_id WHERE t.source_root_id=$1::uuid AND t.relative_path=$2 AND t.source_status='present'`, root.ID, relativePath).Scan(&releaseID, &gotKind, &gotScope, &gotTitle, &anchor)
		if err != nil {
			t.Fatal(err)
		}
		wantAnchor := ingest.CandidateIdentity(root.ID, ingest.CandidateAnchor{Kind: ingest.CandidateKind(kind), Scope: scope})
		if gotKind != kind || gotScope != scope || gotTitle != title || anchor != wantAnchor {
			t.Errorf("%s: %s/%s/%q/%s，期望 %s/%s/%q/%s", relativePath, gotKind, gotScope, gotTitle, anchor, kind, scope, title, wantAnchor)
		}
		return releaseID
	}
	parentRelease := assertAnchor("A/CD1/01.flac", "strict_multidisc", "A", "A")
	leafRelease := assertAnchor("艺人/某精选 CD1/01.flac", "box_leaf", "艺人/某精选 CD1", "某精选")
	assertAnchor("B/CD1/01.flac", "box_leaf", "B/CD1", "CD1")
	assertDatabaseCount(t, f, `SELECT COUNT(*) FROM release_artworks WHERE release_id=$1::uuid AND width=3`, 1, parentRelease)
	assertDatabaseCount(t, f, `SELECT COUNT(*) FROM release_artworks WHERE release_id=$1::uuid`, 0, leafRelease)
	titleRelease := assertAnchor("T/Disc 1 - 夏/01.flac", "strict_multidisc", "T", "Seasons")
	readMedia := func() string {
		var media string
		if err := f.database.QueryRow(`SELECT string_agg(position::text||':'||title,',' ORDER BY position) FROM media WHERE release_id=$1::uuid`, titleRelease).Scan(&media); err != nil {
			t.Fatal(err)
		}
		return media
	}
	if got := readMedia(); got != "1:夏,2:冬" {
		t.Fatalf("碟名没有写入: %q", got)
	}
	// 仅去掉目录碟名，曲目内容不变；同一个发行及介质恢复默认标题。
	moveFixture(t, root, "T/Disc 1 - 夏", "T/CD1")
	moveFixture(t, root, "T/Disc 2 - 冬", "T/CD2")
	requireScanStatus(t, runTestScan(t, f, service, root), "succeeded")
	if got := assertAnchor("T/CD1/01.flac", "strict_multidisc", "T", "Seasons"); got != titleRelease {
		t.Fatalf("去掉碟名后发行身份改变: %s → %s", titleRelease, got)
	}
	if got := readMedia(); got != "1:Medium,2:Medium" {
		t.Fatalf("去掉碟名未恢复默认值: %q", got)
	}
	assertDatabaseCount(t, f, `SELECT COUNT(*) FROM scan_observations WHERE root_id=$1::uuid`, 0, root.ID)
}

func TestPostgreSQLDiscParentHeldSiblingPreventsPromotion(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("权限用例必须由非 root 用户运行")
	}
	f := newLibraryFixture(t)
	root := insertScannerRoot(t, f, "disc-parent-held-sibling")
	service := scannerTestApplication(f)
	const oldPath = "Album/Blocked/01.flac"
	const discPath = "Album/CD1/01.flac"
	writeAudio(t, root, oldPath, audioFile{seed: 1, title: "Old", album: "Old Album", track: 1})
	requireFilterScan(t, runFilterLifecycleScan(t, service), "succeeded", 0)
	before := presentTrackIDs(t, f, root.ID)
	if len(before) != 1 || before[oldPath] == "" {
		t.Fatalf("首次扫描必须保存兄弟目录的旧来源：%v", before)
	}

	// 新增碟目录时，兄弟目录读不了；旧来源被保住，父目录仍不独占。
	writeScanFixtureFile(t, root, discPath, parsertest.FLACWithAudio(t, [16]byte{2}, []byte{2}, "TITLE=New"))
	blocked := filepath.Join(root.Path, "Album", "Blocked")
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(blocked, 0o755); err != nil {
			t.Errorf("恢复合成目录权限：%v", err)
		}
	})
	if _, err := os.ReadDir(blocked); !os.IsPermission(err) {
		t.Fatalf("chmod 000 未造成真实权限拒绝：%v", err)
	}

	scan := runFilterLifecycleScan(t, service)
	requireFilterScan(t, scan, "succeeded", 1)
	requireDiagnosticCount(t, f, scan.ID, "walk_error", 1)
	after := presentTrackIDs(t, f, root.ID)
	requireTrackAt(t, after, oldPath, before[oldPath])
	if len(after) != 2 || after[discPath] == "" {
		t.Fatalf("必须保住旧来源并写入新碟目录曲目：%v", after)
	}
	assertDatabaseCount(t, f, `SELECT COUNT(*) FROM tracks WHERE id=$1::uuid AND source_status='present'
		AND source_identity IS NOT NULL AND observed_at >= (SELECT started_at FROM scan_runs WHERE id=$2::uuid)`, 1, before[oldPath], scan.ID)

	var kind, directory, title, anchor string
	if err := f.database.QueryRow(`SELECT r.candidate_kind,r.relative_directory,r.title,r.candidate_anchor
		FROM tracks t JOIN media m ON m.id=t.medium_id JOIN releases r ON r.id=m.release_id
		WHERE t.id=$1::uuid`, after[discPath]).Scan(&kind, &directory, &title, &anchor); err != nil {
		t.Fatal(err)
	}
	wantAnchor := ingest.CandidateIdentity(root.ID, ingest.CandidateAnchor{Kind: ingest.CandidateBoxLeaf, Scope: "Album/CD1"})
	if kind != string(ingest.CandidateBoxLeaf) || directory != "Album/CD1" || title != "CD1" || anchor != wantAnchor {
		t.Fatalf("兄弟目录被保住时不能上提：种类=%q 目录=%q 标题=%q 锚点=%q，期望 box_leaf/Album/CD1/CD1/%s", kind, directory, title, anchor, wantAnchor)
	}
}
