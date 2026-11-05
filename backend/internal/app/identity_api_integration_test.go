package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	identityapi "github.com/qlqq/roomusic/backend/internal/identity/httpapi"
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

// passwordEnvironment 通过真实身份接口准备两份成员会话与一份管理员会话。
type passwordEnvironment struct {
	application           *integrationTestApplication
	admin, member, second integrationTestUser
}

func newPasswordEnvironment(t *testing.T) *passwordEnvironment {
	t.Helper()
	a := newIntegrationTestApplication(t)
	setup := a.request(t, http.MethodPost, "/api/v1/setup/admin", accountBody("curator", identityTestPassword), nil, nil)
	requireIdentityBody(t, setup, 201, "curator", "admin")
	admin := loginPasswordUser(t, a, "curator", identityTestPassword, "admin")
	created := a.request(t, http.MethodPost, "/api/v1/users", accountBody("listener", identityTestPassword), &admin, nil)
	requireIdentityBody(t, created, 201, "listener", "user")
	member := loginPasswordUser(t, a, "listener", identityTestPassword, "user")
	second := loginPasswordUser(t, a, "listener", identityTestPassword, "user")
	return &passwordEnvironment{application: a, admin: admin, member: member, second: second}
}

func loginPasswordUser(t *testing.T, a *integrationTestApplication, username, password, role string) integrationTestUser {
	t.Helper()
	response := a.request(t, http.MethodPost, "/api/v1/auth/login", accountBody(username, password), nil, nil)
	return integrationTestUser{id: requireIdentityBody(t, response, 200, username, role), token: sessionTokenFrom(t, response)}
}

func ownPasswordBody(current, next string) map[string]string {
	return map[string]string{"current_password": current, "new_password": next}
}

func (e *passwordEnvironment) change(t *testing.T, current, next string) *httptest.ResponseRecorder {
	t.Helper()
	return e.application.request(t, http.MethodPost, "/api/v1/auth/password", ownPasswordBody(current, next), &e.member, nil)
}

