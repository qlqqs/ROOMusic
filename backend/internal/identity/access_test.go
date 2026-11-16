package identity

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
)

func TestAccessOriginIfPresent(t *testing.T) {
	for name, guard := range map[string]*Guard{
		"legacy":     NewGuard(nil, false, ""),
		"same-host":  NewGuardWithSameHost(nil),
		"allow-list": NewGuardWithAllowedOrigins(nil, []string{"http://roomusic.test"}),
	} {
		t.Run(name, func(t *testing.T) {
			guard.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
			for _, tc := range []struct {
				name   string
				values []string
				want   bool
			}{
				{"absent", nil, true}, {"trusted", []string{"http://roomusic.test"}, true},
				{"untrusted", []string{"http://untrusted.invalid"}, false}, {"empty", []string{""}, false}, {"null", []string{"null"}, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					r := httptest.NewRequest("POST", "http://roomusic.test/api/v1/mcp", nil)
					if tc.values != nil {
						r.Header["Origin"] = tc.values
					}
					w := httptest.NewRecorder()
					if got := guard.RequireOriginIfPresent(w, r); got != tc.want {
						t.Fatalf("放行=%v，期望 %v", got, tc.want)
					}
					if !tc.want && w.Code != 403 {
						t.Fatalf("status=%d", w.Code)
					}
				})
			}
		})
	}
}
