package library

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLRatingScanMovesFavoritesAndRatingsAtomically(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail bool
	}{
		{"transfer", false},
		{"rollback", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLibraryFixture(t)
			root := insertScannerRoot(t, f, "rating-disc-merge")
			scanner := scannerTestApplication(f)
			const firstPath, secondPath = "A/CD1/01.flac", "A/CD2/03.flac"
			writeAudio(t, root, firstPath, audioFile{seed: 1, title: "One", album: "First", track: 1})
			writeAudio(t, root, secondPath, audioFile{seed: 2, title: "Two", album: "Second", track: 3})
			requireScanStatus(t, runTestScan(t, f, scanner, root), "succeeded")
			groupOf := func(title string) string {
				t.Helper()
				release := releaseIDsByTitle(t, f, root.ID)[title]
				var group string
				if err := f.database.QueryRowContext(t.Context(), `SELECT group_id FROM releases WHERE id=$1`, release).Scan(&group); err != nil {
					t.Fatalf("查询发行 %q 的专辑组：%v", title, err)
				}
				return group
			}
			source, second := groupOf("First"), groupOf("Second")

			service := personal.NewService(f.database)
			both, ratingOnly := uuid.New(), uuid.New()
			for _, user := range []string{both, ratingOnly} {
				if _, err := f.database.ExecContext(t.Context(), `INSERT INTO users(id,username,password_hash) VALUES($1,$1::uuid::text,'test-only')`, user); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := service.SetFavorite(t.Context(), both, "album", source, true); err != nil {
				t.Fatal(err)
			}
			for _, seed := range []struct {
				user, group string
				rating      int
			}{
				{both, source, 4},
				{ratingOnly, second, 2},
			} {
				if _, err := service.SetRating(t.Context(), seed.user, "album", seed.group, seed.rating); err != nil {
					t.Fatal(err)
				}
			}

			// 在真实转移已写入收藏和评分之后注入错误，验证调用方回滚整个转移事务。
			var injected atomic.Bool
			scanner.SetAlbumFavoriteMover(func(ctx context.Context, tx *sql.Tx, moves map[string]string) (int, error) {
				moved, err := service.MoveAlbumFavorites(ctx, tx, moves)
				if err != nil || !tc.fail {
					return moved, err
				}
				target := moves[source]
				var favorites, ratings, revision int
				err = tx.QueryRowContext(ctx, `SELECT
					(SELECT count(*) FROM favorite_albums WHERE user_id=$1 AND group_id=$2),
					(SELECT count(*) FROM rating_albums WHERE group_id=$2 AND ((user_id=$1 AND rating=4) OR (user_id=$3 AND rating=2))),
					(SELECT revision FROM personal_favorite_state WHERE user_id=$1)`, both, target, ratingOnly).
					Scan(&favorites, &ratings, &revision)
				if err != nil || moved != 1 || favorites != 1 || ratings != 2 || revision != 2 {
					t.Errorf("注入错误前的转移为 %d 条收藏，目标收藏 %d 条、评分 %d 条、收藏版本 %d，查询错误 %v",
						moved, favorites, ratings, revision, err)
					return moved, errors.New("验证事务内转移状态失败")
				}
				injected.Store(true)
				return moved, errors.New("注入评分及收藏转移事务错误")
			})
			writeAudio(t, root, firstPath, audioFile{seed: 1, title: "One", album: "Together", track: 1})
			writeAudio(t, root, secondPath, audioFile{seed: 2, title: "Two", album: "Together", track: 1})
			requireScanStatus(t, runTestScan(t, f, scanner, root), "succeeded")
			if tc.fail && !injected.Load() {
				t.Fatal("扫描未触发预定的转移错误")
			}
			target := groupOf("Together")
			if target == source || target == second {
				t.Fatalf("扫描未生成新专辑组，得到 %s", target)
			}

			wantFavoriteGroup, wantOtherGroup, wantRevision := target, target, 2
			if tc.fail {
				wantFavoriteGroup, wantOtherGroup, wantRevision = source, second, 1
			}
			assertDatabaseCount(t, f, `SELECT count(*) FROM favorite_albums`, 1)
			assertDatabaseCount(t, f, `SELECT count(*) FROM favorite_albums WHERE user_id=$1 AND group_id=$2`, 1, both, wantFavoriteGroup)
			assertDatabaseCount(t, f, `SELECT count(*) FROM rating_albums`, 2)
			assertDatabaseCount(t, f, `SELECT count(*) FROM rating_albums WHERE user_id=$1 AND group_id=$2 AND rating=4`, 1, both, wantFavoriteGroup)
			assertDatabaseCount(t, f, `SELECT count(*) FROM rating_albums WHERE user_id=$1 AND group_id=$2 AND rating=2`, 1, ratingOnly, wantOtherGroup)
			assertDatabaseCount(t, f, `SELECT revision FROM personal_favorite_state WHERE user_id=$1`, wantRevision, both)
			assertDatabaseCount(t, f, `SELECT revision FROM personal_favorite_state WHERE user_id=$1`, 0, ratingOnly)
		})
	}
}
