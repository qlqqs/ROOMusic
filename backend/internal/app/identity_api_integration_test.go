package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

const identityTestPassword = "identity-test-password"

func accountBody(username, password string) map[string]string {
	return map[string]string{"username": username, "password": password}
}

func mustExec(t *testing.T, application *integrationTestApplication, statement string, arguments ...any) {
	t.Helper()
	if _, err := application.database.Exec(statement, arguments...); err != nil {
		t.Fatalf("execute %q: %v", statement, err)
	}
}

func countRows(t *testing.T, application *integrationTestApplication, query string, arguments ...any) int {
	t.Helper()
	var count int
	if err := application.database.QueryRow(query, arguments...).Scan(&count); err != nil {
		t.Fatalf("count rows with %q: %v", query, err)
	}
	return count
}

// installRejectingInsertTrigger 让满足 condition 的 INSERT 以 SQLSTATE P0001 失败。它不是唯一约束冲突（23505），
// 用来注入“其他数据库错误”，与 TestPostgreSQLRootOperationTransactionsAndConflicts 里的做法相同。
func installRejectingInsertTrigger(t *testing.T, application *integrationTestApplication, table, condition string) {
	t.Helper()
	name := "reject_" + table + "_insert"
	mustExec(t, application, fmt.Sprintf(`CREATE FUNCTION %[1]s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF %[3]s THEN RAISE EXCEPTION 'test rejection'; END IF; RETURN NEW; END $$;
CREATE TRIGGER %[1]s BEFORE INSERT ON %[2]s FOR EACH ROW EXECUTE FUNCTION %[1]s()`, name, table, condition))
}

func sessionTokenFrom(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == identity.SessionCookieName && cookie.Value != "" {
			return cookie.Value
		}
	}
	t.Fatalf("response did not set the session cookie: %s", response.Body.String())
	return ""
}

// requireIdentityBody 断言响应恰好是 {id, username, role}，id 是小写 UUID，并返回 id。
func requireIdentityBody(t *testing.T, response *httptest.ResponseRecorder, status int, username, role string) string {
	t.Helper()
	if response.Code != status {
		t.Fatalf("expected HTTP %d, got %d: %s", status, response.Code, response.Body.String())
	}
	body := decodeJSONMap(t, response)
	id, _ := body["id"].(string)
	if !uuid.Valid(id) || id != strings.ToLower(id) {
		t.Fatalf("response id %q is not a lowercase UUID: %s", id, response.Body.String())
	}
	if body["username"] != username || body["role"] != role || len(body) != 3 {
		t.Fatalf("unexpected identity body: %s", response.Body.String())
	}
	return id
}

func apiErrorMessage(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope httpserver.APIError
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode API error: %v: %s", err, response.Body.String())
	}
	return envelope.Error.Message
}

