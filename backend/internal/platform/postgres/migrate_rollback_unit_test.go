package postgres

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/qlqq/roomusic/backend/migrations"
)

func rollbackTestFS(last int64) fstest.MapFS {
	statements := map[int64]string{}
	for v := int64(1); v <= last; v++ {
		statements[v] = "SELECT 1;"
	}
	statements[1] = `CREATE TABLE schema_migrations(version bigint PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now()); INSERT INTO schema_migrations(version) VALUES(1); CREATE TABLE rollback_probe(id int PRIMARY KEY, value text); INSERT INTO rollback_probe VALUES(1,'原有数据');`
	statements[8] = `ALTER TABLE schema_migrations ADD COLUMN name text, ADD COLUMN checksum text;`
	source := testMigrationFS(statements).(fstest.MapFS)
	for v := int64(31); v <= last; v++ {
		source[fmt.Sprintf("%04d_test.down.sql", v)] = &fstest.MapFile{Data: []byte("SELECT 1;\n")}
	}
	return source
}

func TestLoadMigrationsRollbackValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		alter func(fstest.MapFS)
		want  string
	}{
		{"缺少回退", func(f fstest.MapFS) { delete(f, "0031_test.down.sql") }, "migration 31 (0031_test.sql) is missing rollback file 0031_test.down.sql"},
		{"旧版本不得有回退", func(f fstest.MapFS) { f["0030_test.down.sql"] = &fstest.MapFile{Data: []byte("SELECT 1;")} }, "must not have rollback file"},
		{"孤儿回退", func(f fstest.MapFS) { f["0032_test.down.sql"] = &fstest.MapFile{Data: []byte("SELECT 1;")} }, "no matching forward migration"},
		{"名字不完全一致", func(f fstest.MapFS) {
			f["0031_other.down.sql"] = f["0031_test.down.sql"]
			delete(f, "0031_test.down.sql")
		}, "missing rollback file"},
		{"非UTF8", func(f fstest.MapFS) { f["0031_test.down.sql"].Data = []byte{255} }, "UTF-8 without NUL"},
		{"NUL", func(f fstest.MapFS) { f["0031_test.down.sql"].Data = []byte("SELECT 1;\x00") }, "UTF-8 without NUL"},
		{"空回退", func(f fstest.MapFS) { f["0031_test.down.sql"].Data = []byte(" \n-- 注释\n/* 外 /* 内 */ */ ; ") }, "is empty"},
		{"空理由", func(f fstest.MapFS) { f["0031_test.down.sql"].Data = []byte("-- roomusic:irreversible   \n-- 注释") }, "requires a reason"},
		{"标记后还有SQL", func(f fstest.MapFS) {
			f["0031_test.down.sql"].Data = []byte("-- roomusic:irreversible 无法恢复\nSELECT 1;")
		}, "must not be followed by SQL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := rollbackTestFS(31)
			tc.alter(f)
			_, err := loadMigrations(f)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("期望 %q，得到 %v", tc.want, err)
			}
		})
	}
	source := rollbackTestFS(31)
	raw := []byte("-- 回退原文\nSELECT 1;\n")
	source["0031_test.down.sql"].Data = raw
	got, err := loadMigrations(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 31 || got[30].Down == nil || got[30].Down.Checksum != migrationTestSHA256Hex(raw) || string(got[30].Down.SQL) != string(raw) {
		t.Fatalf("回退被当作正向或原文改变：%+v", got)
	}
	if got[0].Down != nil {
		t.Fatal("旧迁移不应有回退")
	}
	if _, err := loadMigrations(migrations.Files); err != nil {
		t.Fatalf("真实嵌入迁移不符合回退合同：%v", err)
	}
}

