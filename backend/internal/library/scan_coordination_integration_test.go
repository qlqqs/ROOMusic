package library

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLScanCoordinationReusesActiveScanAndReleasesHolder(t *testing.T) {
	fixture := newLibraryFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var schema string
	if err := fixture.database.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatalf("read fixture schema: %v", err)
	}
	scopedURL, err := postgrestest.ScopedURL(postgrestest.BaseURL(t), schema)
	if err != nil {
		t.Fatalf("scope second connection pool: %v", err)
	}
	contender := openScanCoordinationPool(t, scopedURL)
	scanID := uuid.New()
	if _, err := fixture.database.ExecContext(ctx, `INSERT INTO scan_runs(id,status,started_at) VALUES($1::uuid,'running',NOW())`, scanID); err != nil {
		t.Fatalf("insert running scan: %v", err)
	}
	holder, acquired, err := acquireScanExecution(ctx, fixture.database)
	cleanupScanHolder(t, holder)
	if err != nil || !acquired {
		t.Fatalf("acquire first holder: acquired=%v err=%v", acquired, err)
	}
	var holderPID, contenderPID int
	if err := holder.connection.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatalf("read holder session: %v", err)
	}
	if err := contender.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&contenderPID); err != nil {
		t.Fatalf("read contender session: %v", err)
	}
	if holderPID == contenderPID {
		t.Fatal("contention must use independent physical sessions")
	}
	other, acquired, err := acquireScanExecution(ctx, contender)
	cleanupScanHolder(t, other)
	if err != nil || acquired || other != nil {
		t.Fatalf("second session acquired live lock: holder=%v acquired=%v err=%v", other, acquired, err)
	}

	service := NewService(ctx, contender, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() {
		cancel()
		waitContext, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer waitCancel()
		if err := service.WaitForScans(waitContext); err != nil {
			t.Errorf("wait for contender workers: %v", err)
		}
	})
	result, err := service.StartScan(ctx)
	if err != nil {
		t.Fatalf("reuse active scan: %v", err)
	}
	if result.Started || result.Scan.ID != scanID || result.Scan.ScanRunID != scanID || result.Scan.Status != "running" || result.Scan.FinishedAt != nil {
		t.Fatalf("contender did not reuse running scan %s: %+v", scanID, result)
	}
	var total, running int
	if err := contender.QueryRowContext(ctx, `SELECT COUNT(*),COUNT(*) FILTER (WHERE id=$1::uuid AND status='running' AND finished_at IS NULL) FROM scan_runs`, scanID).Scan(&total, &running); err != nil {
		t.Fatalf("query scan rows after contention: %v", err)
	}
	if total != 1 || running != 1 {
		t.Fatalf("contender created or replaced a scan: total=%d original_running=%d", total, running)
	}

	if err := holder.close(ctx); err != nil {
		t.Fatalf("release first holder: %v", err)
	}
	other, acquired, err = acquireScanExecution(ctx, contender)
	cleanupScanHolder(t, other)
	if err != nil || !acquired {
		t.Fatalf("acquire after unlock: acquired=%v err=%v", acquired, err)
	}
	var reacquiredPID int
	if err := other.connection.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&reacquiredPID); err != nil {
		t.Fatalf("read new holder session: %v", err)
	}
	if reacquiredPID != contenderPID || reacquiredPID == holderPID {
		t.Fatalf("reacquired session = %d, want contender %d rather than holder %d", reacquiredPID, contenderPID, holderPID)
	}
}

