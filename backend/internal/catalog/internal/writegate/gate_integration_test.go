package writegate

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
)

func gatePool(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", postgrestest.NewSchema(t, "catalog_gate_"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func gateTransaction(t *testing.T, ctx context.Context, db *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })
	return tx
}

func TestPostgreSQLWriteGateSeparatesSchemas(t *testing.T) {
	first, second := gatePool(t), gatePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	owner, other := gateTransaction(t, ctx, first), gateTransaction(t, ctx, second)
	if err := Lock(ctx, owner); err != nil {
		t.Fatal(err)
	}
	// 第一份事务仍持锁，另一 schema 必须独立取得自己的 CATP。
	if err := Lock(ctx, other); err != nil {
		t.Fatalf("跨 schema 的目录写者互相阻塞：%v", err)
	}
}

func TestPostgreSQLWriteGateSameSchemaAndTransactionRelease(t *testing.T) {
	for _, commit := range []bool{false, true} {
		name := "rollback"
		if commit {
			name = "commit"
		}
		t.Run(name, func(t *testing.T) {
			db := gatePool(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			owner, contender := gateTransaction(t, ctx, db), gateTransaction(t, ctx, db)
			if err := Lock(ctx, owner); err != nil {
				t.Fatal(err)
			}
			try := func(want bool) {
				t.Helper()
				var acquired bool
				if err := contender.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock($1::integer,hashtext(current_schema()))`, 0x43415450).Scan(&acquired); err != nil || acquired != want {
					t.Fatalf("同 schema CATP acquired=%v，期望 %v，错误：%v", acquired, want, err)
				}
			}
			try(false)
			var err error
			if commit {
				err = owner.Commit()
			} else {
				err = owner.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
			try(true)
		})
	}
}
