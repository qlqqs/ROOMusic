package main

import (
	"context"
	"database/sql"

	"github.com/qlqq/roomusic/backend/internal/library"
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
	if err := library.RecoverInterruptedScans(ctx, connection); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return &databaseState{connection: connection, ready: true}, nil
}

func readinessStatus(database *databaseState) (int, string) {
	if database == nil || !database.ready {
		return 503, "not_ready"
	}
	return 200, "ready"
}
