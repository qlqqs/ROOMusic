package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type integrationTestApplication struct {
	database    *databaseState
	handler     http.Handler
	origin      string
	allowedRoot string
}

type integrationTestUser struct {
	id    string
	token string
}

func newIntegrationTestApplication(t *testing.T) *integrationTestApplication {
	t.Helper()
	scopedURL := postgrestest.NewSchema(t, "roomusic_test_")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	database, err := openDatabase(ctx, scopedURL)
	if err != nil {
		t.Fatalf("open isolated application database: %v", err)
	}
	t.Cleanup(func() {
		if err := database.connection.Close(); err != nil {
			t.Errorf("close isolated application database: %v", err)
		}
	})

	allowedRoot := t.TempDir()
	config := serverConfig{
		AllowedLibraryRoots: []string{allowedRoot},
		DataDirectory:       t.TempDir(),
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &integrationTestApplication{
		database:    database,
		handler:     buildApplicationHandler(config, database, logger),
		origin:      "http://roomusic.test",
		allowedRoot: allowedRoot,
	}
}

func (application *integrationTestApplication) createUser(t *testing.T, role string) integrationTestUser {
	t.Helper()
	user := integrationTestUser{id: uuid.New(), token: generateSessionToken()}
	username := role + "-" + strings.ReplaceAll(uuid.New(), "-", "")
	if _, err := application.database.connection.Exec(`INSERT INTO users(id,username,password_hash,role) VALUES($1::uuid,$2,'test-only',$3)`, user.id, username, role); err != nil {
		t.Fatalf("insert %s fixture: %v", role, err)
	}
	if _, err := application.database.connection.Exec(`INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2::uuid,NOW()+INTERVAL '1 hour')`, hashSessionToken(user.token), user.id); err != nil {
		t.Fatalf("insert %s session fixture: %v", role, err)
	}
	return user
}

func (application *integrationTestApplication) request(t *testing.T, method, path string, body any, user *integrationTestUser, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode request body: %v", err)
		}
		requestBody = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, requestBody)
	request.Host = "roomusic.test"
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && method != http.MethodHead {
		request.Header.Set("Origin", application.origin)
	}
	if user != nil {
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: user.token})
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	application.handler.ServeHTTP(response, request)
	return response
}

func requireAPIError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("expected HTTP %d, got %d: %s", status, response.Code, response.Body.String())
	}
	var envelope apiError
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode API error: %v", err)
	}
	if envelope.Error.Code != code {
		t.Fatalf("expected error code %q, got %q: %s", code, envelope.Error.Code, response.Body.String())
	}
}

func TestPostgreSQLPermissionMatrixAndImmediateSessionInvalidation(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin := application.createUser(t, "admin")
	ordinary := application.createUser(t, "user")
	scanID := uuid.New()
	if _, err := application.database.connection.Exec(`INSERT INTO scan_runs(id,status,started_at,finished_at) VALUES($1::uuid,'succeeded',NOW(),NOW())`, scanID); err != nil {
		t.Fatalf("insert scan fixture: %v", err)
	}

	for _, testCase := range []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{name: "list users", method: http.MethodGet, path: "/api/v1/users"},
		{name: "create user", method: http.MethodPost, path: "/api/v1/users", body: map[string]string{"Username": "blocked-user", "Password": "long-enough-password"}},
		{name: "update user", method: http.MethodPatch, path: "/api/v1/users/" + ordinary.id, body: map[string]bool{"disabled": true}},
		{name: "revoke sessions", method: http.MethodPost, path: "/api/v1/users/" + ordinary.id + "/sessions/revoke", body: map[string]any{}},
		{name: "list roots", method: http.MethodGet, path: "/api/v1/library-roots"},
		{name: "create root", method: http.MethodPost, path: "/api/v1/library-roots", body: map[string]string{"path": t.TempDir()}},
		{name: "update root", method: http.MethodPatch, path: "/api/v1/library-roots/" + uuid.New(), body: map[string]any{"status": "disabled", "expected_revision": 1}},
		{name: "restore root", method: http.MethodPost, path: "/api/v1/library-roots/" + uuid.New() + "/restore", body: map[string]int{"expected_revision": 2}},
		{name: "operation history", method: http.MethodGet, path: "/api/v1/library-root-operations"},
		{name: "start scan", method: http.MethodPost, path: "/api/v1/scans", body: map[string]any{}},
		{name: "scan diagnostics", method: http.MethodGet, path: "/api/v1/scans/" + scanID + "/diagnostics"},
	} {
		t.Run("ordinary user cannot "+testCase.name, func(t *testing.T) {
			response := application.request(t, testCase.method, testCase.path, testCase.body, &ordinary, nil)
			requireAPIError(t, response, http.StatusForbidden, "forbidden")
		})
	}

	for _, path := range []string{"/api/v1/auth/me", "/api/v1/releases", "/api/v1/scans/" + scanID} {
		response := application.request(t, http.MethodGet, path, nil, &ordinary, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("ordinary user could not read %s: HTTP %d: %s", path, response.Code, response.Body.String())
		}
	}
	for _, path := range []string{"/api/v1/users", "/api/v1/library-roots", "/api/v1/library-root-operations", "/api/v1/scans/" + scanID + "/diagnostics"} {
		response := application.request(t, http.MethodGet, path, nil, &admin, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("administrator could not read %s: HTTP %d: %s", path, response.Code, response.Body.String())
		}
	}

	disabledUser := application.createUser(t, "user")
	response := application.request(t, http.MethodPatch, "/api/v1/users/"+disabledUser.id, map[string]bool{"disabled": true}, &admin, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("disable user: HTTP %d: %s", response.Code, response.Body.String())
	}
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/auth/me", nil, &disabledUser, nil), http.StatusUnauthorized, "unauthorized")

	revokedUser := application.createUser(t, "user")
	response = application.request(t, http.MethodPost, "/api/v1/users/"+revokedUser.id+"/sessions/revoke", map[string]any{}, &admin, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("revoke user sessions: HTTP %d: %s", response.Code, response.Body.String())
	}
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/auth/me", nil, &revokedUser, nil), http.StatusUnauthorized, "unauthorized")

	expiredUser := application.createUser(t, "user")
	if _, err := application.database.connection.Exec(`UPDATE sessions SET expires_at=NOW()-INTERVAL '1 second' WHERE token_hash=$1`, hashSessionToken(expiredUser.token)); err != nil {
		t.Fatalf("expire user session: %v", err)
	}
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/auth/me", nil, &expiredUser, nil), http.StatusUnauthorized, "unauthorized")

	for _, path := range []string{"/api/v1/releases", "/api/v1/users", "/api/v1/library-roots", "/api/v1/library-root-operations"} {
		requireAPIError(t, application.request(t, http.MethodGet, path, nil, nil, nil), http.StatusUnauthorized, "unauthorized")
	}
}

