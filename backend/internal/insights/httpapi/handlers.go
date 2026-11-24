// Package httpapi 提供管理员的匿名统计设置与公开许可信息。
package httpapi

import (
	"errors"
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/insights"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

func Register(mux *http.ServeMux, guard *identity.Guard, s *insights.Service, sourceURL string) {
	mux.HandleFunc("GET /api/v1/system/about", func(w http.ResponseWriter, r *http.Request) {
		httpserver.WriteJSON(w, 200, map[string]string{"license": "AGPL-3.0-only", "source_url": sourceURL})
	})
	const base = "/api/v1/admin/insights"
	admin := func(fn http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" && r.Method != "HEAD" && !guard.RequireSameOrigin(w, r) {
				return
			}
			if _, ok := guard.RequireAdmin(w, r); ok {
				fn(w, r)
			}
		}
	}
	unavailable := func(w http.ResponseWriter, r *http.Request) {
		httpserver.WriteError(w, r, 503, "insights_unavailable", "统计设置暂不可用")
	}
	status := func(w http.ResponseWriter, r *http.Request) {
		v, err := s.Status(r.Context())
		if err != nil {
			unavailable(w, r)
			return
		}
		httpserver.WriteJSON(w, 200, v)
	}
	mux.HandleFunc("GET "+base, admin(status))
	mux.HandleFunc("PUT "+base, admin(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Enabled *bool `json:"enabled"`
		}
		if !httpserver.DecodeJSON(w, r, &in) {
			return
		}
		if in.Enabled == nil {
			httpserver.WriteError(w, r, 400, "invalid_input", "需要设置 enabled")
			return
		}
		err := s.SetEnabled(r.Context(), *in.Enabled)
		if errors.Is(err, insights.ErrForcedOff) {
			httpserver.WriteError(w, r, 409, "insights_forced_off", "部署配置已关闭匿名统计")
			return
		}
		if err != nil {
			unavailable(w, r)
			return
		}
		status(w, r)
	}))
	mux.HandleFunc("POST "+base+"/notice-acknowledgement", admin(func(w http.ResponseWriter, r *http.Request) {
		if err := s.Acknowledge(r.Context()); err != nil {
			unavailable(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
}
