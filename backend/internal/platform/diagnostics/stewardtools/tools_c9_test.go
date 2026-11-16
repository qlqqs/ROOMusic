package stewardtools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/diagnostics"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func TestC9DiagnosticsToolsOutputAndValidation(t *testing.T) {
	ring := diagnostics.NewLogRing("/synthetic/music")
	logger := slog.New(diagnostics.NewHandler(slog.NewJSONHandler(io.Discard, nil), ring))
	logger.Error("private-message", "module", "library", "PaSsWoRd", "secret-sentinel", "api_KEY", "key-sentinel", "cookie", "cookie-sentinel", "dsn", "postgres://u:dsn-sentinel@db/app", "authorization", "Bearer bearer-sentinel", "body", "body-sentinel", "safe", "/synthetic/music/album/track.flac", "outside", "/outside/private/file.flac", "error", errors.New("error-sentinel /outside/private/file.flac"), "nested", slog.GroupValue(slog.String("ToKeN", "nested-sentinel")))
	service := &diagnostics.Service{Logs: ring, StartedAt: time.Now(), DataDirectory: t.TempDir(), BackupDirectory: t.TempDir(), MusicDirectories: func(context.Context) ([]diagnostics.Directory, error) { return nil, nil }}
	registry := tool.NewRegistry()
	for _, d := range StewardTools(service) {
		if err := registry.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	caller := tool.Caller{User: identity.User{Role: "admin"}}
	for _, name := range []string{"list_recent_logs", "get_system_status", "get_disk_usage"} {
		t.Run(name, func(t *testing.T) {
			result, err := registry.Invoke(context.Background(), caller, name, json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			for _, bad := range []string{"sentinel", "private-message", "/synthetic/", "/outside/", "postgres://", "Bearer ", service.DataDirectory, service.BackupDirectory, "ROOMUSIC_"} {
				if strings.Contains(string(result.Output), bad) {
					t.Errorf("输出泄露 %q: %s", bad, result.Output)
				}
			}
		})
	}
	for _, input := range []string{`{"limit":0}`, `{"limit":201}`, `{"level":"error"}`, `{"start":"yesterday"}`, `{"start":"2026-11-17T00:00:00Z","end":"2026-11-16T00:00:00Z"}`, `{"unknown":true}`} {
		t.Run(input, func(t *testing.T) {
			_, err := registry.Invoke(context.Background(), caller, "list_recent_logs", json.RawMessage(input))
			var e *tool.Error
			if !errors.As(err, &e) || e.Code != tool.CodeInvalidInput {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestC9RecentLogsRedactsAPIKeyAssignmentsInValues(t *testing.T) {
	for _, value := range []string{"api_key=c9-api-secret", "API_KEY=c9-api-secret", "access_token=c9-token-secret", "provider_api_key=c9-api-secret", "OPENAI_API_KEY=c9-api-secret", "?access_token=c9-token-secret", `{"password":"c9-api-secret"}`, "password: c9-api-secret", "Authorization: Basic c9-api-secret", "Token c9-token-secret", "Bearer c9-token-secret", "Cookie: c9-api-secret", "sk-c9-api-secret", "sk-ant-c9-api-secret", "synthetic:c9-api-secret@host", "https://host/?access_token=c9-token-secret"} {
		t.Run(value, func(t *testing.T) {
			ring := diagnostics.NewLogRing()
			logger := slog.New(diagnostics.NewHandler(slog.NewJSONHandler(io.Discard, nil), ring))
			logger.Info("合成调用失败", "module", "steward", "reason", value)
			registry := tool.NewRegistry()
			for _, d := range StewardTools(&diagnostics.Service{Logs: ring}) {
				if err := registry.Register(d); err != nil {
					t.Fatal(err)
				}
			}
			result, err := registry.Invoke(context.Background(), tool.Caller{User: identity.User{Role: "admin"}}, "list_recent_logs", json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			var out struct {
				Items []diagnostics.LogRecord `json:"items"`
			}
			if err := json.Unmarshal(result.Output, &out); err != nil {
				t.Fatal(err)
			}
			if len(out.Items) != 1 || out.Items[0].Fields["reason"] != "[redacted]" {
				t.Errorf("白名单字段未脱敏: %s", result.Output)
			}
			if strings.Contains(string(result.Output), "c9-api-secret") || strings.Contains(string(result.Output), "c9-token-secret") {
				t.Fatalf("日志工具泄露赋值形式的凭据: %s", result.Output)
			}
		})
	}
}

func TestC9RecentLogsToolFiltersAndLimit(t *testing.T) {
	ring := diagnostics.NewLogRing()
	handler := diagnostics.NewHandler(slog.NewJSONHandler(io.Discard, nil), ring)
	start := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 205; i++ {
		level := slog.LevelInfo
		if i%2 == 0 {
			level = slog.LevelError
		}
		r := slog.NewRecord(start.Add(time.Duration(i)*time.Second), level, "不保存正文", 0)
		r.AddAttrs(slog.String("module", "library"), slog.Int("count", i))
		if err := handler.Handle(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	registry := tool.NewRegistry()
	for _, d := range StewardTools(&diagnostics.Service{Logs: ring}) {
		if err := registry.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name, input string
		count       int
		first       int
	}{{"default", `{}`, 100, 204}, {"maximum", `{"limit":200}`, 200, 204}, {"filtered", `{"level":"ERROR","module":"library","start":"2026-11-16T00:01:40Z","end":"2026-11-16T00:01:44Z"}`, 3, 104}, {"module", `{"module":"other"}`, 0, 0}, {"limit", `{"limit":1}`, 1, 204}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := registry.Invoke(context.Background(), tool.Caller{User: identity.User{Role: "admin"}}, "list_recent_logs", json.RawMessage(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			var out struct {
				Items []diagnostics.LogRecord `json:"items"`
			}
			if err := json.Unmarshal(result.Output, &out); err != nil {
				t.Fatal(err)
			}
			if len(out.Items) != tc.count {
				t.Fatalf("数量=%d want=%d", len(out.Items), tc.count)
			}
			if tc.count > 0 && out.Items[0].Fields["count"] != float64(tc.first) {
				t.Fatalf("顺序=%+v", out.Items[0])
			}
		})
	}
}
