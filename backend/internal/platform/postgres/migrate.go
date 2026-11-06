// Package postgres 负责 PostgreSQL 连接与嵌入迁移的执行：迁移版本连续、校验和防漂移、
// 事务级 advisory lock 串行化并发启动。它不含任何业务表查询。
package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/qlqq/roomusic/backend/migrations"
)

// migrationAdvisoryLockKey 是稳定且由本应用所有的 PostgreSQL advisory lock
// 命名空间。事务级锁会在提交、回滚、连接断开或上下文取消时自动释放。
const migrationAdvisoryLockKey int64 = 0x524f4f4d55534943 // ASCII 字符串 ROOMUSIC。

const migrationMetadataVersion int64 = 8

var migrationFilenamePattern = regexp.MustCompile(`^([0-9]+)_[A-Za-z0-9][A-Za-z0-9._-]*\.sql$`)

type migration struct {
	Version  int64
	Name     string
	SQL      []byte
	Checksum string
	Down     *migrationDown
}

type appliedMigration struct {
	Version      int64
	Name         sql.NullString
	Checksum     sql.NullString
	DownSQL      sql.NullString
	DownChecksum sql.NullString
}

type migrationMetadataColumns struct {
	Name         bool
	Checksum     bool
	DownSQL      bool
	DownChecksum bool
}

func (columns migrationMetadataColumns) complete() bool {
	return columns.Name && columns.Checksum
}

// Open 连接数据库、探测可用性并应用嵌入的迁移。成功时调用方拥有返回的连接。
func Open(ctx context.Context, databaseURL string, options ...OpenOption) (*sql.DB, error) {
	connection, err := Connect(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := applyMigrations(ctx, connection, options...); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return connection, nil
}

// Connect 只连接数据库并探测可用性，不迁移或初始化业务数据。
func Connect(ctx context.Context, databaseURL string) (*sql.DB, error) {
	settings, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return nil, &safeError{message: "数据库连接配置无效", cause: err}
	}
	if _, exists := settings.RuntimeParams["application_name"]; !exists {
		settings.RuntimeParams["application_name"] = "roomusic"
	}
	connection := stdlib.OpenDB(*settings)
	if err := connection.PingContext(ctx); err != nil {
		_ = connection.Close()
		return nil, &safeError{message: "无法连接数据库", cause: err}
	}
	return connection, nil
}

func applyMigrations(ctx context.Context, connection *sql.DB, options ...OpenOption) error {
	settings := openOptions{source: migrations.Files}
	for _, apply := range options {
		apply(&settings)
	}
	return applyMigrationsFromFS(ctx, connection, settings.source, options...)
}

