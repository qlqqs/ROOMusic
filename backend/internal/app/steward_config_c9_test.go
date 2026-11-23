package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/qlqq/roomusic/backend/internal/backup"
	backupapi "github.com/qlqq/roomusic/backend/internal/backup/httpapi"
	backuptools "github.com/qlqq/roomusic/backend/internal/backup/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/dspsettings"
	dsptools "github.com/qlqq/roomusic/backend/internal/dspsettings/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/identity"
	identityapi "github.com/qlqq/roomusic/backend/internal/identity/httpapi"
	identitytools "github.com/qlqq/roomusic/backend/internal/identity/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/library"
	libraryapi "github.com/qlqq/roomusic/backend/internal/library/httpapi"
	librarytools "github.com/qlqq/roomusic/backend/internal/library/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/platform/diagnostics"
	diagnostictools "github.com/qlqq/roomusic/backend/internal/platform/diagnostics/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/playback"
	"github.com/qlqq/roomusic/backend/internal/playback/analysis"
	playbacktools "github.com/qlqq/roomusic/backend/internal/playback/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/steward/token"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
	"github.com/qlqq/roomusic/backend/internal/zones"
	zonestools "github.com/qlqq/roomusic/backend/internal/zones/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/zones/zonestest"
)

type c9Buffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *c9Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}
func (b *c9Buffer) text() string { b.mu.Lock(); defer b.mu.Unlock(); return b.Buffer.String() }

type c9BackupRunner struct{}

func (c9BackupRunner) Run(_ context.Context, c backup.Command) error {
	if len(c.Args) == 1 && c.Args[0] == "--version" {
		_, err := io.WriteString(c.Stdout, "pg (PostgreSQL) 18.1")
		return err
	}
	if strings.Contains(filepath.Base(c.Path), "pg_dump") {
		_, err := io.WriteString(c.Stdout, "PGDMP-c9-synthetic")
		return err
	}
	return nil
}

type c9BackupDatabase struct{}

func (c9BackupDatabase) Lock(context.Context, *sql.DB, bool) (func(), bool, error) {
	return func() {}, true, nil
}
func (c9BackupDatabase) ServerMajor(context.Context, *sql.DB) (int, error) { return 18, nil }

type c9Fixture struct {
	*c3AppFixture
	dir, music, backupDir string
	scans                 *library.Service
	zones                 *zones.Service
	dsp                   *dspsettings.Service
	backups               *backup.Service
	diagnostics           *diagnostics.Service
	defs                  []tool.Definition
	logs                  *c9Buffer
}

