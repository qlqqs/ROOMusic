package main

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
)

type databaseState struct {
	connection *sql.DB
	ready      bool
}

// openDatabase 连接并迁移数据库，再恢复上次进程遗留的运行中扫描。
func openDatabase(ctx context.Context, databaseURL string) (*databaseState, error) {
	connection, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := recoverInterruptedScans(ctx, connection); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return &databaseState{connection: connection, ready: true}, nil
}

func recoverInterruptedScans(ctx context.Context, connection *sql.DB) (err error) {
	holder, acquired, err := acquireScanExecution(ctx, connection)
	if err != nil {
		return fmt.Errorf("acquire scan recovery lock: %w", err)
	}
	if !acquired {
		return nil
	}
	defer func() {
		if closeErr := holder.close(context.Background()); closeErr != nil && err == nil {
			err = fmt.Errorf("release scan recovery lock: %w", closeErr)
		}
	}()
	_, err = holder.connection.ExecContext(ctx, `UPDATE scan_runs
		SET status='incomplete', finished_at=NOW(), error_message='process_restarted'
		WHERE status='running'`)
	if err != nil {
		return fmt.Errorf("recover interrupted scans: %w", err)
	}
	return nil
}

func readinessStatus(database *databaseState) (int, string) {
	if database == nil || !database.ready {
		return 503, "not_ready"
	}
	return 200, "ready"
}
