package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/backup"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"golang.org/x/sys/unix"
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

const systemRetentionFile = ".roomusic-backup-retention.json"

func systemRequireFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := os.Lstat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s 应保留：%v", name, err)
		}
	}
}

func systemRequireMissing(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := os.Lstat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s 应消失：%v", name, err)
		}
	}
}

func systemDecode[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	if response.Code != 200 || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("应返回200 JSON：%d %s", response.Code, response.Body.String())
	}
	var result T
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("响应解码：%v %s", err, response.Body.String())
	}
	return result
}

type systemRetentionResponse struct {
	ConfirmationRequired bool            `json:"confirmation_required"`
	Deletions            int             `json:"deletions"`
	Deleted              int             `json:"deleted"`
	Failed               int             `json:"failed"`
	Overview             backup.Overview `json:"overview"`
}

func TestPostgreSQLSystemMutationAuthorizationAndOrigin(t *testing.T) {
	dir := t.TempDir()
	name := "roomusic-20261106-120000.dump"
	systemWriteFile(t, dir, name, []byte("不能删除"), systemTestNow)
	app, service, _ := newSystemApplication(t, dir)
	admin, member := app.createUser(t, "admin"), app.createUser(t, "user")
	if _, err := service.SaveRetention(context.Background(), backup.Retention{}, false, admin.id); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, systemRetentionFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []struct {
		method, path string
		body         any
	}{
		{"DELETE", "/api/v1/system/backups/" + name, nil},
		{"POST", "/api/v1/system/backups/deletions", map[string]any{"names": []string{name}}},
		{"PUT", "/api/v1/system/backups/retention", map[string]any{"keep_days": 1, "keep_count": 1, "confirm": true}},
	} {
		t.Run(endpoint.method, func(t *testing.T) {
			for _, tc := range []struct {
				label   string
				user    *integrationTestUser
				headers map[string]string
				status  int
				code    string
			}{
				{"匿名", nil, nil, 401, "unauthorized"},
				{"成员", &member, nil, 403, "forbidden"},
				{"无来源", &admin, map[string]string{"Origin": ""}, 403, "origin_required"},
				{"跨源", &admin, map[string]string{"Origin": "https://evil.test"}, 403, "origin_forbidden"},
			} {
				t.Run(tc.label, func(t *testing.T) {
					requireAPIError(t, app.request(t, endpoint.method, endpoint.path, endpoint.body, tc.user, tc.headers), tc.status, tc.code)
					systemRequireFiles(t, dir, name)
					data, err := os.ReadFile(filepath.Join(dir, name))
					if err != nil || string(data) != "不能删除" {
						t.Fatalf("鉴权失败改变备份：%q %v", data, err)
					}
					after, err := os.ReadFile(filepath.Join(dir, systemRetentionFile))
					if err != nil || !bytes.Equal(before, after) {
						t.Fatalf("鉴权失败改变规则：%s %v", after, err)
					}
				})
			}
		})
	}
}

func TestPostgreSQLSystemDeleteBackupOverviewAnd404(t *testing.T) {
	dir := t.TempDir()
	newest, older := "roomusic-20261106-120000-manual.dump", "roomusic-20261105-120000.dump"
	systemWriteFile(t, dir, newest, []byte("最新"), systemTestNow)
	at := systemTestNow.Add(-24 * time.Hour)
	systemWriteFile(t, dir, older, []byte("较旧"), at)
	app, _, _ := newSystemApplication(t, dir)
	admin := app.createUser(t, "admin")
	path := "/api/v1/system/backups/" + newest
	overview := systemDecode[backup.Overview](t, app.request(t, "DELETE", path, nil, &admin, nil))
	if overview.Total != 1 || len(overview.Backups) != 1 || overview.Backups[0].Name != older || overview.Status.LastSuccessAt == nil || !overview.Status.LastSuccessAt.Equal(at) || overview.Retention == nil || overview.Retention.KeepDays == nil || *overview.Retention.KeepDays != 14 || overview.Retention.KeepCount != nil {
		t.Fatalf("删除后Overview：%+v", overview)
	}
	systemRequireMissing(t, dir, newest)
	systemRequireFiles(t, dir, older)
	requireAPIError(t, app.request(t, "DELETE", path, nil, &admin, nil), 404, "backup_not_found")
	overview = systemDecode[backup.Overview](t, app.request(t, "DELETE", "/api/v1/system/backups/"+older, nil, &admin, nil))
	if overview.Total != 0 || overview.Backups == nil || overview.Status.LastSuccessAt != nil {
		t.Fatalf("最后一份可手动删除：%+v", overview)
	}
}