func TestPostgreSQLIdentityResponsesCarryUserID(t *testing.T) {
	application := newIntegrationTestApplication(t)

	setup := application.request(t, http.MethodPost, "/api/v1/setup/admin", accountBody("curator", identityTestPassword), nil, nil)
	adminID := requireIdentityBody(t, setup, http.StatusCreated, "curator", "admin")
	var storedID string
	if err := application.database.QueryRow(`SELECT id::text FROM users WHERE username='curator'`).Scan(&storedID); err != nil || storedID != adminID {
		t.Fatalf("setup response id %q does not match the stored user %q (%v)", adminID, storedID, err)
	}

	login := application.request(t, http.MethodPost, "/api/v1/auth/login", accountBody("curator", identityTestPassword), nil, nil)
	if got := requireIdentityBody(t, login, http.StatusOK, "curator", "admin"); got != adminID {
		t.Fatalf("login id %q, want %q", got, adminID)
	}
	admin := &integrationTestUser{id: adminID, token: sessionTokenFrom(t, login)}
	me := application.request(t, http.MethodGet, "/api/v1/auth/me", nil, admin, nil)
	if got := requireIdentityBody(t, me, http.StatusOK, "curator", "admin"); got != adminID {
		t.Fatalf("me id %q, want %q", got, adminID)
	}

	created := application.request(t, http.MethodPost, "/api/v1/users", accountBody("listener", identityTestPassword), admin, nil)
	memberID := requireIdentityBody(t, created, http.StatusCreated, "listener", "user")
	listing := application.request(t, http.MethodGet, "/api/v1/users", nil, admin, nil)
	var users struct {
		Items []struct{ ID, Username string } `json:"items"`
	}
	if err := json.Unmarshal(listing.Body.Bytes(), &users); err != nil {
		t.Fatalf("decode users: %v: %s", err, listing.Body.String())
	}
	listed := map[string]string{}
	for _, item := range users.Items {
		listed[item.Username] = item.ID
	}
	if listed["listener"] != memberID || listed["curator"] != adminID {
		t.Fatalf("user list ids %v do not match the responses (%s, %s)", listed, memberID, adminID)
	}

	memberLogin := application.request(t, http.MethodPost, "/api/v1/auth/login", accountBody("listener", identityTestPassword), nil, nil)
	if got := requireIdentityBody(t, memberLogin, http.StatusOK, "listener", "user"); got != memberID {
		t.Fatalf("member login id %q, want %q", got, memberID)
	}
	member := &integrationTestUser{id: memberID, token: sessionTokenFrom(t, memberLogin)}
	memberMe := application.request(t, http.MethodGet, "/api/v1/auth/me", nil, member, nil)
	if got := requireIdentityBody(t, memberMe, http.StatusOK, "listener", "user"); got != memberID {
		t.Fatalf("member me id %q, want %q", got, memberID)
	}
}