func newC9Fixture(t *testing.T) *c9Fixture {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	db, err := postgres.Open(ctx, postgrestest.NewSchema(t, "steward_c9_"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dir := t.TempDir()
	music := filepath.Join(dir, "synthetic-music")
	backupDir := filepath.Join(dir, "synthetic-backups")
	for _, p := range []string{music, backupDir} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	base := &c3AppFixture{db: db, mux: http.NewServeMux(), users: map[string]identity.User{}, cookies: map[string]string{}, registry: tool.NewRegistry()}
	f := &c9Fixture{c3AppFixture: base, dir: dir, music: music, backupDir: backupDir, logs: &c9Buffer{}}
	for _, role := range []string{"admin", "user", "other"} {
		user := identity.User{ID: uuid.New(), Username: "c9-" + role, Role: "user"}
		if role == "admin" {
			user.Role = "admin"
		}
		base.users[role] = user
		base.cookies[role] = identity.GenerateSessionToken()
		f.exec(t, `INSERT INTO users(id,username,password_hash,role) VALUES($1,$2,'c9-private-hash',$3)`, user.ID, user.Username, user.Role)
		f.exec(t, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '1 hour')`, identity.HashSessionToken(base.cookies[role]), user.ID)
	}
	ring := diagnostics.NewLogRing(dir, music)
	logger := slog.New(diagnostics.NewHandler(slog.NewJSONHandler(f.logs, nil), ring))
	f.scans = library.NewService(ctx, db, dir, logger)
	f.zones = zones.NewService(db, zones.Options{Renderer: zonestest.NewRenderer(zonestest.Options{})})
	t.Cleanup(f.zones.Close)
	f.dsp = dspsettings.NewService(db)
	f.backups = backup.NewService(config.Backup{Directory: backupDir, DatabaseURL: "postgres://synthetic:synthetic@127.0.0.1/c9", Location: time.UTC}, logger, backup.WithRunner(c9BackupRunner{}), backup.WithDatabaseAccess(c9BackupDatabase{}))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := f.backups.Wait(ctx); err != nil {
			t.Error(err)
		}
	})
	audio := playback.NewService(db, []string{music})
	progress := playback.NewAnalysisProgressProvider(db, analysis.Version, nil)
	f.diagnostics = newDiagnostics(db, config.Server{DataDirectory: dir, AllowedLibraryRoots: []string{music}, Backup: config.Backup{Directory: backupDir}}, f.scans, options{diagnosticLogs: ring, startedAt: time.Now().Add(-time.Minute)})
	cfg := stewardConfig{diagnostics: f.diagnostics, backups: f.backups, allowedRoots: []string{music}, audio: audio, progress: progress, dsp: f.dsp}
	guard := identity.NewGuardWithSameHost(db)
	base.tokens = token.NewStore(db, guard)
	registerSteward(ctx, base.mux, db, guard, newInstanceSecrets(dir), logger, f.scans, nil, f.zones, nil, nil, cfg)
	identityapi.New(db, guard, nil, logger).Register(base.mux)
	libraryapi.New(db, guard, f.scans, []string{music}, logger).Register(base.mux)
	backupapi.New(f.backups, db, guard, ctx).Register(base.mux)
	f.defs = append(f.defs, diagnostictools.StewardTools(f.diagnostics)...)
	f.defs = append(f.defs, backuptools.StewardTools(f.backups, db, logger)...)
	f.defs = append(f.defs, librarytools.ConfigTools(f.scans, []string{music}, logger)...)
	f.defs = append(f.defs, identitytools.StewardTools(db, logger)...)
	f.defs = append(f.defs, playbacktools.StewardTools(audio, catalog.NewStore(db), progress)...)
	f.defs = append(f.defs, zonestools.SettingsTools(f.zones, logger)...)
	f.defs = append(f.defs, dsptools.StewardTools(f.dsp, f.zones, logger)...)
	for _, d := range f.defs {
		if err := f.registry.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	return f
}
func (f *c9Fixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := f.db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}
func (f *c9Fixture) caller(role string) tool.Caller {
	return tool.Caller{User: f.users[role], Source: tool.SourceChat, Mode: "operator", SharedExecution: true, ConversationID: uuid.New(), RequestID: "c9-request", IdempotencyKey: uuid.New()}
}
func c9JSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}
func c9Code(t *testing.T, err error, want string) {
	t.Helper()
	var e *tool.Error
	if !errors.As(err, &e) || string(e.Code) != want {
		t.Fatalf("err=%v want=%s", err, want)
	}
}
func (f *c9Fixture) invoke(t *testing.T, role, name string, input any, want string) json.RawMessage {
	t.Helper()
	r, err := f.registry.Invoke(context.Background(), f.caller(role), name, c9JSON(input))
	if want != "" {
		c9Code(t, err, want)
		return nil
	}
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return r.Output
}
func c9Object(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func c9Names(defs []tool.Definition) map[string]bool {
	out := map[string]bool{}
	for _, d := range defs {
		out[d.Name] = true
	}
	return out
}

func TestPostgreSQLC9ToolContractsAndDenials(t *testing.T) {
	f := newC9Fixture(t)
	if len(f.defs) != 24 {
		t.Fatalf("工具数=%d", len(f.defs))
	}
	counts := map[tool.Scope]int{}
	memberNames := c9Names(f.registry.List(f.caller("user")))
	for _, d := range f.defs {
		counts[d.Scope]++
		t.Run(d.Name, func(t *testing.T) {
			if d.Revertible {
				t.Error("C9 工具不应可撤销")
			}
			for _, mode := range []string{"assistant", "operator", "steward"} {
				caller := f.caller("user")
				caller.Mode = mode
				if d.MinRole == tool.RoleAdmin {
					if memberNames[d.Name] {
						t.Error("成员清单包含管理员工具")
					}
					_, err := f.registry.Invoke(context.Background(), caller, d.Name, c9JSON(map[string]any{}))
					c9Code(t, err, "permission_denied")
					_, err = f.registry.PreviewCall(context.Background(), caller, d.Name, c9JSON(map[string]any{}))
					c9Code(t, err, "permission_denied")
				}
			}
			_, err := f.registry.Invoke(context.Background(), f.caller("admin"), d.Name, json.RawMessage(`{"unknown":true}`))
			c9Code(t, err, "validation_failed")
			if d.Scope == tool.ScopeShared {
				if d.Preview == nil {
					t.Fatal("缺少预览")
				}
				caller := f.caller("admin")
				caller.SharedExecution = false
				_, err = f.registry.Invoke(context.Background(), caller, d.Name, json.RawMessage(`{}`))
				c9Code(t, err, "approval_required")
			}
		})
	}
	if counts[tool.ScopeRead] != 11 || counts[tool.ScopeShared] != 11 || counts[tool.ScopePersonal] != 2 {
		t.Fatal(counts)
	}
	for _, d := range f.registry.List(f.caller("admin")) {
		combined := strings.ToLower(d.Name + " " + d.Description)
		for _, bad := range []string{"reset_password", "delete_backup", "create_member", "relocate", "restore_backup", "run_sql", "run_shell", "新建成员", "重置密码", "恢复备份", "目录迁移", "执行 sql", "执行 shell"} {
			if strings.Contains(combined, bad) {
				t.Errorf("禁用能力 %s: %s", bad, d.Name)
			}
		}
	}
	registry := tool.NewRegistry()
	for _, d := range backuptools.StewardTools(nil, f.db, nil) {
		if err := registry.Register(d); err != nil {
			t.Fatal(err)
		}
		_, err := registry.Invoke(context.Background(), f.caller("admin"), d.Name, json.RawMessage(`{}`))
		c9Code(t, err, "permission_denied")
		_, err = registry.PreviewCall(context.Background(), f.caller("admin"), d.Name, json.RawMessage(`{}`))
		c9Code(t, err, "permission_denied")
	}
	if len(registry.List(f.caller("admin"))) != 0 {
		t.Fatal("未装配备份仍可见")
	}
}

func (f *c9Fixture) mcp(t *testing.T, server *httptest.Server, role string, scope token.Scope) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	_, plain, err := f.tokens.Issue(ctx, f.users[role].ID, token.IssueInput{Name: "C9 合成客户端", Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "c9-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: server.URL + "/api/v1/mcp", HTTPClient: &http.Client{Transport: c3BearerTransport{plain, server.Client().Transport}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}
func c9MCPCall(t *testing.T, s *sdk.ClientSession, name string, input any, want string) string {
	t.Helper()
	out, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: input})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Content) != 1 {
		t.Fatalf("MCP 结果=%+v", out)
	}
	raw := out.Content[0].(*sdk.TextContent).Text
	if want != "" {
		if !out.IsError || !strings.Contains(raw, `"code":"`+want+`"`) {
			t.Fatalf("%s: %s", name, raw)
		}
	} else if out.IsError {
		t.Fatalf("%s: %s", name, raw)
	}
	return raw
}

func TestPostgreSQLC9MCPRolesAndDirectExecution(t *testing.T) {
	f := newC9Fixture(t)
	server := httptest.NewServer(f.mux)
	defer server.Close()
	for _, role := range []string{"user", "admin"} {
		for _, scope := range []token.Scope{token.ScopeRead, token.ScopeReadWrite} {
			t.Run(role+"/"+string(scope), func(t *testing.T) {
				s := f.mcp(t, server, role, scope)
				listed, err := s.ListTools(context.Background(), nil)
				if err != nil {
					t.Fatal(err)
				}
				names := map[string]bool{}
				for _, d := range listed.Tools {
					names[d.Name] = true
				}
				for _, d := range f.defs {
					want := !(role == "user" && d.MinRole == tool.RoleAdmin) && !(scope == token.ScopeRead && d.Scope != tool.ScopeRead)
					if names[d.Name] != want {
						t.Errorf("工具 %s 可见=%v want=%v", d.Name, names[d.Name], want)
					}
					if role == "user" && d.MinRole == tool.RoleAdmin {
						c9MCPCall(t, s, d.Name, map[string]any{}, "permission_denied")
					}
				}
				if role == "admin" {
					if scope == token.ScopeRead {
						c9MCPCall(t, s, "set_scan_schedule", map[string]any{"enabled": true, "hour": 3}, "permission_denied")
					} else {
						c9MCPCall(t, s, "set_scan_schedule", map[string]any{"enabled": true, "hour": 3}, "")
						w := f.request("admin", "GET", "scan-schedule", "", nil)
						if w.Code != 200 || !strings.Contains(w.Body.String(), `"hour":3`) {
							t.Fatalf("MCP 写入未从 HTTP 读到: %d %s", w.Code, w.Body)
						}
					}
				}
			})
		}
	}
}

func TestPostgreSQLC9ConfigToolsSuccessPreviewAndBoundaries(t *testing.T) {
	f := newC9Fixture(t)
	ctx := context.Background()
	rootPath := filepath.Join(f.music, "合成目录")
	excluded := filepath.Join(rootPath, "不扫描")
	if err := os.MkdirAll(excluded, 0700); err != nil {
		t.Fatal(err)
	}
	invoke := func(name string, input any) json.RawMessage { return f.invoke(t, "admin", name, input, "") }
	preview := func(name string, input any) tool.Preview {
		t.Helper()
		snapshot, err := postgrestest.Snapshot(ctx, f.db)
		if err != nil {
			t.Fatal(err)
		}
		p, err := f.registry.PreviewCall(ctx, f.caller("admin"), name, c9JSON(input))
		if err != nil {
			t.Fatal(err)
		}
		after, err := postgrestest.Snapshot(ctx, f.db)
		if err != nil {
			t.Fatal(err)
		}
		if diff := postgrestest.Diff(snapshot, after); len(diff) != 0 {
			t.Fatalf("Preview 写入数据库 %s: %v", name, diff)
		}
		if p.Target == "" || len(p.Changes) == 0 {
			t.Fatalf("卡片无可见动作 %s: %+v", name, p)
		}
		return p
	}
	before, err := f.scans.LoadScanSchedule(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p := preview("set_scan_schedule", map[string]any{"enabled": true, "hour": 3})
	if !reflect.DeepEqual(p.Changes, []tool.Change{{Field: "enabled", Before: before.Enabled, After: true}, {Field: "hour", Before: before.Hour, After: 3}}) {
		t.Fatalf("旧新值=%+v", p)
	}
	invoke("set_scan_schedule", map[string]any{"enabled": true, "hour": 3})
	preview("add_library_root", map[string]any{"path": rootPath})
	raw := invoke("add_library_root", map[string]any{"path": rootPath})
	var added struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
	}
	if err := json.Unmarshal(raw, &added); err != nil {
		t.Fatal(err)
	}
	preview("add_root_exclusion", map[string]any{"root_id": added.ID, "path": excluded})
	invoke("add_root_exclusion", map[string]any{"root_id": added.ID, "path": excluded})
	roots := invoke("list_library_roots", map[string]any{})
	if !strings.Contains(string(roots), "不扫描") || strings.Contains(string(roots), f.dir) {
		t.Fatalf("目录列表=%s", roots)
	}
	preview("remove_root_exclusion", map[string]any{"root_id": added.ID, "path": "不扫描"})
	invoke("remove_root_exclusion", map[string]any{"root_id": added.ID, "path": "不扫描"})
	var revision int64
	if err := f.db.QueryRow(`SELECT revision FROM library_roots WHERE id=$1`, added.ID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	state := map[string]any{"root_id": added.ID, "expected_revision": revision, "enabled": false}
	p = preview("set_library_root_enabled", state)
	if len(p.Changes) != 1 || p.Changes[0].Before != "active" || p.Changes[0].After != "disabled" {
		t.Fatalf("状态预览=%+v", p)
	}
	invoke("set_library_root_enabled", state)
	state["enabled"] = true
	state["expected_revision"] = revision + 1
	invoke("set_library_root_enabled", state)
	f.invoke(t, "admin", "set_library_root_enabled", state, "revision_conflict")
	f.invoke(t, "admin", "add_library_root", map[string]any{"path": t.TempDir()}, "invalid_library_path")
	f.invoke(t, "admin", "add_root_exclusion", map[string]any{"root_id": added.ID, "path": t.TempDir()}, "invalid_library_path")
	f.invoke(t, "admin", "remove_root_exclusion", map[string]any{"root_id": added.ID, "path": "../outside"}, "invalid_input")
	f.invoke(t, "admin", "set_scan_schedule", map[string]any{"enabled": true, "hour": 24}, "validation_failed")
	for _, name := range []string{"get_system_status", "get_disk_usage", "list_recent_logs", "get_backup_overview", "get_scan_schedule", "list_members", "get_analysis_progress"} {
		raw := invoke(name, map[string]any{})
		for _, bad := range []string{f.dir, "postgres://", "c9-private-hash", "ROOMUSIC_"} {
			if strings.Contains(string(raw), bad) {
				t.Errorf("%s 泄露 %s", name, bad)
			}
		}
	}
	member := map[string]any{"member_id": f.users["user"].ID, "disabled": true}
	p = preview("set_member_disabled", member)
	if p.Changes[0].Before != false || p.Changes[0].After != true {
		t.Fatal(p)
	}
	invoke("set_member_disabled", member)
	if w := f.request("user", "GET", "auth/me", "", nil); w.Code != 401 {
		t.Fatalf("停用后会话仍有效: %d", w.Code)
	}
	member["disabled"] = false
	invoke("set_member_disabled", member)
	preview("revoke_member_sessions", map[string]any{"member_id": f.users["other"].ID})
	invoke("revoke_member_sessions", map[string]any{"member_id": f.users["other"].ID})
	if w := f.request("other", "GET", "auth/me", "", nil); w.Code != 401 {
		t.Fatalf("吊销未失效: %d", w.Code)
	}
	f.invoke(t, "admin", "set_member_disabled", map[string]any{"member_id": f.users["admin"].ID, "disabled": true}, "self_target_not_allowed")
	f.invoke(t, "admin", "set_member_disabled", map[string]any{"member_id": uuid.New(), "disabled": true}, "not_found")
	f.invoke(t, "admin", "revoke_member_sessions", map[string]any{"member_id": uuid.New()}, "not_found")
	retentionBefore, err := f.backups.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rule := map[string]any{"keep_days": 7, "keep_count": 2}
	p = preview("set_backup_retention", rule)
	if len(p.Changes) != 1 || !bytes.Equal(c9JSON(p.Changes[0].Before), c9JSON(retentionBefore.Retention)) || !reflect.DeepEqual(c9Object(t, c9JSON(p.Changes[0].After)), c9Object(t, c9JSON(rule))) {
		t.Fatal(p)
	}
	invoke("set_backup_retention", rule)
	f.invoke(t, "admin", "set_backup_retention", map[string]any{"keep_days": 0, "keep_count": 2}, "validation_failed")
	preview("start_backup", map[string]any{})
	invoke("start_backup", map[string]any{})
	// 持有扫描锁并插入合成活动任务，验证重复启动与取消，不启动真实扫描工作者。
	conn, err := f.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1::int,hashtext(current_schema()))`, int32(0x5343414e)); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1::int,hashtext(current_schema()))`, int32(0x5343414e))
	scan := uuid.New()
	f.exec(t, `INSERT INTO scan_runs(id,status,started_at) VALUES($1,'running',now())`, scan)
	preview("start_scan", map[string]any{})
	raw = invoke("start_scan", map[string]any{})
	if !strings.Contains(string(raw), `"started":false`) {
		t.Fatalf("未复用任务: %s", raw)
	}
	preview("cancel_scan", map[string]any{"scan_id": scan})
	raw = invoke("cancel_scan", map[string]any{"scan_id": scan})
	if !strings.Contains(string(raw), `"requested":true`) {
		t.Fatalf("未请求取消: %s", raw)
	}
	f.invoke(t, "admin", "cancel_scan", map[string]any{"scan_id": uuid.New()}, "not_found")
	f.exec(t, `UPDATE scan_runs SET status='canceled',finished_at=now() WHERE id=$1`, scan)
	for _, endpoint := range []struct{ path, contains string }{{"scan-schedule", `"hour":3`}, {"library-roots", "合成目录"}, {"users", f.users["user"].ID}, {"system/backups", `"keep_count":2`}} {
		w := f.request("admin", "GET", endpoint.path, "", nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), endpoint.contains) {
			t.Errorf("HTTP %s 未反映工具写入: %d %s", endpoint.path, w.Code, w.Body)
		}
	}
	events := map[string]bool{}
	for _, line := range strings.Split(f.logs.text(), "\n") {
		if line == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event["event"] != "steward.config.changed" {
			continue
		}
		name := event["tool"].(string)
		events[name] = true
		if event["actor_id"] != f.users["admin"].ID || event["source"] != "chat" || event["target_id"] == "" || event["request_id"] != "c9-request" {
			t.Errorf("配置事件字段错误: %s", line)
		}
		if strings.Contains(line, f.dir) || strings.Contains(line, "postgres://") || strings.Contains(line, "c9-private-hash") {
			t.Errorf("配置事件泄露: %s", line)
		}
	}
	for _, d := range f.defs {
		if d.Scope == tool.ScopeShared && d.Name != "start_scan" && !events[d.Name] {
			t.Errorf("成功配置缺日志事件: %s", d.Name)
		}
	}

}

func TestPostgreSQLC9DisableSelf(t *testing.T) {
	f := newC9Fixture(t)
	f.exec(t, `INSERT INTO users(id,username,password_hash,role) VALUES($1,'c9-second-admin','synthetic','admin')`, uuid.New())
	for _, mode := range []string{"assistant", "operator", "steward"} {
		for _, name := range []string{"set_member_disabled", "revoke_member_sessions"} {
			for _, disabled := range []bool{true, false} {
				t.Run(mode+"/"+name+"/"+strconv.FormatBool(disabled), func(t *testing.T) {
					caller := f.caller("admin")
					caller.Mode = mode
					input := map[string]any{"member_id": strings.ToUpper(caller.User.ID)}
					if name == "set_member_disabled" {
						input["disabled"] = disabled
					}
					_, err := f.registry.PreviewCall(context.Background(), caller, name, c9JSON(input))
					c9Code(t, err, "self_target_not_allowed")
					_, err = f.registry.Invoke(context.Background(), caller, name, c9JSON(input))
					c9Code(t, err, "self_target_not_allowed")
				})
			}
		}
	}
	server := httptest.NewServer(f.mux)
	defer server.Close()
	session := f.mcp(t, server, "admin", token.ScopeReadWrite)
	for _, disabled := range []bool{true, false} {
		c9MCPCall(t, session, "set_member_disabled", map[string]any{"member_id": f.users["admin"].ID, "disabled": disabled}, "self_target_not_allowed")
	}
	c9MCPCall(t, session, "revoke_member_sessions", map[string]any{"member_id": f.users["admin"].ID}, "self_target_not_allowed")
	c9MCPCall(t, session, "set_member_disabled", map[string]any{"member_id": f.users["user"].ID, "disabled": true}, "")
	c9MCPCall(t, session, "set_member_disabled", map[string]any{"member_id": f.users["user"].ID, "disabled": false}, "")
	c9MCPCall(t, session, "revoke_member_sessions", map[string]any{"member_id": f.users["other"].ID}, "")
	if w := f.request("admin", "GET", "auth/me", "", nil); w.Code != 200 {
		t.Fatalf("拒绝自身操作仍改变会话: %d", w.Code)
	}
	// 服务原有的最后管理员保护独立于工具的自身目标拒绝。
	f.exec(t, `DELETE FROM users WHERE username='c9-second-admin'`)
	var userErr *identity.UserManagementError
	err := identity.SetUserDisabled(context.Background(), f.db, f.users["admin"].ID, true)
	if !errors.As(err, &userErr) || userErr.Code != "last_admin" {
		t.Fatalf("最后管理员保护: %v", err)
	}
}

func TestPostgreSQLC9PersonalSettingsIsolation(t *testing.T) {
	f := newC9Fixture(t)
	ctx := context.Background()
	zoneIDs := map[string]string{}
	presets := map[string]string{}
	for _, role := range []string{"user", "other"} {
		d, err := f.zones.RegisterDevice(ctx, f.users[role].ID, zones.RegisterDeviceInput{Name: "C9 合成输出", Kind: "browser", Volume: 0.5})
		if err != nil {
			t.Fatal(err)
		}
		zoneIDs[role] = d.Zone.ZoneID
		peq, err := zones.NewPEQDocument(zones.PEQSettings{Enabled: false, PreampDB: -3, Bands: []zones.PEQBand{}})
		if err != nil {
			t.Fatal(err)
		}
		p, err := f.dsp.CreatePreset(ctx, f.users[role].ID, "合成预设-"+role, peq)
		if err != nil {
			t.Fatal(err)
		}
		presets[role] = p.ID
	}
	for _, role := range []string{"user", "other"} {
		other := "other"
		if role == "other" {
			other = "user"
		}
		t.Run(role, func(t *testing.T) {
			before := f.invoke(t, other, "get_zone_settings", map[string]any{"zone_id": zoneIDs[other]}, "")
			for _, mode := range []string{"assistant", "operator", "steward"} {
				caller := f.caller(role)
				caller.Mode = mode
				for _, name := range []string{"get_zone_settings", "set_zone_settings"} {
					in := map[string]any{"zone_id": zoneIDs[role]}
					if name == "set_zone_settings" {
						in["settings"] = map[string]any{"leveling": "track"}
					}
					if _, err := f.registry.Invoke(ctx, caller, name, c9JSON(in)); err != nil {
						t.Fatal(err)
					}
				}
			}
			list := f.invoke(t, role, "list_peq_presets", map[string]any{}, "")
			if !strings.Contains(string(list), presets[role]) || strings.Contains(string(list), presets[other]) {
				t.Fatalf("预设隔离失败: %s", list)
			}
			raw := f.invoke(t, role, "apply_peq_preset", map[string]any{"zone_id": zoneIDs[role], "preset_id": presets[role]}, "")
			if !strings.Contains(string(raw), `"enabled":false`) {
				t.Fatalf("预设未保留关闭状态: %s", raw)
			}
			for _, name := range []string{"get_zone_settings", "set_zone_settings", "apply_peq_preset"} {
				in := map[string]any{"zone_id": zoneIDs[other]}
				if name == "set_zone_settings" {
					in["settings"] = map[string]any{"leveling": "off"}
				}
				if name == "apply_peq_preset" {
					in["preset_id"] = presets[role]
				}
				f.invoke(t, role, name, in, "not_found")
			}
			f.invoke(t, role, "apply_peq_preset", map[string]any{"zone_id": zoneIDs[role], "preset_id": presets[other]}, "not_found")
			after := f.invoke(t, other, "get_zone_settings", map[string]any{"zone_id": zoneIDs[other]}, "")
			if !bytes.Equal(before, after) {
				t.Fatalf("越权调用改变他人设置: %s -> %s", before, after)
			}
			for _, settings := range []any{nil, map[string]any{"headroom": map[string]any{"enabled": true}}, map[string]any{"leveling": "bad"}, map[string]any{"unknown": 1}} {
				f.invoke(t, role, "set_zone_settings", map[string]any{"zone_id": zoneIDs[role], "settings": settings}, "validation_failed")
			}
		})
	}
}

func c9Model(t *testing.T, calls []map[string]any, inspect func(map[string]any)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var mainHits, reviewHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if inspect != nil {
			inspect(body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		var choice map[string]any
		if body["tool_choice"] != nil {
			reviewHits.Add(1)
			choice = map[string]any{"index": 0, "delta": map[string]any{"content": "不应调用审查"}, "finish_reason": "stop"}
		} else if mainHits.Add(1) == 1 {
			choice = map[string]any{"index": 0, "delta": map[string]any{"tool_calls": calls}, "finish_reason": "tool_calls"}
		} else {
			choice = map[string]any{"index": 0, "delta": map[string]any{"content": "完成"}, "finish_reason": "stop"}
		}
		raw, _ := json.Marshal(map[string]any{"choices": []any{choice}})
		_, _ = io.WriteString(w, "data: "+string(raw)+"\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	return server, &reviewHits
}
func c9ModelCall(index int, name string, input any) map[string]any {
	return map[string]any{"index": index, "id": fmt.Sprintf("c9-call-%d", index), "type": "function", "function": map[string]any{"name": name, "arguments": string(c9JSON(input))}}
}
func (f *c9Fixture) conversation(t *testing.T, role, mode, endpoint string) string {
	t.Helper()
	if role != "admin" {
		u, err := url.Parse(endpoint)
		if err != nil {
			t.Fatal(err)
		}
		port, err := strconv.Atoi(u.Port())
		if err != nil {
			t.Fatal(err)
		}
		w := f.request("admin", "POST", "admin/steward/egress-allowlist", "", map[string]any{"host": u.Hostname(), "port": port})
		if w.Code != 200 {
			t.Fatalf("本机假服务放行: %d %s", w.Code, w.Body)
		}
	}
	w := f.request(role, "PUT", "me/steward/settings", "", map[string]any{"endpoint": endpoint, "model": "c9-local", "protocol": "openai"})
	if w.Code != 200 {
		t.Fatalf("settings: %d %s", w.Code, w.Body)
	}
	w = f.request(role, "POST", "me/steward/conversations", "", map[string]any{"mode": mode})
	if w.Code != 201 {
		t.Fatalf("conversation: %d %s", w.Code, w.Body)
	}
	var value struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value.ID
}

func TestPostgreSQLC9RuntimeMemberModesHideAndDenyAdminTools(t *testing.T) {
	f := newC9Fixture(t)
	for _, mode := range []string{"assistant", "operator", "steward"} {
		t.Run(mode, func(t *testing.T) {
			calls := []map[string]any{}
			admin := map[string]bool{}
			for _, d := range f.defs {
				if d.MinRole == tool.RoleAdmin {
					calls = append(calls, c9ModelCall(len(calls), d.Name, map[string]any{}))
					admin[d.Name] = true
				}
			}
			server, reviews := c9Model(t, calls, func(body map[string]any) {
				tools, _ := body["tools"].([]any)
				for _, v := range tools {
					fn, _ := v.(map[string]any)["function"].(map[string]any)
					if name, _ := fn["name"].(string); admin[name] {
						t.Errorf("%s 模型清单包含 %s", mode, name)
					}
				}
			})
			id := f.conversation(t, "user", mode, server.URL)
			w := f.request("user", "POST", "me/steward/conversations/"+id+"/messages", "", map[string]string{"text": "合成越权尝试"})
			if w.Code != 200 {
				t.Fatalf("消息: %d %s", w.Code, w.Body)
			}
			var denied int
			if err := f.db.QueryRow(`SELECT count(*) FROM steward_tool_calls c JOIN steward_messages m ON m.id=c.message_id WHERE m.conversation_id=$1 AND c.error_code='permission_denied'`, id).Scan(&denied); err != nil {
				t.Fatal(err)
			}
			if denied != len(admin) {
				t.Fatalf("拒绝数=%d want=%d: %s", denied, len(admin), w.Body)
			}
			if reviews.Load() != 0 {
				t.Fatal("越权工具进入审查")
			}
		})
	}
}

func TestPostgreSQLC9AssistantConfirmationAndOperator(t *testing.T) {
	for _, mode := range []string{"assistant", "operator"} {
		t.Run(mode, func(t *testing.T) {
			f := newC9Fixture(t)
			before, err := f.scans.LoadScanSchedule(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			server, reviews := c9Model(t, []map[string]any{c9ModelCall(0, "set_scan_schedule", map[string]any{"enabled": true, "hour": 3})}, nil)
			id := f.conversation(t, "admin", mode, server.URL)
			w := f.request("admin", "POST", "me/steward/conversations/"+id+"/messages", "", map[string]string{"text": "每天凌晨三点扫描"})
			if w.Code != 200 {
				t.Fatalf("run: %d %s", w.Code, w.Body)
			}
			current, err := f.scans.LoadScanSchedule(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if mode == "assistant" {
				if current.Enabled != before.Enabled || current.Hour != before.Hour {
					t.Fatal("未确认已执行")
				}
				if !strings.Contains(w.Body.String(), `"revertible":false`) || !strings.Contains(w.Body.String(), `"reason":"awaiting_confirmation"`) {
					t.Fatalf("未生成不可撤销卡片: %s", w.Body)
				}
				detail, err := runtime.NewStore(f.db).GetConversation(context.Background(), f.users["admin"].ID, id)
				if err != nil || len(detail.Plans) != 1 {
					t.Fatalf("detail=%+v err=%v", detail, err)
				}
				plan := detail.Plans[0]
				if len(plan.Items) != 1 || plan.Items[0].Revertible {
					t.Fatal(plan)
				}
				changes := plan.Items[0].Changes
				if len(changes) != 2 || changes[0].Before != before.Enabled || changes[0].After != true || changes[1].Before != float64(before.Hour) || changes[1].After != float64(3) {
					t.Fatalf("卡片旧新值=%+v", changes)
				}
				decision := map[string]any{"digest": plan.Digest, "accepted_item_ids": []string{plan.Items[0].ID}, "auto_approve_tools": []string{"set_scan_schedule"}}
				path := "me/steward/conversations/" + id + "/plans/" + plan.ID + "/decision"
				denied := f.request("admin", "POST", path, "", decision)
				if denied.Code >= 200 && denied.Code < 300 {
					t.Fatalf("不可撤销工具允许免确认: %s", denied.Body)
				}
				decision["auto_approve_tools"] = []string{}
				w = f.request("admin", "POST", path, "", decision)
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"reason":"completed"`) {
					t.Fatalf("确认: %d %s", w.Code, w.Body)
				}
				detail, err = runtime.NewStore(f.db).GetConversation(context.Background(), f.users["admin"].ID, id)
				if err != nil {
					t.Fatal(err)
				}
				if len(detail.Conversation.AutoApprovedTools) != 0 {
					t.Fatal("不可撤销工具进入免确认名单")
				}
			} else if !current.Enabled || current.Hour != 3 {
				t.Fatalf("operator 未直接执行: %+v %s", current, w.Body)
			}
			if reviews.Load() != 0 {
				t.Fatal("非 steward 调用审查")
			}
			w = f.request("admin", "GET", "scan-schedule", "", nil)
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"hour":3`) || !strings.Contains(w.Body.String(), `"enabled":true`) {
				t.Fatalf("HTTP 未读到配置: %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestPostgreSQLC9StewardAllConfigToolsRequireHuman(t *testing.T) {
	f := newC9Fixture(t)
	if err := os.MkdirAll(filepath.Join(f.music, "未执行目录"), 0700); err != nil {
		t.Fatal(err)
	}
	root := uuid.New()
	f.exec(t, `INSERT INTO library_roots(id,path,status,revision) VALUES($1,$2,'active',1)`, root, f.music)
	inputs := map[string]any{
		"add_library_root": map[string]any{"path": filepath.Join(f.music, "未执行目录")}, "set_library_root_enabled": map[string]any{"root_id": root, "expected_revision": 1, "enabled": false},
		"add_root_exclusion": map[string]any{"root_id": root, "path": filepath.Join(f.music, "未执行排除")}, "remove_root_exclusion": map[string]any{"root_id": root, "path": "未执行排除"},
		"start_scan": map[string]any{}, "cancel_scan": map[string]any{"scan_id": uuid.New()}, "set_scan_schedule": map[string]any{"enabled": true, "hour": 3},
		"set_member_disabled": map[string]any{"member_id": f.users["user"].ID, "disabled": true}, "revoke_member_sessions": map[string]any{"member_id": f.users["user"].ID},
		"start_backup": map[string]any{}, "set_backup_retention": map[string]any{"keep_days": 7, "keep_count": 2}}
	calls := []map[string]any{}
	for _, d := range f.defs {
		if d.Scope == tool.ScopeShared {
			calls = append(calls, c9ModelCall(len(calls), d.Name, inputs[d.Name]))
		}
	}
	server, reviews := c9Model(t, calls, nil)
	id := f.conversation(t, "admin", "steward", server.URL)
	w := f.request("admin", "POST", "me/steward/conversations/"+id+"/messages", "", map[string]string{"text": "合成配置方案"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"reason":"awaiting_confirmation"`) {
		t.Fatalf("run: %d %s", w.Code, w.Body)
	}
	detail, err := runtime.NewStore(f.db).GetConversation(context.Background(), f.users["admin"].ID, id)
	if err != nil || len(detail.Plans) != 1 {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
	p := detail.Plans[0]
	if len(p.Items) != 11 || p.Review == nil || len(p.Review.Items) != 11 {
		t.Fatalf("未全部转人工: %+v", p)
	}
	for _, item := range p.Items {
		if item.Decision != "pending" || item.Revertible || item.Result != nil {
			t.Errorf("配置执行了: %+v", item)
		}
	}
	for _, item := range p.Review.Items {
		if item.Source != "policy" || item.Decision != "needs_human_confirmation" {
			t.Errorf("审查来源错误: %+v", item)
		}
	}
	if reviews.Load() != 0 {
		t.Fatal("不可撤销项仍调用审查模型")
	}
	var disabled bool
	if err := f.db.QueryRow(`SELECT disabled_at IS NOT NULL FROM users WHERE id=$1`, f.users["user"].ID).Scan(&disabled); err != nil || disabled {
		t.Fatal("转人工改变成员", err)
	}
	if strings.Contains(f.logs.text(), `"event":"steward.config.changed"`) {
		t.Fatal("未确认仍记录配置成功")
	}
}