func TestPostgreSQLSystemDeleteBackupPathSafety(t *testing.T) {
	dir := t.TempDir()
	target := "roomusic-20261106-120000-manual.dump"
	systemWriteFile(t, dir, target, []byte("内部目标"), systemTestNow)
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("外部目标"), 0600); err != nil {
		t.Fatal(err)
	}
	symlink, insideLink, directory, fifo := "roomusic-20261106-120001.dump", "roomusic-20261106-120002.dump", "roomusic-20261106-120003.dump", "roomusic-20261106-120004.dump"
	for name, destination := range map[string]string{symlink: outside, insideLink: filepath.Join(dir, target)} {
		if err := os.Symlink(destination, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, directory), 0700); err != nil {
		t.Fatal(err)
	}
	systemWriteFile(t, filepath.Join(dir, directory), "child", []byte("子文件"), systemTestNow)
	if err := unix.Mkfifo(filepath.Join(dir, fifo), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"roomusic-x.dump", ".roomusic-backup-state.json", systemRetentionFile, ".roomusic-test.partial.1"} {
		systemWriteFile(t, dir, name, []byte("保留"), systemTestNow)
	}
	app, _, _ := newSystemApplication(t, dir)
	admin := app.createUser(t, "admin")
	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"..%2F..%2Fetc%2Fpasswd", "..%2F..", "roomusic-20261106-120000%5Cmanual.dump", "roomusic-20261106-120000%00.dump", "roomusic-x.dump", symlink, insideLink, directory, fifo, ".roomusic-backup-state.json", systemRetentionFile, ".roomusic-test.partial.1"} {
		t.Run(invalid, func(t *testing.T) {
			requireAPIError(t, app.request(t, "DELETE", "/api/v1/system/backups/"+invalid, nil, &admin, nil), 404, "backup_not_found")
			after, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(before) != len(after) {
				t.Fatal("拒绝请求改变目录")
			}
			for i := range before {
				if before[i].Name() != after[i].Name() || before[i].Type() != after[i].Type() {
					t.Fatal("目录条目改变")
				}
			}
			for path, want := range map[string]string{outside: "外部目标", filepath.Join(dir, target): "内部目标", filepath.Join(dir, directory, "child"): "子文件", filepath.Join(dir, systemRetentionFile): "保留"} {
				if data, err := os.ReadFile(path); err != nil || string(data) != want {
					t.Fatalf("目标改变：%q %v", data, err)
				}
			}
			for name, destination := range map[string]string{symlink: outside, insideLink: filepath.Join(dir, target)} {
				if got, err := os.Readlink(filepath.Join(dir, name)); err != nil || got != destination {
					t.Fatalf("链接改变：%q %v", got, err)
				}
			}
		})
	}
}

