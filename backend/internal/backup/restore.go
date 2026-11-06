package backup

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
)

type MigrationSummary struct {
	Version int64
	Rows    int64
}
type RestorePreview struct {
	Name              string
	CreatedAt         time.Time
	SizeBytes         int64
	BackupMigrations  MigrationSummary
	CurrentMigrations MigrationSummary
	Database          string
	NewerVersion      bool
}
type TableCount struct {
	Name    string
	Rows    int64
	Missing bool
}
type RestoreResult struct {
	SafetyBackup string
	Migrations   MigrationSummary
	Tables       []TableCount
}

// RestoreError 的 SafetyBackup 非空表示已经进入重建阶段，必须提示恢复命令。
type RestoreError struct {
	Reason       string
	Connections  int
	SafetyBackup string
}

func (e *RestoreError) Error() string {
	switch e.Reason {
	case "invalid_backup":
		return "这不是可用的备份文件。"
	case "permission":
		return "数据库用户没有重建数据库的权限，不能恢复。"
	case "connections":
		return fmt.Sprintf("还有 %d 个连接在使用数据库，先停止 app：docker compose stop app", e.Connections)
	case "safety_backup":
		return "恢复前的备份没有成功，数据库没有改动。"
	case "rebuild", "restore", "verify":
		return "恢复没有成功，数据库现在可能是空的。"
	default:
		return "无法准备数据库恢复。"
	}
}

// RestoreSession 持有已校验文件的句柄，确认期间文件被清理或替换也不会改变恢复输入。
// 调用方必须 Close；恢复操作不可重复执行。
type RestoreSession struct {
	Preview  RestorePreview
	service  *Service
	file     *os.File
	db       *sql.DB
	username string
	used     bool
}

func (r *RestoreSession) Close() {
	if r.file != nil {
		_ = r.file.Close()
		r.file = nil
	}
	if r.db != nil {
		_ = r.db.Close()
		r.db = nil
	}
}