func TestPostgreSQLSetupAdminMapsOnlyUniqueViolationsToSetupClosed(t *testing.T) {
	setup := func(t *testing.T, application *integrationTestApplication, password string) *httptest.ResponseRecorder {
		t.Helper()
		return application.request(t, http.MethodPost, "/api/v1/setup/admin", accountBody("curator", password), nil, nil)
	}
	requireNothingWritten := func(t *testing.T, application *integrationTestApplication) {
		t.Helper()
		if users, markers := countRows(t, application, `SELECT COUNT(*) FROM users WHERE username='curator'`), countRows(t, application, `SELECT COUNT(*) FROM setup_state`); users != 0 || markers != 0 {
			t.Fatalf("failed setup left %d administrators and %d setup markers behind", users, markers)
		}
	}

	t.Run("username already used", func(t *testing.T) {
		application := newIntegrationTestApplication(t)
		mustExec(t, application, `INSERT INTO users(id,username,password_hash,role) VALUES($1::uuid,'curator','test-only','user')`, uuid.New())
		requireAPIError(t, setup(t, application, identityTestPassword), http.StatusConflict, "setup_closed")
		if markers := countRows(t, application, `SELECT COUNT(*) FROM setup_state`); markers != 0 {
			t.Fatalf("conflicting setup wrote %d setup markers", markers)
		}
	})

	t.Run("another setup wins before the marker is written", func(t *testing.T) {
		application := newIntegrationTestApplication(t)
		// 用户写入后立刻由触发器写入初始化标记，模拟并发的另一次初始化抢先完成：随后的标记写入撞上主键。
		mustExec(t, application, `CREATE FUNCTION mark_setup_completed() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN INSERT INTO setup_state (completed_at) VALUES (NOW()); RETURN NEW; END $$;
CREATE TRIGGER mark_setup_completed AFTER INSERT ON users FOR EACH ROW EXECUTE FUNCTION mark_setup_completed()`)
		requireAPIError(t, setup(t, application, identityTestPassword), http.StatusConflict, "setup_closed")
		requireNothingWritten(t, application)
	})

	t.Run("user insert fails for another reason", func(t *testing.T) {
		application := newIntegrationTestApplication(t)
		installRejectingInsertTrigger(t, application, "users", "TRUE")
		requireAPIError(t, setup(t, application, identityTestPassword), http.StatusServiceUnavailable, "database_unavailable")
		requireNothingWritten(t, application)
	})

	t.Run("marker insert fails for another reason", func(t *testing.T) {
		application := newIntegrationTestApplication(t)
		installRejectingInsertTrigger(t, application, "setup_state", "TRUE")
		requireAPIError(t, setup(t, application, identityTestPassword), http.StatusServiceUnavailable, "database_unavailable")
		requireNothingWritten(t, application)
	})

	t.Run("setup state unreadable", func(t *testing.T) {
		application := newIntegrationTestApplication(t)
		mustExec(t, application, `ALTER TABLE setup_state RENAME TO setup_state_hidden`)
		requireAPIError(t, setup(t, application, identityTestPassword), http.StatusServiceUnavailable, "database_unavailable")
		if users := countRows(t, application, `SELECT COUNT(*) FROM users`); users != 0 {
			t.Fatalf("setup with an unreadable state wrote %d users", users)
		}
	})

	t.Run("input bounds", func(t *testing.T) {
		application := newIntegrationTestApplication(t)
		for _, testCase := range []struct {
			name     string
			username string
			password string
		}{
			{"11 ASCII characters", "curator", strings.Repeat("a", 11)},
			{"73 ASCII characters", "curator", strings.Repeat("a", 73)},
			{"Chinese", "curator", strings.Repeat("字", 4)},
			{"fullwidth", "curator", "abcdefghijkＡ"},
			{"emoji", "curator", "abcdefghijk😀"},
			{"tab", "curator", "abcdefghijk\t"},
			{"newline", "curator", "abcdefghijk\n"},
			{"blank username", "  ", identityTestPassword},
		} {
			response := application.request(t, http.MethodPost, "/api/v1/setup/admin", accountBody(testCase.username, testCase.password), nil, nil)
			requireAPIError(t, response, http.StatusBadRequest, "invalid_input")
			if message := apiErrorMessage(t, response); message != "用户名不能为空，密码需要 12 到 72 位英文字母、数字或符号" {
				t.Fatalf("%s: unexpected message %q", testCase.name, message)
			}
		}
		// 非对象的请求体：只写出一个 invalid_json 信封，不再追加第二个 invalid_input 信封。
		requireAPIError(t, application.request(t, http.MethodPost, "/api/v1/setup/admin", "not an object", nil, nil), http.StatusBadRequest, "invalid_json")
		requireNothingWritten(t, application)

		// 恰好 72 位 ASCII 的密码可以完成初始化，之后用同一个密码登录。
		longPassword := strings.Repeat("a", 72)
		requireIdentityBody(t, setup(t, application, longPassword), http.StatusCreated, "curator", "admin")
		login := application.request(t, http.MethodPost, "/api/v1/auth/login", accountBody("curator", longPassword), nil, nil)
		requireIdentityBody(t, login, http.StatusOK, "curator", "admin")
	})
}