func TestPostgreSQLRootOperationTransactionsAndConflicts(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin := application.createUser(t, "admin")
	firstPath := filepath.Join(application.allowedRoot, "first-library")
	secondPath := filepath.Join(application.allowedRoot, "second-library")
	for _, path := range []string{firstPath, secondPath} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("create library fixture: %v", err)
		}
	}

	createResponse := application.request(t, http.MethodPost, "/api/v1/library-roots", map[string]string{"path": firstPath}, &admin, map[string]string{"Idempotency-Key": "create-first"})
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create root: HTTP %d: %s", createResponse.Code, createResponse.Body.String())
	}
	created := decodeJSONMap(t, createResponse)
	rootID := created["id"].(string)
	if created["name"] != filepath.Base(firstPath) || created["status"] != "active" || created["revision"] != float64(1) {
		t.Fatalf("unexpected create result: %#v", created)
	}
	assertRootState(t, application.database.connection, rootID, "active", 1)
	assertOperationCount(t, application.database.connection, rootID, 1)

	replay := application.request(t, http.MethodPost, "/api/v1/library-roots", map[string]string{"path": firstPath}, &admin, map[string]string{"Idempotency-Key": "create-first"})
	if replay.Code != http.StatusCreated || !jsonResponsesEqual(t, replay, createResponse) {
		t.Fatalf("create replay did not return the recorded result: HTTP %d: %s", replay.Code, replay.Body.String())
	}
	assertOperationCount(t, application.database.connection, rootID, 1)

	conflict := application.request(t, http.MethodPost, "/api/v1/library-roots", map[string]string{"path": secondPath}, &admin, map[string]string{"Idempotency-Key": "create-first"})
	requireAPIError(t, conflict, http.StatusConflict, "idempotency_conflict")
	var secondPathRows int
	if err := application.database.connection.QueryRow(`SELECT COUNT(*) FROM library_roots WHERE path=$1`, secondPath).Scan(&secondPathRows); err != nil || secondPathRows != 0 {
		t.Fatalf("idempotency conflict left a second root: count=%d err=%v", secondPathRows, err)
	}

	disableBody := map[string]any{"status": "disabled", "expected_revision": 1}
	disableResponse := application.request(t, http.MethodPatch, "/api/v1/library-roots/"+rootID, disableBody, &admin, map[string]string{"Idempotency-Key": "disable-first"})
	if disableResponse.Code != http.StatusOK {
		t.Fatalf("disable root: HTTP %d: %s", disableResponse.Code, disableResponse.Body.String())
	}
	assertRootState(t, application.database.connection, rootID, "disabled", 2)
	assertOperationState(t, application.database.connection, rootID, "disable", 1, 2, "active", "disabled")

	disableReplay := application.request(t, http.MethodPatch, "/api/v1/library-roots/"+rootID, disableBody, &admin, map[string]string{"Idempotency-Key": "disable-first"})
	if disableReplay.Code != http.StatusOK || !jsonResponsesEqual(t, disableReplay, disableResponse) {
		t.Fatalf("disable replay did not return the recorded result: HTTP %d: %s", disableReplay.Code, disableReplay.Body.String())
	}
	assertOperationCount(t, application.database.connection, rootID, 2)

	for name, body := range map[string]map[string]any{
		"stale revision":   {"status": "disabled", "expected_revision": 1},
		"repeated disable": {"status": "disabled", "expected_revision": 2},
	} {
		t.Run(name, func(t *testing.T) {
			response := application.request(t, http.MethodPatch, "/api/v1/library-roots/"+rootID, body, &admin, map[string]string{"Idempotency-Key": "failed-" + strings.ReplaceAll(name, " ", "-")})
			requireAPIError(t, response, http.StatusConflict, "revision_conflict")
			assertRootState(t, application.database.connection, rootID, "disabled", 2)
			assertOperationCount(t, application.database.connection, rootID, 2)
		})
	}

	restoreBody := map[string]int{"expected_revision": 2}
	restoreResponse := application.request(t, http.MethodPost, "/api/v1/library-roots/"+rootID+"/restore", restoreBody, &admin, map[string]string{"Idempotency-Key": "restore-first"})
	if restoreResponse.Code != http.StatusOK {
		t.Fatalf("restore root: HTTP %d: %s", restoreResponse.Code, restoreResponse.Body.String())
	}
	assertRootState(t, application.database.connection, rootID, "active", 3)
	assertOperationState(t, application.database.connection, rootID, "restore", 2, 3, "disabled", "active")
	restoreReplay := application.request(t, http.MethodPost, "/api/v1/library-roots/"+rootID+"/restore", restoreBody, &admin, map[string]string{"Idempotency-Key": "restore-first"})
	if restoreReplay.Code != http.StatusOK || !jsonResponsesEqual(t, restoreReplay, restoreResponse) {
		t.Fatalf("restore replay did not return the recorded result: HTTP %d: %s", restoreReplay.Code, restoreReplay.Body.String())
	}
	assertOperationCount(t, application.database.connection, rootID, 3)

	modifiedRootID := createAndDisableRoot(t, application, admin, secondPath, "modified")
	if _, err := application.database.connection.Exec(`UPDATE library_roots SET revision=revision+1 WHERE id=$1::uuid`, modifiedRootID); err != nil {
		t.Fatalf("simulate later root modification: %v", err)
	}
	laterModification := application.request(t, http.MethodPost, "/api/v1/library-roots/"+modifiedRootID+"/restore", map[string]int{"expected_revision": 2}, &admin, map[string]string{"Idempotency-Key": "restore-modified"})
	requireAPIError(t, laterModification, http.StatusConflict, "revision_conflict")
	assertRootState(t, application.database.connection, modifiedRootID, "disabled", 3)
	assertOperationCount(t, application.database.connection, modifiedRootID, 2)

	missingInverseID := uuid.New()
	missingInversePath := filepath.Join(application.allowedRoot, "missing-inverse")
	if _, err := application.database.connection.Exec(`INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'disabled',2)`, missingInverseID, missingInversePath); err != nil {
		t.Fatalf("insert root without inverse operation: %v", err)
	}
	missingInverse := application.request(t, http.MethodPost, "/api/v1/library-roots/"+missingInverseID+"/restore", map[string]int{"expected_revision": 2}, &admin, map[string]string{"Idempotency-Key": "restore-missing-inverse"})
	requireAPIError(t, missingInverse, http.StatusConflict, "revision_conflict")
	assertRootState(t, application.database.connection, missingInverseID, "disabled", 2)
	assertOperationCount(t, application.database.connection, missingInverseID, 0)

	rollbackPath := filepath.Join(application.allowedRoot, "rollback-library")
	if err := os.MkdirAll(rollbackPath, 0o755); err != nil {
		t.Fatalf("create rollback library fixture: %v", err)
	}
	rollbackRootID := createRoot(t, application, admin, rollbackPath, "create-rollback")
	if _, err := application.database.connection.Exec(`CREATE FUNCTION reject_disable_operation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation_type='disable' THEN RAISE EXCEPTION 'test rejection'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_disable_operation BEFORE INSERT ON library_root_operations FOR EACH ROW EXECUTE FUNCTION reject_disable_operation()`); err != nil {
		t.Fatalf("install rollback test trigger: %v", err)
	}
	rollbackResponse := application.request(t, http.MethodPatch, "/api/v1/library-roots/"+rollbackRootID, map[string]any{"status": "disabled", "expected_revision": 1}, &admin, map[string]string{"Idempotency-Key": "disable-rollback"})
	requireAPIError(t, rollbackResponse, http.StatusInternalServerError, "operation_failed")
	assertRootState(t, application.database.connection, rollbackRootID, "active", 1)
	assertOperationCount(t, application.database.connection, rollbackRootID, 1)
}

