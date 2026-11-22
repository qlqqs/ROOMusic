package app

import (
	"context"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// A-V17：测试事务持有本 schema 的目录写锁，按数据库实际排队顺序放入两个请求。
// 第一种检查连续写入的前态与归属，第二种检查撤销提交的不再提出标记对等待写者生效。
func TestPostgreSQLCurationConcurrent(t *testing.T) {
	for _, scenario := range []string{"write_write", "revert_write"} {
		t.Run(scenario, func(t *testing.T) {
			f := newCurationFixture(t)
			f.enable(t)
			var original string
			if scenario == "revert_write" {
				original = f.apply(t, f.a, "fixture", "", map[string]any{"title": "较晚值"})
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			tx, err := f.application.database.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			const gate = 0x43415450
			if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1::integer,hashtext(current_schema()))", gate); err != nil {
				t.Fatal(err)
			}
			type outcome struct {
				applied curation.Applied
				err     error
				status  int
			}
			queue := func(run func() outcome, n int) <-chan outcome {
				t.Helper()
				done := make(chan outcome, 1)
				go func() { done <- run() }()
				for {
					select {
					case r := <-done:
						t.Fatalf("第 %d 个请求越过事务持有的目录锁：%+v", n, r)
					default:
					}
					var waiters int
					err := f.application.database.QueryRowContext(ctx, `SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND database=(SELECT oid FROM pg_database WHERE datname=current_database()) AND classid=$1 AND objid=hashtext(current_schema())::oid AND objsubid=2 AND NOT granted`, gate).Scan(&waiters)
					if err != nil {
						t.Fatalf("等待目录锁排队：%v", err)
					}
					if waiters == n {
						return done
					}
				}
			}
			write := func(value string) outcome {
				a, err := f.writer.Apply(ctx, autoRequest(f.a, "fixture", "", map[string]any{"title": value}))
				return outcome{applied: a, err: err}
			}
			first := queue(func() outcome {
				if scenario == "write_write" {
					return write("较早值")
				}
				r := f.revert(t, &f.admin, original, uuid.New())
				return outcome{status: r.Code}
			}, 1)
			second := queue(func() outcome { return write("较晚值") }, 2)
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			a, b := <-first, <-second
			if scenario == "write_write" {
				if a.err != nil || b.err != nil {
					t.Fatalf("两个自动写入应成功：%v, %v", a.err, b.err)
				}
				f.textOwner(t, "title", "较晚值", b.applied.OperationID)
				f.undo(t, b.applied.OperationID)
				f.textOwner(t, "title", "较早值", a.applied.OperationID)
				f.undo(t, a.applied.OperationID)
				f.textOwner(t, "title", "", "")
			} else {
				if a.status != 200 {
					t.Fatalf("先取得锁的撤销 HTTP %d", a.status)
				}
				requireCurationProblem(t, b.err, "nothing_to_apply", "不再提出")
				if b.applied.Skipped["title"] != "suppressed" {
					t.Fatalf("后续写入跳过原因=%+v", b.applied)
				}
				f.textOwner(t, "title", "", "")
			}
		})
	}
}
