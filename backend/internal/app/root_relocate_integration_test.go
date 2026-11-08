package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLRelocateRootOperationTransactionsAndReplay(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin := application.createUser(t, "admin")
	oldPath := createLibraryDirectory(t, application, "old-library")
	newPath := createLibraryDirectory(t, application, "new-library")
	rootID := createRoot(t, application, admin, oldPath, "create-root")

	first := relocateRootRequest(t, application, admin, rootID, newPath, 1, "relocate-first")
	requireRelocatedRoot(t, first, rootID, newPath, "active", 2, []string{})
	assertRelocatedRootState(t, application.database, rootID, newPath, "active", 2)
	assertOperationCount(t, application.database, rootID, 2)
	assertRelocateReceipt(t, application.database, rootID, "relocate-first", oldPath, newPath, 1, 2, first)

	replay := relocateRootRequest(t, application, admin, rootID, newPath, 1, "relocate-first")
	if replay.Code != http.StatusOK || !jsonResponsesEqual(t, replay, first) {
		t.Fatalf("重放没有返回首次收据：%d %s", replay.Code, replay.Body.String())
	}
	requireAPIError(t, relocateRootRequest(t, application, admin, rootID, oldPath, 1, "relocate-first"), http.StatusConflict, "idempotency_conflict")
	assertRelocatedRootState(t, application.database, rootID, newPath, "active", 2)
	assertOperationCount(t, application.database, rootID, 2)

	for _, endpoint := range []string{"/api/v1/library-roots", "/api/v1/library-root-operations"} {
		response := application.request(t, http.MethodGet, endpoint, nil, &admin, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("读取安全投影失败：%d %s", response.Code, response.Body.String())
		}
		assertRelocateResponseRedacted(t, response, application.allowedRoot, oldPath, newPath)
		if endpoint == "/api/v1/library-root-operations" && !strings.Contains(response.Body.String(), `"operation":"relocate"`) {
			t.Fatalf("操作列表缺少改路径记录：%s", response.Body.String())
		}
	}
}

func TestPostgreSQLRelocateRootRejectsInvalidRequestsWithoutChanges(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin := application.createUser(t, "admin")
	oldPath := createLibraryDirectory(t, application, "validation-old")
	newPath := createLibraryDirectory(t, application, "validation-new")
	rootID := createRoot(t, application, admin, oldPath, "create-root")
	occupiedPath := createLibraryDirectory(t, application, "occupied-active")
	occupiedID := createRoot(t, application, admin, occupiedPath, "create-occupied")
	disabledPath := createLibraryDirectory(t, application, "occupied-disabled")
	disabledID := createAndDisableRoot(t, application, admin, disabledPath, "occupied-disabled")
	outsidePath := t.TempDir()
	linkPath := filepath.Join(application.allowedRoot, "directory-link")
	if err := os.Symlink(newPath, linkPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(newPath, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, id, path string
		revision       int64
		status         int
		code           string
	}{
		{"版本冲突", rootID, newPath, 2, http.StatusConflict, "revision_conflict"},
		{"已登记启用根", rootID, occupiedPath, 1, http.StatusConflict, "path_already_registered"},
		{"已登记停用根", rootID, disabledPath, 1, http.StatusConflict, "path_already_registered"},
		{"越界", rootID, outsidePath, 1, http.StatusBadRequest, "invalid_library_path"},
		{"目录链接", rootID, linkPath, 1, http.StatusBadRequest, "invalid_library_path"},
		{"祖先目录链接", rootID, filepath.Join(linkPath, "child"), 1, http.StatusBadRequest, "invalid_library_path"},
		{"未知根", uuid.New(), newPath, 1, http.StatusNotFound, "not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := relocateRootRequest(t, application, admin, tc.id, tc.path, tc.revision, "rejected-relocate")
			requireAPIError(t, response, tc.status, tc.code)
			assertRelocateResponseRedacted(t, response, application.allowedRoot, outsidePath)
			assertRelocatedRootState(t, application.database, rootID, oldPath, "active", 1)
			assertOperationCount(t, application.database, rootID, 1)
		})
	}
	assertRelocatedRootState(t, application.database, occupiedID, occupiedPath, "active", 1)
	assertRelocatedRootState(t, application.database, disabledID, disabledPath, "disabled", 2)
	assertOperationCount(t, application.database, occupiedID, 1)
	assertOperationCount(t, application.database, disabledID, 2)
}

