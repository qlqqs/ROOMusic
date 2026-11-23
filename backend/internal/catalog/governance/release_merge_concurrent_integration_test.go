package governance

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// 收藏服务先持有版本行，再插入或删除收藏。用测试触发器停在写入前，
// 等合并或撤销实际阻塞在收藏版本上才释放，让外键对组行的锁也参与交错。
func TestPostgreSQLReleaseMergeConcurrentFavorites(t *testing.T) {
	for _, action := range []string{"合并", "撤销"} {
		for _, add := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/收藏=%t", action, add), func(t *testing.T) {
				f := newCorrectionFixture(t)
				a, b := f.visibleRelease("A"), f.visibleRelease("B")
				service := personal.NewService(f.database)
				set := func(group string, yes bool) {
					t.Helper()
					if _, err := service.SetFavorite(t.Context(), f.actorID, "album", group, yes); err != nil {
						t.Fatal(err)
					}
				}
				set(b.groupID, true)
				req := f.mergeRequest(a.releaseID, b.releaseID)
				var original string
				if action == "撤销" {
					original = f.mergeRelease(a.releaseID, b.releaseID, operations.TypeReleaseMerge).OperationID
				}
				set(a.groupID, !add)
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
				defer cancel()
				barrier, err := f.database.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer barrier.Rollback()
				var holder int
				if err := barrier.QueryRowContext(ctx, `SELECT pg_backend_pid() FROM pg_advisory_xact_lock(6767,1)`).Scan(&holder); err != nil {
					t.Fatal(err)
				}
				f.exec(`CREATE FUNCTION pause_favorite() RETURNS trigger LANGUAGE plpgsql AS $$
                    BEGIN PERFORM pg_advisory_xact_lock(6767,1);
                    IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW; END $$;
                    CREATE TRIGGER pause_favorite BEFORE INSERT OR DELETE ON favorite_albums FOR EACH ROW EXECUTE FUNCTION pause_favorite()`)
				favoriteDone := make(chan error, 1)
				go func() { _, err := service.SetFavorite(ctx, f.actorID, "album", a.groupID, add); favoriteDone <- err }()
				waitForLockWaiters(t, f.database, holder, 1)
				mergeDone := make(chan error, 1)
				go func() {
					var err error
					if action == "合并" {
						_, err = f.runReleaseMerge(ctx, operations.TypeReleaseMerge, uuid.New(), req)
					} else {
						_, err = operations.Run(ctx, f.database, operations.Spec{ActorID: f.actorID, Type: operations.TypeRevert, Key: uuid.New(), Fingerprint: original, RequestID: "test"}, operations.RevertExecutor(ReleaseMergeOperations(operations.TypeReleaseMerge, mergeTestSuppressions{}), original))
					}
					mergeDone <- err
				}()
				waitForLockWaiters(t, f.database, holder, 2)
				if err := barrier.Commit(); err != nil {
					t.Fatal(err)
				}
				favErr, mergeErr := <-favoriteDone, <-mergeDone
				if favErr != nil || mergeErr != nil {
					t.Fatalf("交错的收藏=%v %s=%v", favErr, action, mergeErr)
				}
			})
		}
	}
}

// 两个事务都在共同的 CATP 后排队，放行后按真实执行器的冲突结果检查。
// 扫描与认领测试使用相同的“写回发行字段”交错，额外取得扫描的生产写锁。
func TestPostgreSQLReleaseMergeConcurrent(t *testing.T) {
	for _, action := range []string{"合并", "撤销"} {
		for _, other := range []string{"扫描", "归组", "认领"} {
			t.Run(action+"/"+other, func(t *testing.T) {
				f := newCorrectionFixture(t)
				a, b, c := f.visibleRelease("A"), f.visibleRelease("B"), f.visibleRelease("C")
				req := f.mergeRequest(a.releaseID, b.releaseID)
				var original string
				if action == "撤销" {
					original = f.mergeRelease(a.releaseID, b.releaseID, operations.TypeReleaseMerge).OperationID
				}
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
				defer cancel()
				runOther := func() error {
					tx, err := f.database.BeginTx(ctx, nil)
					if err != nil {
						return err
					}
					defer tx.Rollback()
					if err = ingest.LockProjectionWrites(ctx, tx); err != nil {
						return err
					}
					if _, err = tx.ExecContext(ctx, `UPDATE releases SET artist='扫描艺人' WHERE id=$1`, a.releaseID); err != nil {
						return err
					}
					return tx.Commit()
				}
				run := func(kind, fp string, execute operations.Executor) error {
					_, err := operations.Run(ctx, f.database, operations.Spec{ActorID: f.actorID, Type: kind, Key: uuid.New(), Fingerprint: fp, RequestID: "test"}, execute)
					return err
				}
				switch other {
				case "归组":
					change := GroupChangeRequest{ReleaseID: a.releaseID, ExpectedRevision: f.revisionOf(a.releaseID), Action: GroupJoin, GroupID: c.groupID}
					runOther = func() error {
						return run(operations.TypeReleaseGroupChange, change.Fingerprint(), GroupChangeExecutor(change))
					}
				case "认领":
					f.mustCorrect(c.releaseID, 1, map[string]CorrectionChange{"title": {Mode: "set", Text: "旧版本修正"}})
					f.exec(`UPDATE tracks SET source_status='missing' WHERE medium_id=$1`, c.mediumID)
					scenario := claimScenario{fixture: f, source: c, target: a}
					claim := scenario.request(scenario.preview(t))
					runOther = func() error { return run(operations.TypeReleaseClaim, claim.Fingerprint(), ClaimExecutor(claim)) }
				}
				barrier, err := f.database.BeginTx(ctx, &sql.TxOptions{})
				if err != nil {
					t.Fatal(err)
				}
				defer barrier.Rollback()
				if err = ingest.LockProjectionWrites(ctx, barrier); err != nil {
					t.Fatal(err)
				}
				var holder int
				if err := barrier.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&holder); err != nil {
					t.Fatal(err)
				}
				mergeDone, otherDone := make(chan error, 1), make(chan error, 1)
				go func() {
					var err error
					if action == "合并" {
						_, err = f.runReleaseMerge(ctx, operations.TypeReleaseMerge, uuid.New(), req)
					} else {
						err = run(operations.TypeRevert, original, operations.RevertExecutor(ReleaseMergeOperations(operations.TypeReleaseMerge, mergeTestSuppressions{}), original))
					}
					mergeDone <- err
				}()
				go func() { otherDone <- runOther() }()
				waitForLockWaiters(t, f.database, holder, 2)
				if err := barrier.Commit(); err != nil {
					t.Fatal(err)
				}
				mergeErr, otherErr := <-mergeDone, <-otherDone
				if mergeErr != nil {
					if action != "合并" || other != "归组" {
						t.Fatalf("%s 与 %s 交错失败：%v", action, other, mergeErr)
					}
					requireMergeError(t, mergeErr, 409, "merge_changed")
				}
				if otherErr != nil {
					t.Fatalf("%s 与 %s 交错失败：%v", other, action, otherErr)
				}
			})
		}
	}
}
