package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	identityapi "github.com/qlqq/roomusic/backend/internal/identity/httpapi"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
)

type loginThrottleClock struct{ now time.Time }

func (c *loginThrottleClock) read() time.Time         { return c.now }
func (c *loginThrottleClock) advance(d time.Duration) { c.now = c.now.Add(d) }

func newLoginThrottleApplication(t *testing.T) (*integrationTestApplication, *loginThrottleClock, *identity.LoginThrottle) {
	t.Helper()
	clock := &loginThrottleClock{now: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)}
	throttle := identity.NewLoginThrottle(clock.read)
	a := newIntegrationTestApplication(t, WithLoginThrottle(throttle))
	requireIdentityBody(t, a.request(t, http.MethodPost, "/api/v1/setup/admin", accountBody("curator", identityTestPassword), nil, nil), 201, "curator", "admin")
	return a, clock, throttle
}

// throttleRequest 只改变 TCP 对端，Origin、Cookie 与 JSON 仍走真实 HTTP 边界。
func throttleRequest(t *testing.T, a *integrationTestApplication, path string, body any, user *integrationTestUser, remote string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded))
	request.Host, request.RemoteAddr = "roomusic.test", remote
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", a.origin)
	request.Header.Set("X-Request-ID", "throttle-test-request")
	if user != nil {
		request.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: user.token})
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	a.handler.ServeHTTP(response, request)
	return response
}

func throttleLogin(t *testing.T, a *integrationTestApplication, username, password, remote string) *httptest.ResponseRecorder {
	t.Helper()
	return throttleRequest(t, a, "/api/v1/auth/login", accountBody(username, password), nil, remote, nil)
}

func requireTooManyAttempts(t *testing.T, response *httptest.ResponseRecorder, retry string) {
	t.Helper()
	requireAPIError(t, response, 429, "too_many_attempts")
	if got := response.Header().Get("Retry-After"); got != retry {
		t.Fatalf("Retry-After=%q，期望 %q", got, retry)
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("限速响应头：%v", response.Header())
	}
	if len(response.Result().Cookies()) != 0 {
		t.Fatal("限速不能修改会话 Cookie")
	}
}

func TestPostgreSQLLoginThrottleLocksAndRecovers(t *testing.T) {
	a, clock, _ := newLoginThrottleApplication(t)
	const remote = "192.0.2.10:4000"
	for i := 0; i < 5; i++ {
		requireAPIError(t, throttleLogin(t, a, "curator", "wrong-password", remote), 401, "invalid_credentials")
	}
	requireTooManyAttempts(t, throttleLogin(t, a, "curator", identityTestPassword, remote), "60")
	if countRows(t, a, `SELECT count(*) FROM sessions`) != 0 {
		t.Fatal("限速期间创建了会话")
	}
	// 不同端口仍是同一来源，伪造转发头也不能绕过。
	clock.advance(500 * time.Millisecond)
	response := throttleRequest(t, a, "/api/v1/auth/login", accountBody("curator", identityTestPassword), nil, "192.0.2.10:5000", map[string]string{"X-Forwarded-For": "192.0.2.99", "X-Real-IP": "192.0.2.99"})
	requireTooManyAttempts(t, response, "60")
	requireIdentityBody(t, throttleLogin(t, a, "curator", identityTestPassword, "192.0.2.11:4000"), 200, "curator", "admin")
	clock.advance(59 * time.Second)
	requireTooManyAttempts(t, throttleLogin(t, a, "curator", identityTestPassword, remote), "1")
	clock.advance(500 * time.Millisecond)
	requireIdentityBody(t, throttleLogin(t, a, "curator", identityTestPassword, remote), 200, "curator", "admin")
}

func TestPostgreSQLLoginThrottleSuccessClearsCombination(t *testing.T) {
	a, _, _ := newLoginThrottleApplication(t)
	for i := 0; i < 4; i++ {
		requireAPIError(t, throttleLogin(t, a, "curator", "wrong-password", "192.0.2.10:1"), 401, "invalid_credentials")
		requireAPIError(t, throttleLogin(t, a, "curator", "wrong-password", "192.0.2.11:1"), 401, "invalid_credentials")
	}
	requireIdentityBody(t, throttleLogin(t, a, "curator", identityTestPassword, "192.0.2.10:1"), 200, "curator", "admin")
	for i := 0; i < 4; i++ {
		requireAPIError(t, throttleLogin(t, a, "curator", "wrong-password", "192.0.2.10:1"), 401, "invalid_credentials")
	}
	requireIdentityBody(t, throttleLogin(t, a, "curator", identityTestPassword, "192.0.2.10:1"), 200, "curator", "admin")
	requireAPIError(t, throttleLogin(t, a, "curator", "wrong-password", "192.0.2.11:1"), 401, "invalid_credentials")
	requireTooManyAttempts(t, throttleLogin(t, a, "curator", identityTestPassword, "192.0.2.11:1"), "60")
}