func TestPostgreSQLScanCoordinationRejectsSingleConnectionPool(t *testing.T) {
	fixture := newLibraryFixture(t)
	fixture.database.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// 占住唯一槽位：若启动绕过容量检查，会一直等到请求超时。
	connection, err := fixture.database.Conn(ctx)
	if err != nil {
		t.Fatalf("reserve only pool connection: %v", err)
	}
	defer connection.Close()
	service := NewService(ctx, fixture.database, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() {
		cancel()
		waitContext, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer waitCancel()
		if err := service.WaitForScans(waitContext); err != nil {
			t.Errorf("wait for rejected scan workers: %v", err)
		}
	})
	started := time.Now()
	result, err := service.StartScan(ctx)
	elapsed := time.Since(started)
	if !errors.Is(err, ErrCoordinationUnavailable) {
		t.Fatalf("single-connection start error = %v, want ErrCoordinationUnavailable", err)
	}
	if elapsed >= time.Second || ctx.Err() != nil {
		t.Fatalf("single-connection start waited instead of failing fast: elapsed=%s context=%v", elapsed, ctx.Err())
	}
	if result.Started || result.Scan.ID != "" {
		t.Fatalf("single-connection pool started a scan: %+v", result)
	}
	var count int
	if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM scan_runs`).Scan(&count); err != nil {
		t.Fatalf("count scans after rejected start: %v", err)
	}
	if count != 0 {
		t.Fatalf("rejected start created %d scans", count)
	}
}

func TestPostgreSQLScanCoordinationSeparatesSchemas(t *testing.T) {
	first := openScanCoordinationPool(t, postgrestest.NewSchema(t, "roomusic_scan_"))
	second := openScanCoordinationPool(t, postgrestest.NewSchema(t, "roomusic_scan_"))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var schemas [2]string
	var keys [2]int32
	var pids [2]int
	for index, database := range []*sql.DB{first, second} {
		holder, acquired, err := acquireScanExecution(ctx, database)
		cleanupScanHolder(t, holder)
		if err != nil || !acquired {
			t.Fatalf("acquire schema %d while the other holder remains live: acquired=%v err=%v", index, acquired, err)
		}
		if err := holder.connection.QueryRowContext(ctx, `SELECT current_schema(),hashtext(current_schema()),pg_backend_pid()`).Scan(&schemas[index], &keys[index], &pids[index]); err != nil {
			t.Fatalf("read schema %d lock identity: %v", index, err)
		}
		if holder.lockClass != 1396916558 || holder.lockKey != keys[index] {
			t.Fatalf("schema %d saved keys = (%d,%d), want (1396916558,%d)", index, holder.lockClass, holder.lockKey, keys[index])
		}
	}
	if schemas[0] == schemas[1] || keys[0] == keys[1] || pids[0] == pids[1] {
		t.Fatalf("expected distinct schemas, keys and physical sessions: schemas=%v keys=%v pids=%v", schemas, keys, pids)
	}
}

func TestPostgreSQLScanCoordinationUnlockUsesSavedKeysAfterSearchPathChange(t *testing.T) {
	scopedURL := postgrestest.NewSchema(t, "roomusic_scan_")
	owner := openScanCoordinationPool(t, scopedURL)
	contender := openScanCoordinationPool(t, scopedURL)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	holder, acquired, err := acquireScanExecution(ctx, owner)
	cleanupScanHolder(t, holder)
	if err != nil || !acquired {
		t.Fatalf("acquire original schema lock: acquired=%v err=%v", acquired, err)
	}
	var originalKey int32
	var holderPID int
	if err := holder.connection.QueryRowContext(ctx, `SELECT hashtext(current_schema()),pg_backend_pid()`).Scan(&originalKey, &holderPID); err != nil {
		t.Fatalf("read original lock identity: %v", err)
	}
	if holder.lockClass != 1396916558 || holder.lockKey != originalKey {
		t.Fatalf("saved keys = (%d,%d), want (1396916558,%d)", holder.lockClass, holder.lockKey, originalKey)
	}
	if _, err := holder.connection.ExecContext(ctx, `SET search_path TO pg_catalog`); err != nil {
		t.Fatalf("change holder search_path: %v", err)
	}
	var changedKey int32
	if err := holder.connection.QueryRowContext(ctx, `SELECT hashtext(current_schema())`).Scan(&changedKey); err != nil {
		t.Fatalf("read changed schema key: %v", err)
	}
	if changedKey == originalKey {
		t.Fatal("search_path change must produce a different schema lock key")
	}
	other, acquired, err := acquireScanExecution(ctx, contender)
	cleanupScanHolder(t, other)
	if err != nil || acquired || other != nil {
		t.Fatalf("search_path change released original lock: holder=%v acquired=%v err=%v", other, acquired, err)
	}
	if err := holder.close(ctx); err != nil {
		t.Fatalf("unlock original keys after search_path change: %v", err)
	}

	// 保持原物理会话存活，再由另一连接池取锁，避免断开释放或同会话重入掩盖解锁错误。
	retained, err := owner.Conn(ctx)
	if err != nil {
		t.Fatalf("retain original physical session: %v", err)
	}
	defer retained.Close()
	var retainedPID int
	if err := retained.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&retainedPID); err != nil {
		t.Fatalf("read retained session: %v", err)
	}
	if retainedPID != holderPID {
		t.Fatalf("original session was replaced: got %d, want %d", retainedPID, holderPID)
	}
	other, acquired, err = acquireScanExecution(ctx, contender)
	cleanupScanHolder(t, other)
	if err != nil || !acquired {
		t.Fatalf("acquire original schema after close: acquired=%v err=%v", acquired, err)
	}
	var contenderPID int
	if err := other.connection.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&contenderPID); err != nil {
		t.Fatalf("read replacement holder session: %v", err)
	}
	if contenderPID == holderPID || other.lockClass != 1396916558 || other.lockKey != originalKey {
		t.Fatalf("replacement holder identity = (%d,%d,%d), original = (%d,1396916558,%d)", contenderPID, other.lockClass, other.lockKey, holderPID, originalKey)
	}
}

// openScanCoordinationPool 打开独立连接池；只测锁的用例无需执行迁移。
func openScanCoordinationPool(t *testing.T, scopedURL string) *sql.DB {
	t.Helper()
	database, err := sql.Open("pgx", scopedURL)
	if err != nil {
		t.Fatalf("open scan coordination pool: %v", err)
	}
	database.SetMaxOpenConns(2)
	database.SetMaxIdleConns(2)
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close scan coordination pool: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		t.Fatalf("ping scan coordination pool: %v", err)
	}
	return database
}

// cleanupScanHolder 即使断言提前退出也有限时地释放 holder；手动 close 后无需再次解锁。
func cleanupScanHolder(t *testing.T, holder *scanExecution) {
	t.Helper()
	if holder == nil {
		return
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := holder.close(ctx); err != nil && !errors.Is(err, sql.ErrConnDone) {
			t.Errorf("clean up scan holder: %v", err)
		}
	})
}
