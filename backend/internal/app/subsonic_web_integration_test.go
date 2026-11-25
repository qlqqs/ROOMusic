package app

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/diagnostics"
	"github.com/qlqq/roomusic/backend/internal/platform/secret"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

func TestPostgreSQLSubsonicWebSettings(t *testing.T) {
	a := newSubsonicTestApplication(t)
	admin := setupSubsonicAdmin(t, a)
	const name = "subsonic-web-listener"
	member := seedSubsonicMember(t, a, name)
	samples := newAPISampleRecorder(t, a)
	samples.scope = "subsonic"
	for _, value := range []string{subsonicPassword, subsonicWrongPass, admin.token, member.token} {
		samples.forbid(value)
	}
	validPassword := map[string]string{"password": subsonicPassword}
	disabled := map[string]bool{"enabled": false}
	call := func(method, path string, body any, user *integrationTestUser, origin string) *httptest.ResponseRecorder {
		return a.request(t, method, path, body, user, map[string]string{"Origin": origin, "X-Request-ID": sampleRequestID})
	}

	for _, route := range []struct {
		name, method, path string
		body               any
	}{
		{"me", http.MethodGet, subsonicMePath, nil},
		{"confirm", http.MethodPost, subsonicConfirmPath, validPassword},
		{"admin-read", http.MethodGet, subsonicAdminPath, nil},
		{"admin-write", http.MethodPut, subsonicAdminPath, disabled},
	} {
		t.Run(route.name, func(t *testing.T) {
			response := call(route.method, route.path, route.body, nil, a.origin)
			requireAPIError(t, response, http.StatusUnauthorized, "unauthorized")
			if route.path == subsonicAdminPath {
				requireAPIError(t, call(route.method, route.path, route.body, &member, a.origin), http.StatusForbidden, "forbidden")
			}
		})
	}

	for _, origin := range []struct{ value, code string }{
		{"", "origin_required"},
		{"https://not-roomusic.example", "origin_forbidden"},
	} {
		t.Run(origin.code, func(t *testing.T) {
			requireAPIError(t, call(http.MethodPost, subsonicConfirmPath, validPassword, &member, origin.value), 403, origin.code)
			requireAPIError(t, call(http.MethodPut, subsonicAdminPath, disabled, &admin, origin.value), 403, origin.code)
		})
	}
	requireSubsonicCredentialCount(t, a, 1)
	samples.record("subsonic/admin-get", call(http.MethodGet, subsonicAdminPath, nil, &admin, a.origin), http.StatusOK)
	samples.record("subsonic/me-needs-confirmation", requireSubsonicMe(t, a, &member, name, true, false), http.StatusOK)

	for _, test := range []struct {
		name string
		body map[string]any
	}{
		{"missing", map[string]any{}},
		{"number", map[string]any{"password": 123}},
		{"null", map[string]any{"password": nil}},
	} {
		t.Run("invalid-password-"+test.name, func(t *testing.T) {
			response := call(http.MethodPost, subsonicConfirmPath, test.body, &member, a.origin)
			requireAPIError(t, response, http.StatusBadRequest, "invalid_input")
			if test.name == "missing" {
				samples.record("subsonic/errors/invalid-input", response, http.StatusBadRequest)
			}
		})
	}
	for _, test := range []struct {
		body map[string]any
		code string
	}{
		{map[string]any{}, "invalid_input"},
		{map[string]any{"enabled": nil}, "invalid_input"},
		{map[string]any{"enabled": "true"}, "invalid_json"},
	} {
		requireAPIError(t, call(http.MethodPut, subsonicAdminPath, test.body, &admin, a.origin), 400, test.code)
	}

	confirmation := call(http.MethodPost, subsonicConfirmPath, validPassword, &member, a.origin)
	samples.record("subsonic/confirm-password", confirmation, http.StatusNoContent)
	samples.record("subsonic/me-ready", requireSubsonicMe(t, a, &member, name, true, true), http.StatusOK)
	device := registerJukeboxDevice(t, a, &member)
	selection := map[string]any{"jukebox_zone_id": device.Zone.ZoneID}
	samples.record("subsonic/me-put", call(http.MethodPut, subsonicMePath, selection, &member, a.origin), http.StatusOK)
	unknown := map[string]any{"jukebox_zone_id": uuid.New()}
	samples.record("subsonic/errors/jukebox-zone-not-found", call(http.MethodPut, subsonicMePath, unknown, &member, a.origin), http.StatusNotFound)
	for i := range 5 {
		response := call(http.MethodPost, subsonicConfirmPath, map[string]string{"password": subsonicWrongPass}, &member, a.origin)
		requireAPIError(t, response, http.StatusForbidden, "current_password_incorrect")
		if i == 0 {
			samples.record("subsonic/errors/current-password-incorrect", response, http.StatusForbidden)
		}
	}
	limited := call(http.MethodPost, subsonicConfirmPath, validPassword, &member, a.origin)
	requireTooManyAttempts(t, limited, "60")
	samples.record("subsonic/errors/too-many-attempts", limited, http.StatusTooManyRequests)

	samples.record("subsonic/admin-put", call(http.MethodPut, subsonicAdminPath, disabled, &admin, a.origin), http.StatusOK)
	response := call(http.MethodPost, subsonicConfirmPath, validPassword, &member, a.origin)
	requireAPIError(t, response, http.StatusConflict, "subsonic_disabled")
	samples.record("subsonic/errors/subsonic-disabled", response, http.StatusConflict)
	samples.finish()
}