func TestParseMigrationDown(t *testing.T) {
	for _, tc := range []struct {
		raw          string
		irreversible bool
		reason       string
		bad          bool
	}{
		{"-- roomusic:irreversible 合并后不可拆分\n-- 备注\n", true, "合并后不可拆分", false},
		{"-- roomusic:irreversible\n", false, "", true},
		{"-- roomusic:irreversible理由", false, "", true},
		{"-- 注释\n-- roomusic:irreversible 理由", false, "", true},
		{"/* 未闭合", false, "", true},
		{"/* 外 /* 内 */ */\nSELECT 1;", false, "", false},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := parseMigrationDown("0031_test.down.sql", []byte(tc.raw))
			if (err != nil) != tc.bad {
				t.Fatalf("解析：%+v %v", got, err)
			}
			if !tc.bad && (got.Irreversible != tc.irreversible || got.Reason != tc.reason || got.Checksum != migrationTestSHA256Hex([]byte(tc.raw))) {
				t.Fatalf("标记/校验和：%+v", got)
			}
		})
	}
}

func rollbackHistory(last int64) []appliedMigration {
	var rows []appliedMigration
	for v := int64(1); v <= last; v++ {
		rows = append(rows, appliedMigration{Version: v})
	}
	return rows
}
func rollbackRow(v int64, raw string) appliedMigration {
	return appliedMigration{Version: v, Name: sql.NullString{String: fmt.Sprintf("%04d_test.sql", v), Valid: true}, DownSQL: sql.NullString{String: raw, Valid: true}, DownChecksum: sql.NullString{String: migrationTestSHA256Hex([]byte(raw)), Valid: true}}
}

func TestBuildMigrationStartupPlanRollback(t *testing.T) {
	available := testMigrationDescriptors(30)
	history := append(rollbackHistory(30), rollbackRow(31, "SELECT 1;"), rollbackRow(32, "SELECT 2;"))
	_, change, err := buildMigrationStartupPlan(available, history, migrationMetadataColumns{Name: true, Checksum: true, DownSQL: true, DownChecksum: true})
	if err != nil || change.Direction != MigrationDowngrade || change.FromVersion != 32 || change.ToVersion != 30 || !reflect.DeepEqual(change.Versions, []int64{32, 31}) {
		t.Fatalf("降级计划：%+v %v", change, err)
	}
	for _, tc := range []struct {
		name, reason string
		rows         func() []appliedMigration
		blocking     int64
	}{
		{"缺口", "not_contiguous", func() []appliedMigration { return append(rollbackHistory(30), rollbackRow(32, "SELECT 1;")) }, 32},
		{"不可回退", "irreversible", func() []appliedMigration {
			return append(rollbackHistory(30), rollbackRow(31, "-- roomusic:irreversible 合并不可逆"))
		}, 31},
		{"缺少SQL", "missing_rollback", func() []appliedMigration { return append(rollbackHistory(30), appliedMigration{Version: 31}) }, 31},
		{"校验和不符", "rollback_checksum_mismatch", func() []appliedMigration {
			r := rollbackRow(31, "SELECT 1;")
			r.DownChecksum.String = "bad"
			return append(rollbackHistory(30), r)
		}, 31},
		{"缺少校验和", "rollback_checksum_mismatch", func() []appliedMigration {
			r := rollbackRow(31, "SELECT 1;")
			r.DownChecksum.Valid = false
			return append(rollbackHistory(30), r)
		}, 31},
		{"本版本有待升级", "pending_upgrade", func() []appliedMigration { return append(rollbackHistory(29), rollbackRow(31, "SELECT 1;")) }, 31},
		{"unknown兼容", "not_contiguous", func() []appliedMigration { return append(rollbackHistory(30), appliedMigration{Version: 99}) }, 99},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := buildMigrationStartupPlan(available, tc.rows(), migrationMetadataColumns{})
			var ahead *SchemaAheadError
			if !errors.As(err, &ahead) || ahead.Reason != tc.reason || ahead.Version != tc.blocking || ahead.SupportedVersion != 30 || !strings.Contains(err.Error(), fmt.Sprintf("unknown version %d", tc.blocking)) {
				t.Fatalf("拒绝分类：%+v %v", ahead, err)
			}
			if tc.reason == "irreversible" && ahead.Detail != "合并不可逆" {
				t.Fatalf("丢失理由：%+v", ahead)
			}
		})
	}
}
