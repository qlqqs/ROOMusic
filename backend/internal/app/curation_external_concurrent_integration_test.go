package app

import (
	"bytes"
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/artwork"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type externalApplyResult struct {
	result curation.ExternalApplied
	err    error
}

func startExternalApply(ctx context.Context, f *curationFixture, req curation.ArtworkRequest) <-chan externalApplyResult {
	ch := make(chan externalApplyResult, 1)
	go func() {
		r, err := f.writer.ApplyArtwork(ctx, req)
		ch <- externalApplyResult{r, err}
	}()
	return ch
}

func startExternalRevert(ctx context.Context, f *curationFixture, id string) <-chan error {
	ch := make(chan error, 1)
	go func() {
		_, err := operations.Run(ctx, f.application.database, operations.Spec{
			Type: operations.TypeRevert, ActorID: f.admin.id, Key: uuid.New(),
			Fingerprint: operations.Fingerprint(map[string]string{"operation_id": id}),
		}, operations.RevertExecutor(curation.ExternalArtworkOperations(f.writer), id))
		ch <- err
	}()
	return ch
}

func awaitExternalResult[T any](t *testing.T, ctx context.Context, ch <-chan T) T {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		var zero T
		return zero
	}
}

func externalBarrier(t *testing.T, ctx context.Context, db *sql.DB, query string, args ...any) (*sql.Tx, int) {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	var pid int
	if err := tx.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		t.Fatal(err)
	}
	return tx, pid
}

func waitExternalKeyLock(t *testing.T, ctx context.Context, db *sql.DB, pid int, key string, want int) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var got int
		err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_locks
			WHERE NOT granted AND locktype='advisory' AND classid=1095914583
			  AND objid::bigint=(hashtext($1)::bigint & 4294967295) AND objsubid=2
			  AND database=(SELECT oid FROM pg_database WHERE datname=current_database())
			  AND EXISTS (SELECT 1 FROM pg_locks holder
			    WHERE holder.pid=$2 AND holder.granted AND holder.locktype='advisory'
			      AND holder.database=pg_locks.database AND holder.classid=pg_locks.classid
			      AND holder.objid=pg_locks.objid AND holder.objsubid=pg_locks.objsubid)`, key, pid).Scan(&got)
		if err != nil {
			t.Fatalf("读取封面键 %s 的等待锁：%v", key, err)
		}
		if got == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("封面键 %s 的等待请求=%d，期望 %d", key, got, want)
		case <-ticker.C:
		}
	}
}

func TestPostgreSQLExternalConcurrent(t *testing.T) {
	t.Run("同发行两次写入", func(t *testing.T) {
		f := newCurationFixture(t)
		f.enable(t)
		app := f.application
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		tx, pid := externalBarrier(t, ctx, app.database, "SELECT id FROM releases WHERE id=$1 FOR UPDATE", f.a)
		first := externalArtworkRequest(f.a)
		second := externalArtworkRequest(f.a)
		second.Data = append(second.Data, 1)
		firstDone := startExternalApply(ctx, f, first)
		waiter := waitScanFeedbackBlockedBy(t, app.database, pid)
		secondDone := startExternalApply(ctx, f, second)
		waitScanFeedbackBlockedBy(t, app.database, waiter)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		result := awaitExternalResult(t, ctx, firstDone)
		id := requireExternalApply(t, result.result, result.err)
		result = awaitExternalResult(t, ctx, secondDone)
		requireExternalError(t, result.result, result.err, 409, "nothing_to_apply", "occupied")
		stored, firstPath := externalArtworkFile(t, app, first.Data)
		_, secondPath := externalArtworkFile(t, app, second.Data)
		externalArtworkState(t, app, f.a, id, stored)
		requireExternalFile(t, firstPath, first.Data)
		requireExternalFile(t, secondPath, nil)
		if n := countRows(t, app, "SELECT count(*) FROM operations"); n != 1 {
			t.Fatalf("并发写入后的操作数=%d，期望 1", n)
		}
	})

	t.Run("同发行撤销与写入", func(t *testing.T) {
		f := newCurationFixture(t)
		f.enable(t)
		app := f.application
		first := externalArtworkRequest(f.a)
		original := applyExternalArtwork(t, f.writer, first)
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		tx, pid := externalBarrier(t, ctx, app.database, "SELECT id FROM releases WHERE id=$1 FOR UPDATE", f.a)
		reverted := startExternalRevert(ctx, f, original)
		waiter := waitScanFeedbackBlockedBy(t, app.database, pid)
		second := externalArtworkRequest(f.a)
		second.Data, second.ExternalID = append(bytes.Clone(first.Data), 1), "fixture-release/43"
		written := startExternalApply(ctx, f, second)
		waitScanFeedbackBlockedBy(t, app.database, waiter)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if err := awaitExternalResult(t, ctx, reverted); err != nil {
			t.Fatal(err)
		}
		result := awaitExternalResult(t, ctx, written)
		id := requireExternalApply(t, result.result, result.err)
		stored, secondPath := externalArtworkFile(t, app, second.Data)
		_, firstPath := externalArtworkFile(t, app, first.Data)
		externalArtworkState(t, app, f.a, id, stored)
		requireExternalFile(t, firstPath, nil)
		requireExternalFile(t, secondPath, second.Data)
	})

	t.Run("跨发行同内容撤销与写入", func(t *testing.T) {
		f := newCurationFixture(t)
		f.enable(t)
		app := f.application
		first := externalArtworkRequest(f.a)
		original := applyExternalArtwork(t, f.writer, first)
		stored, path := externalArtworkFile(t, app, first.Data)
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		tx, pid := externalBarrier(t, ctx, app.database, "SELECT pg_advisory_xact_lock(1095914583,hashtext($1))", stored.Key)
		reverted := startExternalRevert(ctx, f, original)
		// 清理等键锁时，撤销事务必须已经提交。
		waitExternalKeyLock(t, ctx, app.database, pid, stored.Key, 1)
		externalArtworkState(t, app, f.a, "", artwork.Stored{})
		requireExternalFile(t, path, first.Data)
		second := externalArtworkRequest(f.b)
		written := startExternalApply(ctx, f, second)
		waitExternalKeyLock(t, ctx, app.database, pid, stored.Key, 2)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if err := awaitExternalResult(t, ctx, reverted); err != nil {
			t.Fatal(err)
		}
		result := awaitExternalResult(t, ctx, written)
		id := requireExternalApply(t, result.result, result.err)
		externalArtworkState(t, app, f.a, "", artwork.Stored{})
		externalArtworkState(t, app, f.b, id, stored)
		requireExternalFile(t, path, first.Data)
		requireExternalArtworkHTTP(t, app, &f.admin, stored, first.Data)
	})
}
