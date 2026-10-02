package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// API 响应样本：用真实 handler 产生响应，规范化 UUID 与时间后，与提交在 testdata/api-samples 的 JSON 逐字比较。
// 前端 decoder 的契约测试读取同一批样本，所以后端响应一旦漂移，这里先失败。
// 确认是有意的合同变更后，用 ROOMUSIC_UPDATE_API_SAMPLES=1 ./scripts/test-integration.sh 重写样本，并同步前端 decoder。
const (
	apiSamplesDirectory = "testdata/api-samples"
	updateAPISamplesEnv = "ROOMUSIC_UPDATE_API_SAMPLES"
	sampleRequestID     = "sample-request"

	sampleAdminPassword     = "curator-sample-password"
	sampleListenerPassword  = "listener-sample-password"
	sampleArchivistPassword = "archivist-sample-password"
)

func TestPostgreSQLAPIResponseSamples(t *testing.T) {
	application := newIntegrationTestApplication(t)
	samples := newAPISampleRecorder(t, application)
	call := func(method, path string, body any, user *integrationTestUser, extraHeaders ...map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		headers := map[string]string{"X-Request-ID": sampleRequestID}
		for _, extra := range extraHeaders {
			for name, value := range extra {
				headers[name] = value
			}
		}
		return application.request(t, method, path, body, user, headers)
	}
	idempotency := func(key string) map[string]string { return map[string]string{"Idempotency-Key": key} }
	login := func(response *httptest.ResponseRecorder) *integrationTestUser {
		t.Helper()
		for _, cookie := range response.Result().Cookies() {
			if cookie.Name == identity.SessionCookieName && cookie.Value != "" {
				samples.forbid(cookie.Value)
				return &integrationTestUser{token: cookie.Value}
			}
		}
		t.Fatalf("login response did not set the session cookie: %s", response.Body.String())
		return nil
	}
	credentials := func(username, password string) map[string]string {
		return map[string]string{"username": username, "password": password}
	}

	// 初始化与登录。
	samples.record("auth/setup-status-required", call(http.MethodGet, "/api/v1/setup/status", nil, nil), http.StatusOK)
	samples.record("errors/invalid-input", call(http.MethodPost, "/api/v1/setup/admin", credentials("curator", "short"), nil), http.StatusBadRequest)
	samples.record("errors/unauthorized", call(http.MethodGet, "/api/v1/users", nil, nil), http.StatusUnauthorized)
	samples.record("auth/setup-admin-created", call(http.MethodPost, "/api/v1/setup/admin", credentials("curator", sampleAdminPassword), nil), http.StatusCreated)
	samples.record("auth/setup-status-completed", call(http.MethodGet, "/api/v1/setup/status", nil, nil), http.StatusOK)
	samples.record("errors/setup-closed", call(http.MethodPost, "/api/v1/setup/admin", credentials("curator", sampleAdminPassword), nil), http.StatusConflict)
	samples.record("errors/invalid-credentials", call(http.MethodPost, "/api/v1/auth/login", credentials("curator", "wrong-sample-password"), nil), http.StatusUnauthorized)
	samples.record("errors/origin-forbidden", call(http.MethodPost, "/api/v1/auth/login", credentials("curator", sampleAdminPassword), nil, map[string]string{"Origin": "http://untrusted.example"}), http.StatusForbidden)
	loginResponse := call(http.MethodPost, "/api/v1/auth/login", credentials("curator", sampleAdminPassword), nil)
	samples.record("auth/login", loginResponse, http.StatusOK)
	admin := login(loginResponse)
	samples.record("auth/me-admin", call(http.MethodGet, "/api/v1/auth/me", nil, admin), http.StatusOK)

	// 空库状态：没有扫描、没有发行组、没有目录。
	samples.record("scans/active-none", call(http.MethodGet, "/api/v1/scans/active", nil, admin), http.StatusOK)
	samples.record("scans/list-empty", call(http.MethodGet, "/api/v1/scans", nil, admin), http.StatusOK)
	samples.record("errors/invalid-pagination", call(http.MethodGet, "/api/v1/scans?page=0", nil, admin), http.StatusBadRequest)
	samples.record("release-groups/list-empty", call(http.MethodGet, "/api/v1/release-groups?page_size=1", nil, admin), http.StatusOK)
	samples.record("library-roots/list-empty", call(http.MethodGet, "/api/v1/library-roots", nil, admin), http.StatusOK)
	samples.record("library-root-operations/list-empty", call(http.MethodGet, "/api/v1/library-root-operations", nil, admin), http.StatusOK)

	// 成员：新建、重名、停用、重新启用、撤销会话、最后一个管理员。
	samples.record("users/create", call(http.MethodPost, "/api/v1/users", credentials("listener", sampleListenerPassword), admin), http.StatusCreated)
	samples.record("errors/username-taken", call(http.MethodPost, "/api/v1/users", credentials("listener", sampleListenerPassword), admin), http.StatusConflict)
	if response := call(http.MethodPost, "/api/v1/users", credentials("archivist", sampleArchivistPassword), admin); response.Code != http.StatusCreated {
		t.Fatalf("create archivist: HTTP %d: %s", response.Code, response.Body.String())
	}
	userIDs := func() map[string]string {
		t.Helper()
		response := call(http.MethodGet, "/api/v1/users", nil, admin)
		var listing struct {
			Items []struct {
				ID       string `json:"id"`
				Username string `json:"username"`
			} `json:"items"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &listing); err != nil {
			t.Fatalf("decode users: %v: %s", err, response.Body.String())
		}
		ids := map[string]string{}
		for _, item := range listing.Items {
			ids[item.Username] = item.ID
		}
		return ids
	}
	ids := userIDs()
	listener := login(call(http.MethodPost, "/api/v1/auth/login", credentials("listener", sampleListenerPassword), nil))
	samples.record("auth/me-user", call(http.MethodGet, "/api/v1/auth/me", nil, listener), http.StatusOK)
	samples.record("errors/forbidden", call(http.MethodGet, "/api/v1/users", nil, listener), http.StatusForbidden)
	samples.record("errors/last-admin", call(http.MethodPatch, "/api/v1/users/"+ids["curator"], map[string]bool{"disabled": true}, admin), http.StatusConflict)
	samples.record("users/disable", call(http.MethodPatch, "/api/v1/users/"+ids["archivist"], map[string]bool{"disabled": true}, admin), http.StatusOK)
	samples.record("users/list", call(http.MethodGet, "/api/v1/users", nil, admin), http.StatusOK)
	samples.record("users/enable", call(http.MethodPatch, "/api/v1/users/"+ids["archivist"], map[string]bool{"disabled": false}, admin), http.StatusOK)
	samples.record("users/revoke-sessions", call(http.MethodPost, "/api/v1/users/"+ids["listener"]+"/sessions/revoke", map[string]any{}, admin), http.StatusOK)
	if response := call(http.MethodGet, "/api/v1/auth/me", nil, listener); response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked member session still works: HTTP %d: %s", response.Code, response.Body.String())
	}
	samples.record("errors/invalid-id-user", call(http.MethodPatch, "/api/v1/users/not-a-uuid", map[string]bool{"disabled": true}, admin), http.StatusBadRequest)
	samples.record("errors/not-found-user", call(http.MethodPost, "/api/v1/users/"+uuid.New()+"/sessions/revoke", map[string]any{}, admin), http.StatusNotFound)

	// 目录与扫描的合成素材：一个有效的 FLAC 与一个损坏文件，都在测试专用的临时根目录里。
	libraryA := createLibraryDirectory(t, application, "classical-a")
	libraryB := createLibraryDirectory(t, application, "classical-b")
	albumDirectory := filepath.Join(libraryA, "album")
	if err := os.MkdirAll(albumDirectory, 0o755); err != nil {
		t.Fatalf("create album directory: %v", err)
	}
	validFLAC := parsertest.FLACWithAudio(t, [16]byte{1, 2, 3}, bytes.Repeat([]byte{7}, 64),
		"TITLE=Prelude No. 1", "ARTIST=Sample Pianist", "ALBUM=Sample Preludes", "ALBUMARTIST=Sample Pianist", "DATE=2001", "TRACKNUMBER=1")
	if err := os.WriteFile(filepath.Join(albumDirectory, "01.flac"), validFLAC, 0o644); err != nil {
		t.Fatalf("write valid FLAC fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(libraryB, "broken.flac"), []byte("this is not a FLAC stream"), 0o644); err != nil {
		t.Fatalf("write corrupt FLAC fixture: %v", err)
	}

	// 目录：新增、非法路径、缺少幂等键、幂等键冲突。
	samples.record("errors/invalid-library-path", call(http.MethodPost, "/api/v1/library-roots", map[string]string{"path": "/definitely/outside/the/allowed/roots"}, admin, idempotency("sample-invalid-path")), http.StatusBadRequest)
	samples.record("errors/invalid-input-missing-idempotency-key", call(http.MethodPost, "/api/v1/library-roots", map[string]string{"path": libraryA}, admin), http.StatusBadRequest)
	createdA := call(http.MethodPost, "/api/v1/library-roots", map[string]string{"path": libraryA}, admin, idempotency("sample-create-a"))
	samples.record("library-roots/create", createdA, http.StatusCreated)
	samples.record("errors/idempotency-conflict", call(http.MethodPost, "/api/v1/library-roots", map[string]string{"path": libraryB}, admin, idempotency("sample-create-a")), http.StatusConflict)
	createdB := call(http.MethodPost, "/api/v1/library-roots", map[string]string{"path": libraryB}, admin, idempotency("sample-create-b"))
	if createdB.Code != http.StatusCreated {
		t.Fatalf("create second root: HTTP %d: %s", createdB.Code, createdB.Body.String())
	}
	rootB := decodeJSONMap(t, createdB)["id"].(string)

	// 第一次扫描：测试事务先独占 library_roots，worker 读不到目录而停在第一步，
	// 因此“新建（202）”“进行中”“已有扫描（200）”的响应都发生在 running 状态，不依赖时序。
	holder, err := application.database.Begin()
	if err != nil {
		t.Fatalf("begin lock holder: %v", err)
	}
	t.Cleanup(func() { _ = holder.Rollback() })
	if _, err := holder.Exec(`LOCK TABLE library_roots IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("lock library_roots: %v", err)
	}
	started := call(http.MethodPost, "/api/v1/scans", map[string]any{}, admin)
	samples.record("scans/start-created", started, http.StatusAccepted)
	firstScan := decodeJSONMap(t, started)["id"].(string)
	waitForLockWaiters(t, application.database, 1)
	samples.record("scans/active-running", call(http.MethodGet, "/api/v1/scans/active", nil, admin), http.StatusOK)
	samples.record("scans/get-running", call(http.MethodGet, "/api/v1/scans/"+firstScan, nil, admin), http.StatusOK)
	samples.record("scans/start-existing", call(http.MethodPost, "/api/v1/scans", map[string]any{}, admin), http.StatusOK)
	if err := holder.Rollback(); err != nil {
		t.Fatalf("release lock holder: %v", err)
	}
	if status := waitForScanToFinish(t, application, firstScan); status != "incomplete" {
		t.Fatalf("first scan with a corrupt file should be incomplete, got %s", status)
	}
	samples.record("scans/get-incomplete", call(http.MethodGet, "/api/v1/scans/"+firstScan, nil, admin), http.StatusOK)
	samples.record("scans/diagnostics", call(http.MethodGet, "/api/v1/scans/"+firstScan+"/diagnostics", nil, admin), http.StatusOK)

	// 目录：停用、版本冲突、重复登记已停用目录。
	samples.record("library-roots/disable", call(http.MethodPatch, "/api/v1/library-roots/"+rootB, map[string]any{"status": "disabled", "expected_revision": 1}, admin, idempotency("sample-disable-b")), http.StatusOK)
	samples.record("errors/revision-conflict", call(http.MethodPatch, "/api/v1/library-roots/"+rootB, map[string]any{"status": "disabled", "expected_revision": 1}, admin, idempotency("sample-disable-stale")), http.StatusConflict)
	samples.record("library-roots/create-existing-disabled", call(http.MethodPost, "/api/v1/library-roots", map[string]string{"path": libraryB}, admin, idempotency("sample-register-disabled")), http.StatusCreated)
	samples.record("library-roots/list", call(http.MethodGet, "/api/v1/library-roots", nil, admin), http.StatusOK)

	// 第二次扫描只覆盖启用的目录，成功结束；此后才有发行组。
	second := call(http.MethodPost, "/api/v1/scans", map[string]any{}, admin)
	if second.Code != http.StatusAccepted {
		t.Fatalf("start second scan: HTTP %d: %s", second.Code, second.Body.String())
	}
	secondScan := decodeJSONMap(t, second)["id"].(string)
	if status := waitForScanToFinish(t, application, secondScan); status != "succeeded" {
		t.Fatalf("second scan should succeed, got %s", status)
	}
	samples.record("scans/get-succeeded", call(http.MethodGet, "/api/v1/scans/"+secondScan, nil, admin), http.StatusOK)
	samples.record("scans/diagnostics-empty", call(http.MethodGet, "/api/v1/scans/"+secondScan+"/diagnostics", nil, admin), http.StatusOK)
	samples.record("release-groups/list-one", call(http.MethodGet, "/api/v1/release-groups?page_size=1", nil, admin), http.StatusOK)
	var releaseID string
	if err := application.database.QueryRow(`SELECT id::text FROM releases`).Scan(&releaseID); err != nil {
		t.Fatalf("read scanned release: %v", err)
	}
	if _, err := application.database.Exec(`INSERT INTO release_artworks(release_id,content_hash,mime_type,width,height,storage_key,source_type)
		VALUES($1::uuid,$2,'image/webp',640,480,'sample-cover.webp','embedded')`, releaseID, strings.Repeat("c", 64)); err != nil {
		t.Fatalf("insert artwork fixture: %v", err)
	}
	samples.record("release-groups/list-one-with-artwork", call(http.MethodGet, "/api/v1/release-groups?page_size=1", nil, admin), http.StatusOK)

	// 目录恢复与操作记录；再补一条失败记录（当前后端不会写入失败操作，但表允许，界面必须能显示）。
	samples.record("library-roots/restore", call(http.MethodPost, "/api/v1/library-roots/"+rootB+"/restore", map[string]int{"expected_revision": 2}, admin, idempotency("sample-restore-b")), http.StatusOK)
	if _, err := application.database.Exec(`INSERT INTO library_root_operations(id,root_id,actor_id,operation_type,status,idempotency_key,fingerprint,error_code,request_id)
		VALUES($1::uuid,$2::uuid,$3::uuid,'disable','failed','sample-failed-operation','sample-fingerprint','operation_failed','sample-request')`, uuid.New(), rootB, ids["curator"]); err != nil {
		t.Fatalf("insert failed operation fixture: %v", err)
	}
	samples.record("library-root-operations/list", call(http.MethodGet, "/api/v1/library-root-operations", nil, admin), http.StatusOK)

	// 直接写入的扫描：失败（带多类诊断）、取消请求中、已取消。真实扫描无法按需产生这些终态。
	failedScan := uuid.New()
	if _, err := application.database.Exec(`INSERT INTO scan_runs(id,status,started_at,finished_at,error_message) VALUES($1::uuid,'failed',NOW()-INTERVAL '5 minutes',NOW()-INTERVAL '4 minutes','scan_failed')`, failedScan); err != nil {
		t.Fatalf("insert failed scan fixture: %v", err)
	}
	for _, diagnostic := range []struct {
		id      int
		kind    string
		path    any
		message string
	}{
		{101, "parse_failure", "disc-1/01.flac", "音频文件解析失败"},
		{102, "permission_error", "disc-2", "无法读取文件"},
		{103, "database_error", nil, "扫描数据库操作失败"},
		{104, "unsupported_format", "extras/cover.dsf", "不支持的音频格式"},
	} {
		if _, err := application.database.Exec(`INSERT INTO scan_diagnostics(id,scan_run_id,relative_path,kind,message) VALUES($1,$2::uuid,$3,$4,$5)`, diagnostic.id, failedScan, diagnostic.path, diagnostic.kind, diagnostic.message); err != nil {
			t.Fatalf("insert diagnostic fixture: %v", err)
		}
	}
	samples.record("scans/get-failed", call(http.MethodGet, "/api/v1/scans/"+failedScan, nil, admin), http.StatusOK)
	samples.record("scans/diagnostics-multiple-kinds", call(http.MethodGet, "/api/v1/scans/"+failedScan+"/diagnostics", nil, admin), http.StatusOK)

	cancelScan := uuid.New()
	if _, err := application.database.Exec(`INSERT INTO scan_runs(id,status,started_at) VALUES($1::uuid,'running',NOW())`, cancelScan); err != nil {
		t.Fatalf("insert running scan fixture: %v", err)
	}
	samples.record("scans/cancel-requested", call(http.MethodPost, "/api/v1/scans/"+cancelScan+"/cancel", map[string]any{}, admin), http.StatusAccepted)
	samples.record("scans/get-cancel-requested", call(http.MethodGet, "/api/v1/scans/"+cancelScan, nil, admin), http.StatusOK)
	samples.record("scans/list", call(http.MethodGet, "/api/v1/scans", nil, admin), http.StatusOK)
	if _, err := application.database.Exec(`UPDATE scan_runs SET status='canceled',finished_at=NOW() WHERE id=$1::uuid`, cancelScan); err != nil {
		t.Fatalf("finish canceled scan fixture: %v", err)
	}
	samples.record("scans/get-canceled", call(http.MethodGet, "/api/v1/scans/"+cancelScan, nil, admin), http.StatusOK)
	samples.record("scans/cancel-already-finished", call(http.MethodPost, "/api/v1/scans/"+cancelScan+"/cancel", map[string]any{}, admin), http.StatusOK)

	// 通用错误：扫描不存在、标识无效、未知接口、方法不符、数据库不可用的降级响应。
	samples.record("errors/not-found-scan", call(http.MethodGet, "/api/v1/scans/"+uuid.New(), nil, admin), http.StatusNotFound)
	samples.record("errors/invalid-id", call(http.MethodGet, "/api/v1/scans/not-a-uuid", nil, admin), http.StatusBadRequest)
	samples.record("errors/not-found-api", call(http.MethodGet, "/api/v1/missing", nil, admin), http.StatusNotFound)
	samples.record("errors/method-not-allowed", call(http.MethodPut, "/api/v1/users", nil, admin), http.StatusMethodNotAllowed)
	degraded := httptest.NewRecorder()
	degradedRequest := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	degradedRequest.Header.Set("X-Request-ID", sampleRequestID)
	Degraded(testFrontend(), slog.New(slog.NewTextHandler(io.Discard, nil))).ServeHTTP(degraded, degradedRequest)
	samples.record("errors/database-unavailable", degraded, http.StatusServiceUnavailable)

	// 退出：用第二个会话，避免影响上面仍在使用的管理员会话。
	secondAdmin := login(call(http.MethodPost, "/api/v1/auth/login", credentials("curator", sampleAdminPassword), nil))
	samples.record("auth/logout", call(http.MethodPost, "/api/v1/auth/logout", map[string]any{}, secondAdmin), http.StatusOK)

	samples.finish()
}

// waitForScanToFinish 等扫描离开 running，再等 worker 退出：worker 在写入终态之后才释放扫描互斥并清除运行标记，
// 不等它退出，紧接着的下一次启动可能看到残留的“已有扫描”。
func waitForScanToFinish(t *testing.T, application *integrationTestApplication, scanID string) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var status string
	for {
		if err := application.database.QueryRow(`SELECT status FROM scan_runs WHERE id=$1::uuid`, scanID).Scan(&status); err != nil {
			t.Fatalf("read scan status: %v", err)
		}
		if status != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("scan %s did not finish", scanID)
		}
		time.Sleep(20 * time.Millisecond)
	}
	waitContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := application.app.WaitForScans(waitContext); err != nil {
		t.Fatalf("wait for scan worker: %v", err)
	}
	return status
}

