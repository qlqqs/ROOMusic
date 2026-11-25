package app

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/buildinfo"
)

const (
	subsonicAdmin       = "subsonic-admin"
	subsonicPassword    = "subsonic-fixture-password"
	subsonicWrongPass   = "subsonic-incorrect-password"
	subsonicMePath      = "/api/v1/me/subsonic"
	subsonicConfirmPath = "/api/v1/me/subsonic/confirm-password"
	subsonicAdminPath   = "/api/v1/admin/subsonic"
	subsonicSalt        = "f31a9b70e58d2c46"
)

func newSubsonicTestApplication(t *testing.T) *integrationTestApplication {
	t.Helper()
	clock := &loginThrottleClock{now: time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)}
	return newIntegrationTestApplication(t, WithLoginThrottle(identity.NewLoginThrottle(clock.read)))
}

func setupSubsonicAdmin(t *testing.T, a *integrationTestApplication) integrationTestUser {
	t.Helper()
	response := a.request(t, http.MethodPost, "/api/v1/setup/admin", accountBody(subsonicAdmin, subsonicPassword), nil, nil)
	requireIdentityBody(t, response, http.StatusCreated, subsonicAdmin, "admin")
	return loginPasswordUser(t, a, subsonicAdmin, subsonicPassword, "admin")
}

func createSubsonicMember(t *testing.T, a *integrationTestApplication, admin *integrationTestUser, name string) string {
	t.Helper()
	response := a.request(t, http.MethodPost, "/api/v1/users", accountBody(name, subsonicPassword), admin, nil)
	return requireIdentityBody(t, response, http.StatusCreated, name, "user")
}

// seedSubsonicMember 模拟开启兼容接口前已有的成员和网页会话。
func seedSubsonicMember(t *testing.T, a *integrationTestApplication, name string) integrationTestUser {
	t.Helper()
	member := a.createUser(t, "user")
	hash, err := identity.HashPassword(subsonicPassword)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, a, `UPDATE users SET username=$1, password_hash=$2 WHERE id=$3`, name, hash, member.id)
	return member
}

func subsonicToken(password, salt string) string {
	digest := md5.Sum([]byte(password + salt))
	return hex.EncodeToString(digest[:])
}

func subsonicParameters(username, password, mode string) url.Values {
	params := url.Values{"u": {username}, "v": {"1.16.1"}, "c": {"roomusic-integration"}, "f": {"json"}}
	switch mode {
	case "plain":
		params.Set("p", password)
	case "hex":
		params.Set("p", "enc:"+hex.EncodeToString([]byte(password)))
	case "token":
		params.Set("s", subsonicSalt)
		params.Set("t", subsonicToken(password, subsonicSalt))
	}
	return params
}

