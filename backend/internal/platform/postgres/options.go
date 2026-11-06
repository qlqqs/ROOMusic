package postgres

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"log/slog"

	"github.com/qlqq/roomusic/backend/migrations"
)

type MigrationDirection string

const (
	MigrationUpgrade   MigrationDirection = "upgrade"
	MigrationDowngrade MigrationDirection = "downgrade"
)

type MigrationChange struct {
	Direction              MigrationDirection
	FromVersion, ToVersion int64
	Versions               []int64
}

// MigrationBackups 是迁移执行器消费的备份端口；备份失败时不得执行 DDL。
type MigrationBackups interface {
	BackupBeforeMigration(context.Context, *sql.DB, MigrationChange) (string, error)
	LatestPreMigrationBackup(int64) (string, bool)
}
type openOptions struct {
	backups MigrationBackups
	logger  *slog.Logger
	source  fs.FS
}
type OpenOption func(*openOptions)

func WithMigrationBackups(backups MigrationBackups) OpenOption {
	return func(o *openOptions) { o.backups = backups }
}
func WithLogger(logger *slog.Logger) OpenOption { return func(o *openOptions) { o.logger = logger } }

// WithMigrationFS 替换本次 Open 的迁移集合，供隔离迁移演练与跨包集成测试使用。
// 不设置时使用二进制内嵌集合；显式传 nil 会在加载时失败。
func WithMigrationFS(source fs.FS) OpenOption {
	return func(o *openOptions) { o.source = source }
}

var ErrPreMigrationBackup = errors.New("迁移前备份失败")

// LatestMigrationVersion 返回本二进制支持的最高迁移版本，供恢复确认提示使用。
func LatestMigrationVersion() (int64, error) {
	available, err := loadMigrations(migrations.Files)
	if err != nil {
		return 0, err
	}
	return available[len(available)-1].Version, nil
}

// safeError 保留错误链供 errors.Is/As 使用，但日志只得到固定、安全的描述。
type safeError struct {
	message string
	cause   error
}

func (e *safeError) Error() string { return e.message }
func (e *safeError) Unwrap() error { return e.cause }

type preMigrationBackupError struct{ cause error }

func (e *preMigrationBackupError) Error() string        { return ErrPreMigrationBackup.Error() }
func (e *preMigrationBackupError) Unwrap() error        { return e.cause }
func (e *preMigrationBackupError) Is(target error) bool { return target == ErrPreMigrationBackup }