type apiSampleRecorder struct {
	t         *testing.T
	samples   map[string][]byte
	forbidden []string
}

func newAPISampleRecorder(t *testing.T, application *integrationTestApplication) *apiSampleRecorder {
	t.Helper()
	return &apiSampleRecorder{
		t:       t,
		samples: map[string][]byte{},
		// 样本不得包含服务器路径、凭据、会话令牌与 Cookie 名。
		forbidden: []string{
			application.allowedRoot, os.TempDir(), identity.SessionCookieName, "password",
			sampleAdminPassword, sampleListenerPassword, sampleArchivistPassword,
		},
	}
}

// forbid 把运行期产生的敏感值（会话令牌）加入禁止出现在样本里的清单。
func (recorder *apiSampleRecorder) forbid(value string) {
	recorder.forbidden = append(recorder.forbidden, value)
}

func (recorder *apiSampleRecorder) record(name string, response *httptest.ResponseRecorder, wantStatus int) {
	t := recorder.t
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("sample %s: expected HTTP %d, got %d: %s", name, wantStatus, response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("sample %s: expected Content-Type application/json, got %q", name, contentType)
	}
	if _, exists := recorder.samples[name]; exists {
		t.Fatalf("sample %s was recorded twice", name)
	}
	body := response.Body.String()
	for _, secret := range recorder.forbidden {
		if strings.Contains(body, secret) {
			t.Fatalf("sample %s contains a forbidden value %q: %s", name, secret, body)
		}
	}
	recorder.samples[name] = encodeAPISample(t, name, response.Code, response.Body.Bytes())
}