func TestPostgreSQLSystemDeleteBackupsResultsAndValidation(t *testing.T) {
	dir := t.TempDir()
	first, second, kept, missing := "roomusic-20261106-120002.dump", "roomusic-20261106-120001.dump", "roomusic-20261106-120000.dump", "roomusic-20261105-120000.dump"
	for _, name := range []string{first, second, kept} {
		systemWriteFile(t, dir, name, []byte("备份"), systemTestNow)
	}
	app, _, _ := newSystemApplication(t, dir)
	admin := app.createUser(t, "admin")
	path := "/api/v1/system/backups/deletions"
	tooMany := make([]string, 501)
	for i := range tooMany {
		tooMany[i] = first
	}
	for _, tc := range []struct {
		name string
		body any
	}{
		{"缺键", map[string]any{}}, {"空数组", map[string]any{"names": []string{}}}, {"501项", map[string]any{"names": tooMany}},
		{"null元素", map[string]any{"names": []any{first, nil}}}, {"数字元素", map[string]any{"names": []any{first, 3}}},
		{"非数组", map[string]any{"names": first}}, {"null数组", map[string]any{"names": nil}}, {"非对象", []any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireAPIError(t, app.request(t, "POST", path, tc.body, &admin, nil), 400, "invalid_input")
			systemRequireFiles(t, dir, first, second, kept)
		})
	}
	requireAPIError(t, systemRawRequest(app, "POST", path, `{} {}`, &admin), 400, "invalid_json")
	result := systemDecode[struct {
		backup.DeleteResult
		Overview backup.Overview `json:"overview"`
	}](t, app.request(t, "POST", path, map[string]any{"names": []string{first, missing, first, "../invalid", second, missing}}, &admin, nil))
	want := backup.DeleteResult{Deleted: []string{first, second}, Missing: []string{missing, "../invalid"}, Failed: []string{}}
	if !reflect.DeepEqual(result.DeleteResult, want) || result.Overview.Total != 1 || result.Overview.Backups[0].Name != kept || result.Overview.Retention == nil {
		t.Fatalf("批量结果：%+v", result)
	}
	systemRequireMissing(t, dir, first, second)
	systemRequireFiles(t, dir, kept)
	// 边界500项允许，重复项只处理一次。
	boundary := systemDecode[struct{ backup.DeleteResult }](t, app.request(t, "POST", path, map[string]any{"names": tooMany[:500]}, &admin, nil))
	if !reflect.DeepEqual(boundary.Missing, []string{first}) || len(boundary.Failed) != 0 {
		t.Fatalf("500项边界：%+v", boundary)
	}
}

func TestPostgreSQLSystemRetentionValidationAndConfirmation(t *testing.T) {
	dir := t.TempDir()
	newest, middle, oldest := "roomusic-20261106-120000.dump", "roomusic-20261105-120000-manual.dump", "roomusic-20261104-120000-pre-restore.dump"
	for i, name := range []string{newest, middle, oldest} {
		systemWriteFile(t, dir, name, []byte("备份"), systemTestNow.Add(-time.Duration(i)*24*time.Hour))
	}
	app, service, _ := newSystemApplication(t, dir)
	admin := app.createUser(t, "admin")
	if _, err := service.SaveRetention(context.Background(), backup.Retention{}, false, admin.id); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, systemRetentionFile))
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/system/backups/retention"
	for _, body := range []string{
		`{}`, `{"keep_days":null}`, `{"keep_count":null}`, `{"keep_days":0,"keep_count":null}`, `{"keep_days":3651,"keep_count":null}`,
		`{"keep_days":null,"keep_count":0}`, `{"keep_days":null,"keep_count":1001}`, `{"keep_days":1.5,"keep_count":null}`,
		`{"keep_days":"3","keep_count":null}`, `{"keep_days":null,"keep_count":1.5}`, `{"keep_days":null,"keep_count":"3"}`,
		`{"keep_days":null,"keep_count":null,"confirm":"true"}`, `{"keep_days":null,"keep_count":null,"confirm":1}`, `{"keep_days":null,"keep_count":null,"confirm":null}`, `null`, `[]`,
	} {
		t.Run(body, func(t *testing.T) {
			requireAPIError(t, app.request(t, "PUT", path, json.RawMessage(body), &admin, nil), 400, "invalid_input")
			systemRequireFiles(t, dir, newest, middle, oldest)
			after, err := os.ReadFile(filepath.Join(dir, systemRetentionFile))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("无效输入改变规则：%s %v", after, err)
			}
		})
	}
	requireAPIError(t, systemRawRequest(app, "PUT", path, `{"keep_days":`, &admin), 400, "invalid_json")
	for _, confirm := range []any{nil, false} {
		body := map[string]any{"keep_days": 30, "keep_count": 1}
		if confirm != nil {
			body["confirm"] = confirm
		}
		response := app.request(t, "PUT", path, body, &admin, nil)
		preview := systemDecode[systemRetentionResponse](t, response)
		if !preview.ConfirmationRequired || preview.Deletions != 2 {
			t.Fatalf("未确认响应：%s", response.Body.String())
		}
		fields := systemDecode[map[string]json.RawMessage](t, response)
		if len(fields) != 2 {
			t.Fatalf("预览只返回确认与数量：%s", response.Body.String())
		}
		systemRequireFiles(t, dir, newest, middle, oldest)
		after, err := os.ReadFile(filepath.Join(dir, systemRetentionFile))
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("不确认不得写规则")
		}
	}
	// 确认期间备份发生变化，使用保存当时的实际计划，而不是先前数量。
	if err := os.Remove(filepath.Join(dir, oldest)); err != nil {
		t.Fatal(err)
	}
	result := systemDecode[systemRetentionResponse](t, app.request(t, "PUT", path, map[string]any{"keep_days": 30, "keep_count": 1, "confirm": true}, &admin, nil))
	if result.ConfirmationRequired || result.Deleted != 1 || result.Failed != 0 || result.Overview.Total != 1 || result.Overview.Retention == nil || result.Overview.Retention.KeepCount == nil || *result.Overview.Retention.KeepCount != 1 {
		t.Fatalf("确认保存结果：%+v", result)
	}
	systemRequireFiles(t, dir, newest, systemRetentionFile)
	systemRequireMissing(t, dir, middle, oldest)
	for _, rule := range []map[string]any{
		{"keep_days": nil, "keep_count": nil},
		{"keep_days": 3650, "keep_count": 1000, "confirm": true, "ignored": "扩展字段"},
		{"keep_days": 1, "keep_count": 1},
	} {
		result = systemDecode[systemRetentionResponse](t, app.request(t, "PUT", path, rule, &admin, nil))
		if result.ConfirmationRequired || result.Deleted != 0 || result.Failed != 0 {
			t.Fatalf("无需删除时直接保存：%+v", result)
		}
	}
}

