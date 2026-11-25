package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/diagnostics"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

const (
	diagnosticsPath       = "/api/v1/admin/diagnostics"
	clientErrorsPath      = "/api/v1/diagnostics/client-errors"
	diagnosticsTestOrigin = "http://roomusic.test"
	diagnosticsRequestID  = "diagnostics-test-request"
	adminSession          = "synthetic-admin-session"
	memberSession         = "synthetic-member-session"
	secondMemberSession   = "synthetic-member-second-session"
	otherUserSession      = "synthetic-other-user-session"
	memberID              = "10000000-0000-4000-8000-000000000002"
	clientErrorMessage    = "合成页面报错正文"
	clientErrorBody       = `{"kind":"render","name":"TypeError","message":"` + clientErrorMessage + `","route":"/albums/$groupId"}`
)

func TestAdminDiagnosticsAuthorization(t *testing.T) {
	fixture := newDiagnosticsFixture(t)
	for _, tc := range []struct {
		name, session string
		status        int
		code          string
	}{
		{"anonymous", "", http.StatusUnauthorized, "unauthorized"},
		{"member", memberSession, http.StatusForbidden, "forbidden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := fixture.serve(diagnosticsRequest(t, http.MethodGet, diagnosticsPath, tc.session, ""))
			requireDiagnosticsError(t, response, tc.status, tc.code)
		})
	}
}

func TestAdminDiagnosticsExport(t *testing.T) {
	fixture := newDiagnosticsFixture(t)
	fixture.service.DataDirectory = t.TempDir()
	fixture.service.BackupDirectory = t.TempDir()
	logger := slog.New(diagnostics.NewHandler(slog.NewJSONHandler(io.Discard, nil), fixture.service.Logs))
	for i := range 205 {
		logger.Info("synthetic diagnostic log", "count", i)
	}

	response := fixture.serve(diagnosticsRequest(t, http.MethodGet, diagnosticsPath, adminSession, ""))
	body := readDiagnosticsExport(t, response)
	if len(body.Disks) != 2 {
		t.Fatalf("disks 有 %d 项，期望数据和备份目录", len(body.Disks))
	}
	directories := map[string]string{"data": fixture.service.DataDirectory, "backup": fixture.service.BackupDirectory}
	for _, disk := range body.Disks {
		directory, ok := directories[disk.Kind]
		if !ok || disk.Name != filepath.Base(directory) || disk.TotalBytes == nil || disk.AvailableBytes == nil || disk.ErrorCode != "" {
			t.Errorf("磁盘信息不符：%+v", disk)
		}
		delete(directories, disk.Kind)
	}
	if len(body.Logs) != 200 {
		t.Fatalf("logs 有 %d 条，期望 200 条", len(body.Logs))
	}
	for i, record := range body.Logs {
		if want := float64(204 - i); record.Fields["count"] != want || record.Time.IsZero() || record.Level != "INFO" {
			t.Fatalf("logs[%d] = %+v，期望 count=%v、时间和 INFO 级别", i, record, want)
		}
	}
}

func TestAdminDiagnosticsDiskListUnavailable(t *testing.T) {
	fixture := newDiagnosticsFixture(t)
	const privateError = "synthetic disk error /private/music"
	fixture.service.MusicDirectories = func(context.Context) ([]diagnostics.Directory, error) {
		return nil, errors.New(privateError)
	}

	response := fixture.serve(diagnosticsRequest(t, http.MethodGet, diagnosticsPath, adminSession, ""))
	body := readDiagnosticsExport(t, response)
	if body.Disks == nil || len(body.Disks) != 0 || body.Logs == nil || len(body.Logs) != 0 {
		t.Fatalf("期望 disks 和 logs 都是空数组，得到 disks=%#v logs=%#v", body.Disks, body.Logs)
	}
	if !slices.Contains(body.Status.Errors, "disk_unavailable") || !slices.Contains(body.Status.Errors, "database_unavailable") {
		t.Errorf("status.errors = %v，期望保留数据库错误并追加磁盘错误", body.Status.Errors)
	}
	if strings.Contains(response.Body.String(), privateError) {
		t.Error("诊断导出泄露磁盘错误原文")
	}
}