func TestPostgreSQLRootOperationIdempotencyBindsTarget(t *testing.T) {
	application := newIntegrationTestApplication(t)
	database := application.database.connection
	admin := application.createUser(t, "admin")

	t.Run("disable with another target", func(t *testing.T) {
		rootA := createRoot(t, application, admin, createLibraryDirectory(t, application, "disable-a"), "disable-a-create")
		rootB := createRoot(t, application, admin, createLibraryDirectory(t, application, "disable-b"), "disable-b-create")
		body := map[string]any{"status": "disabled", "expected_revision": 1}
		key := map[string]string{"Idempotency-Key": "shared-disable"}

		first := application.request(t, http.MethodPatch, "/api/v1/library-roots/"+rootA, body, &admin, key)
		if first.Code != http.StatusOK {
			t.Fatalf("disable first target: HTTP %d: %s", first.Code, first.Body.String())
		}
		cross := application.request(t, http.MethodPatch, "/api/v1/library-roots/"+rootB, body, &admin, key)
		requireAPIError(t, cross, http.StatusConflict, "idempotency_conflict")
		if strings.Contains(cross.Body.String(), rootA) {
			t.Fatalf("cross-target conflict disclosed the first target: %s", cross.Body.String())
		}
		assertRootState(t, database, rootB, "active", 1)
		assertOperationCount(t, database, rootB, 1)
		assertRootState(t, database, rootA, "disabled", 2)
		assertOperationCount(t, database, rootA, 2)

		// 路由 ID 大小写不同仍是同一个目标，重放返回原结果。
		upper := application.request(t, http.MethodPatch, "/api/v1/library-roots/"+strings.ToUpper(rootA), body, &admin, key)
		if upper.Code != http.StatusOK || !jsonResponsesEqual(t, upper, first) {
			t.Fatalf("upper-case route replay did not return the recorded result: HTTP %d: %s", upper.Code, upper.Body.String())
		}
		assertOperationCount(t, database, rootA, 2)
	})

	t.Run("restore with another target", func(t *testing.T) {
		rootA := createAndDisableRoot(t, application, admin, createLibraryDirectory(t, application, "restore-a"), "restore-a")
		rootB := createAndDisableRoot(t, application, admin, createLibraryDirectory(t, application, "restore-b"), "restore-b")
		body := map[string]int{"expected_revision": 2}
		key := map[string]string{"Idempotency-Key": "shared-restore"}

		first := application.request(t, http.MethodPost, "/api/v1/library-roots/"+rootA+"/restore", body, &admin, key)
		if first.Code != http.StatusOK {
			t.Fatalf("restore first target: HTTP %d: %s", first.Code, first.Body.String())
		}
		cross := application.request(t, http.MethodPost, "/api/v1/library-roots/"+rootB+"/restore", body, &admin, key)
		requireAPIError(t, cross, http.StatusConflict, "idempotency_conflict")
		if strings.Contains(cross.Body.String(), rootA) {
			t.Fatalf("cross-target conflict disclosed the first target: %s", cross.Body.String())
		}
		assertRootState(t, database, rootB, "disabled", 2)
		assertOperationCount(t, database, rootB, 2)
		assertRootState(t, database, rootA, "active", 3)
		assertOperationCount(t, database, rootA, 3)
	})

	t.Run("malformed identifiers", func(t *testing.T) {
		var before int
		if err := database.QueryRow(`SELECT COUNT(*) FROM library_root_operations`).Scan(&before); err != nil {
			t.Fatalf("count root operations: %v", err)
		}
		key := map[string]string{"Idempotency-Key": "malformed-identifier"}
		disable := application.request(t, http.MethodPatch, "/api/v1/library-roots/not-a-uuid", map[string]any{"status": "disabled", "expected_revision": 1}, &admin, key)
		requireAPIError(t, disable, http.StatusBadRequest, "invalid_id")
		restore := application.request(t, http.MethodPost, "/api/v1/library-roots/not-a-uuid/restore", map[string]int{"expected_revision": 1}, &admin, key)
		requireAPIError(t, restore, http.StatusBadRequest, "invalid_id")
		var after int
		if err := database.QueryRow(`SELECT COUNT(*) FROM library_root_operations`).Scan(&after); err != nil || after != before {
			t.Fatalf("malformed identifier changed operation records: before=%d after=%d err=%v", before, after, err)
		}
	})

	t.Run("replay after losing the admin role", func(t *testing.T) {
		formerAdmin := application.createUser(t, "admin")
		rootID := createRoot(t, application, formerAdmin, createLibraryDirectory(t, application, "former-admin"), "former-admin-create")
		body := map[string]any{"status": "disabled", "expected_revision": 1}
		key := map[string]string{"Idempotency-Key": "former-admin-disable"}
		first := application.request(t, http.MethodPatch, "/api/v1/library-roots/"+rootID, body, &formerAdmin, key)
		if first.Code != http.StatusOK {
			t.Fatalf("disable before losing the role: HTTP %d: %s", first.Code, first.Body.String())
		}
		if _, err := database.Exec(`UPDATE users SET role='user' WHERE id=$1::uuid`, formerAdmin.id); err != nil {
			t.Fatalf("demote administrator: %v", err)
		}
		replay := application.request(t, http.MethodPatch, "/api/v1/library-roots/"+rootID, body, &formerAdmin, key)
		requireAPIError(t, replay, http.StatusForbidden, "forbidden")
		if strings.Contains(replay.Body.String(), rootID) {
			t.Fatalf("forbidden replay disclosed the recorded result: %s", replay.Body.String())
		}
	})
}

