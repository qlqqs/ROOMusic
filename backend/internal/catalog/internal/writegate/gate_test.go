package writegate

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

type gateExecer struct {
	ctx   context.Context
	query string
	args  []any
	err   error
}

func (e *gateExecer) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	e.ctx, e.query, e.args = ctx, query, args
	return nil, e.err
}

func TestLockUsesTransactionGateAndReturnsFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failure := errors.New("写锁等待失败")
	for _, want := range []error{nil, failure} {
		e := &gateExecer{err: want}
		if err := Lock(ctx, e); !errors.Is(err, want) {
			t.Fatalf("锁错误未原样返回：%v", err)
		}
		if e.ctx != ctx || e.query != `SELECT pg_advisory_xact_lock($1::integer, $2::integer)` || len(e.args) != 2 || e.args[0] != 0x43415450 || e.args[1] != 1 {
			t.Fatalf("必须使用调用者事务与独立CATP键空间：%+v", e)
		}
	}
}