func TestPostgreSQLLoginThrottleMissingAndDisabledUsersMatchWrongPassword(t *testing.T) {
	a, _, _ := newLoginThrottleApplication(t)
	mustExec(t, a, `INSERT INTO users(id,username,password_hash,role,disabled_at) SELECT gen_random_uuid(),'disabled',password_hash,'user',NOW() FROM users WHERE username='curator'`)
	var baseline []map[string]any
	for index, username := range []string{"curator", "missing", "disabled"} {
		var responses []map[string]any
		for i := 0; i < 5; i++ {
			response := throttleLogin(t, a, username, "wrong-password", "192.0.2.10:1")
			requireAPIError(t, response, 401, "invalid_credentials")
			responses = append(responses, decodeJSONMap(t, response))
		}
		response := throttleLogin(t, a, username, identityTestPassword, "192.0.2.10:1")
		requireTooManyAttempts(t, response, "60")
		responses = append(responses, decodeJSONMap(t, response))
		if index == 0 {
			baseline = responses
		} else if !reflect.DeepEqual(baseline, responses) {
			t.Fatalf("%s 的失败提示或计数不同：%v", username, responses)
		}
	}
}

func TestPostgreSQLLoginThrottleSourceLimitAndRecovery(t *testing.T) {
	a, clock, throttle := newLoginThrottleApplication(t)
	const remote = "192.0.2.10:1"
	for i := 0; i < 30; i++ {
		requireAPIError(t, throttleLogin(t, a, fmt.Sprintf("missing-%d", i), "wrong-password", remote), 401, "invalid_credentials")
	}
	requireTooManyAttempts(t, throttleLogin(t, a, "curator", identityTestPassword, remote), "60")
	requireIdentityBody(t, throttleLogin(t, a, "curator", identityTestPassword, "192.0.2.11:1"), 200, "curator", "admin")
	// 来源暂停必须先于查库；专用业务连接池已关闭，仍应返回 429 而非 503。
	unavailable := closedLoginDatabaseApplication(t, a, throttle)
	requireTooManyAttempts(t, throttleLogin(t, unavailable, "curator", identityTestPassword, remote), "60")
	clock.advance(time.Minute)
	requireIdentityBody(t, throttleLogin(t, a, "curator", identityTestPassword, remote), 200, "curator", "admin")
}

