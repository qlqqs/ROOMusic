package main

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

	_ "github.com/jackc/pgx/v5/stdlib"
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
}

type appliedMigration struct {
	Version  int64
	Name     sql.NullString
	Checksum sql.NullString
}

type migrationMetadataColumns struct {
	Name     bool
	Checksum bool
}

func (columns migrationMetadataColumns) complete() bool {
	return columns.Name && columns.Checksum
}

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
	return applyMigrationsFromFS(ctx, connection, migrations.Files)
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
	for _, entry := range entries {
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

	sort.Slice(result, func(i, j int) bool { return result[i].Version < result[j].Version })
	for index, current := range result {
		expected := int64(index + 1)
		if current.Version != expected {
			return nil, fmt.Errorf("missing migration version %d before %d", expected, current.Version)
		}
	}
	return result, nil
}

func applyMigrationsFromFS(ctx context.Context, connection *sql.DB, source fs.FS) (err error) {
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
	plan, err := buildMigrationPlan(available, history, columns)
	if err != nil {
		return err
	}

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
			columns.Name = true
			columns.Checksum = true
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
			)`).Scan(&columns.Name, &columns.Checksum)
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
	query := fmt.Sprintf(`SELECT version, %s, %s FROM schema_migrations ORDER BY version`, nameColumn, checksumColumn)
	rows, err := transaction.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations history: %w", err)
	}
	defer rows.Close()

	result := make([]appliedMigration, 0)
	seen := make(map[int64]struct{})
	for rows.Next() {
		var item appliedMigration
		if err := rows.Scan(&item.Version, &item.Name, &item.Checksum); err != nil {
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
	maximumAvailable := int64(0)
	for _, candidate := range available {
		if _, exists := descriptors[candidate.Version]; exists {
			return migrationPlan{}, fmt.Errorf("duplicate migration version %d", candidate.Version)
		}
		descriptors[candidate.Version] = candidate
		if candidate.Version > maximumAvailable {
			maximumAvailable = candidate.Version
		}
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
	if maximumRecorded > maximumAvailable {
		return migrationPlan{}, fmt.Errorf("schema_migrations contains future version %d (latest supported %d)", maximumRecorded, maximumAvailable)
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
		var storedName, storedChecksum sql.NullString
		if err := transaction.QueryRowContext(ctx, `
			INSERT INTO schema_migrations(version,name,checksum)
			VALUES($1,$2,$3)
			ON CONFLICT (version) DO UPDATE
			SET name=COALESCE(schema_migrations.name, EXCLUDED.name),
			    checksum=COALESCE(schema_migrations.checksum, EXCLUDED.checksum)
			RETURNING name,checksum`, candidate.Version, candidate.Name, candidate.Checksum).Scan(&storedName, &storedChecksum); err != nil {
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

func readinessStatus(database *databaseState) (int, string) {
	if database == nil || !database.ready {
		return 503, "not_ready"
	}
	return 200, "ready"
}
