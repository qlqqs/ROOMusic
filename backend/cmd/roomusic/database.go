package main

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"sort"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/qlqq/roomusic/backend/migrations"
)

type databaseState struct {
	connection *sql.DB
	ready      bool
}

func openDatabase(ctx context.Context, databaseURL string) (*databaseState, error) {
	connection, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := connection.PingContext(ctx); err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	if err := applyMigrations(ctx, connection); err != nil {
		_ = connection.Close()
		return nil, err
	}
	if err := recoverInterruptedScans(ctx, connection); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return &databaseState{connection: connection, ready: true}, nil
}

func recoverInterruptedScans(ctx context.Context, connection *sql.DB) error {
	_, err := connection.ExecContext(ctx, `UPDATE scan_runs
		SET status='incomplete', finished_at=NOW(), error_message='process_restarted'
		WHERE status='running'`)
	if err != nil {
		return fmt.Errorf("recover interrupted scans: %w", err)
	}
	return nil
}

func applyMigrations(ctx context.Context, connection *sql.DB) error {
	entries, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		return fmt.Errorf("discover migrations: %w", err)
	}
	sort.Strings(entries)
	for _, entry := range entries {
		migrationSQL, readErr := fs.ReadFile(migrations.Files, entry)
		if readErr != nil {
			return fmt.Errorf("read migration %s: %w", entry, readErr)
		}
		if _, execErr := connection.ExecContext(ctx, string(migrationSQL)); execErr != nil {
			return fmt.Errorf("apply migration %s: %w", entry, execErr)
		}
	}
	return nil
}

func readinessStatus(database *databaseState) (int, string) {
	if database == nil || !database.ready {
		return 503, "not_ready"
	}
	return 200, "ready"
}
