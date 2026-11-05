package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/app"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
)

const (
	resetTestOldPassword = "old-cli-password-123"
	resetTestNewPassword = " new-cli-password-456 "
	resetTestUsage       = "用法：roomusic reset-password <用户名>\n"
	resetTestRule        = "密码需要 12 到 72 位英文字母、数字或符号。\n"
)

// 默认夹具禁止读取配置或连接数据库，防止前置拒绝路径意外产生副作用。
func resetCommandTestEnv(t *testing.T, input string) (resetPasswordEnv, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	return resetPasswordEnv{
		stdin: strings.NewReader(input), stdout: stdout, stderr: stderr,
		isTerminal: func() bool { return false },
		readSecret: func(string) (string, error) { return "", errors.New("不应读取终端") },
		databaseURL: func() (string, error) {
			t.Error("前置拒绝路径不应读取数据库配置")
			return "", errors.New("不应读取配置")
		},
		connect: func(context.Context, string) (*sql.DB, error) {
			t.Error("前置拒绝路径不应连接数据库")
			return nil, errors.New("不应连接数据库")
		},
	}, stdout, stderr
}

func requireResetCommandResult(t *testing.T, code, wantCode int, stdout, stderr *bytes.Buffer, wantOut, wantErr string) {
	t.Helper()
	if code != wantCode || stdout.String() != wantOut || stderr.String() != wantErr {
		t.Fatalf("命令结果：exit=%d stdout=%q stderr=%q；期望 exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String(), wantCode, wantOut, wantErr)
	}
}

func TestResetPasswordCommandUsage(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		code int
		out  string
		err  string
	}{
		{"no command", nil, 2, "", resetTestUsage},
		{"missing username", []string{"reset-password"}, 2, "", resetTestUsage},
		{"two usernames", []string{"reset-password", "curator", "listener"}, 2, "", resetTestUsage},
		{"help", []string{"help"}, 0, resetTestUsage, ""},
		{"short help", []string{"-h"}, 0, resetTestUsage, ""},
		{"long help", []string{"--help"}, 0, resetTestUsage, ""},
		{"unknown command", []string{"unknown"}, 2, "", "未知命令：unknown\n" + resetTestUsage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, stdout, stderr := resetCommandTestEnv(t, "")
			env.isTerminal = func() bool { t.Error("参数错误或帮助不应读密码"); return false }
			code := runCommand(tc.args, env)
			requireResetCommandResult(t, code, tc.code, stdout, stderr, tc.out, tc.err)
		})
	}
}

func TestResetPasswordCommandRejectsInvalidPasswordBeforeConnecting(t *testing.T) {
	for _, tc := range []struct{ name, input, message string }{
		{"11 bytes", strings.Repeat("a", 11) + "\n", resetTestRule},
		{"73 bytes", strings.Repeat("a", 73) + "\n", resetTestRule},
		{"Chinese", "中文密码不被接受\n", resetTestRule},
		{"control character", "abcdefghijkl\t\n", resetTestRule},
		{"empty line", "\n", "没有读到密码。\n"},
		{"empty CRLF", "\r\n", "没有读到密码。\n"},
		{"empty EOF", "", "没有读到密码。\n"},
		{"over input limit", strings.Repeat("a", 4097), resetTestRule},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, stdout, stderr := resetCommandTestEnv(t, tc.input)
			code := runCommand([]string{"reset-password", "curator"}, env)
			requireResetCommandResult(t, code, 1, stdout, stderr, "", tc.message)
		})
	}
}

func TestResetPasswordCommandReadsOneLineFromPipe(t *testing.T) {
	for _, tc := range []struct{ name, input, password, message string }{
		{"CRLF", "pass word 1234\r\n", "pass word 1234", ""},
		{"LF and second line", "pass word 1234\nignored second line\n", "pass word 1234", ""},
		{"EOF", "pass word 1234", "pass word 1234", ""},
		{"leading and trailing spaces", "  pass word 1234  \r\n", "  pass word 1234  ", ""},
		{"spaces at EOF", "  pass word 1234  ", "  pass word 1234  ", ""},
		{"spaces only are preserved", "            \n", "            ", ""},
		{"bare CR at EOF is preserved", "pass word 1234\r", "pass word 1234\r", ""},
		{"4 KiB limit", strings.Repeat("a", 4097), strings.Repeat("a", 4096), ""},
		{"empty first line", "\npass word 1234\n", "", "没有读到密码。"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, stdout, stderr := resetCommandTestEnv(t, tc.input)
			password, message := readResetPassword(context.Background(), env)
			if password != tc.password || message != tc.message {
				t.Fatalf("单行输入解析不符合合同：password=%q message=%q", password, message)
			}
			requireResetCommandResult(t, 0, 0, stdout, stderr, "", "")
		})
	}
}