func registerJukeboxDevice(t *testing.T, a *integrationTestApplication, user *integrationTestUser) zones.RegisterDeviceResult {
	t.Helper()
	body := map[string]any{"name": "合成点唱机设备", "kind": "browser", "volume": 0.75, "muted": false}
	response := a.request(t, http.MethodPost, zonesSamplePrefix+"/devices", body, user, nil)
	return zonesSampleDecode[zones.RegisterDeviceResult](t, response, http.StatusCreated)
}

func TestPostgreSQLSubsonicDiagnosticsHideCredentials(t *testing.T) {
	logger, logs := jsonLogger()
	a := newIntegrationTestApplicationWithLogger(t, logger)
	admin := setupSubsonicAdmin(t, a)
	const password = "subsonic-log-fixture+&-password"
	response := a.request(t, http.MethodPost, "/api/v1/users", accountBody("subsonic-log-listener", password), &admin, nil)
	requireIdentityBody(t, response, http.StatusCreated, "subsonic-log-listener", "user")

	want := make(map[string]string)
	for _, test := range []struct{ method, endpoint, mode string }{
		{http.MethodGet, "ping.view", "plain"},
		{http.MethodGet, "getLicense", "hex"},
		{http.MethodPost, "getMusicFolders", "token"},
	} {
		params := subsonicParameters("subsonic-log-listener", password, test.mode)
		response := requestSubsonic(a, test.method, test.endpoint, params)
		requireSubsonicOK(t, response)
		id := response.Header().Get("X-Request-ID")
		if _, exists := want[id]; id == "" || exists {
			t.Fatal("Subsonic 请求缺少唯一的 X-Request-ID")
		}
		want[id] = strings.TrimSuffix(test.endpoint, ".view")
	}

	export := a.request(t, http.MethodGet, "/api/v1/admin/diagnostics", nil, &admin, nil)
	if export.Code != http.StatusOK {
		t.Fatalf("诊断导出返回 HTTP %d，期望 200", export.Code)
	}
	for _, value := range []struct{ name, content string }{
		{"p 明文", password},
		{"p 编码", hex.EncodeToString([]byte(password))},
		{"t", subsonicToken(password, subsonicSalt)},
		{"s", subsonicSalt},
	} {
		for _, output := range []struct{ name, content string }{
			{"请求日志", logs.String()},
			{"诊断导出", export.Body.String()},
		} {
			// 同时检查原值、JSON 转义值和 URL 编码值。
			encoded, err := json.Marshal(value.content)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(output.content, value.content) ||
				strings.Contains(output.content, string(encoded[1:len(encoded)-1])) ||
				strings.Contains(output.content, url.QueryEscape(value.content)) {
				t.Errorf("%s 包含 %s 的凭据值", output.name, value.name)
			}
		}
	}

	checkEvents := func(label string, events []map[string]any) {
		t.Helper()
		seen := make(map[string]bool)
		for _, event := range events {
			id, _ := event["request_id"].(string)
			endpoint, ok := want[id]
			if !ok {
				continue
			}
			if seen[id] || event["route_template"] != "/rest/" || event["subsonic_endpoint"] != endpoint {
				t.Errorf("%s 中请求 %s 的路由为 %v、接口名为 %v，期望 /rest/ 和 %s 各记录一次",
					label, id, event["route_template"], event["subsonic_endpoint"], endpoint)
			}
			seen[id] = true
		}
		if len(seen) != len(want) {
			t.Errorf("%s 有 %d 条对应的请求完成事件，期望 %d 条", label, len(seen), len(want))
		}
	}
	checkEvents("请求日志", logEvents(t, logs, "http.request.completed"))
	var report struct {
		Logs []diagnostics.LogRecord `json:"logs"`
	}
	if err := json.Unmarshal(export.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	var events []map[string]any
	for _, record := range report.Logs {
		if record.Fields["event"] == "http.request.completed" {
			events = append(events, record.Fields)
		}
	}
	checkEvents("诊断导出", events)
}

func TestPostgreSQLSubsonicSealFailurePreservesIdentity(t *testing.T) {
	logger, logs := jsonLogger()
	a := newIntegrationTestApplicationWithLogger(t, logger)
	admin := a.createUser(t, "admin")
	keyPath := filepath.Join(a.dataDirectory, "steward", "instance.key")
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("invalid synthetic key"), 0o600); err != nil {
		t.Fatal(err)
	}

	warnings := 0
	checkWarning := func(id string) {
		t.Helper()
		warnings++
		events := logEvents(t, logs, "subsonic.credential.seal_failed")
		if len(events) != warnings {
			t.Fatalf("加密失败 WARN 日志共 %d 条，期望 %d 条", len(events), warnings)
		}
		event := events[warnings-1]
		if event["level"] != "WARN" || event["module"] != "subsonic" || event["user_id"] != id || event["error_kind"] != "secret_unavailable" {
			t.Error("加密失败日志的级别、模块、用户或错误分类不符")
		}
	}
	const (
		name            = "subsonic-bad-key-member"
		changedPassword = "subsonic-change-with-bad-key"
		resetPassword   = "subsonic-reset-with-bad-key"
	)
	id := createSubsonicMember(t, a, &admin, name)
	checkWarning(id)
	requireSubsonicCredentialCount(t, a, 0)
	member := loginPasswordUser(t, a, name, subsonicPassword, "user")
	checkWarning(id)
	requireSubsonicCredentialCount(t, a, 0)

	// 用独立测试密钥生成已有副本，验证改密时旧副本仍会被删除。
	store := secret.New(filepath.Join(a.dataDirectory, "steward", "fixture.key"))
	var ciphertexts [][]byte
	seedCredential := func(password string) {
		t.Helper()
		sealed, err := store.Seal(id, "subsonic-password", []byte(password))
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, a, `INSERT INTO subsonic_credentials(user_id, sealed) VALUES($1,$2)`, id, sealed)
		ciphertexts = append(ciphertexts, sealed)
	}
	seedCredential(subsonicPassword)
	response := a.request(t, http.MethodPost, "/api/v1/auth/password", ownPasswordBody(subsonicPassword, changedPassword), &member, nil)
	requirePasswordSuccess(t, response, "password_changed")
	checkWarning(id)
	requireSubsonicCredentialCount(t, a, 0)
	requireSubsonicOK(t, requestSubsonic(a, http.MethodGet, "ping", subsonicParameters(name, changedPassword, "plain")))

	seedCredential(changedPassword)
	response = a.request(t, http.MethodPost, "/api/v1/users/"+id+"/password", map[string]string{"password": resetPassword}, &admin, nil)
	requirePasswordSuccess(t, response, "password_reset")
	checkWarning(id)
	requireSubsonicCredentialCount(t, a, 0)
	requireSubsonicOK(t, requestSubsonic(a, http.MethodGet, "ping", subsonicParameters(name, resetPassword, "plain")))

	// 管理员重置会撤销成员会话。另一个保有合成会话的成员用来验证确认入口。
	confirmation := seedSubsonicMember(t, a, "subsonic-confirm-bad-key")
	response = a.request(t, http.MethodPost, subsonicConfirmPath, map[string]string{"password": subsonicPassword}, &confirmation, nil)
	requireAPIError(t, response, 503, "database_unavailable")
	requireSubsonicCredentialCount(t, a, 0)
	requireSubsonicMe(t, a, &confirmation, "subsonic-confirm-bad-key", true, false)

	output := logs.String()
	for _, password := range []string{subsonicPassword, changedPassword, resetPassword} {
		if strings.Contains(output, password) {
			t.Error("加密失败日志包含密码")
		}
	}
	for _, sealed := range ciphertexts {
		if strings.Contains(output, hex.EncodeToString(sealed)) || strings.Contains(output, base64.StdEncoding.EncodeToString(sealed)) {
			t.Error("加密失败日志包含密码副本密文")
		}
	}
}