func TestAdminDiagnosticsCanceledSystemStatus(t *testing.T) {
	fixture := newDiagnosticsFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	fixture.service.Database = openDiagnosticsTestDB(t, diagnosticsTestDB{ping: func(pingCtx context.Context) error {
		// 会话校验完成、系统状态开始探测数据库时取消请求。
		cancel()
		return pingCtx.Err()
	}})

	request := diagnosticsRequest(t, http.MethodGet, diagnosticsPath, adminSession, "").WithContext(ctx)
	response := fixture.serve(request)
	detail := requireDiagnosticsError(t, response, http.StatusServiceUnavailable, "diagnostics_unavailable")
	if detail.Message != "暂时无法读取诊断信息" {
		t.Errorf("诊断不可用错误消息 = %q", detail.Message)
	}
}

func TestClientErrorOriginAndAuthentication(t *testing.T) {
	fixture := newDiagnosticsFixture(t)
	for _, tc := range []struct {
		name, origin, session string
		status                int
		code                  string
	}{
		{"cross origin", "http://other.test", memberSession, http.StatusForbidden, "origin_forbidden"},
		{"origin checked before authentication", "http://other.test", "", http.StatusForbidden, "origin_forbidden"},
		{"missing origin", "", memberSession, http.StatusForbidden, "origin_required"},
		{"anonymous", diagnosticsTestOrigin, "", http.StatusUnauthorized, "unauthorized"},
		{"invalid session", diagnosticsTestOrigin, "synthetic-invalid-session", http.StatusUnauthorized, "unauthorized"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := diagnosticsRequest(t, http.MethodPost, clientErrorsPath, tc.session, clientErrorBody)
			request.Header.Set("Origin", tc.origin)
			requireDiagnosticsError(t, fixture.serve(request), tc.status, tc.code)
		})
	}
}

func TestClientErrorValidation(t *testing.T) {
	fixture := newDiagnosticsFixture(t)
	const messageBody = `{"kind":"error","name":"TypeError","message":"%s","route":"/"}`
	const bodyLimit = 4 * 1024
	maxBody := clientErrorBody + strings.Repeat(" ", bodyLimit-len(clientErrorBody))
	for _, tc := range []struct{ name, body, code string }{
		{"unknown field", strings.TrimSuffix(clientErrorBody, "}") + `,"stack":"synthetic"}`, "invalid_json"},
		{"duplicate field", strings.TrimSuffix(clientErrorBody, "}") + `,"kind":"error"}`, "invalid_json"},
		{"trailing JSON", clientErrorBody + `{}`, "invalid_json"},
		{"invalid UTF-8", fmt.Sprintf(messageBody, "\xff"), "invalid_json"},
		{"4097 bytes", maxBody + " ", "invalid_json"},
		{"invalid kind", `{"kind":"warn","name":"TypeError","message":"","route":"/"}`, "invalid_client_error"},
		{"invalid name", `{"kind":"error","name":"1TypeError","message":"","route":"/"}`, "invalid_client_error"},
		{"missing message", `{"kind":"error","name":"TypeError","route":"/"}`, "invalid_client_error"},
		{"non-string message", `{"kind":"error","name":"TypeError","message":null,"route":"/"}`, "invalid_client_error"},
		{"501 Chinese characters", fmt.Sprintf(messageBody, strings.Repeat("错", 501)), "invalid_client_error"},
		{"invalid route", `{"kind":"error","name":"TypeError","message":"","route":"/albums?tab=all"}`, "invalid_client_error"},
		{"4096 bytes", maxBody, ""},
		{"500 Chinese characters", fmt.Sprintf(messageBody, strings.Repeat("错", 500)), ""},
		{"empty message", fmt.Sprintf(messageBody, ""), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := fixture.stdout.Len()
			response := fixture.serve(diagnosticsRequest(t, http.MethodPost, clientErrorsPath, memberSession, tc.body))
			if tc.code == "" {
				requireClientErrorAccepted(t, response)
				return
			}
			requireDiagnosticsError(t, response, http.StatusBadRequest, tc.code)
			if fixture.stdout.Len() != before {
				t.Error("无效上报产生日志")
			}
		})
	}
}

