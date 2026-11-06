package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const rollbackRequiredFromVersion int64 = 31
const irreversibleMarker = "-- roomusic:irreversible"

type migrationDown struct {
	Name         string
	SQL          []byte
	Checksum     string
	Irreversible bool
	Reason       string
}

// parseMigrationDown 同时校验发布文件和库中的回退副本，不改变参与校验和的原始字节。
func parseMigrationDown(name string, data []byte) (migrationDown, error) {
	down := migrationDown{Name: name, SQL: data}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return down, fmt.Errorf("rollback file %s must be UTF-8 without NUL", name)
	}
	hash := sha256.Sum256(data)
	down.Checksum = hex.EncodeToString(hash[:])
	text := strings.TrimSpace(string(data))
	if strings.HasPrefix(text, irreversibleMarker) {
		lines := strings.Split(text, "\n")
		suffix := strings.TrimPrefix(lines[0], irreversibleMarker)
		if len(suffix) == 0 || (suffix[0] != ' ' && suffix[0] != '\t') || strings.TrimSpace(suffix) == "" {
			return down, fmt.Errorf("rollback file %s: irreversible marker requires a reason", name)
		}
		for _, line := range lines[1:] {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "--") {
				return down, fmt.Errorf("rollback file %s: irreversible marker must not be followed by SQL", name)
			}
		}
		down.Irreversible, down.Reason = true, strings.TrimSpace(suffix)
		return down, nil
	}
	// 这里只识别前导注释与空白，不改写或尝试解析实际 SQL；块注释允许 PostgreSQL 的嵌套形式。
	for text != "" {
		if strings.HasPrefix(text, "--") {
			if strings.HasPrefix(text, irreversibleMarker) {
				return down, fmt.Errorf("rollback file %s: irreversible marker must be first", name)
			}
			_, rest, _ := strings.Cut(text, "\n")
			text = strings.TrimSpace(rest)
		} else if strings.HasPrefix(text, "/*") {
			depth, i := 1, 2
			for i < len(text) && depth > 0 {
				switch {
				case strings.HasPrefix(text[i:], "/*"):
					depth++
					i += 2
				case strings.HasPrefix(text[i:], "*/"):
					depth--
					i += 2
				default:
					i++
				}
			}
			if depth != 0 {
				return down, fmt.Errorf("rollback file %s: unterminated comment", name)
			}
			text = strings.TrimSpace(text[i:])
		} else if strings.HasPrefix(text, ";") {
			text = strings.TrimSpace(text[1:])
		} else {
			return down, nil
		}
	}
	return down, fmt.Errorf("rollback file %s is empty", name)
}

// SchemaAheadError 表示库来自更新版本且无法自动回退；拒绝发生在任何 DDL 之前。
type SchemaAheadError struct {
	DatabaseVersion, SupportedVersion, Version int64
	Name, Reason, Detail                       string
}

func (e *SchemaAheadError) Error() string {
	return fmt.Sprintf("schema_migrations contains unknown version %d (latest supported %d): cannot roll back: %s", e.Version, e.SupportedVersion, e.Reason)
}

// RollbackFailedError 保留底层错误链，但 Error 不暴露 SQL、数据库对象内容或连接信息。
type RollbackFailedError struct {
	DatabaseVersion, SupportedVersion, Version int64
	Name, Backup                               string
	Err                                        error
}

func (e *RollbackFailedError) Error() string {
	return fmt.Sprintf("rollback migration %d failed (database version %d, latest supported %d)", e.Version, e.DatabaseVersion, e.SupportedVersion)
}
func (e *RollbackFailedError) Unwrap() error { return e.Err }