func TestPostgreSQLRootOperationConcurrentRetriesConverge(t *testing.T) {
	application := newIntegrationTestApplication(t)
	database := application.database.connection
	admin := application.createUser(t, "admin")

	t.Run("create", func(t *testing.T) {
		path := createLibraryDirectory(t, application, "concurrent-create")
		responses := runOverlappingRootRequests(t, application, func() *httptest.ResponseRecorder {
			return application.request(t, http.MethodPost, "/api/v1/library-roots", map[string]string{"path": path}, &admin, map[string]string{"Idempotency-Key": "concurrent-create"})
		})
		for index, response := range responses {
			if response.Code != http.StatusCreated || !jsonResponsesEqual(t, response, responses[0]) {
				t.Fatalf("concurrent create %d: HTTP %d: %s", index, response.Code, response.Body.String())
			}
		}
		rootID := decodeJSONMap(t, responses[0])["id"].(string)
		assertRootState(t, database, rootID, "active", 1)
		assertOperationCount(t, database, rootID, 1)
	})

	t.Run("disable", func(t *testing.T) {
		rootID := createRoot(t, application, admin, createLibraryDirectory(t, application, "concurrent-disable"), "concurrent-disable-create")
		responses := runOverlappingRootRequests(t, application, func() *httptest.ResponseRecorder {
			return application.request(t, http.MethodPatch, "/api/v1/library-roots/"+rootID, map[string]any{"status": "disabled", "expected_revision": 1}, &admin, map[string]string{"Idempotency-Key": "concurrent-disable"})
		})
		for index, response := range responses {
			if response.Code != http.StatusOK || !jsonResponsesEqual(t, response, responses[0]) {
				t.Fatalf("concurrent disable %d: HTTP %d: %s", index, response.Code, response.Body.String())
			}
		}
		assertRootState(t, database, rootID, "disabled", 2)
		assertOperationCount(t, database, rootID, 2)
	})
}

