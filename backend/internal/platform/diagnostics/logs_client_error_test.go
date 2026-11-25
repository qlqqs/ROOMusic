package diagnostics

import (
	"io"
	"log/slog"
	"testing"
)

func TestDiagnosticLogClientRouteRedaction(t *testing.T) {
	for _, tc := range []struct{ key, input, want string }{
		{"client_route", "/albums/$groupId", "/albums/$groupId"},
		{"client_route", "/settings/about", "/settings/about"},
		{"client_route", "/", "/"},
		{"client_route", "/me/", "/me/"},
		{"client_route", "/$", "/$"},
		{"client_route", "/albums/123", "[redacted]"},
		{"client_route", "/albums/30000000-0000-4000-8000-000000000001", "[redacted]"},
		{"client_route", "/settings/sk-synthetic", "[redacted]"},
		{"route_template", "GET /api/v1/releases/{id}", "GET /api/v1/releases/{id}"},
		{"route_template", "/readyz", "/readyz"},
		{"route_template", "<unmatched>", "<unmatched>"},
		{"route_template", "/settings/about", "[redacted]"},
	} {
		t.Run(tc.key+" "+tc.input, func(t *testing.T) {
			ring := NewLogRing()
			logger := slog.New(NewHandler(slog.NewJSONHandler(io.Discard, nil), ring))
			logger.Info("synthetic diagnostic log", tc.key, tc.input)
			records := ring.Recent(LogFilter{})
			if len(records) != 1 {
				t.Fatalf("日志环记录数 = %d，期望 1", len(records))
			}
			if got := records[0].Fields[tc.key]; got != tc.want {
				t.Errorf("%s = %q，期望 %q", tc.key, got, tc.want)
			}
		})
	}
}

func TestDiagnosticLogHandlerDoesNotDuplicateForSameRing(t *testing.T) {
	ring := NewLogRing()
	output := &c9Handler{}
	wrapped := NewHandler(output, ring)
	logger := slog.New(NewHandler(wrapped, ring))
	logger.Warn("client error reported", "event", "client.error.reported")

	if len(output.records) != 1 {
		t.Errorf("原 Handler 收到 %d 条日志，期望 1 条", len(output.records))
	}
	if got := len(ring.Recent(LogFilter{})); got != 1 {
		t.Errorf("重复包装同一个日志环后记录数 = %d，期望 1", got)
	}
}