// loadMigrations 在产生任何数据库副作用前发现并校验嵌入的迁移集合。校验和覆盖
// embed.FS 中发布的原始字节，包括注释和末尾换行。
func loadMigrations(source fs.FS) ([]migration, error) {
	if source == nil {
		return nil, errors.New("migration source is nil")
	}
	entries, err := fs.Glob(source, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("discover migrations: %w", err)
	}
	if len(entries) == 0 {
		return nil, errors.New("discover migrations: no migration files found")
	}
	sort.Strings(entries)

	result := make([]migration, 0, len(entries))
	seenVersions := make(map[int64]string, len(entries))
	rollbackFiles := make(map[string]bool)
	for _, entry := range entries {
		if strings.HasSuffix(entry, ".down.sql") {
			rollbackFiles[entry] = true
			continue
		}
		if entry != path.Base(entry) {
			return nil, fmt.Errorf("invalid migration path %q", entry)
		}
		matches := migrationFilenamePattern.FindStringSubmatch(entry)
		if len(matches) != 2 {
			return nil, fmt.Errorf("invalid migration filename %q", entry)
		}
		version, parseErr := strconv.ParseInt(matches[1], 10, 64)
		if parseErr != nil || version <= 0 {
			if parseErr == nil {
				parseErr = errors.New("version must be a positive integer")
			}
			return nil, fmt.Errorf("invalid migration version in %q: %w", entry, parseErr)
		}
		if previous, exists := seenVersions[version]; exists {
			return nil, fmt.Errorf("duplicate migration version %d in %q and %q", version, previous, entry)
		}
		migrationSQL, readErr := fs.ReadFile(source, entry)
		if readErr != nil {
			return nil, fmt.Errorf("read migration %s: %w", entry, readErr)
		}
		hash := sha256.Sum256(migrationSQL)
		seenVersions[version] = entry
		result = append(result, migration{
			Version:  version,
			Name:     entry,
			SQL:      migrationSQL,
			Checksum: hex.EncodeToString(hash[:]),
		})
	}

	for i := range result {
		candidate := &result[i]
		name := strings.TrimSuffix(candidate.Name, ".sql") + ".down.sql"
		if candidate.Version < rollbackRequiredFromVersion {
			if rollbackFiles[name] {
				return nil, fmt.Errorf("migration %d (%s) must not have rollback file %s", candidate.Version, candidate.Name, name)
			}
			continue
		}
		if !rollbackFiles[name] {
			return nil, fmt.Errorf("migration %d (%s) is missing rollback file %s", candidate.Version, candidate.Name, name)
		}
		data, err := fs.ReadFile(source, name)
		if err != nil {
			return nil, fmt.Errorf("read rollback file %s: %w", name, err)
		}
		down, err := parseMigrationDown(name, data)
		if err != nil {
			return nil, err
		}
		candidate.Down = &down
		delete(rollbackFiles, name)
	}
	for _, name := range entries {
		if rollbackFiles[name] {
			return nil, fmt.Errorf("rollback file %s has no matching forward migration", name)
		}
	}
	if len(result) == 0 {
		return nil, errors.New("discover migrations: no forward migration files found")
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Version < result[j].Version })
	for index, current := range result {
		expected := int64(index + 1)
		if current.Version != expected {
			return nil, fmt.Errorf("missing migration version %d before %d", expected, current.Version)
		}
	}
	return result, nil
}

