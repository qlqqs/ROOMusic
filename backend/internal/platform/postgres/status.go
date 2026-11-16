package postgres

import (
	"context"
	"database/sql"
)

// CurrentMigrationVersion 返回当前 schema 已提交的迁移版本，不执行迁移。
func CurrentMigrationVersion(ctx context.Context, db *sql.DB) (int64, error) {
	var version int64
	err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&version)
	return version, err
}