func (s *Service) PrepareRestore(ctx context.Context, input string) (session *RestoreSession, err error) {
	path := input
	if !strings.Contains(input, "/") {
		path = filepath.Join(s.settings.Directory, input)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, &RestoreError{Reason: "invalid_backup"}
	}
	session = &RestoreSession{service: s, file: file}
	defer func() {
		if err != nil {
			session.Close()
		}
	}()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return session, &RestoreError{Reason: "invalid_backup"}
	}
	_, env, err := connectionArguments(s.settings.DatabaseURL)
	if err != nil {
		return session, err
	}
	if err = s.verify(ctx, file, env); err != nil {
		return session, &RestoreError{Reason: "invalid_backup"}
	}
	summary, err := s.archiveMigrations(ctx, file, env)
	if err != nil {
		return session, &RestoreError{Reason: "invalid_backup"}
	}
	databaseURL, err := restoreURL(s.settings.DatabaseURL, "")
	if err != nil {
		return session, err
	}
	session.db, err = postgres.Connect(ctx, databaseURL)
	if err != nil {
		return session, &RestoreError{Reason: "connect"}
	}
	session.Preview = RestorePreview{Name: filepath.Base(input), CreatedAt: info.ModTime(), SizeBytes: info.Size(), BackupMigrations: summary}
	var permitted bool
	err = session.db.QueryRowContext(ctx, `SELECT current_database(), current_user, (SELECT rolsuper OR rolcreatedb FROM pg_roles WHERE rolname=current_user)`).Scan(&session.Preview.Database, &session.username, &permitted)
	if err != nil {
		return session, &RestoreError{Reason: "connect"}
	}
	if !permitted {
		return session, &RestoreError{Reason: "permission"}
	}
	// 维护连接使用 postgres，不能同时删除维护库自身。
	if session.Preview.Database == "postgres" || session.Preview.Database == "template0" || session.Preview.Database == "template1" {
		return session, &RestoreError{Reason: "maintenance_database"}
	}
	if err = session.checkConnections(ctx); err != nil {
		return session, err
	}
	session.Preview.CurrentMigrations, err = databaseMigrations(ctx, session.db)
	if err != nil {
		return session, &RestoreError{Reason: "connect"}
	}
	latest, err := postgres.LatestMigrationVersion()
	if err != nil {
		return session, &RestoreError{Reason: "prepare"}
	}
	session.Preview.NewerVersion = summary.Version > latest
	return session, nil
}
func restoreURL(raw, database string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		return "", failure("prepare", "unsupported_database_url")
	}
	query := parsed.Query()
	query.Set("application_name", "roomusic-restore")
	parsed.RawQuery = query.Encode()
	if database != "" {
		parsed.Path = "/" + database
		parsed.RawPath = ""
	}
	return parsed.String(), nil
}
func (s *Service) archiveMigrations(ctx context.Context, file *os.File, env []string) (MigrationSummary, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return MigrationSummary{}, err
	}
	output := &boundedBuffer{limit: 4 * 1024 * 1024}
	if err := s.command(ctx, "verify", "pg_restore", []string{"--data-only", "--schema=public", "--table=schema_migrations", "--file=-"}, env, file, output); err != nil {
		return MigrationSummary{}, err
	}
	if output.exceeded {
		return MigrationSummary{}, errors.New("迁移记录过大")
	}
	scanner := bufio.NewScanner(strings.NewReader(output.String()))
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	copying := false
	summary := MigrationSummary{}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "COPY public.schema_migrations ") && strings.HasSuffix(line, " FROM stdin;") {
			copying = true
			continue
		}
		if line == `\.` {
			copying = false
			continue
		}
		if !copying {
			continue
		}
		version, err := strconv.ParseInt(strings.SplitN(line, "\t", 2)[0], 10, 64)
		if err != nil || version < 1 {
			return MigrationSummary{}, errors.New("迁移记录无效")
		}
		summary.Rows++
		if version > summary.Version {
			summary.Version = version
		}
	}
	if scanner.Err() != nil || copying {
		return MigrationSummary{}, errors.New("迁移记录不完整")
	}
	return summary, nil
}
func databaseMigrations(ctx context.Context, db *sql.DB) (MigrationSummary, error) {
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('public.schema_migrations') IS NOT NULL`).Scan(&exists); err != nil {
		return MigrationSummary{}, err
	}
	if !exists {
		return MigrationSummary{}, nil
	}
	var summary MigrationSummary
	err := db.QueryRowContext(ctx, `SELECT COALESCE(max(version),0),count(*) FROM public.schema_migrations`).Scan(&summary.Version, &summary.Rows)
	return summary, err
}
func (r *RestoreSession) checkConnections(ctx context.Context) error {
	var count int
	err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND backend_type='client backend'`).Scan(&count)
	if err != nil {
		return &RestoreError{Reason: "connect"}
	}
	if count > 0 {
		return &RestoreError{Reason: "connections", Connections: count}
	}
	return nil
}
func (r *RestoreSession) Restore(ctx context.Context) (RestoreResult, error) {
	if r.used || r.db == nil || r.file == nil {
		return RestoreResult{}, &RestoreError{Reason: "prepare"}
	}
	r.used = true
	if err := r.checkConnections(ctx); err != nil {
		return RestoreResult{}, err
	}
	safety, err := r.service.RunNow(ctx, r.db, SourcePreRestore)
	if err != nil {
		return RestoreResult{}, &RestoreError{Reason: "safety_backup"}
	}
	result := RestoreResult{SafetyBackup: safety.Name}
	fail := func(reason string) (RestoreResult, error) {
		return result, &RestoreError{Reason: reason, SafetyBackup: safety.Name}
	}
	_ = r.db.Close()
	r.db = nil
	maintenanceURL, err := restoreURL(r.service.settings.DatabaseURL, "postgres")
	if err != nil {
		return fail("rebuild")
	}
	maintenance, err := postgres.Connect(ctx, maintenanceURL)
	if err != nil {
		return fail("rebuild")
	}
	defer maintenance.Close()
	database := pgx.Identifier{r.Preview.Database}.Sanitize()
	owner := pgx.Identifier{r.username}.Sanitize()
	if _, err = maintenance.ExecContext(ctx, `DROP DATABASE `+database+` WITH (FORCE)`); err != nil {
		return fail("rebuild")
	}
	if _, err = maintenance.ExecContext(ctx, `CREATE DATABASE `+database+` OWNER `+owner); err != nil {
		return fail("rebuild")
	}
	address, env, err := connectionArguments(r.service.settings.DatabaseURL)
	if err != nil {
		return fail("restore")
	}
	if _, err = r.file.Seek(0, io.SeekStart); err != nil {
		return fail("restore")
	}
	if err = r.service.command(ctx, "verify", "pg_restore", []string{"--no-owner", "--no-password", "--exit-on-error", "--single-transaction", "--dbname=" + address}, env, r.file, io.Discard); err != nil {
		return fail("restore")
	}
	targetURL, err := restoreURL(r.service.settings.DatabaseURL, "")
	if err != nil {
		return fail("verify")
	}
	target, err := postgres.Connect(ctx, targetURL)
	if err != nil {
		return fail("verify")
	}
	defer target.Close()
	result.Migrations, err = databaseMigrations(ctx, target)
	if err != nil || result.Migrations != r.Preview.BackupMigrations {
		return fail("verify")
	}
	for _, name := range []string{"users", "releases", "tracks", "favorite_tracks", "favorite_albums"} {
		count := TableCount{Name: name}
		var exists bool
		if err = target.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+name).Scan(&exists); err != nil {
			return fail("verify")
		}
		count.Missing = !exists
		if exists {
			if err = target.QueryRowContext(ctx, `SELECT count(*) FROM `+pgx.Identifier{"public", name}.Sanitize()).Scan(&count.Rows); err != nil {
				return fail("verify")
			}
		}
		result.Tables = append(result.Tables, count)
	}
	return result, nil
}
