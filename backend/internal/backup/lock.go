package backup

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"time"
)

const backupAdvisoryLockKey int64 = 0x524f4f4d4241434b

// DatabaseAccess 隔离会话锁与服务端版本查询，便于无数据库的 Service 单元测试。
// 生产实现持有专用连接直到 release，不允许把持锁连接归还池中。
type DatabaseAccess interface {
	Lock(context.Context, *sql.DB, bool) (release func(), acquired bool, err error)
	ServerMajor(context.Context, *sql.DB) (int, error)
}
type postgresAccess struct{}

func (postgresAccess) Lock(ctx context.Context, db *sql.DB, wait bool) (func(), bool, error) {
	if db == nil {
		return nil, false, failure("prepare", "connect_failed")
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, false, failure("prepare", "connect_failed")
	}
	var acquired bool
	if wait {
		_, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock($1::bigint)`, backupAdvisoryLockKey)
		acquired = err == nil
	} else {
		err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1::bigint)`, backupAdvisoryLockKey).Scan(&acquired)
	}
	if err != nil {
		// 请求取消可能发生在服务端已取锁之后；不复用结果不确定的连接。
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		_ = conn.Close()
		return nil, false, failure("prepare", "connect_failed")
	}
	if !acquired {
		_ = conn.Close()
		return nil, false, nil
	}
	return func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRowContext(unlockCtx, `SELECT pg_advisory_unlock($1::bigint)`, backupAdvisoryLockKey).Scan(&unlocked); err != nil || !unlocked {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		_ = conn.Close()
	}, true, nil
}
func (postgresAccess) ServerMajor(ctx context.Context, db *sql.DB) (int, error) {
	if db == nil {
		return 0, failure("prepare", "connect_failed")
	}
	var version int
	if err := db.QueryRowContext(ctx, `SHOW server_version_num`).Scan(&version); err != nil {
		return 0, failure("prepare", "connect_failed")
	}
	return version / 10000, nil
}

func (s *Service) acquire(ctx context.Context, db *sql.DB, wait bool) (func(), error) {
	release, acquired, err := s.database.Lock(ctx, db, false)
	if err != nil {
		return nil, err
	}
	if acquired {
		return release, nil
	}
	if !wait {
		return nil, ErrBusy
	}
	if s.onWaiting != nil {
		s.onWaiting()
	}
	s.logger.Info("等待其他备份完成", "event", "backup.waiting_for_lock")
	type result struct {
		release  func()
		acquired bool
		err      error
	}
	done := make(chan result, 1)
	go func() {
		release, acquired, err := s.database.Lock(ctx, db, true)
		done <- result{release, acquired, err}
	}()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case result := <-done:
			if result.err != nil {
				return nil, result.err
			}
			if !result.acquired {
				return nil, failure("prepare", "connect_failed")
			}
			return result.release, nil
		case <-ticker.C:
			s.logger.Info("等待其他备份完成", "event", "backup.waiting_for_lock")
		}
	}
}