func TestPostgreSQLRootOperationHistoryIsAdminOnlyAndRedacted(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin := application.createUser(t, "admin")
	ordinary := application.createUser(t, "user")
	fullPath := filepath.Join(application.allowedRoot, "private-library")
	if err := os.MkdirAll(fullPath, 0o755); err != nil {
		t.Fatalf("create library fixture: %v", err)
	}
	rootID := createRoot(t, application, admin, fullPath, "history-create")
	response := application.request(t, http.MethodGet, "/api/v1/library-root-operations", nil, &admin, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("read root operation history: HTTP %d: %s", response.Code, response.Body.String())
	}
	responseBody := response.Body.String()
	for _, secret := range []string{fullPath, admin.token, ordinary.token, "password_hash", "postgres"} {
		if strings.Contains(responseBody, secret) {
			t.Fatalf("operation history disclosed sensitive value %q: %s", secret, responseBody)
		}
	}
	if !strings.Contains(responseBody, rootID) || !strings.Contains(responseBody, `"operation":"create"`) {
		t.Fatalf("operation history omitted safe audit fields: %s", responseBody)
	}
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/library-root-operations", nil, &ordinary, nil), http.StatusForbidden, "forbidden")
}

func TestPostgreSQLUserTransactionProtectsLastAdmin(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin := application.createUser(t, "admin")
	secondAdmin := application.createUser(t, "admin")

	// Disabling one of two administrators succeeds and revokes its sessions atomically.
	response := application.request(t, http.MethodPatch, "/api/v1/users/"+secondAdmin.id,
		map[string]bool{"disabled": true}, &admin, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("disable second admin: HTTP %d: %s", response.Code, response.Body.String())
	}
	var revoked bool
	if err := application.database.connection.QueryRow(`SELECT revoked_at IS NOT NULL FROM sessions WHERE token_hash=$1`, hashSessionToken(secondAdmin.token)).Scan(&revoked); err != nil {
		t.Fatalf("read revoked session: %v", err)
	}
	if !revoked {
		t.Fatal("disabled administrator session was not revoked")
	}

	// The remaining administrator cannot be disabled, preserving the last-admin invariant.
	response = application.request(t, http.MethodPatch, "/api/v1/users/"+admin.id,
		map[string]bool{"disabled": true}, &admin, nil)
	requireAPIError(t, response, http.StatusConflict, "last_admin")
	var adminDisabled bool
	if err := application.database.connection.QueryRow(`SELECT disabled_at IS NOT NULL FROM users WHERE id=$1::uuid`, admin.id).Scan(&adminDisabled); err != nil {
		t.Fatalf("read remaining admin: %v", err)
	}
	if adminDisabled {
		t.Fatal("last administrator was disabled")
	}

	unknownID := uuid.New()
	response = application.request(t, http.MethodPatch, "/api/v1/users/"+unknownID,
		map[string]bool{"disabled": true}, &admin, nil)
	requireAPIError(t, response, http.StatusNotFound, "not_found")
}