// finish 在比较模式下要求样本与提交的文件逐字一致、没有多余文件；更新模式下重写整个目录。
func (recorder *apiSampleRecorder) finish() {
	t := recorder.t
	t.Helper()
	if os.Getenv(updateAPISamplesEnv) == "1" {
		if err := os.RemoveAll(apiSamplesDirectory); err != nil {
			t.Fatalf("clear sample directory: %v", err)
		}
		for name, content := range recorder.samples {
			path := sampleFilePath(name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("create sample directory: %v", err)
			}
			if err := os.WriteFile(path, content, 0o644); err != nil {
				t.Fatalf("write sample %s: %v", name, err)
			}
		}
		t.Logf("已重写 %d 个 API 样本；请检查 git diff，并同步前端 decoder 与契约测试", len(recorder.samples))
		return
	}

	committed := map[string][]byte{}
	walkErr := filepath.WalkDir(apiSamplesDirectory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		relative, _ := filepath.Rel(apiSamplesDirectory, path)
		committed[strings.TrimSuffix(filepath.ToSlash(relative), ".json")] = content
		return nil
	})
	if walkErr != nil && !os.IsNotExist(walkErr) {
		t.Fatalf("read committed samples: %v", walkErr)
	}

	var problems []string
	for _, name := range sortedKeys(recorder.samples) {
		expected, exists := committed[name]
		switch {
		case !exists:
			problems = append(problems, fmt.Sprintf("缺少样本文件 %s", sampleFilePath(name)))
		case !bytes.Equal(expected, recorder.samples[name]):
			problems = append(problems, fmt.Sprintf("样本 %s 与当前响应不一致：%s", sampleFilePath(name), firstDifference(expected, recorder.samples[name])))
		}
	}
	for _, name := range sortedKeys(committed) {
		if _, produced := recorder.samples[name]; !produced {
			problems = append(problems, fmt.Sprintf("多余的样本文件 %s（本次测试没有产出）", sampleFilePath(name)))
		}
	}
	if len(problems) > 0 {
		t.Fatalf("API 响应样本与提交的文件不一致：\n  %s\n如果这是有意的合同变更，请运行 %s=1 ./scripts/test-integration.sh 重写样本，并同步前端 decoder 与契约测试。",
			strings.Join(problems, "\n  "), updateAPISamplesEnv)
	}
}