type resetTestErrorReader struct{}

func (resetTestErrorReader) Read([]byte) (int, error) {
	return 0, errors.New("读取失败，包含敏感细节")
}

func TestResetPasswordCommandInputReadError(t *testing.T) {
	env, stdout, stderr := resetCommandTestEnv(t, "")
	env.stdin = resetTestErrorReader{}
	code := runResetPassword(context.Background(), []string{"curator"}, env)
	requireResetCommandResult(t, code, 1, stdout, stderr, "", "没有读到密码。\n")
}

func TestResetPasswordCommandTerminalMismatch(t *testing.T) {
	for _, tc := range []struct {
		name       string
		values     []string
		failRead   int
		message    string
		promptText string
	}{
		{"mismatch", []string{resetTestNewPassword, resetTestOldPassword}, -1, "两次输入的密码不一致。\n", "新密码：\n再输入一次：\n"},
		{"first read error", nil, 0, "没有读到密码。\n", "新密码：\n"},
		{"confirmation error", []string{resetTestNewPassword}, 1, "没有读到密码。\n", "新密码：\n再输入一次：\n"},
		{"empty passwords", []string{"", ""}, -1, "没有读到密码。\n", "新密码：\n再输入一次：\n"},
		{"invalid matching passwords", []string{"short", "short"}, -1, resetTestRule, "新密码：\n再输入一次：\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, stdout, stderr := resetCommandTestEnv(t, "")
			env.isTerminal = func() bool { return true }
			reads := 0
			env.readSecret = func(prompt string) (string, error) {
				// 终端适配器负责将调用方给出的提示写到 stderr，而不是回显密码。
				fmt.Fprintln(stderr, prompt)
				index := reads
				reads++
				if index == tc.failRead || index >= len(tc.values) {
					return "", errors.New("终端错误含密码 " + resetTestNewPassword)
				}
				return tc.values[index], nil
			}
			code := runResetPassword(context.Background(), []string{"curator"}, env)
			requireResetCommandResult(t, code, 1, stdout, stderr, "", tc.promptText+tc.message)
		})
	}
}

func TestResetPasswordCommandTerminalMatchingPreservesSpaces(t *testing.T) {
	env, stdout, stderr := resetCommandTestEnv(t, "")
	env.isTerminal = func() bool { return true }
	var prompts []string
	env.readSecret = func(prompt string) (string, error) {
		prompts = append(prompts, prompt)
		fmt.Fprintln(stderr, prompt)
		return resetTestNewPassword, nil
	}
	password, message := readResetPassword(context.Background(), env)
	if password != resetTestNewPassword || message != "" || strings.Join(prompts, "|") != "新密码：|再输入一次：" {
		t.Fatal("终端输入必须要求两次确认并原样保留首尾空格")
	}
	requireResetCommandResult(t, 0, 0, stdout, stderr, "", "新密码：\n再输入一次：\n")
}

func TestResetPasswordCommandMissingDatabaseURL(t *testing.T) {
	for _, value := range []string{"", "  \t\n"} {
		t.Run(fmt.Sprintf("value=%q", value), func(t *testing.T) {
			t.Setenv("ROOMUSIC_DATABASE_URL", value)
			env, stdout, stderr := resetCommandTestEnv(t, resetTestNewPassword+"\n")
			env.databaseURL = config.DatabaseURL
			code := runResetPassword(context.Background(), []string{"curator"}, env)
			requireResetCommandResult(t, code, 1, stdout, stderr, "", "没有设置 ROOMUSIC_DATABASE_URL。\n")
		})
	}
}