func TestClientErrorRateLimitByUser(t *testing.T) {
	fixture := newDiagnosticsFixture(t)
	send := func(session string) *httptest.ResponseRecorder {
		return fixture.serve(diagnosticsRequest(t, http.MethodPost, clientErrorsPath, session, clientErrorBody))
	}
	for range 19 {
		requireClientErrorAccepted(t, send(memberSession))
	}
	requireClientErrorAccepted(t, send(secondMemberSession))

	before := fixture.stdout.Len()
	requireDiagnosticsError(t, send(memberSession), http.StatusTooManyRequests, "client_errors_throttled")
	if fixture.stdout.Len() != before {
		t.Error("被限流的第 21 条上报产生日志")
	}
	if got := len(fixture.service.Logs.Recent(diagnostics.LogFilter{Level: "WARN"})); got != 20 {
		t.Errorf("限流后日志环有 %d 条 WARN，期望 20 条", got)
	}
	requireClientErrorAccepted(t, send(otherUserSession))
}

func TestClientErrorLogging(t *testing.T) {
	fixture := newDiagnosticsFixture(t)
	response := fixture.serve(diagnosticsRequest(t, http.MethodPost, clientErrorsPath, memberSession, clientErrorBody))
	requireClientErrorAccepted(t, response)

	var stdout map[string]any
	if err := json.Unmarshal(fixture.stdout.Bytes(), &stdout); err != nil {
		t.Fatalf("期望恰好一条 JSON 日志，得到 %q：%v", fixture.stdout.Bytes(), err)
	}
	if stdout["level"] != "WARN" || stdout["msg"] != "client error reported" || stdout["message"] != clientErrorMessage {
		t.Errorf("stdout 日志的级别、固定消息或上报正文不符：%v", stdout)
	}

	records := fixture.service.Logs.Recent(diagnostics.LogFilter{})
	if len(records) != 1 {
		t.Fatalf("日志环记录数 = %d，期望 1", len(records))
	}
	record := records[0]
	if record.Level != "WARN" || record.Fields["msg"] != "client error reported" {
		t.Errorf("日志环的级别或固定消息不符：%+v", record)
	}
	if _, ok := record.Fields["message"]; ok {
		t.Error("日志环保留了自由正文 message 字段")
	}
	for key, value := range record.Fields {
		if text, ok := value.(string); ok && strings.Contains(text, clientErrorMessage) {
			t.Errorf("日志环的 %q 字段含上报正文", key)
		}
	}
	for key, want := range map[string]string{
		"event":        "client.error.reported",
		"module":       "frontend",
		"kind":         "render",
		"error_kind":   "TypeError",
		"actor_id":     memberID,
		"client_route": "/albums/$groupId",
		"request_id":   diagnosticsRequestID,
	} {
		if stdout[key] != want || record.Fields[key] != want {
			t.Errorf("%s 的值为 stdout=%q、日志环=%q，期望 %q", key, stdout[key], record.Fields[key], want)
		}
	}
}

type diagnosticsExport struct {
	GeneratedAt string                  `json:"generated_at"`
	Status      *diagnostics.Status     `json:"status"`
	Disks       []diagnostics.DiskUsage `json:"disks"`
	Logs        []diagnostics.LogRecord `json:"logs"`
}

func readDiagnosticsExport(t *testing.T, response *httptest.ResponseRecorder) diagnosticsExport {
	t.Helper()
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("诊断导出返回 HTTP %d，响应头 %v，正文 %s", response.Code, response.Header(), response.Body)
	}
	var body diagnosticsExport
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(time.RFC3339, body.GeneratedAt); err != nil || !strings.HasSuffix(body.GeneratedAt, "Z") {
		t.Errorf("generated_at = %q，期望 RFC 3339 UTC", body.GeneratedAt)
	}
	if body.Status == nil || body.Status.Build["version"] == "" || body.Status.StartedAt.IsZero() || body.Status.Errors == nil {
		t.Fatalf("status 缺少系统状态字段：%+v", body.Status)
	}
	return body
}

func requireDiagnosticsError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) httpserver.APIErrorDetail {
	t.Helper()
	if response.Code != status || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("期望 HTTP %d JSON，得到 HTTP %d，响应头 %v，正文 %s", status, response.Code, response.Header(), response.Body)
	}
	var body httpserver.APIError
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != code || body.Error.Message == "" || body.RequestID != diagnosticsRequestID || response.Header().Get("X-Request-ID") != body.RequestID {
		t.Errorf("错误信封为 %+v，期望错误码 %q、错误消息和请求 ID %q", body, code, diagnosticsRequestID)
	}
	return body.Error
}

func requireClientErrorAccepted(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf("期望 204 空响应，得到 HTTP %d，正文 %q", response.Code, response.Body.Bytes())
	}
}

