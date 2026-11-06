package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/backup"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
)

const systemBackupLock int64 = 0x524f4f4d4241434b

var systemTestNow = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

type systemRunner struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *systemRunner) Run(ctx context.Context, c backup.Command) error {
	if len(c.Args) == 1 && c.Args[0] == "--version" {
		_, err := fmt.Fprintln(c.Stdout, "pg (PostgreSQL) 18.1")
		return err
	}
	if filepath.Base(c.Path) == "pg_dump" {
		r.once.Do(func() { close(r.entered) })
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.release:
		}
		_, err := c.Stdout.Write([]byte("PGDMP-system-test"))
		return err
	}
	return nil
}
func newSystemApplication(t *testing.T, dir string) (*integrationTestApplication, *backup.Service, *systemRunner) {
	t.Helper()
	runner := &systemRunner{entered: make(chan struct{}), release: make(chan struct{})}
	service := backup.NewService(config.Backup{Directory: dir, DatabaseURL: "postgres://test:secret@localhost/test", Location: time.UTC, KeepDays: 14, Hour: 3}, nil, backup.WithClock(func() time.Time { return systemTestNow }), backup.WithRunner(runner))
	application := newIntegrationTestApplication(t, WithBackups(service))
	// 后注册先清理：放行并等待后台备份，才允许应用关闭连接池。
	t.Cleanup(func() {
		select {
		case <-runner.release:
		default:
			close(runner.release)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := service.Wait(ctx); err != nil {
			t.Error(err)
		}
	})
	return application, service, runner
}
func systemWriteFile(t *testing.T, dir, name string, content []byte, at time.Time) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}
func TestPostgreSQLSystemAuthorizationAndOrigin(t *testing.T) {
	app, _, _ := newSystemApplication(t, t.TempDir())
	admin := app.createUser(t, "admin")
	member := app.createUser(t, "user")
	for _, path := range []string{"/api/v1/system/version", "/api/v1/system/backups", "/api/v1/system/backups/roomusic-20261106-120000.dump"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			requireAPIError(t, app.request(t, method, path, nil, nil, nil), 401, "unauthorized")
			requireAPIError(t, app.request(t, method, path, nil, &member, nil), 403, "forbidden")
		}
	}
	requireAPIError(t, app.request(t, "POST", "/api/v1/system/backups", nil, nil, nil), 401, "unauthorized")
	requireAPIError(t, app.request(t, "POST", "/api/v1/system/backups", nil, &member, nil), 403, "forbidden")
	for _, tc := range []struct{ origin, code string }{{"", "origin_required"}, {"https://evil.test", "origin_forbidden"}} {
		requireAPIError(t, app.request(t, "POST", "/api/v1/system/backups", nil, &admin, map[string]string{"Origin": tc.origin}), 403, tc.code)
	}
	response := app.request(t, "GET", "/api/v1/system/version", nil, &admin, nil)
	if response.Code != 200 {
		t.Fatalf("版本接口%d %s", response.Code, response.Body.String())
	}
}
func TestPostgreSQLSystemStart202Then200AndExternal409(t *testing.T) {
	app, service, runner := newSystemApplication(t, t.TempDir())
	admin := app.createUser(t, "admin")
	first := app.request(t, "POST", "/api/v1/system/backups", nil, &admin, nil)
	if first.Code != 202 {
		t.Fatalf("开始应202：%d %s", first.Code, first.Body.String())
	}
	select {
	case <-runner.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("备份未开始")
	}
	second := app.request(t, "POST", "/api/v1/system/backups", nil, &admin, nil)
	if second.Code != 200 {
		t.Fatalf("重复应200：%d %s", second.Code, second.Body.String())
	}
	var one, two struct {
		Status backup.Status `json:"status"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &one); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &two); err != nil {
		t.Fatal(err)
	}
	if one.Status.Running == nil || two.Status.Running == nil || *one.Status.Running != *two.Status.Running {
		t.Fatalf("重复请求没有复用同一备份：%+v %+v", one, two)
	}
	close(runner.release)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := service.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	// 新Service模拟另一个应用进程，不能复用已经Wait关闭的Service。
	other := backup.NewService(config.Backup{Directory: t.TempDir(), Location: time.UTC}, nil)
	app2 := newIntegrationTestApplication(t, WithBackups(other))
	admin2 := app2.createUser(t, "admin")
	holder, err := app2.database.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if _, err := holder.ExecContext(ctx, `SELECT pg_advisory_lock($1::bigint)`, systemBackupLock); err != nil {
		t.Fatal(err)
	}
	defer holder.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1::bigint)`, systemBackupLock)
	requireAPIError(t, app2.request(t, "POST", "/api/v1/system/backups", nil, &admin2, nil), 409, "backup_busy")
}
func TestPostgreSQLSystemDownloadHeadersBytesRangeAndPathSafety(t *testing.T) {
	dir := t.TempDir()
	name := "roomusic-20261106-120000-manual.dump"
	payload := []byte("PGDMP\x00\x01\xff合成备份\n")
	systemWriteFile(t, dir, name, payload, systemTestNow)
	app, _, _ := newSystemApplication(t, dir)
	admin := app.createUser(t, "admin")
	path := "/api/v1/system/backups/" + name
	response := app.request(t, "GET", path, nil, &admin, nil)
	if response.Code != 200 || response.Body.String() != string(payload) {
		t.Fatalf("下载字节不一致：%d %q", response.Code, response.Body.Bytes())
	}
	for header, want := range map[string]string{"Content-Type": "application/octet-stream", "Content-Disposition": `attachment; filename="` + name + `"`, "Cache-Control": "private, no-store", "X-Content-Type-Options": "nosniff", "Accept-Ranges": "bytes", "Content-Length": fmt.Sprint(len(payload))} {
		if got := response.Header().Get(header); got != want {
			t.Errorf("%s=%q，期望%q", header, got, want)
		}
	}
	if response.Header().Get("Last-Modified") == "" || response.Header().Get("ETag") != "" {
		t.Error("Last-Modified或ETag不合合同")
	}
	ranged := app.request(t, "GET", path, nil, &admin, map[string]string{"Range": "bytes=2-6"})
	if ranged.Code != 206 || ranged.Body.String() != string(payload[2:7]) || ranged.Header().Get("Content-Range") != fmt.Sprintf("bytes 2-6/%d", len(payload)) {
		t.Fatalf("Range：%d %q %v", ranged.Code, ranged.Body.Bytes(), ranged.Header())
	}
	head := app.request(t, "HEAD", path, nil, &admin, nil)
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != fmt.Sprint(len(payload)) {
		t.Fatalf("HEAD：%d %q", head.Code, head.Body.String())
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("不可读"), 0600); err != nil {
		t.Fatal(err)
	}
	symlink := "roomusic-20261106-120001.dump"
	insideLink := "roomusic-20261106-120002.dump"
	directory := "roomusic-20261106-120003.dump"
	if err := os.Symlink(outside, filepath.Join(dir, symlink)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, name), filepath.Join(dir, insideLink)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, directory), 0700); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"..%2F..%2Fetc%2Fpasswd", "..%2F..", "roomusic-20261106-120000%5Cmanual.dump", "roomusic-20261106-120000%00.dump", "roomusic-x.dump", symlink, insideLink, directory} {
		t.Run(invalid, func(t *testing.T) {
			requireAPIError(t, app.request(t, "GET", "/api/v1/system/backups/"+invalid, nil, &admin, nil), 404, "backup_not_found")
		})
	}
}