func sampleFilePath(name string) string {
	return filepath.Join(apiSamplesDirectory, filepath.FromSlash(name)+".json")
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func firstDifference(committed, actual []byte) string {
	committedLines := strings.Split(string(committed), "\n")
	actualLines := strings.Split(string(actual), "\n")
	for index := 0; index < len(committedLines) || index < len(actualLines); index++ {
		var left, right string
		if index < len(committedLines) {
			left = committedLines[index]
		}
		if index < len(actualLines) {
			right = actualLines[index]
		}
		if left != right {
			return fmt.Sprintf("第 %d 行，提交的是 %q，当前是 %q", index+1, strings.TrimSpace(left), strings.TrimSpace(right))
		}
	}
	return "内容相同"
}

var (
	sampleUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	sampleTimePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$`)
)

// encodeAPISample 把响应写成 {"status":…, "body":…}：用 UseNumber 保留数字原样，规范化 UUID 与时间，
// 按键排序、两空格缩进、末尾换行，使重复运行得到完全相同的字节。
func encodeAPISample(t *testing.T, name string, status int, body []byte) []byte {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var parsed any
	if err := decoder.Decode(&parsed); err != nil {
		t.Fatalf("sample %s: response is not JSON: %v: %s", name, err, body)
	}
	if _, err := decoder.Token(); err != io.EOF {
		t.Fatalf("sample %s: response has trailing data: %s", name, body)
	}
	normalizer := newSampleNormalizer()
	normalizer.collect(parsed)
	normalizer.finalize()
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(map[string]any{"status": status, "body": normalizer.replace(parsed)}); err != nil {
		t.Fatalf("sample %s: encode: %v", name, err)
	}
	return output.Bytes()
}

// sampleNormalizer 把随机 UUID 替换为按首次出现顺序编号的固定值，把时间替换为按先后顺序逐秒递增的固定值：
// 相同的值得到相同的替换，先后关系保留，具体的随机值与墙钟时间不进入样本。
type sampleNormalizer struct {
	uuidOrder []string
	uuids     map[string]string
	instants  map[int64]string
}

func newSampleNormalizer() *sampleNormalizer {
	return &sampleNormalizer{uuids: map[string]string{}, instants: map[int64]string{}}
}

func (normalizer *sampleNormalizer) collect(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range sortedKeys(typed) {
			normalizer.collect(typed[key])
		}
	case []any:
		for _, item := range typed {
			normalizer.collect(item)
		}
	case string:
		if sampleUUIDPattern.MatchString(typed) {
			lower := strings.ToLower(typed)
			if _, seen := normalizer.uuids[lower]; !seen {
				normalizer.uuids[lower] = ""
				normalizer.uuidOrder = append(normalizer.uuidOrder, lower)
			}
		} else if sampleTimePattern.MatchString(typed) {
			if parsed, err := time.Parse(time.RFC3339Nano, typed); err == nil {
				normalizer.instants[parsed.UnixNano()] = ""
			}
		}
	}
}

func (normalizer *sampleNormalizer) finalize() {
	for index, original := range normalizer.uuidOrder {
		normalizer.uuids[original] = fmt.Sprintf("00000000-0000-4000-8000-%012d", index+1)
	}
	instants := make([]int64, 0, len(normalizer.instants))
	for instant := range normalizer.instants {
		instants = append(instants, instant)
	}
	sort.Slice(instants, func(left, right int) bool { return instants[left] < instants[right] })
	base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	for rank, instant := range instants {
		normalizer.instants[instant] = base.Add(time.Duration(rank) * time.Second).Format("2006-01-02T15:04:05Z")
	}
}

func (normalizer *sampleNormalizer) replace(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = normalizer.replace(item)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = normalizer.replace(item)
		}
		return result
	case string:
		if sampleUUIDPattern.MatchString(typed) {
			return normalizer.uuids[strings.ToLower(typed)]
		}
		if sampleTimePattern.MatchString(typed) {
			if parsed, err := time.Parse(time.RFC3339Nano, typed); err == nil {
				return normalizer.instants[parsed.UnixNano()]
			}
		}
	}
	return value
}