func TestPostgreSQLCreateUserMapsOnlyUniqueViolationsToUsernameTaken(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin := application.createUser(t, "admin")
	create := func(username, password string) *httptest.ResponseRecorder {
		return application.request(t, http.MethodPost, "/api/v1/users", accountBody(username, password), &admin, nil)
	}

	requireIdentityBody(t, create("listener", identityTestPassword), http.StatusCreated, "listener", "user")
	requireAPIError(t, create("listener", identityTestPassword), http.StatusConflict, "username_taken")
	if count := countRows(t, application, `SELECT COUNT(*) FROM users WHERE username='listener'`); count != 1 {
		t.Fatalf("expected one listener after the conflict, got %d", count)
	}

	// 其他数据库错误不是“用户名已存在”：返回 503，且没有留下用户；触发器只拦截这一个用户名。
	installRejectingInsertTrigger(t, application, "users", "NEW.username = 'blocked-by-trigger'")
	blocked := create("blocked-by-trigger", identityTestPassword)
	requireAPIError(t, blocked, http.StatusServiceUnavailable, "database_unavailable")
	if message := apiErrorMessage(t, blocked); message != "无法创建用户" {
		t.Fatalf("unexpected message %q", message)
	}
	if count := countRows(t, application, `SELECT COUNT(*) FROM users WHERE username='blocked-by-trigger'`); count != 0 {
		t.Fatalf("failed insert left %d users behind", count)
	}
	requireIdentityBody(t, create("unaffected", identityTestPassword), http.StatusCreated, "unaffected", "user")

	// 密码只允许 12–72 位可打印 ASCII（含空格），其余返回 400；通过的密码都能登录。
	for index, testCase := range []struct {
		name     string
		password string
		status   int
	}{
		{"11 ASCII characters", strings.Repeat("a", 11), http.StatusBadRequest},
		{"12 ASCII characters", strings.Repeat("a", 12), http.StatusCreated},
		{"72 ASCII characters", strings.Repeat("a", 72), http.StatusCreated},
		{"73 ASCII characters", strings.Repeat("a", 73), http.StatusBadRequest},
		{"Chinese", strings.Repeat("字", 4), http.StatusBadRequest},
		{"Chinese at 72 bytes", strings.Repeat("字", 24), http.StatusBadRequest},
		{"fullwidth", "abcdefghijkＡ", http.StatusBadRequest},
		{"emoji", "abcdefghijk😀", http.StatusBadRequest},
		{"tab", "abcdefghijk\t", http.StatusBadRequest},
		{"newline", "abcdefghijk\n", http.StatusBadRequest},
		{"spaces and symbols", " Abc123!@#~  ", http.StatusCreated},
	} {
		username := fmt.Sprintf("bounds-%d", index)
		response := create(username, testCase.password)
		if testCase.status == http.StatusBadRequest {
			requireAPIError(t, response, http.StatusBadRequest, "invalid_input")
			if message := apiErrorMessage(t, response); message != "用户名不能为空，密码需要 12 到 72 位英文字母、数字或符号" {
				t.Fatalf("%s: unexpected message %q", testCase.name, message)
			}
			if count := countRows(t, application, `SELECT COUNT(*) FROM users WHERE username=$1`, username); count != 0 {
				t.Fatalf("%s: invalid password left %d users behind", testCase.name, count)
			}
			continue
		}
		requireIdentityBody(t, response, http.StatusCreated, username, "user")
		login := application.request(t, http.MethodPost, "/api/v1/auth/login", accountBody(username, testCase.password), nil, nil)
		requireIdentityBody(t, login, http.StatusOK, username, "user")
	}
	requireAPIError(t, create("  ", identityTestPassword), http.StatusBadRequest, "invalid_input")
	// 非对象的请求体：只写出一个 invalid_json 信封。
	requireAPIError(t, application.request(t, http.MethodPost, "/api/v1/users", "not an object", &admin, nil), http.StatusBadRequest, "invalid_json")
}

