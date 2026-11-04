package personal

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestPostgreSQLReadTransactionJITIsLocal(t *testing.T) {
	f := newPersonalFixture(t)
	ctx := context.Background()
	conn, err := f.database.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var baseline string
	if err := conn.QueryRowContext(ctx, `SELECT current_setting('jit')`).Scan(&baseline); err != nil {
		t.Fatal(err)
	}
	for _, finish := range []string{"commit", "rollback"} {
		t.Run(finish, func(t *testing.T) {
			tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			if err := setLocalReadJITOff(ctx, tx); err != nil {
				t.Fatal(err)
			}
			var inside string
			if err := tx.QueryRowContext(ctx, `SELECT current_setting('jit')`).Scan(&inside); err != nil || inside != "off" {
				t.Fatalf("transaction jit=%q err=%v", inside, err)
			}
			if finish == "commit" {
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
			} else if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			var after string
			if err := conn.QueryRowContext(ctx, `SELECT current_setting('jit')`).Scan(&after); err != nil || after != baseline {
				t.Fatalf("connection jit after %s=%q, baseline=%q err=%v", finish, after, baseline, err)
			}
		})
	}
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := setLocalReadJITOff(cancelled, tx); !errors.Is(err, context.Canceled) {
		t.Fatalf("SET LOCAL failure not propagated: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var afterFailure string
	if err := conn.QueryRowContext(ctx, `SELECT current_setting('jit')`).Scan(&afterFailure); err != nil || afterFailure != baseline {
		t.Fatalf("failed SET LOCAL changed connection value: %q baseline=%q err=%v", afterFailure, baseline, err)
	}
}

func TestPostgreSQLServiceReadTransactionJITLifecycle(t *testing.T) {
	f := newPersonalFixture(t)
	f.database.SetMaxOpenConns(1)
	f.database.SetMaxIdleConns(1)
	ctx := context.Background()
	var baseline string
	var pid int
	if err := f.database.QueryRowContext(ctx, `SELECT current_setting('jit'),pg_backend_pid()`).Scan(&baseline, &pid); err != nil {
		t.Fatal(err)
	}
	for _, commit := range []bool{true, false} {
		tx, err := f.service.readTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var inside, readOnly, isolation string
		if err := tx.QueryRowContext(ctx, `SELECT current_setting('jit'),current_setting('transaction_read_only'),current_setting('transaction_isolation')`).Scan(&inside, &readOnly, &isolation); err != nil || inside != "off" || readOnly != "on" || isolation != "repeatable read" {
			t.Fatalf("read transaction settings jit=%q read_only=%q isolation=%q err=%v", inside, readOnly, isolation, err)
		}
		if commit {
			err = tx.Commit()
		} else {
			err = tx.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
		var after string
		var afterPID int
		if err := f.database.QueryRowContext(ctx, `SELECT current_setting('jit'),pg_backend_pid()`).Scan(&after, &afterPID); err != nil || after != baseline || afterPID != pid {
			t.Fatalf("connection default changed after commit=%v: value=%q baseline=%q pid=%d initial=%d err=%v", commit, after, baseline, afterPID, pid, err)
		}
	}
}
