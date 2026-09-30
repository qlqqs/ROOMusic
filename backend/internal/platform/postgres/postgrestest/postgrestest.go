// Package postgrestest 为集成测试提供隔离 schema 的 PostgreSQL 连接 URL。
// 它只依赖标准库与 pgx 驱动，不导入 postgres 包，因此 postgres 自己的测试也能使用它。
package postgrestest

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// Environment 是指向一次性 PostgreSQL 测试库的环境变量名。
const Environment = "ROOMUSIC_TEST_DATABASE_URL"

// BaseURL 返回环境变量配置的测试库 URL。未配置时跳过测试；跳过不能被当作事务证据。
func BaseURL(t testing.TB) string {
	t.Helper()
	base := strings.TrimSpace(os.Getenv(Environment))
	if base == "" {
		t.Skipf("%s is not configured", Environment)
	}
	return base
}

// ScopedURL 返回把 search_path 固定到 schema 的连接 URL。
func ScopedURL(databaseURL, schema string) (string, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// NewSchema 在测试库里创建名为 prefix 加随机后缀的隔离 schema，返回固定了 search_path 的连接 URL，
// 并在测试结束时删除该 schema。调用方之后打开的连接会先于 schema 清理关闭（t.Cleanup 后进先出）。
func NewSchema(t testing.TB, prefix string) string {
	t.Helper()
	base := BaseURL(t)
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatalf("open integration test admin connection: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		_ = admin.Close()
		t.Fatalf("ping integration test admin connection: %v", err)
	}
	schema := prefix + strings.ReplaceAll(uuid.New(), "-", "")
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		_ = admin.Close()
		t.Fatalf("create isolated integration schema: %v", err)
	}
	t.Cleanup(func() {
		defer admin.Close()
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		if _, err := admin.ExecContext(cleanupContext, `DROP SCHEMA IF EXISTS `+schema+` CASCADE`); err != nil {
			t.Errorf("drop isolated integration schema: %v", err)
		}
	})
	scoped, err := ScopedURL(base, schema)
	if err != nil {
		t.Fatalf("scope integration database URL: %v", err)
	}
	return scoped
}
