package catalog

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

// 只验证事务编排和错误链；真实 PostgreSQL 的连接设置恢复另由集成测试验证。
func TestCatalogReadLocalJITLifecycle(t *testing.T) {
	for _, search := range []bool{false, true} {
		for _, failure := range []string{"", "begin", "configure", "state", "read", "commit"} {
			if !search && failure == "state" {
				continue
			}
			t.Run(fmt.Sprintf("search=%t/failure=%s", search, failure), func(t *testing.T) {
				cause := errors.New("受控事务故障")
				connection := &localJITTestConnection{failure: failure, cause: cause}
				db := sql.OpenDB(connection)
				t.Cleanup(func() { _ = db.Close() })
				read := func(tx *sql.Tx) (int, error) {
					if tx == nil || !connection.active || !connection.configured {
						t.Fatal("回调必须使用已关闭 JIT 的活动事务")
					}
					connection.events = append(connection.events, "read")
					if failure == "read" {
						return 42, cause
					}
					return 42, nil
				}
				var result int
				var err error
				if search {
					result, err = searchSnapshot(context.Background(), NewStore(db), func(snapshot *Store, state CatalogState, generation int64) (int, error) {
						if state.SearchMode != "legacy" || state.Epoch != 7 || generation != 0 {
							t.Fatalf("搜索状态未沿用事务读取结果: %+v generation=%d", state, generation)
						}
						tx, ok := snapshot.database.(*sql.Tx)
						if !ok {
							t.Fatal("搜索回调未使用事务 Store")
						}
						return read(tx)
					})
				} else {
					result, err = readWithLocalJITOff(context.Background(), db, read)
				}
				if failure == "" {
					if err != nil || result != 42 {
						t.Fatalf("成功结果: %d, %v", result, err)
					}
				} else if result != 0 || !errors.Is(err, ErrQuery) || !errors.Is(err, cause) {
					t.Fatalf("失败必须返回零值并保留分类及底层错误: %d, %v", result, err)
				}
				if search && (failure == "state" || failure == "read") {
					stage := "load search state"
					if failure == "read" {
						stage = "read search snapshot"
					}
					if !strings.Contains(err.Error(), stage) {
						t.Fatalf("丢失搜索阶段诊断: %v", err)
					}
				}
				want := []string{"begin", "configure", "read", "commit"}
				if search {
					want = []string{"begin", "configure", "state", "read", "commit"}
				}
				if failure != "" {
					for i, event := range want {
						if event == failure {
							want = want[:i+1]
							break
						}
					}
					if failure != "begin" && failure != "commit" {
						want = append(want, "rollback")
					}
				}
				if !reflect.DeepEqual(connection.events, want) || connection.active {
					t.Fatalf("事务顺序/释放错误: events=%v want=%v active=%t", connection.events, want, connection.active)
				}
				if connection.options.Isolation != driver.IsolationLevel(sql.LevelRepeatableRead) || !connection.options.ReadOnly {
					t.Fatalf("事务不是只读 RR: %+v", connection.options)
				}
			})
		}
	}
}

func TestSearchSnapshotRequiresTransactionBeginner(t *testing.T) {
	called := false
	result, err := searchSnapshot(context.Background(), NewStore(struct{ Queryer }{}), func(*Store, CatalogState, int64) (int, error) {
		called = true
		return 42, nil
	})
	if result != 0 || called || !errors.Is(err, ErrQuery) {
		t.Fatalf("不支持快照的输入未被拒绝: result=%d called=%t err=%v", result, called, err)
	}
}

type localJITTestConnection struct {
	failure    string
	cause      error
	events     []string
	options    driver.TxOptions
	active     bool
	configured bool
}

func (c *localJITTestConnection) Connect(context.Context) (driver.Conn, error) { return c, nil }
func (c *localJITTestConnection) Driver() driver.Driver                        { return c }
func (c *localJITTestConnection) Open(string) (driver.Conn, error)             { return c, nil }
func (c *localJITTestConnection) Close() error                                 { return nil }
func (c *localJITTestConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("不应预编译语句")
}
func (c *localJITTestConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("必须显式提供只读事务选项")
}
func (c *localJITTestConnection) BeginTx(_ context.Context, options driver.TxOptions) (driver.Tx, error) {
	c.events = append(c.events, "begin")
	c.options = options
	if c.failure == "begin" {
		return nil, c.cause
	}
	c.active = true
	return c, nil
}
func (c *localJITTestConnection) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	if query != "SET LOCAL jit = off" || !c.active || c.configured {
		return nil, fmt.Errorf("非预期配置语句或事务状态: %s", query)
	}
	c.events = append(c.events, "configure")
	if c.failure == "configure" {
		return nil, c.cause
	}
	c.configured = true
	return driver.RowsAffected(0), nil
}
func (c *localJITTestConnection) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if !c.active || !c.configured || !strings.Contains(query, "FROM catalog_projection_state") {
		return nil, fmt.Errorf("搜索状态不在已配置事务内读取: %s", query)
	}
	if strings.Contains(query, "SELECT catalog_epoch") {
		return &localJITStateRows{done: true}, nil
	}
	c.events = append(c.events, "state")
	if c.failure == "state" {
		return nil, c.cause
	}
	return &localJITStateRows{}, nil
}
func (c *localJITTestConnection) Commit() error {
	c.events = append(c.events, "commit")
	c.active = false
	if c.failure == "commit" {
		return c.cause
	}
	return nil
}
func (c *localJITTestConnection) Rollback() error {
	c.events = append(c.events, "rollback")
	c.active = false
	return nil
}

type localJITStateRows struct{ done bool }

func (*localJITStateRows) Columns() []string { return make([]string, 12) }
func (*localJITStateRows) Close() error      { return nil }
func (r *localJITStateRows) Next(values []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	copy(values, []driver.Value{int64(1), int64(0), int64(1), int64(1), int64(0), int64(0), int64(7), "build", "building", false, int64(0), time.Time{}})
	return nil
}