func TestPostgreSQLSystemRetentionOverviewAndRestart(t *testing.T) {
	dir := t.TempDir()
	app, _, _ := newSystemApplication(t, dir)
	admin := app.createUser(t, "admin")
	get := func() backup.Overview {
		return systemDecode[backup.Overview](t, app.request(t, "GET", "/api/v1/system/backups", nil, &admin, nil))
	}
	initial := get()
	if initial.Retention == nil || initial.Retention.KeepDays == nil || *initial.Retention.KeepDays != 14 || initial.Retention.KeepCount != nil {
		t.Fatalf("默认规则：%+v", initial.Retention)
	}
	if err := os.WriteFile(filepath.Join(dir, systemRetentionFile), []byte(`{"version":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	if overview := get(); overview.Retention != nil {
		t.Fatalf("损坏规则返回null：%+v", overview.Retention)
	}
	saved := systemDecode[systemRetentionResponse](t, app.request(t, "PUT", "/api/v1/system/backups/retention", map[string]any{"keep_days": nil, "keep_count": 3}, &admin, nil))
	if saved.Overview.Retention == nil || saved.Overview.Retention.KeepDays != nil || saved.Overview.Retention.KeepCount == nil || *saved.Overview.Retention.KeepCount != 3 {
		t.Fatalf("保存修复：%+v", saved)
	}
	settings := config.Backup{Directory: dir, Location: time.UTC, KeepDays: 1}
	restarted := backup.NewService(settings, nil)
	after, err := restarted.Overview(context.Background())
	if err != nil || !reflect.DeepEqual(after.Retention, saved.Overview.Retention) {
		t.Fatalf("重启不能回退到环境变量：%+v %v", after, err)
	}
	// 换空备份卷时没有保存过的规则，使用新进程配置。
	settings.Directory = t.TempDir()
	fresh, err := backup.NewService(settings, nil).Overview(context.Background())
	if err != nil || fresh.Retention == nil || fresh.Retention.KeepDays == nil || *fresh.Retention.KeepDays != 1 || fresh.Retention.KeepCount != nil {
		t.Fatalf("新卷默认规则：%+v %v", fresh, err)
	}
}

func TestPostgreSQLSystemMutationsNotWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 可绕过目录权限")
	}
	dir := t.TempDir()
	newest, old := "roomusic-20261106-120000.dump", "roomusic-20261105-120000.dump"
	systemWriteFile(t, dir, newest, []byte("备份"), systemTestNow)
	systemWriteFile(t, dir, old, []byte("备份"), systemTestNow.Add(-24*time.Hour))
	app, service, _ := newSystemApplication(t, dir)
	admin := app.createUser(t, "admin")
	if _, err := service.SaveRetention(context.Background(), backup.Retention{}, false, admin.id); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, systemRetentionFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0700); err != nil {
			t.Error(err)
		}
	})
	requireAPIError(t, app.request(t, "DELETE", "/api/v1/system/backups/"+old, nil, &admin, nil), 503, "backup_directory_not_writable")
	result := systemDecode[struct {
		backup.DeleteResult
		Overview backup.Overview `json:"overview"`
	}](t, app.request(t, "POST", "/api/v1/system/backups/deletions", map[string]any{"names": []string{old, newest, old}}, &admin, nil))
	if !reflect.DeepEqual(result.Failed, []string{old, newest}) || len(result.Deleted) != 0 || result.Overview.Total != 2 {
		t.Fatalf("批量失败仍返回200名单：%+v", result)
	}
	requireAPIError(t, app.request(t, "PUT", "/api/v1/system/backups/retention", map[string]any{"keep_days": nil, "keep_count": 1, "confirm": true}, &admin, nil), 503, "backup_directory_not_writable")
	systemRequireFiles(t, dir, newest, old)
	after, err := os.ReadFile(filepath.Join(dir, systemRetentionFile))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("保存失败不能改规则")
	}
}

func TestPostgreSQLSystemRetentionSurvivesDatabaseRestore(t *testing.T) {
	// 独立随机库，不使用应用 fixture 的 schema，也不接触用户数据库。
	address := postgrestest.NewDatabase(t, "system_retention_restore_")
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	db, err := postgres.Connect(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations(version bigint PRIMARY KEY); INSERT INTO schema_migrations VALUES(1); CREATE TABLE restore_probe(value text); INSERT INTO restore_probe VALUES('备份时的内容')`); err != nil {
		t.Fatal(err)
	}
	settings := config.Backup{Directory: t.TempDir(), DatabaseURL: address, Location: time.UTC, KeepDays: 14, PGBinDirectory: os.Getenv("ROOMUSIC_TEST_PG_BIN_DIR")}
	if settings.PGBinDirectory == "" {
		t.Fatal("必须由隔离测试脚本提供固定PG工具，不能跳过真实恢复")
	}
	service := backup.NewService(settings, nil)
	archive, err := service.RunNow(ctx, db, backup.SourceManual)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE restore_probe SET value='备份后的内容'`); err != nil {
		t.Fatal(err)
	}
	days, count := 60, 5
	rule := backup.Retention{KeepDays: &days, KeepCount: &count}
	if _, err := service.SaveRetention(ctx, rule, false, "restore-admin"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(settings.Directory, systemRetentionFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	restorer := backup.NewService(settings, nil)
	session, err := restorer.PrepareRestore(ctx, archive.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.Restore(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.SafetyBackup == "" {
		t.Fatal("真实恢复必须产生恢复前备份")
	}
	restored, err := postgres.Connect(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var value string
	if err := restored.QueryRowContext(ctx, `SELECT value FROM restore_probe`).Scan(&value); err != nil || value != "备份时的内容" {
		t.Fatalf("必须真的恢复数据：%q %v", value, err)
	}
	after, err := os.ReadFile(filepath.Join(settings.Directory, systemRetentionFile))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("数据库恢复不能覆盖规则文件：%s %v", after, err)
	}
	overview, err := backup.NewService(settings, nil).Overview(ctx)
	if err != nil || !reflect.DeepEqual(overview.Retention, &rule) {
		t.Fatalf("恢复后规则回退：%+v %v", overview.Retention, err)
	}
	systemRequireFiles(t, settings.Directory, archive.Name, result.SafetyBackup)
}

// systemRawRequest 只供语法损坏的 JSON，避免通用夹具在发送前就拒绝编码。
func systemRawRequest(app *integrationTestApplication, method, path, body string, user *integrationTestUser) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Host = "roomusic.test"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", app.origin)
	if user != nil {
		request.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: user.token})
	}
	response := httptest.NewRecorder()
	app.handler.ServeHTTP(response, request)
	return response
}
