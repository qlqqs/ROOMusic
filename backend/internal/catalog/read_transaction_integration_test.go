package catalog

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestPostgreSQLCatalogReadLocalJIT(t *testing.T) {
	f := newPlacementFixture(t)
	ctx := context.Background()
	conn, err := f.database.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var before string
	if err := conn.QueryRowContext(ctx, "SHOW jit").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before != "on" {
		t.Fatalf("隔离测试库需默认开启 JIT 以检测配置泄漏：jit=%s", before)
	}
	for _, fail := range []bool{false, true} {
		expected := errors.New("受控读取故障")
		_, err := readWithLocalJITOff(ctx, conn, func(tx *sql.Tx) (bool, error) {
			var jit, readOnly, isolation string
			if err := tx.QueryRowContext(ctx, "SHOW jit").Scan(&jit); err != nil {
				return false, err
			}
			if err := tx.QueryRowContext(ctx, "SHOW transaction_read_only").Scan(&readOnly); err != nil {
				return false, err
			}
			if err := tx.QueryRowContext(ctx, "SHOW transaction_isolation").Scan(&isolation); err != nil {
				return false, err
			}
			if jit != "off" || readOnly != "on" || isolation != "repeatable read" {
				t.Fatalf("事务配置 %s/%s/%s", jit, readOnly, isolation)
			}
			if fail {
				return false, expected
			}
			return true, nil
		})
		if fail {
			if !errors.Is(err, ErrQuery) || !errors.Is(err, expected) {
				t.Fatalf("查询错误链: %v", err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
		var after string
		if err := conn.QueryRowContext(ctx, "SHOW jit").Scan(&after); err != nil {
			t.Fatal(err)
		}
		if after != before {
			t.Fatalf("连接配置泄漏: %s -> %s", before, after)
		}
	}
	for _, read := range []func() error{
		func() error { _, err := ReadRecentlyAddedReleaseGroups(ctx, conn); return err },
		func() error {
			_, err := ReadReleaseGroupsByAddedAt(ctx, conn, ListParams{Page: 1, PageSize: 50})
			return err
		},
	} {
		if err := read(); err != nil {
			t.Fatal(err)
		}
		var after string
		if err := conn.QueryRowContext(ctx, "SHOW jit").Scan(&after); err != nil || after != before {
			t.Fatalf("入口连接配置泄漏: %s %v", after, err)
		}
	}
}

type abortedReadBeginner struct{ connection *sql.Conn }

func (b abortedReadBeginner) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	tx, err := b.connection.BeginTx(ctx, opts)
	if err == nil {
		_, _ = tx.ExecContext(ctx, "SELECT 1/0")
	}
	return tx, err
}
func TestPostgreSQLCatalogReadLocalJITConfigurationFailure(t *testing.T) {
	f := newPlacementFixture(t)
	ctx := context.Background()
	conn, err := f.database.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var before string
	if err := conn.QueryRowContext(ctx, "SHOW jit").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before != "on" {
		t.Fatalf("隔离测试库需默认开启 JIT 以检测配置泄漏：jit=%s", before)
	}
	called := false
	_, err = readWithLocalJITOff(ctx, abortedReadBeginner{conn}, func(*sql.Tx) (bool, error) { called = true; return true, nil })
	if !errors.Is(err, ErrQuery) || called {
		t.Fatalf("配置失败未短路: %v %v", err, called)
	}
	var after string
	if err := conn.QueryRowContext(ctx, "SHOW jit").Scan(&after); err != nil || before != after {
		t.Fatalf("失败后连接不可复用或配置泄漏: %s %v", after, err)
	}
}