func requirePasswordSuccess(t *testing.T, response *httptest.ResponseRecorder, field string) {
	t.Helper()
	if response.Code != 200 || !reflect.DeepEqual(decodeJSONMap(t, response), map[string]any{field: true}) {
		t.Fatalf("密码接口响应不符：HTTP %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/json" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("密码接口响应头不符：%v", response.Header())
	}
	if cookies := response.Header().Values("Set-Cookie"); len(cookies) != 0 {
		t.Fatalf("密码接口不应重设 Cookie：%v", cookies)
	}
}

func requirePasswordSession(t *testing.T, a *integrationTestApplication, user *integrationTestUser, status int) {
	t.Helper()
	response := a.request(t, http.MethodGet, "/api/v1/auth/me", nil, user, nil)
	if status == 401 {
		requireAPIError(t, response, 401, "unauthorized")
	} else if response.Code != status {
		t.Fatalf("会话状态：HTTP %d %s，期望 %d", response.Code, response.Body.String(), status)
	}
}

// passwordDatabaseState 比较所有身份行与会话行，包含哈希、过期时间和撤销时间，不只比较数量。
func passwordDatabaseState(t *testing.T, a *integrationTestApplication) string {
	t.Helper()
	var state string
	if err := a.database.QueryRow(`SELECT jsonb_build_object(
		'users',(SELECT COALESCE(jsonb_agg(to_jsonb(u) ORDER BY u.id),'[]'::jsonb) FROM users u),
		'sessions',(SELECT COALESCE(jsonb_agg(to_jsonb(s) ORDER BY s.token_hash),'[]'::jsonb) FROM sessions s),
		'operations',(SELECT count(*) FROM operations))::text`).Scan(&state); err != nil {
		t.Fatalf("读取身份快照：%v", err)
	}
	return state
}

func requirePasswordStateUnchanged(t *testing.T, a *integrationTestApplication, before string) {
	t.Helper()
	if passwordDatabaseState(t, a) != before {
		t.Fatal("失败的密码操作改变了用户、会话或操作记录")
	}
}

func installRejectingPasswordSessionTrigger(t *testing.T, a *integrationTestApplication) {
	t.Helper()
	mustExec(t, a, `CREATE FUNCTION reject_password_session_update() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN RAISE EXCEPTION 'password test rejection' USING ERRCODE='P0001'; END $$;
	CREATE TRIGGER reject_password_session_update BEFORE UPDATE ON sessions
	FOR EACH ROW EXECUTE FUNCTION reject_password_session_update()`)
}

func TestPostgreSQLChangeOwnPasswordKeepsCurrentSessionAndRevokesOthers(t *testing.T) {
	e := newPasswordEnvironment(t)
	a := e.application
	var expiresBefore, expiresAfter time.Time
	if err := a.database.QueryRow(`SELECT expires_at FROM sessions WHERE token_hash=$1`, identity.HashSessionToken(e.member.token)).Scan(&expiresBefore); err != nil {
		t.Fatal(err)
	}
	var adminBefore string
	if err := a.database.QueryRow(`SELECT row_to_json(s)::text FROM sessions s WHERE token_hash=$1`, identity.HashSessionToken(e.admin.token)).Scan(&adminBefore); err != nil {
		t.Fatal(err)
	}
	next := "changed-member-password"
	requirePasswordSuccess(t, e.change(t, identityTestPassword, next), "password_changed")
	requirePasswordSession(t, a, &e.member, 200)
	requirePasswordSession(t, a, &e.second, 401)
	requirePasswordSession(t, a, &e.admin, 200)
	if err := a.database.QueryRow(`SELECT expires_at FROM sessions WHERE token_hash=$1`, identity.HashSessionToken(e.member.token)).Scan(&expiresAfter); err != nil || !expiresAfter.Equal(expiresBefore) {
		t.Fatalf("当前会话到期时间发生变化：%v", err)
	}
	var adminAfter string
	if err := a.database.QueryRow(`SELECT row_to_json(s)::text FROM sessions s WHERE token_hash=$1`, identity.HashSessionToken(e.admin.token)).Scan(&adminAfter); err != nil || adminBefore != adminAfter {
		t.Fatalf("其他账号会话发生变化：%v", err)
	}
	requireAPIError(t, a.request(t, http.MethodPost, "/api/v1/auth/login", accountBody("listener", identityTestPassword), nil, nil), 401, "invalid_credentials")
	loginPasswordUser(t, a, "listener", next, "user")
	if countRows(t, a, `SELECT count(*) FROM operations`) != 0 {
		t.Fatal("密码操作不应写入治理记录")
	}
}

func TestPostgreSQLChangeOwnPasswordRejectsWrongCurrentPassword(t *testing.T) {
	e := newPasswordEnvironment(t)
	before := passwordDatabaseState(t, e.application)
	response := e.change(t, "wrong-current-password", "changed-member-password")
	requireAPIError(t, response, 403, "current_password_incorrect")
	if message := apiErrorMessage(t, response); message != "当前密码不正确" {
		t.Fatalf("当前密码错误提示：%q", message)
	}
	requirePasswordStateUnchanged(t, e.application, before)
	requirePasswordSession(t, e.application, &e.member, 200)
	requirePasswordSession(t, e.application, &e.second, 200)
	loginPasswordUser(t, e.application, "listener", identityTestPassword, "user")
}

func TestPostgreSQLChangeOwnPasswordValidatesNewPassword(t *testing.T) {
	e := newPasswordEnvironment(t)
	before := passwordDatabaseState(t, e.application)
	for _, tc := range []struct{ name, next string }{
		{"11位", strings.Repeat("a", 11)}, {"73位", strings.Repeat("a", 73)},
		{"中文", "abcdefghijk中"}, {"制表符", "abcdefghijk\t"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireAPIError(t, e.change(t, identityTestPassword, tc.next), 400, "invalid_input")
			requirePasswordStateUnchanged(t, e.application, before)
		})
	}
	// 两项都错时必须先拒绝新密码，不把它误报为当前密码不正确。
	requireAPIError(t, e.change(t, "wrong", "short"), 400, "invalid_input")
	for _, body := range []any{"not an object", nil, []string{"array"}} {
		response := e.application.request(t, http.MethodPost, "/api/v1/auth/password", body, &e.member, nil)
		requireAPIError(t, response, 400, "invalid_json")
		decoder := json.NewDecoder(strings.NewReader(response.Body.String()))
		var envelope any
		if err := decoder.Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		if err := decoder.Decode(&envelope); err != io.EOF {
			t.Fatalf("非法 JSON 响应不是单个信封：%v", err)
		}
		requirePasswordStateUnchanged(t, e.application, before)
	}
	requirePasswordSession(t, e.application, &e.member, 200)
	requirePasswordSession(t, e.application, &e.second, 200)
	current := identityTestPassword
	for _, next := range []string{strings.Repeat("a", 12), strings.Repeat("b", 72)} {
		requirePasswordSuccess(t, e.change(t, current, next), "password_changed")
		loginPasswordUser(t, e.application, "listener", next, "user")
		current = next
	}
	// 与当前密码相同也允许，仍保留当前会话、撤销其他设备。
	requirePasswordSuccess(t, e.change(t, current, current), "password_changed")
	requirePasswordSession(t, e.application, &e.member, 200)
}