func TestPostgreSQLUserRoutesRejectMalformedAndUnknownIDs(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin := application.createUser(t, "admin")
	ordinary := application.createUser(t, "user")
	target := application.createUser(t, "user")
	disable := map[string]bool{"disabled": true}

	// 先鉴权再校验标识：未登录 401、普通用户 403，不因畸形标识改变。
	requireAPIError(t, application.request(t, http.MethodPatch, "/api/v1/users/not-a-uuid", disable, nil, nil), http.StatusUnauthorized, "unauthorized")
	requireAPIError(t, application.request(t, http.MethodPatch, "/api/v1/users/not-a-uuid", disable, &ordinary, nil), http.StatusForbidden, "forbidden")
	requireAPIError(t, application.request(t, http.MethodPost, "/api/v1/users/not-a-uuid/sessions/revoke", map[string]any{}, &ordinary, nil), http.StatusForbidden, "forbidden")

	for _, malformed := range []string{"not-a-uuid", "0123456789abcdef0123456789abcdef", "01234567-89ab-cdef-0123-456789abcdeg"} {
		patch := application.request(t, http.MethodPatch, "/api/v1/users/"+malformed, disable, &admin, nil)
		requireAPIError(t, patch, http.StatusBadRequest, "invalid_id")
		if message := apiErrorMessage(t, patch); message != "用户标识无效" {
			t.Fatalf("unexpected message %q", message)
		}
		requireAPIError(t, application.request(t, http.MethodPost, "/api/v1/users/"+malformed+"/sessions/revoke", map[string]any{}, &admin, nil), http.StatusBadRequest, "invalid_id")
	}

	// 撤销未知用户的会话是 404，不再返回 {revoked:true}。
	unknownID := uuid.New()
	revoke := application.request(t, http.MethodPost, "/api/v1/users/"+unknownID+"/sessions/revoke", map[string]any{}, &admin, nil)
	requireAPIError(t, revoke, http.StatusNotFound, "not_found")
	if message := apiErrorMessage(t, revoke); message != "用户不存在" {
		t.Fatalf("unexpected message %q", message)
	}
	requireAPIError(t, application.request(t, http.MethodPatch, "/api/v1/users/"+unknownID, disable, &admin, nil), http.StatusNotFound, "not_found")

	// 已存在的用户：大写的 UUID 同样有效，撤销后该用户的会话立即失效。
	revoked := application.request(t, http.MethodPost, "/api/v1/users/"+strings.ToUpper(target.id)+"/sessions/revoke", map[string]any{}, &admin, nil)
	if revoked.Code != http.StatusOK || decodeJSONMap(t, revoked)["revoked"] != true {
		t.Fatalf("revoke existing user: HTTP %d: %s", revoked.Code, revoked.Body.String())
	}
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/auth/me", nil, &target, nil), http.StatusUnauthorized, "unauthorized")

	// 请求体问题：缺少 disabled 是 invalid_input，非对象是 invalid_json（只写出一个信封）。
	requireAPIError(t, application.request(t, http.MethodPatch, "/api/v1/users/"+ordinary.id, map[string]any{}, &admin, nil), http.StatusBadRequest, "invalid_input")
	requireAPIError(t, application.request(t, http.MethodPatch, "/api/v1/users/"+ordinary.id, "not an object", &admin, nil), http.StatusBadRequest, "invalid_json")
}

func TestPostgreSQLListUsersFailsLoudlyOnUnreadableRows(t *testing.T) {
	application := newIntegrationTestApplication(t)
	admin := application.createUser(t, "admin")
	requireStatus := func(want int) {
		t.Helper()
		response := application.request(t, http.MethodGet, "/api/v1/users", nil, &admin, nil)
		if response.Code != want {
			t.Fatalf("list users: expected HTTP %d, got %d: %s", want, response.Code, response.Body.String())
		}
		if want == http.StatusServiceUnavailable {
			requireAPIError(t, response, want, "database_unavailable")
		}
	}
	requireStatus(http.StatusOK)

	// 测试库里放宽 username 的 NOT NULL，写入空值，使该行无法读成字符串。以前这一行被静默跳过，列表会少人而不报错。
	mustExec(t, application, `ALTER TABLE users ALTER COLUMN username DROP NOT NULL`)
	mustExec(t, application, `INSERT INTO users(id,username,password_hash,role) VALUES($1::uuid,NULL,'test-only','user')`, uuid.New())
	requireStatus(http.StatusServiceUnavailable)
}

func TestPostgreSQLUnauthenticatedMessageIsRoleNeutral(t *testing.T) {
	application := newIntegrationTestApplication(t)
	ordinary := application.createUser(t, "user")
	mustExec(t, application, `UPDATE sessions SET revoked_at=NOW() WHERE user_id=$1::uuid`, ordinary.id)

	for _, testCase := range []struct {
		name string
		user *integrationTestUser
		path string
	}{
		{"no session on a protected route", nil, "/api/v1/users"},
		{"no session on me", nil, "/api/v1/auth/me"},
		{"revoked ordinary user session", &ordinary, "/api/v1/auth/me"},
		{"revoked ordinary user session on a catalog route", &ordinary, "/api/v1/releases"},
	} {
		response := application.request(t, http.MethodGet, testCase.path, nil, testCase.user, nil)
		requireAPIError(t, response, http.StatusUnauthorized, "unauthorized")
		if message := apiErrorMessage(t, response); message != "需要登录" {
			t.Fatalf("%s: message %q names a role or differs from the neutral text", testCase.name, message)
		}
	}
}