func TestPostgreSQLSaveObservationCreatesSeparateDiscMedia(t *testing.T) {
	application := newIntegrationTestApplication(t)
	rootID := uuid.New()
	rootPath := filepath.Join(application.allowedRoot, "multi-disc")
	if err := os.MkdirAll(rootPath, 0o755); err != nil {
		t.Fatalf("create root fixture: %v", err)
	}
	if _, err := application.database.connection.Exec(`INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'active',1)`, rootID, rootPath); err != nil {
		t.Fatalf("insert root fixture: %v", err)
	}
	scanID := uuid.New()
	if _, err := application.database.connection.Exec(`INSERT INTO scan_runs(id,status,started_at) VALUES($1::uuid,'running',NOW())`, scanID); err != nil {
		t.Fatalf("insert scan fixture: %v", err)
	}
	root := registeredRoot{ID: rootID, Path: rootPath}
	roomusic := &roomusicApplication{config: serverConfig{DataDirectory: t.TempDir()}, database: application.database, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for _, disc := range []int{1, 2} {
		observation := audioObservation{Album: "Two Disc", Artist: "Artist", Title: fmt.Sprintf("Track %d", disc), TrackNumber: 1, DiscNumber: disc}
		if err := roomusic.saveObservation(context.Background(), scanID, root, fmt.Sprintf("disc%d/track.flac", disc), observation); err != nil {
			t.Fatalf("save disc %d observation: %v", disc, err)
		}
	}
	var mediaCount int
	if err := application.database.connection.QueryRow(`SELECT COUNT(*) FROM media m JOIN releases r ON r.id=m.release_id WHERE r.source_root_id=$1::uuid`, rootID).Scan(&mediaCount); err != nil {
		t.Fatalf("count media: %v", err)
	}
	if mediaCount != 2 {
		t.Fatalf("expected two media rows, got %d", mediaCount)
	}
}

func decodeJSONMap(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode JSON response: %v", err)
	}
	return result
}