func TestPostgreSQLChangeOwnPasswordRequiresSessionAndOrigin(t *testing.T) {
	e := newPasswordEnvironment(t)
	a := e.application
	body := ownPasswordBody(identityTestPassword, "changed-member-password")
	before := passwordDatabaseState(t, a)
	requireAPIError(t, a.request(t, http.MethodPost, "/api/v1/auth/password", body, nil, nil), 401, "unauthorized")
	requireAPIError(t, a.request(t, http.MethodPost, "/api/v1/auth/password", body, &e.member, map[string]string{"Origin": ""}), 403, "origin_required")
	requireAPIError(t, a.request(t, http.MethodPost, "/api/v1/auth/password", body, &e.member, map[string]string{"Origin": "http://untrusted.example"}), 403, "origin_forbidden")
	requirePasswordStateUnchanged(t, a, before)
	mustExec(t, a, `UPDATE sessions SET expires_at=NOW()-INTERVAL '1 second' WHERE token_hash=$1`, identity.HashSessionToken(e.second.token))
	requireAPIError(t, a.request(t, http.MethodPost, "/api/v1/auth/password", body, &e.second, nil), 401, "unauthorized")
	// 直接停用但保留有效 session，证明鉴权本身会检查用户状态。
	mustExec(t, a, `UPDATE users SET disabled_at=NOW() WHERE id=$1::uuid`, e.member.id)
	before = passwordDatabaseState(t, a)
	requireAPIError(t, e.change(t, identityTestPassword, "changed-member-password"), 401, "unauthorized")
	requirePasswordStateUnchanged(t, a, before)
}

func TestPostgreSQLChangeOwnPasswordIsAtomic(t *testing.T) {
	e := newPasswordEnvironment(t)
	installRejectingPasswordSessionTrigger(t, e.application)
	before := passwordDatabaseState(t, e.application)
	requireAPIError(t, e.change(t, identityTestPassword, "changed-member-password"), 503, "database_unavailable")
	requirePasswordStateUnchanged(t, e.application, before)
	requirePasswordSession(t, e.application, &e.member, 200)
	requirePasswordSession(t, e.application, &e.second, 200)
	loginPasswordUser(t, e.application, "listener", identityTestPassword, "user")
}