func TestResetPasswordCommandConnectionFailureAndTimeout(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"driver error", errors.New("postgres://secret-user:secret-pass@secret-host:5432/secret-db")},
		{"deadline", fmt.Errorf("连接超时: %w", context.DeadlineExceeded)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, stdout, stderr := resetCommandTestEnv(t, resetTestNewPassword+"\n")
			const databaseURL = "postgres://secret-user:secret-pass@secret-host:5432/secret-db"
			env.databaseURL = func() (string, error) { return databaseURL, nil }
			connected := false
			env.connect = func(ctx context.Context, gotURL string) (*sql.DB, error) {
				connected = true
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 30*time.Second || gotURL != databaseURL {
					t.Error("连接必须收到原始 URL 与不超过 30 秒的截止时间")
				}
				return nil, tc.err
			}
			code := runResetPassword(context.Background(), []string{"curator"}, env)
			requireResetCommandResult(t, code, 1, stdout, stderr, "", "无法连接数据库。\n")
			if !connected {
				t.Fatal("有效密码没有尝试连接数据库")
			}
			requireResetOutputHasNoSecrets(t, stdout.String()+stderr.String(), resetTestNewPassword, databaseURL)
		})
	}
}

func TestResetPasswordCommandAcceptsPasswordBoundariesBeforeConnecting(t *testing.T) {
	for _, password := range []string{strings.Repeat("a", 12), strings.Repeat("a", 72), strings.Repeat(" ", 12)} {
		t.Run(fmt.Sprintf("length=%d/spaces=%t", len(password), strings.TrimSpace(password) == ""), func(t *testing.T) {
			env, stdout, stderr := resetCommandTestEnv(t, password+"\n")
			env.databaseURL = func() (string, error) { return "test-url", nil }
			connected := false
			env.connect = func(context.Context, string) (*sql.DB, error) {
				connected = true
				return nil, errors.New("故意中止于连接边界")
			}
			code := runResetPassword(context.Background(), []string{"curator"}, env)
			requireResetCommandResult(t, code, 1, stdout, stderr, "", "无法连接数据库。\n")
			if !connected {
				t.Fatal("12/72 位 ASCII 与合法空格密码必须通过前置校验")
			}
		})
	}
}

// 只用于取消用例：管道读取会阻塞，关闭后这次 Read 以错误结束，不会再次调用。
type resetTestBlockingPipeReader struct {
	reader            io.Reader
	started, finished chan struct{}
}

func (r resetTestBlockingPipeReader) Read(p []byte) (int, error) {
	close(r.started)
	defer close(r.finished)
	return r.reader.Read(p)
}

func TestResetPasswordCommandCancellation(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		t.Run(fmt.Sprintf("terminal=%t", terminal), func(t *testing.T) {
			env, stdout, stderr := resetCommandTestEnv(t, "")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			env.stdin = reader
			env.isTerminal = func() bool { return terminal }
			started, released, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			if !terminal {
				env.stdin = resetTestBlockingPipeReader{reader: reader, started: started, finished: finished}
			}
			if terminal {
				env.readSecret = func(string) (string, error) {
					close(started)
					<-released
					defer close(finished)
					return resetTestNewPassword, nil
				}
			}
			result := make(chan int, 1)
			go func() { result <- runResetPassword(ctx, []string{"curator"}, env) }()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				close(released)
				t.Fatal("没有开始读取密码")
			}
			cancel()
			select {
			case code := <-result:
				requireResetCommandResult(t, code, 130, stdout, stderr, "", "")
			case <-time.After(2 * time.Second):
				t.Error("取消必须立即结束，不得等待密码输入")
			}
			if terminal {
				close(released)
			} else {
				_ = reader.Close()
			}
			select {
			case <-finished:
			case <-time.After(2 * time.Second):
				t.Error("取消后输入协程未结束")
			}
		})
	}

	t.Run("during connection", func(t *testing.T) {
		env, stdout, stderr := resetCommandTestEnv(t, resetTestNewPassword+"\n")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		env.databaseURL = func() (string, error) { return "unused", nil }
		env.connect = func(context.Context, string) (*sql.DB, error) {
			cancel()
			return nil, context.Canceled
		}
		code := runResetPassword(ctx, []string{"curator"}, env)
		requireResetCommandResult(t, code, 130, stdout, stderr, "", "")
	})
}