func (f *c9Fixture) audio(t *testing.T) (string, string, string) {
	t.Helper()
	root, group, release, medium, track := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	path := filepath.Join(f.music, "c9-private-track.flac")
	content := parsertest.FLACStream()
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `INSERT INTO library_roots(id,path,status,revision) VALUES($1,$2,'active',1)`, root, f.music)
	f.exec(t, `INSERT INTO release_groups(id) VALUES($1)`, group)
	f.exec(t, `INSERT INTO releases(id,group_id,title,artist,source_root_id,relative_directory,candidate_anchor,candidate_kind) VALUES($1,$2,'合成音频专辑','合成艺人',$3,'c9-audio',$4,'ordinary_directory')`, release, group, root, release)
	f.exec(t, `INSERT INTO media(id,release_id,position) VALUES($1,$2,1)`, medium, release)
	f.exec(t, `INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,source_kind,codec,source_status,observed_at,source_size,source_modified_at,content_fingerprint,sample_rate,bit_depth,channels,duration_seconds) VALUES($1,$2,1,'合成曲目','合成艺人',$3,'c9-private-track.flac','flac_vorbis_comment','flac','present',now(),$4,$5,'c9-audio-fingerprint',44100,16,2,240)`, track, medium, root, info.Size(), info.ModTime().UTC().Truncate(time.Microsecond))
	f.exec(t, `INSERT INTO track_analyses(track_id,analyzer_version,content_fingerprint,source_size,source_modified_at,status,integrated_lufs,true_peak_dbtp,duration_ms,waveform) VALUES($1,$2,'c9-audio-fingerprint',$3,$4,'done',-18,-1,240000,$5)`, track, analysis.Version, info.Size(), info.ModTime().UTC().Truncate(time.Microsecond), []byte{1, 2, 3})
	return root, track, path
}
func TestPostgreSQLC9TrackAudioFactsAndPrivacy(t *testing.T) {
	f := newC9Fixture(t)
	root, track, path := f.audio(t)
	check := func(role, status string) json.RawMessage {
		t.Helper()
		raw := f.invoke(t, role, "get_track_audio", map[string]any{"track_id": track}, "")
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		if out["source_check"] != status || out["sample_rate"] != float64(44100) || out["bit_depth"] != float64(16) || out["duration_seconds"] != float64(240) || out["analysis"] == nil {
			t.Fatalf("音频事实丢失: %s", raw)
		}
		for _, bad := range []string{f.dir, path, "c9-private-track", `"url"`, `"path"`, `"stream"`, `"waveform"`, `"etag"`, `"file"`, "/api/v1/"} {
			if strings.Contains(strings.ToLower(string(raw)), strings.ToLower(bad)) {
				t.Errorf("音频输出泄露 %s: %s", bad, raw)
			}
		}
		return raw
	}
	for _, mode := range []string{"assistant", "operator", "steward"} {
		caller := f.caller("user")
		caller.Mode = mode
		if _, err := f.registry.Invoke(context.Background(), caller, "get_track_audio", c9JSON(map[string]any{"track_id": track})); err != nil {
			t.Fatal(err)
		}
	}
	a := check("user", "verified")
	b := check("other", "verified")
	if !bytes.Equal(a, b) {
		t.Fatal("成员读取共享音频事实不一致")
	}
	f.exec(t, `UPDATE tracks SET source_kind='m4a',codec='alac' WHERE id=$1`, track)
	check("user", "not_directly_playable")
	f.exec(t, `UPDATE library_roots SET status='disabled' WHERE id=$1`, root)
	check("user", "root_disabled")
	f.exec(t, `UPDATE library_roots SET status='active' WHERE id=$1`, root)
	f.exec(t, `UPDATE tracks SET source_kind='flac_vorbis_comment',codec='flac' WHERE id=$1`, track)
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	check("user", "source_changed")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	check("user", "source_unavailable")
	f.invoke(t, "user", "get_track_audio", map[string]any{"track_id": uuid.New()}, "not_found")
	f.invoke(t, "user", "get_track_audio", map[string]any{"track_id": strings.Repeat("x", 36)}, "validation_failed")
}