func TestPostgreSQLConcurrentOwnPasswordChangesSucceedOnce(t *testing.T) {
	e := newPasswordEnvironment(t)
	a := e.application
	holder, err := a.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	var requests sync.WaitGroup
	finished := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		_ = holder.Rollback()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("取消后密码请求未退出")
		}
	})
	// 即使准备阶段失败，也保证清理等待的协程能结束。
	defer func() { go func() { requests.Wait(); close(finished) }() }()
	var holderPID int
	if err := holder.QueryRow(`SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Exec(`SELECT 1 FROM users WHERE id=$1::uuid FOR UPDATE`, e.member.id); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(ownPasswordBody(identityTestPassword, "concurrent-new-password"))
	if err != nil {
		t.Fatal(err)
	}
	responses := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", strings.NewReader(string(body))).WithContext(ctx)
		request.Host = "roomusic.test"
		request.Header.Set("Origin", a.origin)
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: e.member.token})
		requests.Add(1)
		go func() {
			defer requests.Done()
			response := httptest.NewRecorder()
			a.handler.ServeHTTP(response, request)
			responses <- response
		}()
	}
	// 只统计当前 holder 直接或间接阻塞的连接，不让其他测试 schema 凑足屏障。
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting int
		err := a.database.QueryRowContext(ctx, `WITH RECURSIVE blocked(pid) AS (
		 SELECT pid FROM pg_stat_activity WHERE $1::int=ANY(pg_blocking_pids(pid))
		 UNION
		 SELECT activity.pid FROM pg_stat_activity activity JOIN blocked b ON b.pid=ANY(pg_blocking_pids(activity.pid))
		) SELECT count(*) FROM blocked`, holderPID).Scan(&waiting)
		if err != nil {
			t.Fatalf("读取密码请求锁等待：%v", err)
		}
		if waiting >= 2 {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("两个密码请求未同时竞争用户锁")
		}
	}
	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	statuses := []int{}
	for range 2 {
		select {
		case response := <-responses:
			statuses = append(statuses, response.Code)
			if response.Code == 403 {
				requireAPIError(t, response, 403, "current_password_incorrect")
			}
		case <-ctx.Done():
			t.Fatal("并发改密码未完成")
		}
	}
	sort.Ints(statuses)
	if !reflect.DeepEqual(statuses, []int{200, 403}) {
		t.Fatalf("并发改密码状态：%v", statuses)
	}
	requirePasswordSession(t, a, &e.member, 200)
	requirePasswordSession(t, a, &e.second, 401)
	loginPasswordUser(t, a, "listener", "concurrent-new-password", "user")
}

func TestPostgreSQLAdminResetPasswordRevokesAllSessions(t *testing.T) {
	e := newPasswordEnvironment(t)
	a := e.application
	response := a.request(t, http.MethodPost, "/api/v1/users/"+e.member.id+"/password", map[string]string{"password": "reset-member-password"}, &e.admin, nil)
	requirePasswordSuccess(t, response, "password_reset")
	requirePasswordSession(t, a, &e.member, 401)
	requirePasswordSession(t, a, &e.second, 401)
	requirePasswordSession(t, a, &e.admin, 200)
	requireAPIError(t, a.request(t, http.MethodPost, "/api/v1/auth/login", accountBody("listener", identityTestPassword), nil, nil), 401, "invalid_credentials")
	loginPasswordUser(t, a, "listener", "reset-member-password", "user")
	if countRows(t, a, `SELECT count(*) FROM operations`) != 0 {
		t.Fatal("重置不应写入治理记录")
	}
}

func TestPostgreSQLAdminResetPasswordKeepsDisabledMemberDisabled(t *testing.T) {
	e := newPasswordEnvironment(t)
	a := e.application
	path := "/api/v1/users/" + e.member.id
	response := a.request(t, http.MethodPatch, path, map[string]bool{"disabled": true}, &e.admin, nil)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var disabledBefore, disabledAfter time.Time
	if err := a.database.QueryRow(`SELECT disabled_at FROM users WHERE id=$1::uuid`, e.member.id).Scan(&disabledBefore); err != nil {
		t.Fatal(err)
	}
	requirePasswordSuccess(t, a.request(t, http.MethodPost, path+"/password", map[string]string{"password": "reset-disabled-password"}, &e.admin, nil), "password_reset")
	if err := a.database.QueryRow(`SELECT disabled_at FROM users WHERE id=$1::uuid`, e.member.id).Scan(&disabledAfter); err != nil || !disabledBefore.Equal(disabledAfter) {
		t.Fatalf("重置改变了停用状态：%v", err)
	}
	requirePasswordSession(t, a, &e.member, 401)
	requirePasswordSession(t, a, &e.second, 401)
	requireAPIError(t, a.request(t, http.MethodPost, "/api/v1/auth/login", accountBody("listener", "reset-disabled-password"), nil, nil), 401, "invalid_credentials")
	response = a.request(t, http.MethodPatch, path, map[string]bool{"disabled": false}, &e.admin, nil)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	loginPasswordUser(t, a, "listener", "reset-disabled-password", "user")
}

func TestPostgreSQLAdminResetPasswordRejectsInvalidRequests(t *testing.T) {
	e := newPasswordEnvironment(t)
	a := e.application
	path := "/api/v1/users/" + e.member.id + "/password"
	body := map[string]string{"password": "reset-member-password"}
	before := passwordDatabaseState(t, a)
	for _, tc := range []struct {
		name, path string
		body       any
		user       *integrationTestUser
		headers    map[string]string
		status     int
		code       string
	}{
		{"未登录", path, body, nil, nil, 401, "unauthorized"},
		{"普通成员", path, body, &e.member, nil, 403, "forbidden"},
		{"非UUID", "/api/v1/users/not-a-uuid/password", body, &e.admin, nil, 400, "invalid_id"},
		{"未知成员", "/api/v1/users/" + uuid.New() + "/password", body, &e.admin, nil, 404, "not_found"},
		{"重置自己", "/api/v1/users/" + e.admin.id + "/password", body, &e.admin, nil, 403, "self_password_reset"},
		{"大写UUID重置自己", "/api/v1/users/" + strings.ToUpper(e.admin.id) + "/password", body, &e.admin, nil, 403, "self_password_reset"},
		{"11位", path, map[string]string{"password": strings.Repeat("a", 11)}, &e.admin, nil, 400, "invalid_input"},
		{"73位", path, map[string]string{"password": strings.Repeat("a", 73)}, &e.admin, nil, 400, "invalid_input"},
		{"中文", path, map[string]string{"password": "abcdefghijk中"}, &e.admin, nil, 400, "invalid_input"},
		{"缺Origin", path, body, &e.admin, map[string]string{"Origin": ""}, 403, "origin_required"},
		{"跨源", path, body, &e.admin, map[string]string{"Origin": "http://untrusted.example"}, 403, "origin_forbidden"},
		{"非法JSON", path, "not an object", &e.admin, nil, 400, "invalid_json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireAPIError(t, a.request(t, http.MethodPost, tc.path, tc.body, tc.user, tc.headers), tc.status, tc.code)
			requirePasswordStateUnchanged(t, a, before)
		})
	}
	t.Run("非UUID不访问业务数据库", func(t *testing.T) {
		// 鉴权仍用隔离库；handler 的业务数据库为 nil，一旦越过 ID 校验访问它就会 panic。
		mux := http.NewServeMux()
		identityapi.New(nil, identity.NewGuard(a.database, false, "")).Register(mux)
		withoutDatabase := *a
		withoutDatabase.handler = mux
		requireAPIError(t, withoutDatabase.request(t, http.MethodPost, "/api/v1/users/not-a-uuid/password", body, &e.admin, nil), 400, "invalid_id")
		requirePasswordStateUnchanged(t, a, before)
	})
	installRejectingPasswordSessionTrigger(t, a)
	requireAPIError(t, a.request(t, http.MethodPost, path, body, &e.admin, nil), 503, "database_unavailable")
	requirePasswordStateUnchanged(t, a, before)
	requirePasswordSession(t, a, &e.member, 200)
	requirePasswordSession(t, a, &e.second, 200)
	requirePasswordSession(t, a, &e.admin, 200)
}