func TestResetPasswordCommandErrorMessages(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		connecting bool
		deadline   bool
		message    string
	}{
		{"unknown user", fmt.Errorf("查询: %w", identity.ErrUserNotFound), false, false, "找不到用户“curator”。\n"},
		{"database failure", errors.New("包含敏感 SQL 和密码"), false, false, "数据库出错，密码没有修改。\n"},
		{"connecting", identity.ErrUserNotFound, true, false, "无法连接数据库。\n"},
		{"wrapped deadline", fmt.Errorf("查询: %w", context.DeadlineExceeded), false, false, "无法连接数据库。\n"},
		{"expired context", identity.ErrUserNotFound, false, true, "无法连接数据库。\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, stdout, stderr := resetCommandTestEnv(t, "")
			ctx := context.Background()
			if tc.deadline {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
			}
			code := passwordCommandError(ctx, env, tc.err, "curator", tc.connecting)
			requireResetCommandResult(t, code, 1, stdout, stderr, "", tc.message)
		})
	}
}

type resetPasswordApplication struct {
	database *sql.DB
	url      string
	handler  http.Handler
	admin    *http.Cookie
	sessions []*http.Cookie
	member   *http.Cookie
}

func newResetPasswordApplication(t *testing.T) *resetPasswordApplication {
	t.Helper()
	scopedURL := postgrestest.NewSchema(t, "roomusic_reset_password_")
	database, err := postgres.Open(context.Background(), scopedURL)
	if err != nil {
		t.Fatalf("迁移测试 schema：%v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	application := app.New(ctx, config.Server{PublicURL: "http://roomusic.test", DataDirectory: t.TempDir()}, database, frontendSub(), nil)
	t.Cleanup(func() {
		cancel()
		wait, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := application.WaitForCatalogMaintenance(wait); err != nil {
			t.Errorf("等待后台维护退出：%v", err)
		}
		if err := application.WaitForScans(wait); err != nil {
			t.Errorf("等待扫描退出：%v", err)
		}
	})
	fixture := &resetPasswordApplication{database: database, url: scopedURL, handler: application.Handler()}
	fixture.request(t, http.MethodPost, "/api/v1/setup/admin", "curator", resetTestOldPassword, nil, http.StatusCreated)
	fixture.admin = fixture.login(t, "curator", resetTestOldPassword)
	fixture.sessions = []*http.Cookie{fixture.admin, fixture.login(t, "curator", resetTestOldPassword)}
	fixture.request(t, http.MethodPost, "/api/v1/users", "listener", resetTestOldPassword, fixture.admin, http.StatusCreated)
	fixture.member = fixture.login(t, "listener", resetTestOldPassword)
	return fixture
}

func (fixture *resetPasswordApplication) request(t *testing.T, method, path, username, password string, cookie *http.Cookie, wantStatus int) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if method == http.MethodPost {
		encoded, err := json.Marshal(map[string]string{"username": username, "password": password})
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, "http://roomusic.test"+path, body)
	request.Header.Set("Origin", "http://roomusic.test")
	request.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, request)
	if response.Code != wantStatus {
		t.Fatalf("%s %s：HTTP %d，期望 %d；%s", method, path, response.Code, wantStatus, response.Body.String())
	}
	return response
}

func (fixture *resetPasswordApplication) login(t *testing.T, username, password string) *http.Cookie {
	t.Helper()
	response := fixture.request(t, http.MethodPost, "/api/v1/auth/login", username, password, nil, http.StatusOK)
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == identity.SessionCookieName && cookie.Value != "" {
			return cookie
		}
	}
	t.Fatal("登录未返回会话 cookie")
	return nil
}

func (fixture *resetPasswordApplication) command(t *testing.T, username, password string) (int, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	env, stdout, stderr := resetCommandTestEnv(t, password+"\r\n")
	env.databaseURL = func() (string, error) { return fixture.url, nil }
	env.connect = postgres.Connect
	return runCommand([]string{"reset-password", username}, env), stdout, stderr
}