func jsonResponsesEqual(t *testing.T, first, second *httptest.ResponseRecorder) bool {
	t.Helper()
	var firstValue, secondValue any
	if err := json.Unmarshal(first.Body.Bytes(), &firstValue); err != nil {
		t.Fatalf("decode first JSON response: %v", err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondValue); err != nil {
		t.Fatalf("decode second JSON response: %v", err)
	}
	return reflect.DeepEqual(firstValue, secondValue)
}

func createRoot(t *testing.T, application *integrationTestApplication, admin integrationTestUser, path, key string) string {
	t.Helper()
	response := application.request(t, http.MethodPost, "/api/v1/library-roots", map[string]string{"path": path}, &admin, map[string]string{"Idempotency-Key": key})
	if response.Code != http.StatusCreated {
		t.Fatalf("create root %q: HTTP %d: %s", path, response.Code, response.Body.String())
	}
	return decodeJSONMap(t, response)["id"].(string)
}

func createAndDisableRoot(t *testing.T, application *integrationTestApplication, admin integrationTestUser, path, keyPrefix string) string {
	t.Helper()
	rootID := createRoot(t, application, admin, path, keyPrefix+"-create")
	response := application.request(t, http.MethodPatch, "/api/v1/library-roots/"+rootID, map[string]any{"status": "disabled", "expected_revision": 1}, &admin, map[string]string{"Idempotency-Key": keyPrefix + "-disable"})
	if response.Code != http.StatusOK {
		t.Fatalf("disable root %q: HTTP %d: %s", path, response.Code, response.Body.String())
	}
	return rootID
}

func createLibraryDirectory(t *testing.T, application *integrationTestApplication, name string) string {
	t.Helper()
	path := filepath.Join(application.allowedRoot, name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("create library fixture %q: %v", name, err)
	}
	return path
}

const overlappingRootRequests = 8

// runOverlappingRootRequests 先让测试事务独占 library_roots，再并发发出同一个请求，
// 等所有请求都卡在数据库锁上才释放，因此这些请求一定真正重叠。
func runOverlappingRootRequests(t *testing.T, application *integrationTestApplication, send func() *httptest.ResponseRecorder) []*httptest.ResponseRecorder {
	t.Helper()
	holder, err := application.database.connection.Begin()
	if err != nil {
		t.Fatalf("begin lock holder: %v", err)
	}
	// 测试提前失败时也要放锁，否则 schema 清理会一直等待。
	t.Cleanup(func() { _ = holder.Rollback() })
	if _, err := holder.Exec(`LOCK TABLE library_roots IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("lock library_roots: %v", err)
	}
	responses := make(chan *httptest.ResponseRecorder, overlappingRootRequests)
	for i := 0; i < overlappingRootRequests; i++ {
		go func() { responses <- send() }()
	}
	waitForLockWaiters(t, application.database.connection, overlappingRootRequests)
	if err := holder.Rollback(); err != nil {
		t.Fatalf("release lock holder: %v", err)
	}
	results := make([]*httptest.ResponseRecorder, 0, overlappingRootRequests)
	timeout := time.After(15 * time.Second)
	for len(results) < overlappingRootRequests {
		select {
		case response := <-responses:
			results = append(results, response)
		case <-timeout:
			t.Fatalf("only %d of %d overlapping requests finished", len(results), overlappingRootRequests)
		}
	}
	return results
}

func waitForLockWaiters(t *testing.T, database *sql.DB, expected int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := database.QueryRow(`SELECT COUNT(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND pid<>pg_backend_pid()`).Scan(&waiting); err != nil {
			t.Fatalf("count lock waiters: %v", err)
		}
		if waiting >= expected {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected %d requests waiting on database locks, got %d\n%s", expected, waiting, describeDatabaseSessions(database))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// describeDatabaseSessions 返回当前库的会话快照，供并发测试超时时定位卡点。
func describeDatabaseSessions(database *sql.DB) string {
	rows, err := database.Query(`SELECT pid,state,COALESCE(wait_event_type,''),COALESCE(wait_event,''),LEFT(query,100) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() ORDER BY pid`)
	if err != nil {
		return fmt.Sprintf("read database sessions: %v", err)
	}
	defer rows.Close()
	var snapshot strings.Builder
	for rows.Next() {
		var pid int
		var state, waitType, waitEvent, query string
		if err := rows.Scan(&pid, &state, &waitType, &waitEvent, &query); err != nil {
			return fmt.Sprintf("scan database sessions: %v", err)
		}
		fmt.Fprintf(&snapshot, "pid=%d state=%s wait=%s/%s query=%q\n", pid, state, waitType, waitEvent, query)
	}
	return snapshot.String()
}

func assertRootState(t *testing.T, database *sql.DB, rootID, expectedStatus string, expectedRevision int64) {
	t.Helper()
	var status string
	var revision int64
	if err := database.QueryRow(`SELECT status,revision FROM library_roots WHERE id=$1::uuid`, rootID).Scan(&status, &revision); err != nil {
		t.Fatalf("read root state: %v", err)
	}
	if status != expectedStatus || revision != expectedRevision {
		t.Fatalf("expected root state %s/%d, got %s/%d", expectedStatus, expectedRevision, status, revision)
	}
}

func assertOperationCount(t *testing.T, database *sql.DB, rootID string, expected int) {
	t.Helper()
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM library_root_operations WHERE root_id=$1::uuid`, rootID).Scan(&count); err != nil {
		t.Fatalf("count root operations: %v", err)
	}
	if count != expected {
		t.Fatalf("expected %d root operations, got %d", expected, count)
	}
}

func assertOperationState(t *testing.T, database *sql.DB, rootID, operation string, expectedRevision, resultRevision int64, beforeStatus, afterStatus string) {
	t.Helper()
	var actualExpected, actualResult int64
	var actualBefore, actualAfter string
	err := database.QueryRow(`SELECT expected_revision,result_revision,before_state->>'status',after_state->>'status' FROM library_root_operations WHERE root_id=$1::uuid AND operation_type=$2`, rootID, operation).Scan(&actualExpected, &actualResult, &actualBefore, &actualAfter)
	if err != nil {
		t.Fatalf("read %s operation state: %v", operation, err)
	}
	if actualExpected != expectedRevision || actualResult != resultRevision || actualBefore != beforeStatus || actualAfter != afterStatus {
		t.Fatalf("unexpected %s operation state: revisions %d/%d, statuses %s/%s", operation, actualExpected, actualResult, actualBefore, actualAfter)
	}
}

func TestPostgreSQLScanCancellationIsPersistentAndIdempotent(t *testing.T) {
	roomusic := newIntegrationTestApplication(t)
	admin := roomusic.createUser(t, "admin")
	scanID := uuid.New()
	if _, err := roomusic.database.connection.Exec(`INSERT INTO scan_runs(id,status,started_at) VALUES($1::uuid,'running',NOW())`, scanID); err != nil {
		t.Fatalf("insert running scan: %v", err)
	}

	first := roomusic.request(t, http.MethodPost, "/api/v1/scans/"+scanID+"/cancel", map[string]any{}, &admin, nil)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first cancel: HTTP %d: %s", first.Code, first.Body.String())
	}
	var firstBody scanDTO
	if err := json.Unmarshal(first.Body.Bytes(), &firstBody); err != nil {
		t.Fatalf("decode first cancel: %v", err)
	}
	if firstBody.CancelRequestedAt == nil || firstBody.Status != "running" {
		t.Fatalf("unexpected first cancel response: %+v", firstBody)
	}

	second := roomusic.request(t, http.MethodPost, "/api/v1/scans/"+scanID+"/cancel", map[string]any{}, &admin, nil)
	if second.Code != http.StatusAccepted {
		t.Fatalf("second cancel: HTTP %d: %s", second.Code, second.Body.String())
	}
	var secondBody scanDTO
	if err := json.Unmarshal(second.Body.Bytes(), &secondBody); err != nil {
		t.Fatalf("decode second cancel: %v", err)
	}
	if secondBody.CancelRequestedAt == nil || !secondBody.CancelRequestedAt.Equal(*firstBody.CancelRequestedAt) {
		t.Fatalf("cancel timestamp changed: first=%v second=%v", firstBody.CancelRequestedAt, secondBody.CancelRequestedAt)
	}

	active := roomusic.request(t, http.MethodGet, "/api/v1/scans/active", nil, &admin, nil)
	if active.Code != http.StatusOK || !strings.Contains(active.Body.String(), scanID) {
		t.Fatalf("active scan response: HTTP %d: %s", active.Code, active.Body.String())
	}
}

func TestPostgreSQLScanRecoverySkipsLiveHolder(t *testing.T) {
	roomusic := newIntegrationTestApplication(t)
	scanID := uuid.New()
	if _, err := roomusic.database.connection.Exec(`INSERT INTO scan_runs(id,status,started_at) VALUES($1::uuid,'running',NOW())`, scanID); err != nil {
		t.Fatalf("insert running scan: %v", err)
	}
	holder, acquired, err := acquireScanExecution(context.Background(), roomusic.database.connection)
	if err != nil || !acquired {
		t.Fatalf("acquire live holder: acquired=%v err=%v", acquired, err)
	}

	if err := recoverInterruptedScans(context.Background(), roomusic.database.connection); err != nil {
		t.Fatalf("recover while holder is live: %v", err)
	}
	var status string
	if err := roomusic.database.connection.QueryRow(`SELECT status FROM scan_runs WHERE id=$1::uuid`, scanID).Scan(&status); err != nil {
		t.Fatalf("query scan status: %v", err)
	}
	if status != "running" {
		t.Fatalf("live scan was recovered as %q", status)
	}

	if err := holder.close(context.Background()); err != nil {
		t.Fatalf("release live holder: %v", err)
	}
	if err := recoverInterruptedScans(context.Background(), roomusic.database.connection); err != nil {
		t.Fatalf("recover stale scan: %v", err)
	}
	if err := roomusic.database.connection.QueryRow(`SELECT status FROM scan_runs WHERE id=$1::uuid`, scanID).Scan(&status); err != nil {
		t.Fatalf("query recovered scan status: %v", err)
	}
	if status != "incomplete" {
		t.Fatalf("stale scan status = %q, want incomplete", status)
	}
}

func TestPostgreSQLCanceledScanDoesNotReconcileMissingSources(t *testing.T) {
	application := newIntegrationTestApplication(t)
	rootID := uuid.New()
	rootPath := filepath.Join(application.allowedRoot, "cancel-safe")
	if err := os.MkdirAll(rootPath, 0o755); err != nil {
		t.Fatalf("create root fixture: %v", err)
	}
	if _, err := application.database.connection.Exec(`INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'active',1)`, rootID, rootPath); err != nil {
		t.Fatalf("insert root fixture: %v", err)
	}
	scanID := uuid.New()
	if _, err := application.database.connection.Exec(`INSERT INTO scan_runs(id,status,started_at,cancel_requested_at) VALUES($1::uuid,'running',NOW(),NOW())`, scanID); err != nil {
		t.Fatalf("insert canceled scan fixture: %v", err)
	}
	roomusic := &roomusicApplication{config: serverConfig{DataDirectory: t.TempDir()}, database: application.database, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	root := registeredRoot{ID: rootID, Path: rootPath}
	observation := audioObservation{Album: "Album", Artist: "Artist", Title: "Track", TrackNumber: 1, DiscNumber: 1}
	if err := roomusic.saveObservation(context.Background(), scanID, root, "track.flac", observation); err != nil {
		t.Fatalf("save observation: %v", err)
	}
	if _, err := application.database.connection.Exec(`UPDATE tracks SET observed_at=NOW()-INTERVAL '1 hour' WHERE source_root_id=$1::uuid`, rootID); err != nil {
		t.Fatalf("age observation: %v", err)
	}
	holder, acquired, err := acquireScanExecution(context.Background(), application.database.connection)
	if err != nil || !acquired {
		t.Fatalf("acquire scan holder: acquired=%v err=%v", acquired, err)
	}
	defer holder.close(context.Background())
	status, err := roomusic.finalizeScan(context.Background(), holder.connection, scanID, []string{rootID}, scanOutcome{Complete: true})
	if err != nil {
		t.Fatalf("finalize canceled scan: %v", err)
	}
	if status != "canceled" {
		t.Fatalf("terminal status = %q, want canceled", status)
	}
	var sourceStatus string
	if err := application.database.connection.QueryRow(`SELECT source_status FROM tracks WHERE source_root_id=$1::uuid`, rootID).Scan(&sourceStatus); err != nil {
		t.Fatalf("query source status: %v", err)
	}
	if sourceStatus != "present" {
		t.Fatalf("canceled scan marked source %q", sourceStatus)
	}
}