type diagnosticsFixture struct {
	handler http.Handler
	service *diagnostics.Service
	stdout  *bytes.Buffer
}

func newDiagnosticsFixture(t *testing.T) diagnosticsFixture {
	t.Helper()
	member := identity.User{ID: memberID, Username: "member", Role: "user"}
	sessions := map[string]identity.User{
		adminSession:        {ID: "10000000-0000-4000-8000-000000000001", Username: "admin", Role: "admin"},
		memberSession:       member,
		secondMemberSession: member,
		otherUserSession:    {ID: "10000000-0000-4000-8000-000000000003", Username: "other", Role: "user"},
	}
	hashedSessions := make(map[string]identity.User, len(sessions))
	for token, user := range sessions {
		hashedSessions[string(identity.HashSessionToken(token))] = user
	}
	db := openDiagnosticsTestDB(t, diagnosticsTestDB{sessions: hashedSessions})
	discard := slog.New(slog.NewJSONHandler(io.Discard, nil))
	guard := identity.NewGuardWithSameHost(db).WithLogger(discard)
	service := &diagnostics.Service{
		StartedAt: time.Now(),
		Logs:      diagnostics.NewLogRing(),
		MusicDirectories: func(context.Context) ([]diagnostics.Directory, error) {
			return nil, nil
		},
	}
	stdout := &bytes.Buffer{}
	logger := slog.New(diagnostics.NewHandler(slog.NewJSONHandler(stdout, nil), service.Logs))
	mux := http.NewServeMux()
	Register(mux, guard, service, logger)
	// 保留真实请求 ID 中间件，单独捕获页面报错日志。
	return diagnosticsFixture{handler: httpserver.Middleware(discard, mux), service: service, stdout: stdout}
}

func (f diagnosticsFixture) serve(request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

func diagnosticsRequest(t *testing.T, method, path, session, body string) *http.Request {
	request := httptest.NewRequestWithContext(t.Context(), method, diagnosticsTestOrigin+path, strings.NewReader(body))
	request.Header.Set("X-Request-ID", diagnosticsRequestID)
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", diagnosticsTestOrigin)
	}
	if session != "" {
		request.AddCookie(&http.Cookie{Name: identity.SessionCookieName, Value: session})
	}
	return request
}

// diagnosticsTestDB 在不连接外部数据库的情况下提供会话查询和状态探测替身。
type diagnosticsTestDB struct {
	sessions map[string]identity.User
	ping     func(context.Context) error
}

func openDiagnosticsTestDB(t *testing.T, connector diagnosticsTestDB) *sql.DB {
	t.Helper()
	db := sql.OpenDB(connector)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

func (db diagnosticsTestDB) Connect(context.Context) (driver.Conn, error) {
	return diagnosticsTestConn{db}, nil
}

func (db diagnosticsTestDB) Driver() driver.Driver { return db }

func (db diagnosticsTestDB) Open(string) (driver.Conn, error) {
	return db.Connect(context.Background())
}

type diagnosticsTestConn struct{ diagnosticsTestDB }

func (diagnosticsTestConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (diagnosticsTestConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }
func (diagnosticsTestConn) Close() error                        { return nil }

func (c diagnosticsTestConn) Ping(ctx context.Context) error {
	if c.ping != nil {
		return c.ping(ctx)
	}
	return nil
}

func (c diagnosticsTestConn) QueryContext(_ context.Context, _ string, args []driver.NamedValue) (driver.Rows, error) {
	if len(args) != 1 {
		return nil, errors.New("会话查询需要一个令牌哈希参数")
	}
	hash, ok := args[0].Value.([]byte)
	if !ok {
		return nil, errors.New("会话令牌哈希必须为字节切片")
	}
	rows := &diagnosticsSessionRows{}
	if user, ok := c.sessions[string(hash)]; ok {
		rows.values = []driver.Value{user.ID, user.Username, user.Role}
	}
	return rows, nil
}

type diagnosticsSessionRows struct{ values []driver.Value }

func (*diagnosticsSessionRows) Columns() []string { return []string{"id", "username", "role"} }
func (*diagnosticsSessionRows) Close() error      { return nil }

func (r *diagnosticsSessionRows) Next(dest []driver.Value) error {
	if len(r.values) == 0 {
		return io.EOF
	}
	copy(dest, r.values)
	r.values = nil
	return nil
}