// 全行快照同时覆盖用户名、角色、停用状态、哈希、过期时间、撤销时间及令牌哈希。
func resetPasswordDataSnapshot(t *testing.T, database *sql.DB) string {
	t.Helper()
	var snapshot string
	if err := database.QueryRow(`SELECT jsonb_build_object(
 'users',(SELECT COALESCE(jsonb_agg(to_jsonb(u) ORDER BY u.id),'[]'::jsonb) FROM users u),
 'sessions',(SELECT COALESCE(jsonb_agg(to_jsonb(s) ORDER BY s.token_hash),'[]'::jsonb) FROM sessions s),
 'migrations',(SELECT COALESCE(jsonb_agg(to_jsonb(m) ORDER BY m.version),'[]'::jsonb) FROM schema_migrations m))::text`).Scan(&snapshot); err != nil {
		t.Fatalf("读取密码与会话快照：%v", err)
	}
	return snapshot
}

func requireResetOutputHasNoSecrets(t *testing.T, output, password, databaseURL string) {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("解析测试 URL：%v", err)
	}
	secrets := []string{password, databaseURL, parsed.Host, parsed.Hostname(), parsed.Port(), strings.TrimPrefix(parsed.Path, "/"), parsed.Query().Get("search_path")}
	if parsed.User != nil {
		secrets = append(secrets, parsed.User.Username())
		if value, ok := parsed.User.Password(); ok {
			secrets = append(secrets, value)
		}
	}
	for index, secret := range secrets {
		if secret != "" && strings.Contains(output, secret) {
			t.Errorf("输出泄漏第 %d 项秘密或数据库 URL 组件", index)
		}
	}
}

