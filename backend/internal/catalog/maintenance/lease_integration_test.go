package maintenance

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
)

func leasePool(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", postgrestest.NewSchema(t, "catalog_lease_"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func holdLease(t *testing.T, ctx context.Context, db *sql.DB) (*sql.Conn, int32, func()) {
	t.Helper()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	key, acquired, err := acquireLease(ctx, conn)
	if err != nil || !acquired {
		conn.Close()
		t.Fatalf("取得维护租约：acquired=%v err=%v", acquired, err)
	}
	closed := false
	release := func() {
		if !closed {
			releaseLease(conn, key)
			closed = true
		}
	}
	t.Cleanup(release)
	return conn, key, release
}

func TestPostgreSQLMaintenanceLeaseSeparatesSchemas(t *testing.T) {
	first, second := leasePool(t), leasePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	owner, key, _ := holdLease(t, ctx, first)
	_, otherKey, _ := holdLease(t, ctx, second)
	var expected int32
	if err := owner.QueryRowContext(ctx, `SELECT hashtext(current_schema())`).Scan(&expected); err != nil {
		t.Fatal(err)
	}
	if key != expected || key == otherKey {
		t.Fatalf("维护锁必须按 schema 分离：key=%d expected=%d other=%d", key, expected, otherKey)
	}
}

func TestPostgreSQLMaintenanceLeaseSameSchemaAndReacquire(t *testing.T) {
	db := leasePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, key, release := holdLease(t, ctx, db)
	contender, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer contender.Close()
	otherKey, acquired, err := acquireLease(ctx, contender)
	if err != nil || acquired || otherKey != key {
		t.Fatalf("同 schema 租约必须互斥：acquired=%v key=%d want=%d err=%v", acquired, otherKey, key, err)
	}
	release()
	otherKey, acquired, err = acquireLease(ctx, contender)
	if err != nil || !acquired || otherKey != key {
		t.Fatalf("释放后应可重新取得租约：acquired=%v key=%d err=%v", acquired, otherKey, err)
	}
	releaseLease(contender, otherKey)
}

func TestPostgreSQLMaintenanceLeaseReleaseUsesSavedKey(t *testing.T) {
	db := leasePool(t)
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	owner, key, release := holdLease(t, ctx, db)
	var originalPID int
	if err := owner.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&originalPID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.ExecContext(ctx, `SET search_path TO pg_catalog`); err != nil {
		t.Fatal(err)
	}
	release()
	// 正常解锁应归还同一物理连接，而非解错键后靠丢弃连接掩盖泄漏。
	var pid int
	var held bool
	if err := db.QueryRowContext(ctx, `SELECT pg_backend_pid(),EXISTS(
 SELECT 1 FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid()
 AND classid=$1::oid AND objid::bigint=($2::bigint & 4294967295) AND objsubid=2)`, maintenanceNamespace, key).Scan(&pid, &held); err != nil {
		t.Fatal(err)
	}
	if pid != originalPID || held {
		t.Fatalf("必须用保存的键正常解锁：pid=%d original=%d held=%v", pid, originalPID, held)
	}
}

func TestPostgreSQLMaintenanceLeaseFailedReleaseDiscardsConnection(t *testing.T) {
	db := leasePool(t)
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	owner, key, release := holdLease(t, ctx, db)
	var originalPID int
	if err := owner.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&originalPID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.ExecContext(ctx, `SELECT pg_advisory_unlock($1::integer,$2::integer)`, maintenanceNamespace, key); err != nil {
		t.Fatal(err)
	}
	release()
	var pid int
	if err := db.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if pid == originalPID {
		t.Fatal("解锁失败的物理连接不得回池复用")
	}
}