func applyMigrationsFromFS(ctx context.Context, connection *sql.DB, source fs.FS, options ...OpenOption) (err error) {
	var settings openOptions
	for _, apply := range options {
		apply(&settings)
	}
	if connection == nil {
		return errors.New("apply migrations: database connection is nil")
	}
	available, err := loadMigrations(source)
	if err != nil {
		return err
	}
	if len(available) == 0 || available[0].Version != 1 {
		return errors.New("apply migrations: migration set must start at version 1")
	}
	metadataMigration, ok := migrationByVersion(available, migrationMetadataVersion)
	if !ok {
		return fmt.Errorf("apply migrations: metadata migration version %d is missing", migrationMetadataVersion)
	}

	transaction, err := connection.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration transaction: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if rollbackErr := transaction.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			if err == nil {
				err = fmt.Errorf("rollback migration transaction: %w", rollbackErr)
			} else {
				err = fmt.Errorf("%w (rollback migration transaction: %v)", err, rollbackErr)
			}
		}
	}()

	if _, err = transaction.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1::bigint)`, migrationAdvisoryLockKey); err != nil {
		return fmt.Errorf("acquire migration advisory lock: %w", err)
	}

	tableExists, err := schemaMigrationsTableExists(ctx, transaction)
	if err != nil {
		return fmt.Errorf("inspect schema_migrations table: %w", err)
	}
	if !tableExists {
		if err = executeMigration(ctx, transaction, available[0]); err != nil {
			return err
		}
	}

	columns, err := schemaMigrationsMetadataColumns(ctx, transaction)
	if err != nil {
		return fmt.Errorf("inspect schema_migrations metadata columns: %w", err)
	}
	history, err := readAppliedMigrations(ctx, transaction, columns)
	if err != nil {
		return err
	}
	plan, change, err := buildMigrationStartupPlan(available, history, columns)
	if err != nil {
		var ahead *SchemaAheadError
		if errors.As(err, &ahead) {
			logMigrationRefused(settings, ahead)
		}
		return err
	}
	warnRollbackDifferences(settings, available, history)

	// 已有库在此之前只查询系统目录与历史，不持有高于 AccessShareLock 的表锁。
	// 所有 DDL（包括回退列与 0008 元数据列）都必须在备份成功之后执行。
	var backupName string
	if tableExists && len(change.Versions) > 0 && settings.backups != nil {
		var backupErr error
		backupName, backupErr = settings.backups.BackupBeforeMigration(ctx, connection, change)
		if backupErr != nil {
			if settings.logger != nil {
				settings.logger.Error("迁移前备份失败，未执行迁移", "event", "platform.migration.pre_backup_failed", "module", "platform", "direction", change.Direction, "from_version", change.FromVersion, "to_version", change.ToVersion)
			}
			return &preMigrationBackupError{cause: backupErr}
		}
	}
	if !columns.DownSQL || !columns.DownChecksum {
		if _, err = transaction.ExecContext(ctx, `ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS down_sql TEXT, ADD COLUMN IF NOT EXISTS down_checksum TEXT`); err != nil {
			return fmt.Errorf("add migration rollback columns: %w", err)
		}
		columns.DownSQL, columns.DownChecksum = true, true
	}
	if change.Direction == MigrationDowngrade {
		if err = executeMigrationDowngrade(ctx, transaction, history, change, backupName); err != nil {
			var failed *RollbackFailedError
			if errors.As(err, &failed) {
				logMigrationRollbackFailed(settings, failed)
			}
			return err
		}
	} else {
		for _, candidate := range available {
			if plan.applied[candidate.Version] || plan.baseline[candidate.Version] {
				continue
			}
			if candidate.Version == migrationMetadataVersion && columns.complete() {
				plan.applied[candidate.Version] = true
				continue
			}
			if err = executeMigration(ctx, transaction, candidate); err != nil {
				return err
			}
			plan.applied[candidate.Version] = true
			if candidate.Version == migrationMetadataVersion {
				columns.Name, columns.Checksum = true, true
			}
		}
	}

	if !columns.complete() {
		// 只有成功执行 0008 才能添加元数据列；若执行后仍缺列，则保持关闭并报错。
		return fmt.Errorf("migration %d (%s): metadata columns are unavailable after execution", metadataMigration.Version, metadataMigration.Name)
	}
	if err = recordMigrationMetadata(ctx, transaction, available, plan); err != nil {
		return err
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("commit migration transaction: %w", err)
	}
	committed = true
	if len(change.Versions) > 0 && settings.logger != nil {
		event, message := "platform.migration.upgraded", "数据库迁移升级完成"
		if change.Direction == MigrationDowngrade {
			event, message = "platform.migration.downgraded", "数据库迁移降级完成"
		}
		settings.logger.Info(message, "event", event, "module", "platform", "from_version", change.FromVersion, "to_version", change.ToVersion, "versions", change.Versions, "backup", backupName)
	}
	return nil
}

func migrationByVersion(available []migration, version int64) (migration, bool) {
	for _, candidate := range available {
		if candidate.Version == version {
			return candidate, true
		}
	}
	return migration{}, false
}

func executeMigration(ctx context.Context, transaction *sql.Tx, candidate migration) error {
	if _, err := transaction.ExecContext(ctx, string(candidate.SQL)); err != nil {
		return fmt.Errorf("apply migration %d (%s): %w", candidate.Version, candidate.Name, err)
	}
	return nil
}

func schemaMigrationsTableExists(ctx context.Context, transaction *sql.Tx) (bool, error) {
	var exists bool
	if err := transaction.QueryRowContext(ctx, `SELECT to_regclass('schema_migrations') IS NOT NULL`).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func schemaMigrationsMetadataColumns(ctx context.Context, transaction *sql.Tx) (migrationMetadataColumns, error) {
	var columns migrationMetadataColumns
	err := transaction.QueryRowContext(ctx, `
		SELECT
			EXISTS (
				SELECT 1
				FROM pg_attribute
				WHERE attrelid = to_regclass('schema_migrations')
				  AND attname = 'name'
				  AND NOT attisdropped
			),
			EXISTS (
				SELECT 1
				FROM pg_attribute
				WHERE attrelid = to_regclass('schema_migrations')
				  AND attname = 'checksum'
				  AND NOT attisdropped
			),
			EXISTS (
				SELECT 1 FROM pg_attribute
				WHERE attrelid = to_regclass('schema_migrations')
				  AND attname = 'down_sql' AND NOT attisdropped
			),
			EXISTS (
				SELECT 1 FROM pg_attribute
				WHERE attrelid = to_regclass('schema_migrations')
				  AND attname = 'down_checksum' AND NOT attisdropped
			)`).Scan(&columns.Name, &columns.Checksum, &columns.DownSQL, &columns.DownChecksum)
	return columns, err
}

func readAppliedMigrations(ctx context.Context, transaction *sql.Tx, columns migrationMetadataColumns) ([]appliedMigration, error) {
	nameColumn := "NULL::text"
	checksumColumn := "NULL::text"
	if columns.Name {
		nameColumn = "name"
	}
	if columns.Checksum {
		checksumColumn = "checksum"
	}
	downSQLColumn, downChecksumColumn := "NULL::text", "NULL::text"
	if columns.DownSQL {
		downSQLColumn = "down_sql"
	}
	if columns.DownChecksum {
		downChecksumColumn = "down_checksum"
	}
	query := fmt.Sprintf(`SELECT version, %s, %s, %s, %s FROM schema_migrations ORDER BY version`, nameColumn, checksumColumn, downSQLColumn, downChecksumColumn)
	rows, err := transaction.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations history: %w", err)
	}
	defer rows.Close()

	result := make([]appliedMigration, 0)
	seen := make(map[int64]struct{})
	for rows.Next() {
		var item appliedMigration
		if err := rows.Scan(&item.Version, &item.Name, &item.Checksum, &item.DownSQL, &item.DownChecksum); err != nil {
			return nil, fmt.Errorf("read schema_migrations row: %w", err)
		}
		if _, exists := seen[item.Version]; exists {
			return nil, fmt.Errorf("schema_migrations contains duplicate version %d", item.Version)
		}
		seen[item.Version] = struct{}{}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema_migrations history: %w", err)
	}
	return result, nil
}

type migrationPlan struct {
	applied  map[int64]bool
	baseline map[int64]bool
}

func buildMigrationPlan(available []migration, history []appliedMigration, columns migrationMetadataColumns) (migrationPlan, error) {
	if len(available) == 0 {
		return migrationPlan{}, errors.New("build migration plan: no migrations available")
	}
	descriptors := make(map[int64]migration, len(available))
	for _, candidate := range available {
		if _, exists := descriptors[candidate.Version]; exists {
			return migrationPlan{}, fmt.Errorf("duplicate migration version %d", candidate.Version)
		}
		descriptors[candidate.Version] = candidate
	}
	applied := make(map[int64]bool, len(history))
	for _, item := range history {
		if applied[item.Version] {
			return migrationPlan{}, fmt.Errorf("schema_migrations contains duplicate version %d", item.Version)
		}
		if err := validateAppliedMigration(available, item); err != nil {
			return migrationPlan{}, err
		}
		applied[item.Version] = true
	}
	if !applied[1] {
		return migrationPlan{}, errors.New("schema_migrations is missing required version 1")
	}

	maximumRecorded := int64(0)
	for version := range applied {
		if version > maximumRecorded {
			maximumRecorded = version
		}
	}

	baseline := make(map[int64]bool)
	if columns.complete() {
		// 两列同时存在说明 0008 的 DDL 已原子提交。旧执行器可能在 ALTER 后停止、
		// 尚未写入版本行，此时只建立一次性基线，不能再次执行 ALTER。
		if !applied[migrationMetadataVersion] {
			baseline[migrationMetadataVersion] = true
			applied[migrationMetadataVersion] = true
		}
	}
	if maximumRecorded >= 6 {
		for version := int64(6); version <= maximumRecorded; version++ {
			if !applied[version] {
				return migrationPlan{}, fmt.Errorf("schema_migrations history skips version %d before %d", version, maximumRecorded)
			}
		}
		for version := int64(2); version <= 5; version++ {
			if !applied[version] {
				baseline[version] = true
			}
		}
	} else {
		for version := int64(1); version <= maximumRecorded; version++ {
			if !applied[version] {
				return migrationPlan{}, fmt.Errorf("schema_migrations history skips version %d before %d", version, maximumRecorded)
			}
		}
	}

	return migrationPlan{applied: applied, baseline: baseline}, nil
}

func recordMigrationMetadata(ctx context.Context, transaction *sql.Tx, available []migration, plan migrationPlan) error {
	columns, err := schemaMigrationsMetadataColumns(ctx, transaction)
	if err != nil {
		return fmt.Errorf("verify schema_migrations metadata columns: %w", err)
	}
	if !columns.complete() {
		return errors.New("record migration metadata: schema_migrations metadata columns are missing")
	}
	// 迁移 SQL 可能修改 tracking 表；提交前重新读取并校验完整历史，避免
	// 未发布版本、重复行或元数据漂移被下面的逐版本 upsert 静默忽略。
	history, err := readAppliedMigrations(ctx, transaction, columns)
	if err != nil {
		return err
	}
	for _, item := range history {
		if err := validateAppliedMigration(available, item); err != nil {
			return err
		}
	}
	for _, candidate := range available {
		if !plan.applied[candidate.Version] && !plan.baseline[candidate.Version] {
			return fmt.Errorf("migration %d (%s) was neither applied nor baselined", candidate.Version, candidate.Name)
		}
		var downSQL, downChecksum any
		if candidate.Version >= rollbackRequiredFromVersion && candidate.Down != nil {
			downSQL, downChecksum = string(candidate.Down.SQL), candidate.Down.Checksum
		}
		var storedName, storedChecksum sql.NullString
		if err := transaction.QueryRowContext(ctx, `
			INSERT INTO schema_migrations(version,name,checksum,down_sql,down_checksum)
			VALUES($1,$2,$3,$4,$5)
			ON CONFLICT (version) DO UPDATE
			SET name=COALESCE(schema_migrations.name, EXCLUDED.name),
			    checksum=COALESCE(schema_migrations.checksum, EXCLUDED.checksum),
			    down_sql=COALESCE(schema_migrations.down_sql, EXCLUDED.down_sql),
			    down_checksum=COALESCE(schema_migrations.down_checksum, EXCLUDED.down_checksum)
			RETURNING name,checksum`, candidate.Version, candidate.Name, candidate.Checksum, downSQL, downChecksum).Scan(&storedName, &storedChecksum); err != nil {
			return fmt.Errorf("record migration %d (%s): %w", candidate.Version, candidate.Name, err)
		}
		if !storedName.Valid || storedName.String != candidate.Name {
			actual := "NULL"
			if storedName.Valid {
				actual = storedName.String
			}
			return fmt.Errorf("migration %d name drift: database has %q, expected %q", candidate.Version, actual, candidate.Name)
		}
		if !storedChecksum.Valid || storedChecksum.String != candidate.Checksum {
			actual := "NULL"
			if storedChecksum.Valid {
				actual = storedChecksum.String
			}
			return fmt.Errorf("migration %d (%s) checksum drift: database has %q, expected %q", candidate.Version, candidate.Name, actual, candidate.Checksum)
		}
	}
	return nil
}

func validateAppliedMigration(available []migration, item appliedMigration) error {
	candidate, known := migrationByVersion(available, item.Version)
	if !known {
		return fmt.Errorf("schema_migrations contains unknown version %d", item.Version)
	}
	if item.Name.Valid && item.Name.String != candidate.Name {
		return fmt.Errorf("migration %d name drift: database has %q, expected %q", item.Version, item.Name.String, candidate.Name)
	}
	if item.Checksum.Valid && item.Checksum.String != candidate.Checksum {
		return fmt.Errorf("migration %d (%s) checksum drift: database has %q, expected %q", item.Version, candidate.Name, item.Checksum.String, candidate.Checksum)
	}
	return nil
}