func TestPostgreSQLResetPasswordCommandRevokesSessionsAndChangesPassword(t *testing.T) {
	fixture := newResetPasswordApplication(t)
	var oldHash, migrationSnapshot string
	if err := fixture.database.QueryRow(`SELECT password_hash FROM users WHERE username='curator'`).Scan(&oldHash); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.QueryRow(`SELECT jsonb_agg(to_jsonb(m) ORDER BY version)::text FROM schema_migrations m`).Scan(&migrationSnapshot); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := fixture.command(t, "curator", resetTestNewPassword)
	requireResetCommandResult(t, code, 0, stdout, stderr, "已重置“curator”的密码，这个账号已在所有设备上退出登录。\n", "")
	for _, cookie := range fixture.sessions {
		fixture.request(t, http.MethodGet, "/api/v1/auth/me", "", "", cookie, http.StatusUnauthorized)
	}
	fixture.request(t, http.MethodGet, "/api/v1/auth/me", "", "", fixture.member, http.StatusOK)
	var hash, afterMigrations string
	var active int
	if err := fixture.database.QueryRow(`SELECT password_hash FROM users WHERE username='curator'`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash == oldHash || !strings.HasPrefix(hash, "$2") || !identity.VerifyPassword(hash, resetTestNewPassword) {
		t.Fatal("命令没有将原样输入的新密码写为 bcrypt 哈希")
	}
	if err := fixture.database.QueryRow(`SELECT count(*) FROM sessions s JOIN users u ON u.id=s.user_id WHERE u.username='curator' AND s.revoked_at IS NULL`).Scan(&active); err != nil || active != 0 {
		t.Fatalf("旧会话未全部撤销：active=%d err=%v", active, err)
	}
	if err := fixture.database.QueryRow(`SELECT jsonb_agg(to_jsonb(m) ORDER BY version)::text FROM schema_migrations m`).Scan(&afterMigrations); err != nil || afterMigrations != migrationSnapshot {
		t.Fatalf("命令改变了迁移记录：%v", err)
	}
	fixture.request(t, http.MethodPost, "/api/v1/auth/login", "curator", resetTestOldPassword, nil, http.StatusUnauthorized)
	fixture.request(t, http.MethodPost, "/api/v1/auth/login", "curator", strings.TrimSpace(resetTestNewPassword), nil, http.StatusUnauthorized)
	cookie := fixture.login(t, "curator", resetTestNewPassword)
	fixture.request(t, http.MethodGet, "/api/v1/auth/me", "", "", cookie, http.StatusOK)
}

func TestPostgreSQLResetPasswordCommandUnknownUserChangesNothing(t *testing.T) {
	fixture := newResetPasswordApplication(t)
	before := resetPasswordDataSnapshot(t, fixture.database)
	for _, username := range []string{"unknown-user", "Curator", " curator "} {
		t.Run(username, func(t *testing.T) {
			code, stdout, stderr := fixture.command(t, username, resetTestNewPassword)
			requireResetCommandResult(t, code, 1, stdout, stderr, "", "找不到用户“"+username+"”。\n")
			if resetPasswordDataSnapshot(t, fixture.database) != before {
				t.Fatal("不存在或未精确匹配的用户名改变了用户、会话或迁移记录")
			}
		})
	}
}

func TestPostgreSQLResetPasswordCommandInvalidPasswordChangesNothing(t *testing.T) {
	fixture := newResetPasswordApplication(t)
	before := resetPasswordDataSnapshot(t, fixture.database)
	for _, password := range []string{strings.Repeat("a", 11), strings.Repeat("a", 73), "中文密码不被接受", "abcdefghijkl\t", ""} {
		t.Run(fmt.Sprintf("length=%d", len(password)), func(t *testing.T) {
			env, stdout, stderr := resetCommandTestEnv(t, password+"\n")
			message := resetTestRule
			if password == "" {
				message = "没有读到密码。\n"
			}
			code := runCommand([]string{"reset-password", "curator"}, env)
			requireResetCommandResult(t, code, 1, stdout, stderr, "", message)
			if resetPasswordDataSnapshot(t, fixture.database) != before {
				t.Fatal("不合规密码改变了用户、会话或迁移记录")
			}
		})
	}
}

func TestPostgreSQLResetPasswordCommandDisabledAccountRemainsDisabled(t *testing.T) {
	fixture := newResetPasswordApplication(t)
	if _, err := fixture.database.Exec(`UPDATE users SET disabled_at=NOW() WHERE username='listener'`); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := fixture.database.QueryRow(`SELECT disabled_at::text FROM users WHERE username='listener'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := fixture.command(t, "listener", resetTestNewPassword)
	requireResetCommandResult(t, code, 0, stdout, stderr, "已重置“listener”的密码，这个账号已在所有设备上退出登录。\n这个账号现在是停用的，要在成员页重新启用后才能登录。\n", "")
	var after, hash string
	var active int
	if err := fixture.database.QueryRow(`SELECT disabled_at::text,password_hash FROM users WHERE username='listener'`).Scan(&after, &hash); err != nil || before != after || !identity.VerifyPassword(hash, resetTestNewPassword) {
		t.Fatalf("停用账号重置改变了停用状态或未保存新密码：%v", err)
	}
	if err := fixture.database.QueryRow(`SELECT count(*) FROM sessions s JOIN users u ON u.id=s.user_id WHERE u.username='listener' AND revoked_at IS NULL`).Scan(&active); err != nil || active != 0 {
		t.Fatalf("停用账号仍有未撤销会话：count=%d err=%v", active, err)
	}
	fixture.request(t, http.MethodPost, "/api/v1/auth/login", "listener", resetTestNewPassword, nil, http.StatusUnauthorized)
	fixture.request(t, http.MethodGet, "/api/v1/auth/me", "", "", fixture.admin, http.StatusOK)
}

// 故障发生在密码 UPDATE 之后，用真实 PostgreSQL 错误验证回滚与脱敏。
func installResetSessionFailure(t *testing.T, database *sql.DB, detail string) {
	t.Helper()
	if _, err := database.Exec(`CREATE TABLE cli_failure_detail(message text NOT NULL);
 CREATE FUNCTION reject_cli_reset() RETURNS trigger LANGUAGE plpgsql AS $$
 DECLARE detail text;
 BEGIN SELECT message INTO detail FROM cli_failure_detail;
 RAISE EXCEPTION USING MESSAGE = detail; END $$;
 CREATE TRIGGER reject_cli_reset BEFORE UPDATE ON sessions FOR EACH ROW EXECUTE FUNCTION reject_cli_reset()`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO cli_failure_detail(message) VALUES($1)`, detail); err != nil {
		t.Fatal(err)
	}
}

func TestPostgreSQLResetPasswordCommandDatabaseFailureRollsBack(t *testing.T) {
	fixture := newResetPasswordApplication(t)
	installResetSessionFailure(t, fixture.database, "test reset failure")
	before := resetPasswordDataSnapshot(t, fixture.database)
	code, stdout, stderr := fixture.command(t, "curator", resetTestNewPassword)
	requireResetCommandResult(t, code, 1, stdout, stderr, "", "数据库出错，密码没有修改。\n")
	if resetPasswordDataSnapshot(t, fixture.database) != before {
		t.Fatal("撤销会话失败后密码、会话或迁移记录没有完整回滚")
	}
	fixture.request(t, http.MethodGet, "/api/v1/auth/me", "", "", fixture.admin, http.StatusOK)
	fixture.login(t, "curator", resetTestOldPassword)
	fixture.request(t, http.MethodPost, "/api/v1/auth/login", "curator", resetTestNewPassword, nil, http.StatusUnauthorized)
}

func TestPostgreSQLResetPasswordCommandOutputHasNoSecrets(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		fixture := newResetPasswordApplication(t)
		code, stdout, stderr := fixture.command(t, "curator", resetTestNewPassword)
		requireResetCommandResult(t, code, 0, stdout, stderr, "已重置“curator”的密码，这个账号已在所有设备上退出登录。\n", "")
		requireResetOutputHasNoSecrets(t, stdout.String()+stderr.String(), resetTestNewPassword, fixture.url)
	})
	t.Run("database failure", func(t *testing.T) {
		fixture := newResetPasswordApplication(t)
		installResetSessionFailure(t, fixture.database, resetTestNewPassword+" "+fixture.url)
		before := resetPasswordDataSnapshot(t, fixture.database)
		code, stdout, stderr := fixture.command(t, "curator", resetTestNewPassword)
		requireResetCommandResult(t, code, 1, stdout, stderr, "", "数据库出错，密码没有修改。\n")
		requireResetOutputHasNoSecrets(t, stdout.String()+stderr.String(), resetTestNewPassword, fixture.url)
		if resetPasswordDataSnapshot(t, fixture.database) != before {
			t.Fatal("包含敏感细节的数据库异常未完整回滚")
		}
	})
	t.Run("unknown user", func(t *testing.T) {
		fixture := newResetPasswordApplication(t)
		code, stdout, stderr := fixture.command(t, "missing", resetTestNewPassword)
		requireResetCommandResult(t, code, 1, stdout, stderr, "", "找不到用户“missing”。\n")
		requireResetOutputHasNoSecrets(t, stdout.String()+stderr.String(), resetTestNewPassword, fixture.url)
	})
	t.Run("connection failure", func(t *testing.T) {
		// 不启动额外数据库，也不接触生产配置；端口 1 在本机不可连接。
		const databaseURL = "postgres://redacted_cli_user:redacted_cli_db_password@127.0.0.1:1/redacted_cli_database?sslmode=disable&connect_timeout=1"
		env, stdout, stderr := resetCommandTestEnv(t, resetTestNewPassword+"\n")
		env.databaseURL = func() (string, error) { return databaseURL, nil }
		env.connect = postgres.Connect
		code := runResetPassword(context.Background(), []string{"curator"}, env)
		requireResetCommandResult(t, code, 1, stdout, stderr, "", "无法连接数据库。\n")
		requireResetOutputHasNoSecrets(t, stdout.String()+stderr.String(), resetTestNewPassword, databaseURL)
	})
}

func TestPostgreSQLResetPasswordCommandDoesNotMigrate(t *testing.T) {
	scopedURL := postgrestest.NewSchema(t, "roomusic_reset_no_migration_")
	env, stdout, stderr := resetCommandTestEnv(t, resetTestNewPassword+"\n")
	env.databaseURL = func() (string, error) { return scopedURL, nil }
	env.connect = postgres.Connect
	code := runResetPassword(context.Background(), []string{"curator"}, env)
	requireResetCommandResult(t, code, 1, stdout, stderr, "", "数据库出错，密码没有修改。\n")
	requireResetOutputHasNoSecrets(t, stdout.String()+stderr.String(), resetTestNewPassword, scopedURL)
	database, err := postgres.Connect(context.Background(), scopedURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	var relations int
	if err := database.QueryRow(`SELECT count(*) FROM pg_class WHERE relnamespace=current_schema()::regnamespace`).Scan(&relations); err != nil || relations != 0 {
		t.Fatalf("命令在空 schema 中创建了数据库对象：relations=%d err=%v", relations, err)
	}
}
