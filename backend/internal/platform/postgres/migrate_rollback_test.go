package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/migrations"
)

const rollbackBackupName = "roomusic-20261106-120000-pre-migration-32.dump"
const rollbackRestoreName = "roomusic-20261105-120000-pre-migration-30.dump"

type rollbackBackups struct {
	call          func(context.Context, *sql.DB, MigrationChange) (string, error)
	calls         int
	latestVersion int64
}

func (p *rollbackBackups) BackupBeforeMigration(ctx context.Context, db *sql.DB, c MigrationChange) (string, error) {
	p.calls++
	if p.call != nil {
		return p.call(ctx, db, c)
	}
	return rollbackBackupName, nil
}
func (p *rollbackBackups) LatestPreMigrationBackup(v int64) (string, bool) {
	p.latestVersion = v
	return rollbackRestoreName, true
}

func rollbackApply(t *testing.T, db *sql.DB, source fstest.MapFS, options ...OpenOption) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := applyMigrationsFromFS(ctx, db, source, options...); err != nil {
		t.Fatal(err)
	}
}
func rollbackExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
func rollbackSnapshot(t *testing.T, db *sql.DB) postgrestest.SchemaSnapshot {
	t.Helper()
	snapshot, err := postgrestest.Snapshot(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
func rollbackLog(t *testing.T, logs *bytes.Buffer, event string) map[string]any {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record["event"] == event {
			return record
		}
	}
	t.Fatalf("日志缺少 %s：%s", event, logs.String())
	return nil
}
func rollbackAssertRecoveryLog(t *testing.T, logs *bytes.Buffer, event, reason string, database, blocking int64) {
	t.Helper()
	record := rollbackLog(t, logs, event)
	for key, want := range map[string]any{"module": "platform", "level": "ERROR", "database_version": float64(database), "supported_version": float64(30), "blocking_version": float64(blocking), "reason": reason, "restore_backup": rollbackRestoreName} {
		if record[key] != want {
			t.Errorf("日志 %s=%v，期望 %v", key, record[key], want)
		}
	}
	if want := fmt.Sprintf("%04d_test.sql", blocking); record["blocking_name"] != want {
		t.Errorf("阻断迁移名：%v，期望 %s", record["blocking_name"], want)
	}
	detail := ""
	if reason == "irreversible" {
		detail = "合并后无法拆分"
	}
	if record["detail"] != detail {
		t.Errorf("不可回退理由：%v，期望 %q", record["detail"], detail)
	}
	for _, key := range []string{"msg", "message"} {
		text, _ := record[key].(string)
		if !strings.Contains(text, "数据库来自更新的版本") || !strings.Contains(text, rollbackRestoreName) {
			t.Errorf("中文恢复提示 %s：%q", key, text)
		}
	}
	if strings.Contains(logs.String(), "postgres://") || strings.Contains(logs.String(), "DROP TABLE") {
		t.Error("日志泄露连接串或SQL")
	}
}
func rollbackSources() (fstest.MapFS, fstest.MapFS) {
	old, newer := rollbackTestFS(30), rollbackTestFS(32)
	newer["0031_test.sql"].Data = []byte(`ALTER TABLE rollback_probe ADD COLUMN new_value text; CREATE TABLE rollback_child(id int PRIMARY KEY,probe_id int REFERENCES rollback_probe(id)); INSERT INTO rollback_child VALUES(1,1);`)
	newer["0031_test.down.sql"].Data = []byte(`DROP TABLE rollback_child; ALTER TABLE rollback_probe DROP COLUMN new_value;`)
	newer["0032_test.sql"].Data = []byte(`CREATE VIEW rollback_view AS SELECT * FROM rollback_child;`)
	newer["0032_test.down.sql"].Data = []byte(`DROP VIEW rollback_view;`)
	return old, newer
}

func TestPostgreSQLMigrationRollbackUpgradeDowngradeAndReupgrade(t *testing.T) {
	db := openMigrationTestDatabase(t)
	old, newer := rollbackSources()
	rollbackApply(t, db, old)
	initial := rollbackSnapshot(t, db)
	rollbackApply(t, db, newer)
	for _, v := range []int64{31, 32} {
		var raw, checksum string
		if err := db.QueryRow(`SELECT down_sql,down_checksum FROM schema_migrations WHERE version=$1`, v).Scan(&raw, &checksum); err != nil {
			t.Fatal(err)
		}
		want := newer[fmt.Sprintf("%04d_test.down.sql", v)].Data
		if raw != string(want) || checksum != migrationTestSHA256Hex(want) {
			t.Fatalf("回退副本没有同事务写入：%d %q %s", v, raw, checksum)
		}
	}
	var legacy int
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version<=30 AND (down_sql IS NOT NULL OR down_checksum IS NOT NULL)`).Scan(&legacy); err != nil || legacy != 0 {
		t.Fatalf("旧迁移不应记录回退：%d %v", legacy, err)
	}
	upgraded := rollbackSnapshot(t, db)
	var logs bytes.Buffer
	backup := &rollbackBackups{call: func(ctx context.Context, connection *sql.DB, c MigrationChange) (string, error) {
		if c.Direction != MigrationDowngrade || c.FromVersion != 32 || c.ToVersion != 30 || !reflect.DeepEqual(c.Versions, []int64{32, 31}) {
			t.Errorf("降级计划：%+v", c)
		}
		// 第二连接在备份阶段仍能读到完整新结构；DDL 锁会令限时查询失败。
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		var count int
		if err := connection.QueryRowContext(ctx, `SELECT count(*) FROM rollback_view`).Scan(&count); err != nil {
			return "", err
		}
		if count != 1 {
			t.Errorf("备份前已执行回退：%d", count)
		}
		return rollbackBackupName, nil
	}}
	rollbackApply(t, db, old, WithMigrationBackups(backup), WithLogger(slog.New(slog.NewJSONHandler(&logs, nil))))
	if backup.calls != 1 {
		t.Fatalf("备份次数：%d", backup.calls)
	}
	if diff := postgrestest.Diff(initial, rollbackSnapshot(t, db)); len(diff) > 0 {
		t.Fatalf("降级未恢复：%v", diff)
	}
	record := rollbackLog(t, &logs, "platform.migration.downgraded")
	for key, want := range map[string]any{"module": "platform", "level": "INFO", "from_version": float64(32), "to_version": float64(30), "backup": rollbackBackupName} {
		if record[key] != want {
			t.Errorf("降级日志 %s：%v", key, record[key])
		}
	}
	if !reflect.DeepEqual(record["versions"], []any{float64(32), float64(31)}) {
		t.Errorf("日志回退顺序：%v", record["versions"])
	}
	logs.Reset()
	rollbackApply(t, db, newer, WithLogger(slog.New(slog.NewJSONHandler(&logs, nil))))
	if diff := postgrestest.Diff(upgraded, rollbackSnapshot(t, db)); len(diff) > 0 {
		t.Fatalf("再次升级失败：%v", diff)
	}
	rollbackLog(t, &logs, "platform.migration.upgraded")
}

func TestPostgreSQLMigrationRollbackRefusedPreservesDatabase(t *testing.T) {
	for _, tc := range []struct {
		name, reason       string
		change             func(*testing.T, *sql.DB)
		database, blocking int64
	}{
		{"不可回退", "irreversible", func(t *testing.T, db *sql.DB) {
			raw := "-- roomusic:irreversible 合并后无法拆分"
			rollbackExec(t, db, `UPDATE schema_migrations SET down_sql=$1,down_checksum=$2 WHERE version=32`, raw, migrationTestSHA256Hex([]byte(raw)))
		}, 32, 32},
		{"不连续", "not_contiguous", func(t *testing.T, db *sql.DB) { rollbackExec(t, db, `DELETE FROM schema_migrations WHERE version=31`) }, 32, 32},
		{"缺少SQL", "missing_rollback", func(t *testing.T, db *sql.DB) {
			rollbackExec(t, db, `UPDATE schema_migrations SET down_sql=NULL WHERE version=32`)
		}, 32, 32},
		{"校验和不符", "rollback_checksum_mismatch", func(t *testing.T, db *sql.DB) {
			rollbackExec(t, db, `UPDATE schema_migrations SET down_checksum='bad' WHERE version=32`)
		}, 32, 32},
		{"没有回退列", "missing_rollback", func(t *testing.T, db *sql.DB) {
			rollbackExec(t, db, `ALTER TABLE schema_migrations DROP COLUMN down_sql,DROP COLUMN down_checksum`)
		}, 32, 32},
		{"仅缺校验和列", "rollback_checksum_mismatch", func(t *testing.T, db *sql.DB) {
			rollbackExec(t, db, `ALTER TABLE schema_migrations DROP COLUMN down_checksum`)
		}, 32, 32},
		{"仍有待升级", "pending_upgrade", func(t *testing.T, db *sql.DB) { rollbackExec(t, db, `DELETE FROM schema_migrations WHERE version=30`) }, 32, 31},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openMigrationTestDatabase(t)
			old, newer := rollbackSources()
			rollbackApply(t, db, newer)
			tc.change(t, db)
			before := rollbackSnapshot(t, db)
			var logs bytes.Buffer
			backup := &rollbackBackups{}
			err := applyMigrationsFromFS(context.Background(), db, old, WithMigrationBackups(backup), WithLogger(slog.New(slog.NewJSONHandler(&logs, nil))))
			var ahead *SchemaAheadError
			if !errors.As(err, &ahead) || ahead.Reason != tc.reason || ahead.DatabaseVersion != tc.database || ahead.SupportedVersion != 30 || ahead.Version != tc.blocking {
				t.Fatalf("错误分类：%+v %v", ahead, err)
			}
			if !strings.Contains(err.Error(), fmt.Sprintf("unknown version %d", tc.blocking)) {
				t.Error("破坏unknown version兼容")
			}
			if backup.calls != 0 || backup.latestVersion != 30 {
				t.Errorf("拒绝不能做备份，恢复应指向30：%+v", backup)
			}
			if diff := postgrestest.Diff(before, rollbackSnapshot(t, db)); len(diff) > 0 {
				t.Errorf("拒绝改变库（含回退列）：%v", diff)
			}
			rollbackAssertRecoveryLog(t, &logs, "platform.migration.downgrade_refused", tc.reason, tc.database, tc.blocking)
		})
	}
}

func TestPostgreSQLMigrationRollbackSQLFailureIsAtomic(t *testing.T) {
	db := openMigrationTestDatabase(t)
	old, newer := rollbackSources()
	rollbackApply(t, db, newer)
	raw := `DELETE FROM rollback_probe; SELECT * FROM rollback_missing_table;`
	// 32 先成功删视图，31 先删子表再触发错误；必须把全部 DDL、数据和历史删除撤销。
	raw = `DROP TABLE rollback_child; ` + raw
	rollbackExec(t, db, `UPDATE schema_migrations SET down_sql=$1,down_checksum=$2 WHERE version=31`, raw, migrationTestSHA256Hex([]byte(raw)))
	before := rollbackSnapshot(t, db)
	backup := &rollbackBackups{}
	var logs bytes.Buffer
	err := applyMigrationsFromFS(context.Background(), db, old, WithMigrationBackups(backup), WithLogger(slog.New(slog.NewJSONHandler(&logs, nil))))
	var failed *RollbackFailedError
	if !errors.As(err, &failed) || failed.Version != 31 || failed.DatabaseVersion != 32 || failed.SupportedVersion != 30 || failed.Name != "0031_test.sql" || failed.Backup != rollbackBackupName || errors.Unwrap(failed) == nil {
		t.Fatalf("回退错误：%+v %v", failed, err)
	}
	if strings.Contains(err.Error(), "rollback_missing_table") {
		t.Error("安全错误暴露SQL对象")
	}
	if backup.calls != 1 {
		t.Errorf("回退前应备份：%d", backup.calls)
	}
	if diff := postgrestest.Diff(before, rollbackSnapshot(t, db)); len(diff) > 0 {
		t.Errorf("失败未整体回滚：%v", diff)
	}
	rollbackAssertRecoveryLog(t, &logs, "platform.migration.downgrade_failed", "rollback_failed", 32, 31)
	if record := rollbackLog(t, &logs, "platform.migration.downgrade_failed"); record["backup"] != rollbackBackupName {
		t.Errorf("丢失本次备份：%v", record)
	}
}

func TestPostgreSQLMigrationRollbackPreBackupFailurePreservesDatabase(t *testing.T) {
	db := openMigrationTestDatabase(t)
	old, newer := rollbackSources()
	rollbackApply(t, db, newer)
	before := rollbackSnapshot(t, db)
	sentinel := errors.New("合成备份错误")
	var logs bytes.Buffer
	backup := &rollbackBackups{call: func(context.Context, *sql.DB, MigrationChange) (string, error) { return "", sentinel }}
	err := applyMigrationsFromFS(context.Background(), db, old, WithMigrationBackups(backup), WithLogger(slog.New(slog.NewJSONHandler(&logs, nil))))
	if !errors.Is(err, ErrPreMigrationBackup) || !errors.Is(err, sentinel) {
		t.Fatalf("备份错误链：%v", err)
	}
	if diff := postgrestest.Diff(before, rollbackSnapshot(t, db)); len(diff) > 0 {
		t.Errorf("备份失败改变数据库：%v", diff)
	}
	record := rollbackLog(t, &logs, "platform.migration.pre_backup_failed")
	if record["direction"] != "downgrade" || record["from_version"] != float64(32) || record["to_version"] != float64(30) {
		t.Errorf("备份失败字段：%v", record)
	}
}

func TestPostgreSQLMigrationRollbackKnownCopyDiffersOnlyWarns(t *testing.T) {
	db := openMigrationTestDatabase(t)
	source := rollbackTestFS(31)
	rollbackApply(t, db, source)
	raw := "-- 已入库的发布副本\nSELECT 2;"
	checksum := migrationTestSHA256Hex([]byte(raw))
	rollbackExec(t, db, `UPDATE schema_migrations SET down_sql=$1,down_checksum=$2 WHERE version=31`, raw, checksum)
	before := rollbackSnapshot(t, db)
	var logs bytes.Buffer
	rollbackApply(t, db, source, WithLogger(slog.New(slog.NewJSONHandler(&logs, nil))))
	record := rollbackLog(t, &logs, "platform.migration.rollback_differs")
	if record["level"] != "WARN" || record["module"] != "platform" || record["version"] != float64(31) {
		t.Errorf("告警字段：%v", record)
	}
	if diff := postgrestest.Diff(before, rollbackSnapshot(t, db)); len(diff) > 0 {
		t.Errorf("覆盖了数据库回退副本：%v", diff)
	}
}

func TestPostgreSQLMigrationRollbackLegacyColumnsAreTransactional(t *testing.T) {
	db := openMigrationTestDatabase(t)
	// 真实旧执行器留下 1/6/7 稀疏历史，既没有元数据列，也没有回退列。
	for version := int64(1); version <= 7; version++ {
		rollbackExec(t, db, string(findMigrationByVersion(t, version).SQL))
	}
	before := rollbackSnapshot(t, db)
	available, err := loadMigrations(migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	source := migrationPrefix(available, available[len(available)-1].Version)
	version := available[len(available)-1].Version + 1
	name := fmt.Sprintf("%04d_failure.sql", version)
	source[name] = &fstest.MapFile{Data: []byte(`SELECT * FROM legacy_upgrade_missing_table;`)}
	source[fmt.Sprintf("%04d_failure.down.sql", version)] = &fstest.MapFile{Data: []byte(`SELECT 1;`)}
	backup := &rollbackBackups{call: func(ctx context.Context, connection *sql.DB, c MigrationChange) (string, error) {
		if c.Direction != MigrationUpgrade || c.FromVersion != 7 || c.ToVersion != version {
			t.Errorf("旧库升级计划：%+v", c)
		}
		lockCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		var columns int
		if err := connection.QueryRowContext(lockCtx, `SELECT count(*) FROM pg_attribute WHERE attrelid='schema_migrations'::regclass AND attname IN ('name','checksum','down_sql','down_checksum') AND NOT attisdropped`).Scan(&columns); err != nil {
			return "", err
		}
		if columns != 0 {
			t.Errorf("备份前已加列：%d", columns)
		}
		tx, err := connection.BeginTx(lockCtx, nil)
		if err != nil {
			return "", err
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(lockCtx, `LOCK TABLE schema_migrations IN ACCESS SHARE MODE`); err != nil {
			return "", err
		}
		return "roomusic-20261106-120000-pre-migration-7.dump", nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := applyMigrationsFromFS(ctx, db, source, WithMigrationBackups(backup)); err == nil || !strings.Contains(err.Error(), "legacy_upgrade_missing_table") {
		t.Fatalf("合成末尾迁移应失败：%v", err)
	}
	if backup.calls != 1 {
		t.Errorf("备份次数：%d", backup.calls)
	}
	if diff := postgrestest.Diff(before, rollbackSnapshot(t, db)); len(diff) > 0 {
		t.Fatalf("0008和回退列未与升级一起回滚：%v", diff)
	}
	source[name].Data = []byte(`SELECT 1;`)
	rollbackApply(t, db, source, WithMigrationBackups(backup))
	var columns int
	if err := db.QueryRow(`SELECT count(*) FROM pg_attribute WHERE attrelid='schema_migrations'::regclass AND attname IN ('name','checksum','down_sql','down_checksum') AND NOT attisdropped`).Scan(&columns); err != nil || columns != 4 {
		t.Fatalf("重试未补齐追踪列：%d %v", columns, err)
	}
}

func TestMigrationRecoveryLogWithoutBackup(t *testing.T) {
	var logs bytes.Buffer
	settings := openOptions{logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	logMigrationRefused(settings, &SchemaAheadError{DatabaseVersion: 32, SupportedVersion: 30, Version: 31, Name: "0031_test.sql", Reason: "missing_rollback"})
	record := rollbackLog(t, &logs, "platform.migration.downgrade_refused")
	if record["restore_backup"] != "" || !strings.Contains(record["message"].(string), "升级前的那份迁移前备份") {
		t.Fatalf("没有归档时应提供中文兜底提示：%v", record)
	}
}