func TestPostgreSQLRelocateRootScanLockRejects(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin := application.createUser(t, "admin")
	oldPath := createLibraryDirectory(t, application, "lock-old")
	newPath := createLibraryDirectory(t, application, "lock-new")
	rootID := createRoot(t, application, admin, oldPath, "create-root")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	holder, err := application.database.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 独立物理连接模拟另一进程的扫描；不写 running 行，防止仅查状态的实现误通过。
	const scanLockClass = 0x5343414e
	var lockKey, holderPID int
	if err := holder.QueryRowContext(ctx, `SELECT hashtext(current_schema()),pg_backend_pid()`).Scan(&lockKey, &holderPID); err != nil {
		_ = holder.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := holder.ExecContext(cleanupContext, `SELECT pg_advisory_unlock($1::int,$2::int)`, scanLockClass, lockKey); err != nil {
			t.Errorf("清理扫描锁失败：%v", err)
		}
		if err := holder.Close(); err != nil {
			t.Errorf("关闭扫描锁连接失败：%v", err)
		}
	})
	var acquired bool
	if err := holder.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1::int,$2::int)`, scanLockClass, lockKey).Scan(&acquired); err != nil || !acquired {
		t.Fatalf("持有模拟扫描锁失败：acquired=%v err=%v", acquired, err)
	}
	var requestPID int
	if err := application.database.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&requestPID); err != nil || requestPID == holderPID {
		t.Fatalf("请求未使用独立连接：holder=%d request=%d err=%v", holderPID, requestPID, err)
	}
	response := relocateRootRequest(t, application, admin, rootID, newPath, 1, "during-scan")
	requireAPIError(t, response, http.StatusConflict, "scan_running")
	assertRelocateResponseRedacted(t, response, application.allowedRoot)
	assertRelocatedRootState(t, application.database, rootID, oldPath, "active", 1)
	assertOperationCount(t, application.database, rootID, 1)
}

func TestPostgreSQLRelocateDisabledRootThenRestoreKeepsNewPath(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin := application.createUser(t, "admin")
	oldPath := createLibraryDirectory(t, application, "disabled-old")
	newPath := createLibraryDirectory(t, application, "disabled-new")
	rootID := createAndDisableRoot(t, application, admin, oldPath, "disabled")
	response := relocateRootRequest(t, application, admin, rootID, newPath, 2, "relocate-disabled")
	requireRelocatedRoot(t, response, rootID, newPath, "disabled", 3, []string{})
	assertRelocatedRootState(t, application.database, rootID, newPath, "disabled", 3)
	assertRelocateReceipt(t, application.database, rootID, "relocate-disabled", oldPath, newPath, 2, 3, response)

	restored := application.request(t, http.MethodPost, "/api/v1/library-roots/"+rootID+"/restore", map[string]int{"expected_revision": 3}, &admin, idempotencyHeader("restore-after-relocate"))
	if restored.Code != http.StatusOK {
		t.Fatalf("停用后改路径应仍能恢复：%d %s", restored.Code, restored.Body.String())
	}
	assertRelocatedRootState(t, application.database, rootID, newPath, "active", 4)
	assertOperationState(t, application.database, rootID, "restore", 3, 4, "disabled", "active")
	assertOperationCount(t, application.database, rootID, 4)
}

func relocateRootRequest(t *testing.T, application *integrationTestApplication, admin integrationTestUser, rootID, path string, revision int64, key string) *httptest.ResponseRecorder {
	t.Helper()
	return application.request(t, http.MethodPost, "/api/v1/library-roots/"+rootID+"/relocate", map[string]any{"path": path, "expected_revision": revision}, &admin, idempotencyHeader(key))
}

func requireRelocatedRoot(t *testing.T, response *httptest.ResponseRecorder, rootID, path, status string, revision int64, excluded []string) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("改路径失败：%d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/json" || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Request-ID") == "" {
		t.Fatalf("改路径响应缺少 JSON、缓存或关联头：%v", response.Header())
	}
	// 严格六字段，排除项即使为空也必须是数组，不带时间戳或数据库前后态。
	excludedValues := make([]any, len(excluded))
	for index, relative := range excluded {
		excludedValues[index] = relative
	}
	want := map[string]any{"id": rootID, "name": filepath.Base(path), "path": filepath.Base(path), "status": status, "revision": float64(revision), "excluded_paths": excludedValues}
	if got := decodeJSONMap(t, response); !reflect.DeepEqual(got, want) {
		t.Fatalf("改路径响应错误：got=%v want=%v", got, want)
	}
	assertRelocateResponseRedacted(t, response, filepath.Dir(path))
}

func assertRelocatedRootState(t *testing.T, database *sql.DB, rootID, path, status string, revision int64) {
	t.Helper()
	assertRootState(t, database, rootID, status, revision)
	var actual string
	if err := database.QueryRow(`SELECT path FROM library_roots WHERE id=$1::uuid`, rootID).Scan(&actual); err != nil || actual != path {
		t.Fatalf("目录路径错误：got=%q want=%q err=%v", actual, path, err)
	}
}

func assertRelocateReceipt(t *testing.T, database *sql.DB, rootID, key, oldPath, newPath string, expected, result int64, response *httptest.ResponseRecorder) {
	t.Helper()
	var status string
	var expectedRevision, resultRevision int64
	var before, after, receipt []byte
	err := database.QueryRow(`SELECT status,expected_revision,result_revision,before_state,after_state,response
		FROM library_root_operations WHERE root_id=$1::uuid AND operation_type='relocate' AND idempotency_key=$2`, rootID, key).
		Scan(&status, &expectedRevision, &resultRevision, &before, &after, &receipt)
	if err != nil || status != "succeeded" || expectedRevision != expected || resultRevision != result {
		t.Fatalf("改路径收据错误：status=%s revision=%d/%d err=%v", status, expectedRevision, resultRevision, err)
	}
	for _, state := range []struct {
		body     []byte
		path     string
		revision int64
	}{{before, oldPath, expected}, {after, newPath, result}} {
		var got map[string]any
		if err := json.Unmarshal(state.body, &got); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{"path": state.path, "revision": float64(state.revision)}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("改路径收据前后态错误：got=%v want=%v", got, want)
		}
	}
	var recorded map[string]any
	if err := json.Unmarshal(receipt, &recorded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recorded, decodeJSONMap(t, response)) {
		t.Fatalf("数据库重放响应与首次响应不一致：%s", receipt)
	}
}

func assertRelocateResponseRedacted(t *testing.T, response *httptest.ResponseRecorder, paths ...string) {
	t.Helper()
	for _, secret := range append(paths, `"before_state"`, `"after_state"`) {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("响应泄露绝对路径或内部前后态：%s", response.Body.String())
		}
	}
}