func requestSubsonic(a *integrationTestApplication, method, endpoint string, params url.Values) *httptest.ResponseRecorder {
	path := "/rest/" + endpoint
	var request *http.Request
	if method == http.MethodPost {
		request = httptest.NewRequest(method, path, strings.NewReader(params.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Origin", "https://subsonic-client.example")
	} else {
		request = httptest.NewRequest(method, path+"?"+params.Encode(), nil)
	}
	recorder := httptest.NewRecorder()
	a.handler.ServeHTTP(recorder, request)
	return recorder
}

func requireSubsonicHTTP(t *testing.T, response *httptest.ResponseRecorder, status int, contentType string) {
	t.Helper()
	if response.Code != status || response.Header().Get("Content-Type") != contentType ||
		response.Header().Get("Cache-Control") != "no-store" || len(response.Result().Cookies()) != 0 {
		t.Fatalf("Subsonic HTTP 响应为 %d，Content-Type 为 %q，期望 %d %q、禁止缓存且不设会话 Cookie。正文 %s",
			response.Code, response.Header().Get("Content-Type"), status, contentType, response.Body)
	}
}

func requireSubsonicJSON(t *testing.T, response *httptest.ResponseRecorder, status int, errorCode *int) map[string]any {
	t.Helper()
	requireSubsonicHTTP(t, response, status, "application/json")
	root := decodeJSONMap(t, response)
	body, ok := root["subsonic-response"].(map[string]any)
	if !ok || len(root) != 1 {
		t.Fatalf("Subsonic JSON 缺少顶层外壳，正文 %s", response.Body)
	}
	version := buildinfo.Current().Version
	if version == "" {
		version = "dev"
	}
	if body["version"] != "1.16.1" || body["type"] != "roomusic" || body["serverVersion"] != version || body["openSubsonic"] != true {
		t.Fatalf("Subsonic JSON 外壳字段不符，正文 %s", response.Body)
	}
	if errorCode == nil {
		if _, hasError := body["error"]; body["status"] != "ok" || hasError {
			t.Fatalf("Subsonic JSON 应成功，正文 %s", response.Body)
		}
	} else {
		detail, _ := body["error"].(map[string]any)
		message, _ := detail["message"].(string)
		if body["status"] != "failed" || detail["code"] != float64(*errorCode) || message == "" {
			t.Fatalf("Subsonic 错误码应为 %d，正文 %s", *errorCode, response.Body)
		}
	}
	return body
}

func requireSubsonicFailure(t *testing.T, response *httptest.ResponseRecorder, status, code int) {
	t.Helper()
	requireSubsonicJSON(t, response, status, &code)
}

func requireSubsonicOK(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	return requireSubsonicJSON(t, response, http.StatusOK, nil)
}

func requireSubsonicWebJSON(t *testing.T, response *httptest.ResponseRecorder, want map[string]any) {
	t.Helper()
	if response.Code != http.StatusOK || !reflect.DeepEqual(decodeJSONMap(t, response), want) {
		t.Fatalf("Subsonic 网页接口返回 HTTP %d 和 %s，期望 200 和 %v", response.Code, response.Body, want)
	}
}

func requireSubsonicCredentialCount(t *testing.T, a *integrationTestApplication, want int) {
	t.Helper()
	got := countRows(t, a, "SELECT count(*) FROM subsonic_credentials")
	if got != want {
		t.Fatalf("密码副本数为 %d，期望 %d", got, want)
	}
}

func setSubsonicEnabled(t *testing.T, a *integrationTestApplication, admin *integrationTestUser, enabled bool) {
	t.Helper()
	body := map[string]any{"enabled": enabled}
	requireSubsonicWebJSON(t, a.request(t, http.MethodPut, subsonicAdminPath, body, admin, nil), body)
	requireSubsonicWebJSON(t, a.request(t, http.MethodGet, subsonicAdminPath, nil, admin, nil), body)
}

func requireSubsonicMe(t *testing.T, a *integrationTestApplication, member *integrationTestUser, name string, enabled, ready bool) {
	t.Helper()
	requireSubsonicWebJSON(t, a.request(t, http.MethodGet, subsonicMePath, nil, member, nil),
		map[string]any{"enabled": enabled, "ready": ready, "username": name})
}

func TestPostgreSQLSubsonicAuthenticationAndPayloads(t *testing.T) {
	a := newSubsonicTestApplication(t)
	admin := setupSubsonicAdmin(t, a)
	const member = "subsonic-listener"
	createSubsonicMember(t, a, &admin, member)
	requireSubsonicCredentialCount(t, a, 2)
	sessions := countRows(t, a, "SELECT count(*) FROM sessions")
	for _, mode := range []string{"plain", "hex", "token"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			t.Run(mode+"/"+method, func(t *testing.T) {
				body := requireSubsonicOK(t, requestSubsonic(a, method, "ping.view", subsonicParameters(member, subsonicPassword, mode)))
				if len(body) != 5 {
					t.Fatalf("ping 响应应只有协议外壳字段，实际为 %v", body)
				}
			})
		}
	}
	if got := countRows(t, a, "SELECT count(*) FROM sessions"); got != sessions {
		t.Fatalf("Subsonic 请求改变了网页会话数，原为 %d，现为 %d", sessions, got)
	}
	for _, format := range []string{"", "xml"} {
		t.Run("xml-format="+format, func(t *testing.T) {
			params := subsonicParameters(member, subsonicPassword, "plain")
			params.Set("f", format)
			if format == "" {
				params.Del("f")
			}
			response := requestSubsonic(a, http.MethodGet, "ping", params)
			requireSubsonicHTTP(t, response, http.StatusOK, "application/xml; charset=utf-8")
			var body struct {
				XMLName       xml.Name `xml:"http://subsonic.org/restapi subsonic-response"`
				Status        string   `xml:"status,attr"`
				Version       string   `xml:"version,attr"`
				Type          string   `xml:"type,attr"`
				ServerVersion string   `xml:"serverVersion,attr"`
				OpenSubsonic  bool     `xml:"openSubsonic,attr"`
			}
			if err := xml.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Status != "ok" ||
				body.Version != "1.16.1" || body.Type != "roomusic" || body.ServerVersion == "" || !body.OpenSubsonic {
				t.Fatalf("ping XML 外壳不符，正文 %s，解析错误 %v", response.Body, err)
			}
		})
	}

	for _, test := range []struct{ endpoint, field, json string }{
		{"getLicense", "license", `{"valid":true}`},
		{"getOpenSubsonicExtensions", "openSubsonicExtensions", `[{"name":"formPost","versions":[1]}]`},
		{"getMusicFolders", "musicFolders", `{"musicFolder":[{"id":1,"name":"ROOMusic"}]}`},
		{"getUser", "user", `{"username":"subsonic-listener","scrobblingEnabled":false,"adminRole":false,"settingsRole":false,"downloadRole":true,"uploadRole":false,"playlistRole":false,"coverArtRole":false,"commentRole":false,"podcastRole":false,"streamRole":true,"jukeboxRole":true,"shareRole":false,"videoConversionRole":false,"folder":[1]}`},
	} {
		t.Run(test.endpoint, func(t *testing.T) {
			params := subsonicParameters(member, subsonicPassword, "token")
			if test.endpoint == "getUser" {
				params.Set("username", member)
			}
			body := requireSubsonicOK(t, requestSubsonic(a, http.MethodGet, test.endpoint, params))
			var want any
			if err := json.Unmarshal([]byte(test.json), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(body[test.field], want) || len(body) != 6 {
				t.Fatalf("%s 载荷为 %v，期望 %s", test.endpoint, body[test.field], test.json)
			}
		})
	}
}

func TestPostgreSQLSubsonicProtocolErrors(t *testing.T) {
	a := newSubsonicTestApplication(t)
	admin := setupSubsonicAdmin(t, a)
	disabled := createSubsonicMember(t, a, &admin, "subsonic-disabled-member")
	requireSubsonicOK(t, requestSubsonic(a, http.MethodGet, "ping", subsonicParameters("subsonic-disabled-member", subsonicPassword, "plain")))
	response := a.request(t, http.MethodPatch, "/api/v1/users/"+disabled, map[string]bool{"disabled": true}, &admin, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("停用测试成员失败，HTTP %d，正文 %s", response.Code, response.Body)
	}
	requireSubsonicFailure(t, requestSubsonic(a, http.MethodGet, "ping", subsonicParameters("subsonic-disabled-member", subsonicPassword, "plain")), 200, 40)

	requireSubsonicOK(t, requestSubsonic(a, http.MethodGet, "ping", subsonicParameters(subsonicAdmin, subsonicPassword, "plain")))
	requireSubsonicFailure(t, requestSubsonic(a, http.MethodGet, "ping", subsonicParameters(subsonicAdmin, subsonicWrongPass, "plain")), 200, 40)
	for _, missing := range []string{"u", "v", "c", "p", "t", "s"} {
		t.Run("missing-"+missing, func(t *testing.T) {
			mode := "plain"
			if missing == "t" || missing == "s" {
				mode = "token"
			}
			params := subsonicParameters(subsonicAdmin, subsonicPassword, mode)
			params.Del(missing)
			requireSubsonicFailure(t, requestSubsonic(a, http.MethodGet, "ping.view", params), 200, 10)
		})
	}
	for _, test := range []struct {
		endpoint, key, value string
		status, code         int
	}{
		{"getUser", "username", "subsonic-disabled-member", 200, 50},
		{"getUser.view", "username", "", 200, 10},
		{"unknown.view", "f", "json", 404, 0},
	} {
		t.Run(fmt.Sprintf("%s/%d", test.endpoint, test.code), func(t *testing.T) {
			params := subsonicParameters(subsonicAdmin, subsonicPassword, "token")
			if test.value != "" {
				params.Set(test.key, test.value)
			}
			requireSubsonicFailure(t, requestSubsonic(a, http.MethodGet, test.endpoint, params), test.status, test.code)
		})
	}
	params := subsonicParameters(subsonicAdmin, subsonicPassword, "plain")
	params.Set("f", "jsonp")
	response = requestSubsonic(a, http.MethodGet, "ping", params)
	requireSubsonicHTTP(t, response, http.StatusOK, "application/xml; charset=utf-8")
	var failed struct {
		XMLName xml.Name `xml:"http://subsonic.org/restapi subsonic-response"`
		Status  string   `xml:"status,attr"`
		Error   struct {
			Code    string `xml:"code,attr"`
			Message string `xml:"message,attr"`
		} `xml:"error"`
	}
	if err := xml.Unmarshal(response.Body.Bytes(), &failed); err != nil || failed.Status != "failed" || failed.Error.Code != "0" || failed.Error.Message == "" {
		t.Fatalf("不支持的格式应返回 XML 错误码 0，正文 %s，解析错误 %v", response.Body, err)
	}
}

func TestPostgreSQLSubsonicSharesWebLoginThrottle(t *testing.T) {
	a := newSubsonicTestApplication(t)
	admin := setupSubsonicAdmin(t, a)
	for _, source := range []string{"subsonic", "web"} {
		t.Run(source, func(t *testing.T) {
			name := "subsonic-throttle-" + source
			createSubsonicMember(t, a, &admin, name)
			ping := func(password string) *httptest.ResponseRecorder {
				return requestSubsonic(a, http.MethodGet, "ping", subsonicParameters(name, password, "plain"))
			}
			login := func(password string) *httptest.ResponseRecorder {
				return a.request(t, http.MethodPost, "/api/v1/auth/login", accountBody(name, password), nil, nil)
			}

			// 先预热正确密码的缓存，之后即使缓存命中也要经过限流。
			requireSubsonicOK(t, ping(subsonicPassword))
			for range 5 {
				if source == "subsonic" {
					requireSubsonicFailure(t, ping(subsonicWrongPass), 200, 40)
				} else {
					requireAPIError(t, login(subsonicWrongPass), 401, "invalid_credentials")
				}
			}
			requireSubsonicFailure(t, ping(subsonicPassword), 200, 40)
			requireTooManyAttempts(t, login(subsonicPassword), "60")
		})
	}
}

func TestPostgreSQLSubsonicExistingMembers(t *testing.T) {
	a := newSubsonicTestApplication(t)
	admin := setupSubsonicAdmin(t, a)
	setSubsonicEnabled(t, a, &admin, false)
	confirmation := seedSubsonicMember(t, a, "subsonic-confirmed")
	relogin := seedSubsonicMember(t, a, "subsonic-relogin")
	setSubsonicEnabled(t, a, &admin, true)

	for i, test := range []struct {
		name, method string
		member       *integrationTestUser
	}{
		{"subsonic-confirmed", "confirm", &confirmation},
		{"subsonic-relogin", "login", &relogin},
	} {
		t.Run(test.method, func(t *testing.T) {
			ping := func(mode string) *httptest.ResponseRecorder {
				return requestSubsonic(a, http.MethodGet, "ping", subsonicParameters(test.name, subsonicPassword, mode))
			}
			requireSubsonicMe(t, a, test.member, test.name, true, false)
			requireSubsonicOK(t, ping("plain"))
			requireSubsonicCredentialCount(t, a, i)
			requireSubsonicFailure(t, ping("token"), 200, 40)
			if test.method == "confirm" {
				response := a.request(t, http.MethodPost, subsonicConfirmPath, map[string]string{"password": subsonicPassword}, test.member, nil)
				if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
					t.Fatalf("确认密码应返回空的 204，实际为 %d %s", response.Code, response.Body)
				}
			} else {
				loginPasswordUser(t, a, test.name, subsonicPassword, "user")
			}
			requireSubsonicCredentialCount(t, a, i+1)
			requireSubsonicMe(t, a, test.member, test.name, true, true)
			requireSubsonicOK(t, ping("token"))
		})
	}
}

func TestPostgreSQLSubsonicDisableClearsCredentials(t *testing.T) {
	a := newSubsonicTestApplication(t)
	admin := setupSubsonicAdmin(t, a)
	const member = "subsonic-disable-listener"
	createSubsonicMember(t, a, &admin, member)
	requireSubsonicCredentialCount(t, a, 2)
	requireSubsonicOK(t, requestSubsonic(a, http.MethodGet, "ping", subsonicParameters(member, subsonicPassword, "plain")))

	mustExec(t, a, `CREATE FUNCTION reject_subsonic_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'test credential deletion failure'; END
$$;
CREATE TRIGGER reject_subsonic_delete BEFORE DELETE ON subsonic_credentials
FOR EACH ROW EXECUTE FUNCTION reject_subsonic_delete();`)
	response := a.request(t, http.MethodPut, subsonicAdminPath, map[string]bool{"enabled": false}, &admin, nil)
	requireAPIError(t, response, 503, "database_unavailable")
	requireSubsonicWebJSON(t, a.request(t, http.MethodGet, subsonicAdminPath, nil, &admin, nil), map[string]any{"enabled": true})
	requireSubsonicCredentialCount(t, a, 2)
	mustExec(t, a, `DROP TRIGGER reject_subsonic_delete ON subsonic_credentials`)

	setSubsonicEnabled(t, a, &admin, false)
	requireSubsonicCredentialCount(t, a, 0)
	requireSubsonicMe(t, a, &admin, subsonicAdmin, false, false)
	for _, endpoint := range []string{"ping.view", "getLicense", "getUser", "getMusicFolders", "getOpenSubsonicExtensions", "unknown.view"} {
		t.Run(endpoint, func(t *testing.T) {
			params := subsonicParameters(member, subsonicPassword, "plain")
			params.Set("username", member)
			requireSubsonicFailure(t, requestSubsonic(a, http.MethodGet, endpoint, params), 200, 40)
		})
	}
	loginPasswordUser(t, a, member, subsonicPassword, "user")
	requireSubsonicCredentialCount(t, a, 0)

	setSubsonicEnabled(t, a, &admin, true)
	params := subsonicParameters(member, subsonicPassword, "token")
	requireSubsonicFailure(t, requestSubsonic(a, http.MethodGet, "ping", params), 200, 40)
	loginPasswordUser(t, a, member, subsonicPassword, "user")
	requireSubsonicCredentialCount(t, a, 1)
	requireSubsonicOK(t, requestSubsonic(a, http.MethodGet, "ping", params))
}

func TestPostgreSQLSubsonicPasswordRotation(t *testing.T) {
	a := newSubsonicTestApplication(t)
	admin := setupSubsonicAdmin(t, a)
	for _, operation := range []string{"self", "admin", "database"} {
		t.Run(operation, func(t *testing.T) {
			name := "subsonic-rotation-" + operation
			id := createSubsonicMember(t, a, &admin, name)
			ping := func(password, mode string) *httptest.ResponseRecorder {
				return requestSubsonic(a, http.MethodGet, "ping.view", subsonicParameters(name, password, mode))
			}
			modes := []string{"plain", "hex", "token"}
			for _, mode := range modes {
				requireSubsonicOK(t, ping(subsonicPassword, mode))
			}
			const next = "subsonic-rotated-fixture-password"
			switch operation {
			case "self":
				member := loginPasswordUser(t, a, name, subsonicPassword, "user")
				response := a.request(t, http.MethodPost, "/api/v1/auth/password", ownPasswordBody(subsonicPassword, next), &member, nil)
				requirePasswordSuccess(t, response, "password_changed")
			case "admin":
				response := a.request(t, http.MethodPost, "/api/v1/users/"+id+"/password", map[string]string{"password": next}, &admin, nil)
				requirePasswordSuccess(t, response, "password_reset")
			case "database":
				hash, err := identity.HashPassword(next)
				if err != nil {
					t.Fatal(err)
				}
				mustExec(t, a, `UPDATE users SET password_hash=$1 WHERE id=$2`, hash, id)
			}

			if operation == "database" {
				if got := countRows(t, a, "SELECT count(*) FROM subsonic_credentials WHERE user_id=$1", id); got != 0 {
					t.Fatalf("数据库改密后应删除该用户的副本，实际仍有 %d 行", got)
				}
				requireSubsonicFailure(t, ping(next, "token"), 200, 40)
			} else {
				requireSubsonicOK(t, ping(next, "token"))
			}
			for _, mode := range modes {
				t.Run("old-"+mode, func(t *testing.T) {
					requireSubsonicFailure(t, ping(subsonicPassword, mode), 200, 40)
				})
			}
			if operation == "database" {
				requireSubsonicOK(t, ping(next, "plain"))
			}
		})
	}
}