func TestPostgreSQLC9ConfigEventFieldsAndMCPSource(t *testing.T) {
	f := newC9Fixture(t)
	caller := f.caller("admin")
	caller.ConversationID = uuid.New()
	_, err := f.registry.Invoke(context.Background(), caller, "set_scan_schedule", json.RawMessage(`{"enabled":true,"hour":3}`))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(f.mux)
	defer server.Close()
	session := f.mcp(t, server, "admin", token.ScopeReadWrite)
	c9MCPCall(t, session, "set_scan_schedule", map[string]any{"enabled": false, "hour": 4}, "")
	events := map[string]map[string]any{}
	for _, line := range strings.Split(f.logs.text(), "\n") {
		if line == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event["event"] != "steward.config.changed" {
			continue
		}
		source := event["source"].(string)
		events[source] = event
		for _, field := range []string{"time", "level", "event", "message", "module", "tool", "actor_id", "source", "target_id", "conversation_id", "token_id", "request_id"} {
			if _, ok := event[field]; !ok {
				t.Errorf("配置事件缺字段 %s: %s", field, line)
			}
		}
		if event["tool"] != "set_scan_schedule" || event["actor_id"] != caller.User.ID || event["target_id"] != "scan_schedule" || event["module"] != "steward" || event["level"] != "INFO" {
			t.Fatalf("事件值=%s", line)
		}
		for _, bad := range []string{f.dir, "postgres://", "enabled", "hour", `"input"`, "Bearer ", f.cookies["admin"]} {
			if strings.Contains(line, bad) {
				t.Errorf("配置事件含输入或凭据: %s", line)
			}
		}
	}
	if len(events) != 2 || events["chat"]["conversation_id"] != caller.ConversationID || events["chat"]["request_id"] != caller.RequestID || events["mcp"]["token_id"] == "" || events["mcp"]["conversation_id"] != "" {
		t.Fatalf("来源字段错误: %+v", events)
	}
	raw := f.invoke(t, "admin", "list_recent_logs", map[string]any{"module": "steward"}, "")
	var recent struct {
		Items []diagnostics.LogRecord `json:"items"`
	}
	if err := json.Unmarshal(raw, &recent); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, record := range recent.Items {
		if _, exists := record.Fields["source"]; exists {
			t.Errorf("source 不应入环: %+v", record)
		}
		if record.Fields["conversation_id"] == caller.ConversationID {
			found["chat"] = true
		}
		if record.Fields["token_id"] == events["mcp"]["token_id"] {
			found["mcp"] = true
		}
	}
	if !found["chat"] || !found["mcp"] {
		t.Fatalf("记录 ID 丢失: %s", raw)
	}
}