func closedLoginDatabaseApplication(t *testing.T, a *integrationTestApplication, throttle *identity.LoginThrottle) *integrationTestApplication {
	t.Helper()
	var schema string
	if err := a.database.QueryRow(`SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	scoped, err := postgrestest.ScopedURL(postgrestest.BaseURL(t), schema)
	if err != nil {
		t.Fatal(err)
	}
	closed, err := sql.Open("pgx", scoped)
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	identityapi.New(closed, identity.NewGuard(a.database, false, ""), throttle, nil).Register(mux)
	unavailable := *a
	unavailable.handler = mux
	return &unavailable
}

func TestPostgreSQLLoginThrottleDatabaseErrorsDoNotCount(t *testing.T) {
	a, _, throttle := newLoginThrottleApplication(t)
	unavailable := closedLoginDatabaseApplication(t, a, throttle)
	const source = "192.0.2.10"
	for i := 0; i < 6; i++ {
		response := throttleLogin(t, unavailable, "curator", "wrong-password", source+":1")
		requireAPIError(t, response, 503, "database_unavailable")
		if response.Header().Get("Retry-After") != "" {
			t.Fatal("数据库故障不应带限速头")
		}
	}
	var hash string
	if err := a.database.QueryRow(`SELECT password_hash FROM users WHERE username='curator'`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(hash))
	attempt, err := throttle.Begin("curator", source, [8]byte(digest[:8]))
	if err != nil || attempt == nil {
		t.Fatalf("数据库错误误锁账号：%v", err)
	}
	if attempt.Failures() != 1 {
		t.Fatalf("数据库错误被计入账号次数：%d", attempt.Failures())
	}
	attempt.Succeeded()
	requireIdentityBody(t, throttleLogin(t, a, "curator", identityTestPassword, source+":1"), 200, "curator", "admin")
	// 直接在同一注入实例检查来源额度，避免用 30 次 bcrypt 掩盖“不计数”的断言。
	for i := 0; i < 30; i++ {
		if _, err := throttle.Begin(fmt.Sprintf("probe-%d", i), source, [8]byte{}); err != nil {
			t.Fatalf("第 %d 次已耗尽来源额度，数据库错误被计数：%v", i+1, err)
		}
	}
	if _, err := throttle.Begin("overflow", source, [8]byte{}); err == nil {
		t.Fatal("来源上限探针没有生效")
	}
}

func TestPostgreSQLLoginThrottleSessionWriteErrorsDoNotCount(t *testing.T) {
	a, _, throttle := newLoginThrottleApplication(t)
	installRejectingInsertTrigger(t, a, "sessions", "TRUE")
	for i := 0; i < 6; i++ {
		requireAPIError(t, throttleLogin(t, a, "curator", identityTestPassword, "192.0.2.10:1"), 503, "database_unavailable")
	}
	for i := 0; i < 30; i++ {
		if _, err := throttle.Begin(fmt.Sprint(i), "192.0.2.10", [8]byte{}); err != nil {
			t.Fatalf("会话写错占用了来源额度：%v", err)
		}
	}
	mustExec(t, a, `DROP TRIGGER reject_sessions_insert ON sessions`)
	requireIdentityBody(t, throttleLogin(t, a, "curator", identityTestPassword, "192.0.2.11:1"), 200, "curator", "admin")
}

func TestPostgreSQLLoginThrottleResetPasswordImmediatelyUnlocks(t *testing.T) {
	for _, mode := range []string{"command", "web"} {
		t.Run(mode, func(t *testing.T) {
			a, _, _ := newLoginThrottleApplication(t)
			admin := loginPasswordUser(t, a, "curator", identityTestPassword, "admin")
			requireIdentityBody(t, a.request(t, http.MethodPost, "/api/v1/users", accountBody("listener", identityTestPassword), &admin, nil), 201, "listener", "user")
			for i := 0; i < 5; i++ {
				requireAPIError(t, throttleLogin(t, a, "listener", "wrong-password", "192.0.2.10:1"), 401, "invalid_credentials")
			}
			requireTooManyAttempts(t, throttleLogin(t, a, "listener", identityTestPassword, "192.0.2.10:1"), "60")
			const next = "reset-member-password"
			if mode == "command" {
				if _, err := identity.ResetPasswordByUsername(context.Background(), a.database, "listener", next); err != nil {
					t.Fatal(err)
				}
			} else {
				var id string
				if err := a.database.QueryRow(`SELECT id::text FROM users WHERE username='listener'`).Scan(&id); err != nil {
					t.Fatal(err)
				}
				requirePasswordSuccess(t, a.request(t, http.MethodPost, "/api/v1/users/"+id+"/password", map[string]string{"password": next}, &admin, nil), "password_reset")
			}
			requireIdentityBody(t, throttleLogin(t, a, "listener", next, "192.0.2.10:1"), 200, "listener", "user")
		})
	}
}

func TestPostgreSQLPasswordThrottleAcrossSourcesKeepsSession(t *testing.T) {
	a, clock, _ := newLoginThrottleApplication(t)
	admin := loginPasswordUser(t, a, "curator", identityTestPassword, "admin")
	requireIdentityBody(t, a.request(t, http.MethodPost, "/api/v1/users", accountBody("listener", identityTestPassword), &admin, nil), 201, "listener", "user")
	user := loginPasswordUser(t, a, "listener", identityTestPassword, "user")
	before := passwordDatabaseState(t, a)
	for i := 0; i < 5; i++ {
		requireAPIError(t, throttleRequest(t, a, "/api/v1/auth/password", ownPasswordBody("wrong-password", identityTestPassword), &user, "192.0.2.10:1", nil), 403, "current_password_incorrect")
	}
	response := throttleRequest(t, a, "/api/v1/auth/password", ownPasswordBody(identityTestPassword, identityTestPassword), &user, "192.0.2.11:1", nil)
	requireTooManyAttempts(t, response, "60")
	if apiErrorMessage(t, response) != "当前密码输错次数太多，请过几分钟再试" {
		t.Fatal("改密码限速提示不符")
	}
	requirePasswordStateUnchanged(t, a, before)
	requirePasswordSession(t, a, &user, 200)
	// 登录与改密码的命名空间独立。
	requireIdentityBody(t, throttleLogin(t, a, "listener", identityTestPassword, "192.0.2.10:1"), 200, "listener", "user")
	clock.advance(time.Minute)
	requirePasswordSuccess(t, throttleRequest(t, a, "/api/v1/auth/password", ownPasswordBody(identityTestPassword, identityTestPassword), &user, "192.0.2.11:1", nil), "password_changed")
	requirePasswordSession(t, a, &user, 200)
}

func TestPostgreSQLPasswordThrottleSuccessClearsFailures(t *testing.T) {
	a, _, _ := newLoginThrottleApplication(t)
	admin := loginPasswordUser(t, a, "curator", identityTestPassword, "admin")
	requireIdentityBody(t, a.request(t, http.MethodPost, "/api/v1/users", accountBody("listener", identityTestPassword), &admin, nil), 201, "listener", "user")
	user := loginPasswordUser(t, a, "listener", identityTestPassword, "user")
	for cycle := 0; cycle < 2; cycle++ {
		for i := 0; i < 4; i++ {
			requireAPIError(t, a.request(t, http.MethodPost, "/api/v1/auth/password", ownPasswordBody("wrong-password", identityTestPassword), &user, nil), 403, "current_password_incorrect")
		}
		requirePasswordSuccess(t, a.request(t, http.MethodPost, "/api/v1/auth/password", ownPasswordBody(identityTestPassword, identityTestPassword), &user, nil), "password_changed")
	}
	requirePasswordSession(t, a, &user, 200)
}
