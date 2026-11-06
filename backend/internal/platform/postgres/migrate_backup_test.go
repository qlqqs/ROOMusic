package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
)

type migrationBackupProbe struct {
	call  func(context.Context, *sql.DB, MigrationChange) (string, error)
	calls int
}

func (p *migrationBackupProbe) BackupBeforeMigration(ctx context.Context, db *sql.DB, c MigrationChange) (string, error) {
	p.calls++
	return p.call(ctx, db, c)
}
func (*migrationBackupProbe) LatestPreMigrationBackup(int64) (string, bool) { return "", false }
func backupMigrationFS(extra bool) fs.FS {
	statements := map[int64]string{
		1: `CREATE TABLE schema_migrations(version bigint PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now()); INSERT INTO schema_migrations(version) VALUES(1); CREATE TABLE backup_order_probe(id int PRIMARY KEY, title text); INSERT INTO backup_order_probe VALUES(1,'before');`,
		2: `SELECT 1;`, 3: `SELECT 1;`, 4: `SELECT 1;`, 5: `SELECT 1;`, 6: `SELECT 1;`, 7: `SELECT 1;`,
		8: `ALTER TABLE schema_migrations ADD COLUMN name text, ADD COLUMN checksum text;`,
	}
	if extra {
		statements[9] = `ALTER TABLE backup_order_probe ADD COLUMN migrated boolean NOT NULL DEFAULT true; UPDATE backup_order_probe SET title='after';`
	}
	return testMigrationFS(statements)
}
func backupMigrationDB(t *testing.T) *sql.DB {
	t.Helper()
	db := openMigrationTestDatabaseURL(t, postgrestest.NewDatabase(t, "migration_backup_"))
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { db.Close() })
	return db
}
func TestPostgreSQLMigrationPreBackupBeforeDDLAndLockProbe(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%v", legacy), func(t *testing.T) {
			db := backupMigrationDB(t)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if err := applyMigrationsFromFS(ctx, db, backupMigrationFS(false)); err != nil {
				t.Fatal(err)
			}
			if legacy {
				if _, err := db.Exec(`ALTER TABLE schema_migrations DROP COLUMN name, DROP COLUMN checksum; DELETE FROM schema_migrations WHERE version=8`); err != nil {
					t.Fatal(err)
				}
			}
			dir := t.TempDir()
			name := "roomusic-20261106-120000-pre-migration-8.dump"
			from := int64(8)
			if legacy {
				from = 7
				name = "roomusic-20261106-120000-pre-migration-7.dump"
			}
			probe := &migrationBackupProbe{call: func(ctx context.Context, db *sql.DB, change MigrationChange) (string, error) {
				if change.Direction != MigrationUpgrade || change.FromVersion != from || change.ToVersion != 9 {
					t.Errorf("变更合同：%+v", change)
				}
				wantVersions := []int64{9}
				if legacy {
					wantVersions = []int64{8, 9}
				}
				if !reflect.DeepEqual(change.Versions, wantVersions) {
					t.Errorf("待执行迁移：%v", change.Versions)
				}
				conn, err := db.Conn(ctx)
				if err != nil {
					return "", err
				}
				defer conn.Close()
				tx, err := conn.BeginTx(ctx, nil)
				if err != nil {
					return "", err
				}
				defer tx.Rollback()
				if _, err := tx.ExecContext(ctx, `SET LOCAL lock_timeout='5s'`); err != nil {
					return "", err
				}
				rows, err := tx.QueryContext(ctx, `SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename`)
				if err != nil {
					return "", err
				}
				var names []string
				for rows.Next() {
					var name string
					if err := rows.Scan(&name); err != nil {
						rows.Close()
						return "", err
					}
					names = append(names, name)
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return "", err
				}
				if len(names) < 2 {
					t.Error("锁探针必须包含业务表与schema_migrations")
				}
				for _, name := range names {
					if _, err := tx.ExecContext(ctx, `LOCK TABLE `+pgx.Identifier{"public", name}.Sanitize()+` IN ACCESS SHARE MODE`); err != nil {
						return "", fmt.Errorf("备份前表 %s 被DDL锁住：%w", name, err)
					}
				}
				var title string
				if err := tx.QueryRowContext(ctx, `SELECT title FROM backup_order_probe WHERE id=1`).Scan(&title); err != nil {
					return "", err
				}
				if title != "before" {
					t.Errorf("备份前已改库：%s", title)
				}
				return name, os.WriteFile(filepath.Join(dir, name), []byte("迁移前的合成备份"), 0600)
			}}
			if err := applyMigrationsFromFS(ctx, db, backupMigrationFS(true), WithMigrationBackups(probe)); err != nil {
				t.Fatal(err)
			}
			if probe.calls != 1 {
				t.Fatalf("应调用一次：%d", probe.calls)
			}
			if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
			var title string
			var migrated bool
			if err := db.QueryRow(`SELECT title,migrated FROM backup_order_probe`).Scan(&title, &migrated); err != nil || title != "after" || !migrated {
				t.Fatalf("迁移未执行：%s %v %v", title, migrated, err)
			}
			if err := applyMigrationsFromFS(ctx, db, backupMigrationFS(true), WithMigrationBackups(probe)); err != nil {
				t.Fatal(err)
			}
			if probe.calls != 1 {
				t.Fatal("无待执行迁移时不应备份")
			}
		})
	}
}
func TestPostgreSQLMigrationPreBackupFailurePreservesDatabase(t *testing.T) {
	db := backupMigrationDB(t)
	ctx := context.Background()
	if err := applyMigrationsFromFS(ctx, db, backupMigrationFS(false)); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("合成备份失败")
	probe := &migrationBackupProbe{call: func(context.Context, *sql.DB, MigrationChange) (string, error) { return "", sentinel }}
	err := applyMigrationsFromFS(ctx, db, backupMigrationFS(true), WithMigrationBackups(probe))
	if !errors.Is(err, ErrPreMigrationBackup) || !errors.Is(err, sentinel) {
		t.Fatalf("错误链应保留分类：%v", err)
	}
	var title string
	var count int
	if err := db.QueryRow(`SELECT title FROM backup_order_probe`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	var column bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='backup_order_probe' AND column_name='migrated')`).Scan(&column); err != nil {
		t.Fatal(err)
	}
	if title != "before" || count != 8 || column {
		t.Fatalf("备份失败仍改变数据库：%s %d %v", title, count, column)
	}
}
func TestPostgreSQLMigrationPreBackupEmptyDatabaseDoesNotBackup(t *testing.T) {
	db := backupMigrationDB(t)
	probe := &migrationBackupProbe{call: func(context.Context, *sql.DB, MigrationChange) (string, error) {
		return "", errors.New("空库不得调用")
	}}
	if err := applyMigrationsFromFS(context.Background(), db, backupMigrationFS(true), WithMigrationBackups(probe)); err != nil {
		t.Fatal(err)
	}
	if probe.calls != 0 {
		t.Fatalf("空库备份次数%d", probe.calls)
	}
}
func TestPostgreSQLMigrationPreBackupOpenReturnsClassifiedError(t *testing.T) {
	address := postgrestest.NewDatabase(t, "migration_open_")
	db := openMigrationTestDatabaseURL(t, address)
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE schema_migrations(version bigint PRIMARY KEY,applied_at timestamptz DEFAULT now()); INSERT INTO schema_migrations(version) VALUES(1); CREATE TABLE backup_order_probe(id int); INSERT INTO backup_order_probe VALUES(42)`); err != nil {
		t.Fatal(err)
	}
	probe := &migrationBackupProbe{call: func(context.Context, *sql.DB, MigrationChange) (string, error) { return "", errors.New("合成失败") }}
	opened, err := Open(context.Background(), address, WithMigrationBackups(probe))
	if opened != nil {
		opened.Close()
		t.Error("失败Open不应返回数据库")
	}
	if !errors.Is(err, ErrPreMigrationBackup) {
		t.Fatalf("Open错误：%v", err)
	}
	var id int
	if err := db.QueryRow(`SELECT id FROM backup_order_probe`).Scan(&id); err != nil || id != 42 {
		t.Fatalf("Open失败改变原库：%d %v", id, err)
	}
}