func TestPostgreSQLC9DiagnosticsStatusAndDiskFacts(t *testing.T) {
	f := newC9Fixture(t)
	_, track, _ := f.audio(t)
	t.Setenv("C9_SYNTHETIC_ENV_SECRET", "c9-env-private-value")
	raw := f.invoke(t, "admin", "get_system_status", map[string]any{}, "")
	var status diagnostics.Status
	if err := json.Unmarshal(raw, &status); err != nil {
		t.Fatal(err)
	}
	var migration int64
	if err := f.db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&migration); err != nil {
		t.Fatal(err)
	}
	if !status.DatabaseConnected || status.DatabasePool == nil || status.MigrationVersion == nil || *status.MigrationVersion != migration || len(status.Errors) != 0 || status.UptimeSeconds < 59 {
		t.Fatalf("真实状态=%s", raw)
	}
	stats := status.Catalog.(map[string]any)
	if stats["tracks"] != float64(1) || stats["albums"] != float64(1) {
		t.Fatalf("曲目 %s 的计数未纳入状态: %s", track, raw)
	}
	disk := f.invoke(t, "admin", "get_disk_usage", map[string]any{}, "")
	var usage struct {
		Items []diagnostics.DiskUsage `json:"items"`
	}
	if err := json.Unmarshal(disk, &usage); err != nil {
		t.Fatal(err)
	}
	if len(usage.Items) != 3 {
		t.Fatalf("磁盘清单=%s", disk)
	}
	for _, item := range usage.Items {
		if item.TotalBytes == nil || *item.TotalBytes == 0 || item.AvailableBytes == nil || *item.AvailableBytes > *item.TotalBytes || item.ErrorCode != "" {
			t.Fatalf("磁盘事实=%+v", item)
		}
	}
	for _, value := range []json.RawMessage{raw, disk} {
		for _, bad := range []string{f.dir, "postgres://", "C9_SYNTHETIC_ENV_SECRET", "c9-env-private-value"} {
			if strings.Contains(string(value), bad) {
				t.Errorf("诊断泄露: %s", value)
			}
		}
	}
}

