// Package httpapi 提供管理员诊断导出和已登录用户的页面报错接口。
package httpapi

import (
	"log/slog"
	"net/http"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/diagnostics"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

var (
	errorName   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_$]{0,63}$`)
	clientRoute = regexp.MustCompile(`^/[A-Za-z0-9_$./-]{0,199}$`)
)

func Register(mux *http.ServeMux, guard *identity.Guard, service *diagnostics.Service, logger *slog.Logger) {
	mux.HandleFunc("GET /api/v1/admin/diagnostics", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := guard.RequireAdmin(w, r); !ok {
			return
		}
		status, err := service.SystemStatus(r.Context())
		if err != nil {
			httpserver.WriteError(w, r, http.StatusServiceUnavailable, "diagnostics_unavailable", "暂时无法读取诊断信息")
			return
		}
		disks, err := service.DiskUsage(r.Context())
		if err != nil {
			disks = []diagnostics.DiskUsage{}
			status.Errors = append(status.Errors, "disk_unavailable")
		}
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{
			"generated_at": time.Now().UTC(), "status": status, "disks": disks,
			"logs": service.Logs.Recent(diagnostics.LogFilter{Limit: 200}),
		})
	})

	limiter := reportLimiter{reports: make(map[string][]time.Time)}
	mux.HandleFunc("POST /api/v1/diagnostics/client-errors", func(w http.ResponseWriter, r *http.Request) {
		if !guard.RequireSameOrigin(w, r) {
			return
		}
		user, err := guard.Authenticate(r)
		if err != nil {
			guard.WriteUnauthenticated(w, r)
			return
		}
		var fields map[string]any
		r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
		if _, ok := httpserver.DecodeCommand(w, r, &fields); !ok {
			return
		}
		for key := range fields {
			switch key {
			case "kind", "name", "message", "route":
			default:
				httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_json", "请求格式无效")
				return
			}
		}
		kind, _ := fields["kind"].(string)
		name, _ := fields["name"].(string)
		route, _ := fields["route"].(string)
		message, hasMessage := fields["message"].(string)
		if (kind != "error" && kind != "unhandledrejection" && kind != "render") ||
			!errorName.MatchString(name) || !hasMessage || utf8.RuneCountInString(message) > 500 ||
			!clientRoute.MatchString(route) {
			httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_client_error", "页面报错信息无效")
			return
		}
		if !limiter.allow(user.ID) {
			httpserver.WriteError(w, r, http.StatusTooManyRequests, "client_errors_throttled", "页面报错上报过于频繁，请稍后再试")
			return
		}
		logger.WarnContext(r.Context(), "client error reported", "event", "client.error.reported", "module", "frontend",
			"kind", kind, "error_kind", name, "actor_id", user.ID, "request_id", r.Header.Get("X-Request-ID"),
			"client_route", route, "message", message)
		w.WriteHeader(http.StatusNoContent)
	})
}
