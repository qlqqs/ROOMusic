package personal

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReadTxSetLocalFailureRollsBack(t *testing.T) {
	sentinel := errors.New("injected SET LOCAL failure")
	probe := &readTxDriverProbe{setErr: sentinel}
	service := NewService(openReadTxProbe(t, probe))
	tx, err := service.readTx(context.Background())
	if tx != nil {
		_ = tx.Rollback()
		t.Fatal("failed SET LOCAL returned a usable transaction")
	}
	if !errors.Is(err, sentinel) || err == sentinel {
		t.Fatalf("SET LOCAL cause not wrapped and preserved: %v", err)
	}
	assertReadTxSetupFailure(t, probe)
}

func TestFavoriteTracksSetLocalFailureStopsBeforeBusinessQuery(t *testing.T) {
	sentinel := errors.New("injected SET LOCAL failure")
	probe := &readTxDriverProbe{setErr: sentinel}
	service := NewService(openReadTxProbe(t, probe))
	_, err := service.FavoriteTracks(context.Background(), "10000000-0000-4000-8000-000000000001", Page{Number: 1, Size: 50})
	if !errors.Is(err, sentinel) {
		t.Fatalf("read use case lost SET LOCAL failure: %v", err)
	}
	assertReadTxSetupFailure(t, probe)
}

func assertReadTxSetupFailure(t *testing.T, probe *readTxDriverProbe) {
	t.Helper()
	want := driver.TxOptions{Isolation: driver.IsolationLevel(sql.LevelRepeatableRead), ReadOnly: true}
	if len(probe.begins) != 1 || probe.begins[0] != want {
		t.Fatalf("read transaction options: %+v, want %+v", probe.begins, want)
	}
	if len(probe.execs) != 1 || probe.execs[0] != "SET LOCAL jit = off" || len(probe.queries) != 0 || probe.prepares != 0 {
		t.Fatalf("unexpected SQL after failed setup: execs=%v queries=%v prepares=%d", probe.execs, probe.queries, probe.prepares)
	}
	if probe.rollbacks != 1 || probe.commits != 0 {
		t.Fatalf("failed setup lifecycle: rollbacks=%d commits=%d", probe.rollbacks, probe.commits)
	}
}

func TestReadTxSetupDoesNotConfigureFavoriteWrite(t *testing.T) {
	probe := &readTxDriverProbe{}
	service := NewService(openReadTxProbe(t, probe))
	tx, err := service.readTx(context.Background())
	if err != nil || tx == nil {
		t.Fatalf("read setup: tx=%v err=%v", tx, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// 已不存在的收藏取消是成功的写用例；仍应使用 READ COMMITTED，不执行读事务的 SET。
	probe.setErr = errors.New("write must not configure read-only JIT")
	result, err := service.SetFavorite(context.Background(), "10000000-0000-4000-8000-000000000001", "track", "20000000-0000-4000-8000-000000000001", false)
	if err != nil || result.Favorited || result.Revision != 0 {
		t.Fatalf("favorite write: %+v err=%v", result, err)
	}
	readOptions := driver.TxOptions{Isolation: driver.IsolationLevel(sql.LevelRepeatableRead), ReadOnly: true}
	writeOptions := driver.TxOptions{Isolation: driver.IsolationLevel(sql.LevelReadCommitted)}
	if len(probe.begins) != 2 || probe.begins[0] != readOptions || probe.begins[1] != writeOptions {
		t.Fatalf("read/write transaction options: %+v", probe.begins)
	}
	if len(probe.execs) != 2 || probe.execs[0] != "SET LOCAL jit = off" || !strings.HasPrefix(probe.execs[1], "INSERT INTO personal_favorite_state(") {
		t.Fatalf("read configuration leaked into write: %v", probe.execs)
	}
	if len(probe.queries) != 2 || probe.prepares != 0 || probe.commits != 2 || probe.rollbacks != 0 {
		t.Fatalf("unexpected lifecycle: queries=%v prepares=%d commits=%d rollbacks=%d", probe.queries, probe.prepares, probe.commits, probe.rollbacks)
	}
}

// 每个测试独占一个 Connector，不注册全局驱动，也不连接 PostgreSQL。
type readTxDriverProbe struct {
	setErr             error
	begins             []driver.TxOptions
	execs, queries     []string
	prepares           int
	commits, rollbacks int
}

func openReadTxProbe(t *testing.T, probe *readTxDriverProbe) *sql.DB {
	t.Helper()
	db := sql.OpenDB(readTxProbeConnector{probe: probe})
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

type readTxProbeConnector struct{ probe *readTxDriverProbe }

func (c readTxProbeConnector) Connect(context.Context) (driver.Conn, error) {
	return &readTxProbeConn{probe: c.probe}, nil
}
func (c readTxProbeConnector) Driver() driver.Driver { return readTxProbeDriver{} }

type readTxProbeDriver struct{}

func (readTxProbeDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use the test connector")
}

type readTxProbeConn struct{ probe *readTxDriverProbe }

func (c *readTxProbeConn) Prepare(string) (driver.Stmt, error) {
	c.probe.prepares++
	return nil, errors.New("unexpected prepared statement")
}
func (c *readTxProbeConn) Close() error { return nil }
func (c *readTxProbeConn) Begin() (driver.Tx, error) {
	return nil, errors.New("BeginTx options are required")
}
func (c *readTxProbeConn) BeginTx(_ context.Context, options driver.TxOptions) (driver.Tx, error) {
	c.probe.begins = append(c.probe.begins, options)
	return readTxProbeTransaction{probe: c.probe}, nil
}
func (c *readTxProbeConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	c.probe.execs = append(c.probe.execs, query)
	if query == "SET LOCAL jit = off" {
		return driver.RowsAffected(0), c.probe.setErr
	}
	if strings.HasPrefix(query, "INSERT INTO personal_favorite_state(") {
		return driver.RowsAffected(0), nil
	}
	return nil, errors.New("unexpected execution")
}
func (c *readTxProbeConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.probe.queries = append(c.probe.queries, query)
	switch {
	case strings.HasPrefix(query, "SELECT revision FROM personal_favorite_state "):
		return &readTxProbeRows{value: int64(0)}, nil
	case strings.HasPrefix(query, "SELECT created_at FROM favorite_tracks "):
		return &readTxProbeRows{}, nil
	default:
		return nil, errors.New("unexpected business query")
	}
}

type readTxProbeTransaction struct{ probe *readTxDriverProbe }

func (tx readTxProbeTransaction) Commit() error   { tx.probe.commits++; return nil }
func (tx readTxProbeTransaction) Rollback() error { tx.probe.rollbacks++; return nil }

type readTxProbeRows struct{ value driver.Value }

func (*readTxProbeRows) Columns() []string { return []string{"value"} }
func (*readTxProbeRows) Close() error      { return nil }
func (r *readTxProbeRows) Next(values []driver.Value) error {
	if r.value == nil {
		return io.EOF
	}
	values[0], r.value = r.value, nil
	return nil
}