func TestPostgreSQLC9PersonalMCPScopesAndIsolation(t *testing.T) {
	f := newC9Fixture(t)
	_, track, _ := f.audio(t)
	ctx := context.Background()
	ids := map[string]string{}
	presetIDs := map[string]string{}
	for _, role := range []string{"user", "other"} {
		d, err := f.zones.RegisterDevice(ctx, f.users[role].ID, zones.RegisterDeviceInput{Name: "C9 MCP 输出", Kind: "browser", Volume: 0.5})
		if err != nil {
			t.Fatal(err)
		}
		ids[role] = d.Zone.ZoneID
		p, err := f.dsp.CreatePreset(ctx, f.users[role].ID, "C9 MCP 预设-"+role, zones.DefaultPEQDocument())
		if err != nil {
			t.Fatal(err)
		}
		presetIDs[role] = p.ID
	}
	server := httptest.NewServer(f.mux)
	defer server.Close()
	for _, role := range []string{"user", "other"} {
		for _, scope := range []token.Scope{token.ScopeRead, token.ScopeReadWrite} {
			t.Run(role+"/"+string(scope), func(t *testing.T) {
				other := "other"
				if role == "other" {
					other = "user"
				}
				s := f.mcp(t, server, role, scope)
				c9MCPCall(t, s, "get_track_audio", map[string]any{"track_id": track}, "")
				c9MCPCall(t, s, "get_zone_settings", map[string]any{"zone_id": ids[role]}, "")
				list := c9MCPCall(t, s, "list_peq_presets", map[string]any{}, "")
				if strings.Contains(list, presetIDs[other]) || !strings.Contains(list, presetIDs[role]) {
					t.Fatalf("MCP 预设隔离=%s", list)
				}
				c9MCPCall(t, s, "get_zone_settings", map[string]any{"zone_id": ids[other]}, "not_found")
				want := ""
				if scope == token.ScopeRead {
					want = "permission_denied"
				}
				c9MCPCall(t, s, "set_zone_settings", map[string]any{"zone_id": ids[role], "settings": map[string]any{"leveling": "album"}}, want)
				c9MCPCall(t, s, "apply_peq_preset", map[string]any{"zone_id": ids[role], "preset_id": presetIDs[role]}, want)
				if scope == token.ScopeReadWrite {
					c9MCPCall(t, s, "set_zone_settings", map[string]any{"zone_id": ids[other], "settings": map[string]any{"leveling": "off"}}, "not_found")
					c9MCPCall(t, s, "apply_peq_preset", map[string]any{"zone_id": ids[role], "preset_id": presetIDs[other]}, "not_found")
				}
			})
		}
	}
}

func TestPostgreSQLC9PreviewPathsAndTargetCards(t *testing.T) {
	f := newC9Fixture(t)
	ctx := context.Background()
	outside := t.TempDir()
	link := filepath.Join(f.music, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(f.music, "missing"), outside, link, "/"} {
		t.Run(path, func(t *testing.T) {
			_, err := f.registry.PreviewCall(ctx, f.caller("admin"), "add_library_root", c9JSON(map[string]any{"path": path}))
			c9Code(t, err, "invalid_library_path")
		})
	}
	// 即使允许根包含 /，工具也拒绝文件系统根。
	registry := tool.NewRegistry()
	for _, d := range librarytools.ConfigTools(f.scans, []string{"/"}, nil) {
		if err := registry.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	for _, preview := range []bool{true, false} {
		var err error
		if preview {
			_, err = registry.PreviewCall(ctx, f.caller("admin"), "add_library_root", json.RawMessage(`{"path":"/"}`))
		} else {
			_, err = registry.Invoke(ctx, f.caller("admin"), "add_library_root", json.RawMessage(`{"path":"/"}`))
		}
		c9Code(t, err, "invalid_library_path")
	}
	targets := map[string]bool{}
	for _, rel := range []string{"one/同名", "two/deep/同名"} {
		path := filepath.Join(f.music, rel)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		p, err := f.registry.PreviewCall(ctx, f.caller("admin"), "add_library_root", c9JSON(map[string]any{"path": path}))
		if err != nil {
			t.Fatal(err)
		}
		if p.Target != rel || !strings.Contains(string(c9JSON(p.Changes)), rel) || strings.Contains(string(c9JSON(p)), f.dir) {
			t.Fatalf("目录层级卡片: %+v", p)
		}
		targets[p.Target] = true
	}
	if len(targets) != 2 {
		t.Fatal("同名目录无法区分")
	}
	root := c9Object(t, f.invoke(t, "admin", "add_library_root", map[string]any{"path": filepath.Join(f.music, "one/同名")}, ""))["id"].(string)
	for name, input := range map[string]any{
		"set_library_root_enabled": map[string]any{"root_id": root, "expected_revision": 1, "enabled": false},
		"add_root_exclusion":       map[string]any{"root_id": root, "path": filepath.Join(f.music, "one/同名/sub")},
		"remove_root_exclusion":    map[string]any{"root_id": root, "path": "sub"},
		"start_scan":               map[string]any{},
	} {
		p, err := f.registry.PreviewCall(ctx, f.caller("admin"), name, c9JSON(input))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(c9JSON(p)), root) || !strings.Contains(string(c9JSON(p)), "同名") {
			t.Errorf("目录卡片=%+v", p)
		}
	}
	for _, name := range []string{"set_member_disabled", "revoke_member_sessions"} {
		input := map[string]any{"member_id": f.users["user"].ID}
		if name == "set_member_disabled" {
			input["disabled"] = true
		}
		p, err := f.registry.PreviewCall(ctx, f.caller("admin"), name, c9JSON(input))
		if err != nil {
			t.Fatal(err)
		}
		if p.Target != f.users["user"].Username+"（user）" {
			t.Errorf("成员卡片=%+v", p)
		}
	}
}