// buildMigrationStartupPlan 是纯计划入口；旧的升级计划与历史基线规则保持不变。
func buildMigrationStartupPlan(available []migration, history []appliedMigration, columns migrationMetadataColumns) (migrationPlan, MigrationChange, error) {
	change := MigrationChange{Direction: MigrationUpgrade}
	if len(available) == 0 {
		return migrationPlan{}, change, fmt.Errorf("build migration plan: no migrations available")
	}
	change.ToVersion = available[len(available)-1].Version
	known := make(map[int64]bool, len(available))
	seen := make(map[int64]bool, len(history))
	var unknown []appliedMigration
	for _, item := range history {
		if seen[item.Version] {
			return migrationPlan{}, change, fmt.Errorf("schema_migrations contains duplicate version %d", item.Version)
		}
		seen[item.Version] = true
		if item.Version > change.FromVersion {
			change.FromVersion = item.Version
		}
		if _, ok := migrationByVersion(available, item.Version); !ok {
			unknown = append(unknown, item)
			continue
		}
		if err := validateAppliedMigration(available, item); err != nil {
			return migrationPlan{}, change, err
		}
		known[item.Version] = true
	}
	if len(unknown) == 0 {
		plan, err := buildMigrationPlan(available, history, columns)
		if err != nil {
			return plan, change, err
		}
		for _, candidate := range available {
			if !plan.applied[candidate.Version] && !plan.baseline[candidate.Version] {
				change.Versions = append(change.Versions, candidate.Version)
			}
		}
		return plan, change, nil
	}
	sort.Slice(unknown, func(i, j int) bool { return unknown[i].Version < unknown[j].Version })
	refuse := func(item appliedMigration, reason, detail string) (migrationPlan, MigrationChange, error) {
		return migrationPlan{}, change, &SchemaAheadError{DatabaseVersion: change.FromVersion, SupportedVersion: change.ToVersion, Version: item.Version, Name: item.Name.String, Reason: reason, Detail: detail}
	}
	for _, candidate := range available {
		if !known[candidate.Version] {
			return refuse(unknown[0], "pending_upgrade", "")
		}
	}
	for i, item := range unknown {
		if item.Version != change.ToVersion+int64(i)+1 {
			return refuse(item, "not_contiguous", "")
		}
	}
	change.Direction = MigrationDowngrade
	for i := len(unknown) - 1; i >= 0; i-- {
		item := unknown[i]
		if item.Version < rollbackRequiredFromVersion || !item.DownSQL.Valid {
			return refuse(item, "missing_rollback", "")
		}
		hash := sha256.Sum256([]byte(item.DownSQL.String))
		if !item.DownChecksum.Valid || hex.EncodeToString(hash[:]) != item.DownChecksum.String {
			return refuse(item, "rollback_checksum_mismatch", "")
		}
		down, err := parseMigrationDown(item.Name.String, []byte(item.DownSQL.String))
		if err != nil {
			return refuse(item, "missing_rollback", "")
		}
		if down.Irreversible {
			return refuse(item, "irreversible", down.Reason)
		}
		change.Versions = append(change.Versions, item.Version)
	}
	return migrationPlan{applied: known, baseline: make(map[int64]bool)}, change, nil
}

func executeMigrationDowngrade(ctx context.Context, tx *sql.Tx, history []appliedMigration, change MigrationChange, backup string) error {
	records := make(map[int64]appliedMigration, len(history))
	for _, item := range history {
		records[item.Version] = item
	}
	for _, version := range change.Versions {
		item := records[version]
		_, err := tx.ExecContext(ctx, item.DownSQL.String)
		if err == nil {
			_, err = tx.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version = $1`, version)
		}
		if err != nil {
			return &RollbackFailedError{DatabaseVersion: change.FromVersion, SupportedVersion: change.ToVersion, Version: version, Name: item.Name.String, Backup: backup, Err: err}
		}
	}
	return nil
}

func warnRollbackDifferences(settings openOptions, available []migration, history []appliedMigration) {
	if settings.logger == nil {
		return
	}
	for _, item := range history {
		candidate, known := migrationByVersion(available, item.Version)
		if known && candidate.Down != nil && item.DownChecksum.Valid && item.DownChecksum.String != "" && item.DownChecksum.String != candidate.Down.Checksum {
			settings.logger.Warn("回退文件与数据库副本不同，保留数据库副本", "event", "platform.migration.rollback_differs", "module", "platform", "version", item.Version)
		}
	}
}

func logMigrationRefused(settings openOptions, e *SchemaAheadError) {
	logMigrationRecovery(settings, "platform.migration.downgrade_refused", e.DatabaseVersion, e.SupportedVersion, e.Version, e.Name, e.Reason, e.Detail, "")
}

func logMigrationRollbackFailed(settings openOptions, e *RollbackFailedError) {
	logMigrationRecovery(settings, "platform.migration.downgrade_failed", e.DatabaseVersion, e.SupportedVersion, e.Version, e.Name, "rollback_failed", "", e.Backup)
}

func logMigrationRecovery(settings openOptions, event string, database, supported, blocking int64, name, reason, detail, backup string) {
	if settings.logger == nil {
		return
	}
	restore := ""
	if settings.backups != nil {
		if filename, ok := settings.backups.LatestPreMigrationBackup(supported); ok {
			restore = filename
		}
	}
	hint := restore
	if hint == "" {
		hint = "<升级前的迁移前备份>"
	}
	message := fmt.Sprintf("数据库来自更新的版本（迁移 %d），迁移 %d 不能自动回退。恢复备份：docker compose stop app && docker compose run --rm app restore %s && docker compose up -d --wait；或改回能识别迁移 %d 的版本。", database, blocking, hint, database)
	attrs := []any{"event", event, "module", "platform", "database_version", database, "supported_version", supported, "blocking_version", blocking, "blocking_name", name, "reason", reason, "detail", detail, "restore_backup", restore, "message", message}
	if event == "platform.migration.downgrade_failed" {
		attrs = append(attrs, "backup", backup)
	}
	settings.logger.Error(message, attrs...)
}
