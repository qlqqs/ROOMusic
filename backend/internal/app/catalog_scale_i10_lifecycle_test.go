package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// I10 独立生命周期：不截获 NewSchema 的 Cleanup，也不改变普通集成测试的清理规则。
// create 成功或失败都保留 schema；reuse/explain 从不重建夹具或启动 maintenance。
// 最后必须显式 cleanup，并以 manifest、数据库指纹、schema 名及数据库内标记共同确认。
type i10Manifest struct {
	Version             int               `json:"version"`
	Schema              string            `json:"schema"`
	Nonce               string            `json:"nonce"`
	DatabaseFingerprint string            `json:"database_fingerprint"`
	FixtureSHA256       string            `json:"fixture_sha256"`
	Ready               bool              `json:"ready"`
	Supplemental        map[string]string `json:"supplemental_runs"`
	Cleaned             bool              `json:"cleaned"`
}
type i10Actions struct{ Build, Measure, Supplement, Explain, Drop bool }

func i10SelectActions(mode string) (i10Actions, error) {
	switch mode {
	case "create":
		return i10Actions{Build: true, Measure: true, Supplement: true, Explain: true}, nil
	case "reuse":
		return i10Actions{Measure: true, Explain: true}, nil
	case "explain":
		return i10Actions{Explain: true}, nil
	case "cleanup":
		return i10Actions{Drop: true}, nil
	default:
		return i10Actions{}, fmt.Errorf("必须显式指定 ROOMUSIC_I10_MODE=create|reuse|explain|cleanup")
	}
}
func i10Budget(value string) (time.Duration, error) {
	if value == "" {
		return 150 * time.Minute, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil || d < 150*time.Minute {
		return 0, fmt.Errorf("I10 总预算必须至少 150m")
	}
	return d, nil
}
func i10Hash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func i10ValidateManifest(m i10Manifest, schema, fingerprint string) error {
	if m.Version != 1 || !regexp.MustCompile(`^i10_search_[a-f0-9]{32}$`).MatchString(m.Schema) || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(m.Nonce) || m.Schema != schema || m.DatabaseFingerprint != fingerprint || m.Cleaned {
		return fmt.Errorf("拒绝复用/删除：manifest、显式 schema 或数据库指纹不匹配，或已清理")
	}
	return nil
}
func i10WriteJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	// 同目录原子替换：进程被中止时仍保留上一个完整 manifest/报告。
	f, err := os.CreateTemp(filepath.Dir(path), ".i10-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
func i10EvidenceDirectory(output, repo string) (string, error) {
	if !filepath.IsAbs(output) || !strings.Contains(filepath.Base(output), "i10-search") {
		return "", fmt.Errorf("ROOMUSIC_I10_OUTPUT 必须是仓外绝对路径，末级目录含 i10-search")
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		return "", err
	}
	actual, err := filepath.EvalSymlinks(output)
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(repo)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, actual)
	if err != nil {
		return "", err
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("规模证据不得写入仓库")
	}
	return actual, nil
}

// open 仅在 create 执行迁移并创建持久隔离 schema；reuse/explain 不迁移、不初始化、不回填。
// 若优化含索引迁移，应由操作者对保留的 schema 显式应用后再 reuse，不能暗中重新发布投影。
func i10OpenSchema(b *testing.B, ctx context.Context, output, mode string, actions i10Actions, tracer *scaleTracer, cleanupError func(string, error)) (*sql.DB, *i10Manifest) {
	b.Helper()
	must := func(err error) {
		if err != nil {
			b.Fatal(err)
		}
	}
	base := os.Getenv(postgrestest.Environment)
	if strings.TrimSpace(base) == "" {
		b.Fatal("I10 必须显式提供一次性测试库 ROOMUSIC_TEST_DATABASE_URL")
	}
	cfg, err := pgx.ParseConfig(base)
	must(err)
	fingerprint := i10Hash([]byte(fmt.Sprintf("%s\x00%d\x00%s\x00%s", cfg.Host, cfg.Port, cfg.Database, cfg.User)))
	file, err := os.OpenFile(filepath.Join(output, "lifecycle.lock"), os.O_CREATE|os.O_RDWR, 0600)
	must(err)
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		b.Fatal("I10 目录已有运行者: ", err)
	}
	b.Cleanup(func() {
		if err := file.Close(); err != nil {
			cleanupError("lifecycle", err)
		}
	})
	path := filepath.Join(output, "fixture.json")
	m := &i10Manifest{}
	if actions.Build {
		if _, err = os.Stat(path); !os.IsNotExist(err) {
			b.Fatal("create 拒绝已有 manifest；请 reuse 或使用新的仓外目录")
		}
		if os.Getenv("ROOMUSIC_I10_SCHEMA") != "" {
			b.Fatal("create 不接受已有 schema")
		}
		fixture, err := os.ReadFile("testdata/search-scale.sql")
		must(err)
		*m = i10Manifest{Version: 1, Schema: "i10_search_" + strings.ReplaceAll(uuid.New(), "-", ""), Nonce: strings.ReplaceAll(uuid.New(), "-", ""), DatabaseFingerprint: fingerprint, FixtureSHA256: i10Hash(fixture), Supplemental: map[string]string{}}
		must(i10WriteJSON(path, m))
	} else {
		raw, err := os.ReadFile(path)
		must(err)
		must(json.Unmarshal(raw, m))
		must(i10ValidateManifest(*m, os.Getenv("ROOMUSIC_I10_SCHEMA"), fingerprint))
		if m.Supplemental == nil {
			m.Supplemental = map[string]string{}
		}
	}
	// 先打印恢复锚点，即使迁移、fixture 或 maintenance 失败也可据此显式清理。
	b.Logf("I10 mode=%s schema=%s manifest=%s（不会自动 DROP）", mode, m.Schema, path)
	admin := stdlib.OpenDB(*cfg)
	defer func() {
		if err := admin.Close(); err != nil {
			cleanupError("lifecycle", err)
		}
	}()
	marker := "roomusic-i10:" + m.Nonce
	name := pgx.Identifier{m.Schema}.Sanitize()
	if actions.Build {
		tx, err := admin.BeginTx(ctx, nil)
		must(err)
		defer tx.Rollback()
		_, err = tx.ExecContext(ctx, "CREATE SCHEMA "+name)
		must(err)
		_, err = tx.ExecContext(ctx, "COMMENT ON SCHEMA "+name+" IS '"+marker+"'")
		must(err)
		must(tx.Commit())
	} else {
		var actual string
		must(admin.QueryRowContext(ctx, `SELECT COALESCE(obj_description(oid,'pg_namespace'),'') FROM pg_namespace WHERE nspname=$1`, m.Schema).Scan(&actual))
		if actual != marker {
			b.Fatal("数据库内 I10 schema 标记不匹配；拒绝读取或删除")
		}
	}
	if actions.Drop {
		_, err = admin.ExecContext(ctx, "DROP SCHEMA "+name+" CASCADE")
		must(err)
		m.Cleaned = true
		must(i10WriteJSON(path, m))
		return nil, m
	}
	scoped, err := postgrestest.ScopedURL(base, m.Schema)
	must(err)
	if actions.Build {
		migrated, err := postgres.Open(ctx, scoped)
		must(err)
		must(migrated.Close())
	}
	cfg, err = pgx.ParseConfig(scoped)
	must(err)
	cfg.Tracer = tracer
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(12)
	db.SetMaxIdleConns(12)
	b.Cleanup(func() {
		if err := db.Close(); err != nil {
			cleanupError("lifecycle", err)
		}
	})
	var actual string
	must(db.QueryRowContext(ctx, "SELECT current_schema()").Scan(&actual))
	if actual != m.Schema {
		b.Fatal("search_path 未命中保留 schema，拒绝回退 public")
	}
	return db, m
}

// 通过回调分离创建步骤，纯测试证明 reuse 不会误走夹具构造/回填。
func i10BuildOnce(actions i10Actions, fixture, initialize, ready func() error) error {
	if actions.Build {
		if err := fixture(); err != nil {
			return err
		}
		if err := initialize(); err != nil {
			return err
		}
	}
	return ready()
}
func i10ClaimSupplement(actions i10Actions, completed map[string]string, name, run string) bool {
	if !actions.Supplement || completed[name] != "" {
		return false
	}
	// 开始前持久化 claimed；被打断后不默默重复昂贵补测，报告仍会显示未完成。
	completed[name] = run
	return true
}

// 同一证据根目录只追加 attempt；不会覆盖旧 I9 对照或之前失败的测量。
func i10AttemptDirectory(output string) (string, error) {
	for i := 1; ; i++ {
		path := filepath.Join(output, fmt.Sprintf("attempt-%02d", i))
		err := os.Mkdir(path, 0700)
		if os.IsExist(err) {
			continue
		}
		return path, err
	}
}