func c9ConfigEventCount(f *c9Fixture, name string) int {
	count := 0
	for _, line := range strings.Split(f.logs.text(), "\n") {
		var event map[string]any
		if json.Unmarshal([]byte(line), &event) == nil && event["event"] == "steward.config.changed" && event["tool"] == name {
			count++
		}
	}
	return count
}

func c9SyntheticBackup(t *testing.T, f *c9Fixture, day int) string {
	t.Helper()
	path := filepath.Join(f.backupDir, fmt.Sprintf("roomusic-202601%02d-000000-manual.dump", day))
	if err := os.WriteFile(path, []byte("PGDMP-c9-synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, day, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPostgreSQLC9RetentionRefusesDeletion(t *testing.T) {
	for _, mode := range []string{"assistant", "operator", "mcp"} {
		t.Run(mode, func(t *testing.T) {
			f := newC9Fixture(t)
			ctx := context.Background()
			old := c9SyntheticBackup(t, f, 1)
			newer := c9SyntheticBackup(t, f, 2)
			safe := map[string]any{"keep_days": nil, "keep_count": 10}
			f.invoke(t, "admin", "set_backup_retention", safe, "")
			policyPath := filepath.Join(f.backupDir, ".roomusic-backup-retention.json")
			before, err := os.ReadFile(policyPath)
			if err != nil {
				t.Fatal(err)
			}
			events := c9ConfigEventCount(f, "set_backup_retention")
			input := map[string]any{"keep_days": nil, "keep_count": 1}
			caller := f.caller("admin")
			caller.Mode = mode
			_, err = f.registry.PreviewCall(ctx, caller, "set_backup_retention", c9JSON(input))
			c9Code(t, err, "retention_would_delete_backups")
			_, err = f.registry.Invoke(ctx, caller, "set_backup_retention", c9JSON(input))
			c9Code(t, err, "retention_would_delete_backups")
			if mode == "mcp" {
				server := httptest.NewServer(f.mux)
				defer server.Close()
				session := f.mcp(t, server, "admin", token.ScopeReadWrite)
				c9MCPCall(t, session, "set_backup_retention", input, "retention_would_delete_backups")
				c9MCPCall(t, session, "set_backup_retention", map[string]any{"keep_days": nil, "keep_count": 20}, "")
				// 恢复基线，后续断言只计算拒绝前后的状态。
				f.invoke(t, "admin", "set_backup_retention", safe, "")
				before, err = os.ReadFile(policyPath)
				if err != nil {
					t.Fatal(err)
				}
				events = c9ConfigEventCount(f, "set_backup_retention")
				c9MCPCall(t, session, "set_backup_retention", input, "retention_would_delete_backups")
			} else {
				server, _ := c9Model(t, []map[string]any{c9ModelCall(0, "set_backup_retention", input)}, nil)
				id := f.conversation(t, "admin", mode, server.URL)
				w := f.request("admin", "POST", "me/steward/conversations/"+id+"/messages", "", map[string]any{"text": "合成保留策略"})
				if w.Code != 200 {
					t.Fatalf("run: %d %s", w.Code, w.Body)
				}
				var denied int
				if err := f.db.QueryRow(`SELECT count(*) FROM steward_tool_calls c JOIN steward_messages m ON m.id=c.message_id WHERE m.conversation_id=$1 AND c.error_code='retention_would_delete_backups'`, id).Scan(&denied); err != nil {
					t.Fatal(err)
				}
				if denied != 1 {
					t.Fatalf("未拒绝清理: %s", w.Body)
				}
			}
			after, err := os.ReadFile(policyPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) || c9ConfigEventCount(f, "set_backup_retention") != events {
				t.Fatal("拒绝清理仍保存策略或记录事件")
			}
			for _, path := range []string{old, newer} {
				raw, err := os.ReadFile(path)
				if err != nil || string(raw) != "PGDMP-c9-synthetic" {
					t.Fatalf("备份改变: %v", err)
				}
			}
			f.invoke(t, "admin", "set_backup_retention", map[string]any{"keep_days": nil, "keep_count": 20}, "")
			overview, err := f.backups.Overview(ctx)
			if err != nil || overview.Retention.KeepCount == nil || *overview.Retention.KeepCount != 20 {
				t.Fatalf("无删除调整未保存: %+v %v", overview, err)
			}
		})
	}
}

func TestPostgreSQLC9RetentionRechecksAfterPreview(t *testing.T) {
	f := newC9Fixture(t)
	c9SyntheticBackup(t, f, 1)
	f.invoke(t, "admin", "set_backup_retention", map[string]any{"keep_days": nil, "keep_count": 10}, "")
	policy := filepath.Join(f.backupDir, ".roomusic-backup-retention.json")
	before, err := os.ReadFile(policy)
	if err != nil {
		t.Fatal(err)
	}
	events := c9ConfigEventCount(f, "set_backup_retention")
	server, _ := c9Model(t, []map[string]any{c9ModelCall(0, "set_backup_retention", map[string]any{"keep_days": nil, "keep_count": 1})}, nil)
	id := f.conversation(t, "admin", "assistant", server.URL)
	w := f.request("admin", "POST", "me/steward/conversations/"+id+"/messages", "", map[string]any{"text": "预览后添加备份"})
	if w.Code != 200 {
		t.Fatalf("run: %d %s", w.Code, w.Body)
	}
	detail, err := runtime.NewStore(f.db).GetConversation(context.Background(), f.users["admin"].ID, id)
	if err != nil || len(detail.Plans) != 1 {
		t.Fatalf("plan: %+v %v", detail, err)
	}
	plan := detail.Plans[0]
	c9SyntheticBackup(t, f, 2)
	w = f.request("admin", "POST", "me/steward/conversations/"+id+"/plans/"+plan.ID+"/decision", "", map[string]any{"digest": plan.Digest, "accepted_item_ids": []string{plan.Items[0].ID}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "retention_would_delete_backups") {
		t.Fatalf("执行未重算: %d %s", w.Code, w.Body)
	}
	after, err := os.ReadFile(policy)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || c9ConfigEventCount(f, "set_backup_retention") != events {
		t.Fatal("拒绝后策略或事件改变")
	}
	overview, err := f.backups.Overview(context.Background())
	if err != nil || len(overview.Backups) != 2 {
		t.Fatalf("备份被删除: %+v %v", overview, err)
	}
}

func TestPostgreSQLC9IdentityRefreshDuringConversation(t *testing.T) {
	for _, mutation := range []string{"disabled", "demoted"} {
		for _, stage := range []string{"handler", "preview"} {
			t.Run(mutation+"/"+stage, func(t *testing.T) {
				f := newC9Fixture(t)
				name, mode, input := "get_scan_schedule", "operator", map[string]any{}
				if stage == "preview" {
					name, mode, input = "set_scan_schedule", "assistant", map[string]any{"enabled": true, "hour": 3}
				}
				var changed atomic.Bool
				server, _ := c9Model(t, []map[string]any{c9ModelCall(0, name, input)}, func(map[string]any) {
					if !changed.CompareAndSwap(false, true) {
						return
					}
					q := `UPDATE users SET disabled_at=now() WHERE id=$1`
					if mutation == "demoted" {
						q = `UPDATE users SET role='user' WHERE id=$1`
					}
					if _, err := f.db.Exec(q, f.users["admin"].ID); err != nil {
						t.Error(err)
					}
				})
				id := f.conversation(t, "admin", mode, server.URL)
				w := f.request("admin", "POST", "me/steward/conversations/"+id+"/messages", "", map[string]any{"text": "合成长会话身份变化"})
				if w.Code != 200 {
					t.Fatalf("run: %d %s", w.Code, w.Body)
				}
				var denied int
				if err := f.db.QueryRow(`SELECT count(*) FROM steward_tool_calls c JOIN steward_messages m ON m.id=c.message_id WHERE m.conversation_id=$1 AND c.error_code='permission_denied'`, id).Scan(&denied); err != nil {
					t.Fatal(err)
				}
				if denied != 1 {
					t.Fatalf("身份变化未拒绝 %s: %s", stage, w.Body)
				}
				if c9ConfigEventCount(f, "set_scan_schedule") != 0 {
					t.Fatal("身份失效仍写配置")
				}
			})
		}
	}
}

type c9BlockedBackupRunner struct {
	entered chan struct{}
	release chan struct{}
}

func (r c9BlockedBackupRunner) Run(ctx context.Context, c backup.Command) error {
	if strings.Contains(filepath.Base(c.Path), "pg_dump") && !(len(c.Args) == 1 && c.Args[0] == "--version") {
		close(r.entered)
		select {
		case <-r.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return (c9BackupRunner{}).Run(ctx, c)
}

func TestPostgreSQLC9NoopConfigEvents(t *testing.T) {
	f := newC9Fixture(t)
	ctx := context.Background()
	conn, err := f.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1::int,hashtext(current_schema()))`, int32(0x5343414e)); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1::int,hashtext(current_schema()))`, int32(0x5343414e))
	scan := uuid.New()
	f.exec(t, `INSERT INTO scan_runs(id,status,started_at) VALUES($1,'running',now())`, scan)
	before := c9ConfigEventCount(f, "start_scan")
	raw := f.invoke(t, "admin", "start_scan", map[string]any{}, "")
	if c9Object(t, raw)["started"] != false || c9ConfigEventCount(f, "start_scan") != before {
		t.Fatalf("重复启动写事件: %s", raw)
	}
	f.exec(t, `UPDATE scan_runs SET status='canceled',finished_at=now() WHERE id=$1`, scan)
	before = c9ConfigEventCount(f, "cancel_scan")
	raw = f.invoke(t, "admin", "cancel_scan", map[string]any{"scan_id": scan}, "")
	if c9Object(t, raw)["requested"] != false || c9ConfigEventCount(f, "cancel_scan") != before {
		t.Fatalf("无效取消写事件: %s", raw)
	}
	blocked := c9BlockedBackupRunner{make(chan struct{}), make(chan struct{})}
	logs := &c9Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	service := backup.NewService(config.Backup{Directory: t.TempDir(), DatabaseURL: "postgres://synthetic:synthetic@localhost/c9", Location: time.UTC}, logger, backup.WithRunner(blocked), backup.WithDatabaseAccess(c9BackupDatabase{}))
	defer func() {
		close(blocked.release)
		if err := service.Wait(ctx); err != nil {
			t.Error(err)
		}
	}()
	registry := tool.NewRegistry()
	for _, d := range backuptools.StewardTools(service, f.db, logger) {
		if err := registry.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	first, err := registry.Invoke(ctx, f.caller("admin"), "start_backup", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if c9Object(t, first.Output)["started"] != true {
		t.Fatal(string(first.Output))
	}
	select {
	case <-blocked.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("假备份未开始")
	}
	second, err := registry.Invoke(ctx, f.caller("admin"), "start_backup", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if c9Object(t, second.Output)["started"] != false || strings.Count(logs.text(), `"event":"steward.config.changed"`) != 1 {
		t.Fatalf("重复备份写事件: %s %s", second.Output, logs.text())
	}
}

func TestPostgreSQLC9DatabaseErrorsAndRecordFailureLog(t *testing.T) {
	f := newC9Fixture(t)
	f.exec(t, `ALTER TABLE scan_schedule RENAME TO c9_unavailable_scan_schedule`)
	f.invoke(t, "admin", "get_scan_schedule", map[string]any{}, "database_unavailable")
	f.exec(t, `ALTER TABLE c9_unavailable_scan_schedule RENAME TO scan_schedule`)
	f.exec(t, `CREATE FUNCTION c9_fail_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'c9-synthetic-private-receipt'; END $$`)
	f.exec(t, `CREATE TRIGGER c9_fail_receipt BEFORE INSERT ON library_root_operations FOR EACH ROW EXECUTE FUNCTION c9_fail_receipt()`)
	path := filepath.Join(f.music, "receipt-failure")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	f.invoke(t, "admin", "add_library_root", map[string]any{"path": path}, "operation_failed")
	count := 0
	for _, line := range strings.Split(f.logs.text(), "\n") {
		var event map[string]any
		if json.Unmarshal([]byte(line), &event) == nil && event["msg"] == "record root operation" {
			count++
			if event["level"] != "ERROR" || event["request_id"] != "c9-request" || !strings.Contains(fmt.Sprint(event["error"]), "c9-synthetic-private-receipt") {
				t.Errorf("记录失败日志=%s", line)
			}
		}
	}
	if count != 1 || c9ConfigEventCount(f, "add_library_root") != 0 {
		t.Fatalf("失败日志数量=%d", count)
	}
	for _, record := range f.diagnostics.Logs.Recent(diagnostics.LogFilter{}) {
		if record.Fields["msg"] == "record root operation" {
			if _, exists := record.Fields["error"]; exists {
				t.Fatal("内部错误进入日志环")
			}
		}
	}
	var roots int
	if err := f.db.QueryRow(`SELECT count(*) FROM library_roots WHERE path=$1`, path).Scan(&roots); err != nil || roots != 0 {
		t.Fatalf("记录失败未回滚: %d %v", roots, err)
	}
}

func TestPostgreSQLC9MemberModesSelfDeniedAndOthersAllowed(t *testing.T) {
	for _, mode := range []string{"assistant", "operator", "steward"} {
		t.Run(mode, func(t *testing.T) {
			f := newC9Fixture(t)
			calls := []map[string]any{
				c9ModelCall(0, "set_member_disabled", map[string]any{"member_id": f.users["admin"].ID, "disabled": true}),
				c9ModelCall(1, "set_member_disabled", map[string]any{"member_id": f.users["admin"].ID, "disabled": false}),
				c9ModelCall(2, "revoke_member_sessions", map[string]any{"member_id": f.users["admin"].ID}),
				c9ModelCall(3, "set_member_disabled", map[string]any{"member_id": f.users["user"].ID, "disabled": true}),
				c9ModelCall(4, "revoke_member_sessions", map[string]any{"member_id": f.users["other"].ID}),
			}
			server, _ := c9Model(t, calls, nil)
			id := f.conversation(t, "admin", mode, server.URL)
			w := f.request("admin", "POST", "me/steward/conversations/"+id+"/messages", "", map[string]any{"text": "合成成员操作"})
			if w.Code != 200 {
				t.Fatalf("run: %d %s", w.Code, w.Body)
			}
			var denied int
			if err := f.db.QueryRow(`SELECT count(*) FROM steward_tool_calls c JOIN steward_messages m ON m.id=c.message_id WHERE m.conversation_id=$1 AND c.error_code='self_target_not_allowed'`, id).Scan(&denied); err != nil {
				t.Fatal(err)
			}
			if denied != 3 {
				t.Fatalf("自身操作拒绝数=%d: %s", denied, w.Body)
			}
			if mode != "operator" {
				detail, err := runtime.NewStore(f.db).GetConversation(context.Background(), f.users["admin"].ID, id)
				if err != nil || len(detail.Plans) != 1 || len(detail.Plans[0].Items) != 2 {
					t.Fatalf("其他成员方案: %+v %v", detail, err)
				}
				plan := detail.Plans[0]
				accepted := []string{}
				for _, item := range plan.Items {
					accepted = append(accepted, item.ID)
				}
				w = f.request("admin", "POST", "me/steward/conversations/"+id+"/plans/"+plan.ID+"/decision", "", map[string]any{"digest": plan.Digest, "accepted_item_ids": accepted})
				if w.Code != 200 {
					t.Fatalf("confirm: %d %s", w.Code, w.Body)
				}
			}
			var disabled, revoked bool
			if err := f.db.QueryRow(`SELECT disabled_at IS NOT NULL FROM users WHERE id=$1`, f.users["user"].ID).Scan(&disabled); err != nil {
				t.Fatal(err)
			}
			if err := f.db.QueryRow(`SELECT revoked_at IS NOT NULL FROM sessions WHERE user_id=$1`, f.users["other"].ID).Scan(&revoked); err != nil {
				t.Fatal(err)
			}
			if !disabled || !revoked {
				t.Fatalf("其他成员未成功: disabled=%v revoked=%v %s", disabled, revoked, w.Body)
			}
			if w := f.request("admin", "GET", "auth/me", "", nil); w.Code != 200 {
				t.Fatalf("管理员自身状态改变: %d", w.Code)
			}
		})
	}
}
