// Package httpapi 提供全局出站代理的管理员接口。
package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/networkproxy"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

func Register(mux *http.ServeMux, guard *identity.Guard, service *networkproxy.Service) {
	const path = "/api/v1/admin/network/proxy"
	admin := func(handler func(http.ResponseWriter, *http.Request, identity.User) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead && !guard.RequireSameOrigin(w, r) {
				return
			}
			user, ok := guard.RequireAdmin(w, r)
			if !ok {
				return
			}
			err := handler(w, r, user)
			switch {
			case err == nil:
			case errors.Is(err, networkproxy.ErrInvalid):
				httpserver.WriteError(w, r, 400, "invalid_input", "代理设置无效")
			case errors.Is(err, networkproxy.ErrConflict):
				httpserver.WriteError(w, r, 409, "version_conflict", "设置已更新，请重新加载")
			default:
				httpserver.WriteError(w, r, 503, "settings_unavailable", "代理设置暂不可用")
			}
		}
	}
	mux.HandleFunc("GET "+path, admin(func(w http.ResponseWriter, r *http.Request, _ identity.User) error {
		settings, err := service.Get(r.Context())
		if err == nil {
			httpserver.WriteJSON(w, 200, settings)
		}
		return err
	}))
	mux.HandleFunc("PUT "+path, admin(func(w http.ResponseWriter, r *http.Request, user identity.User) error {
		var d networkproxy.Draft
		if !httpserver.DecodeJSON(w, r, &d) {
			return nil
		}
		settings, err := service.Save(r.Context(), d, user.ID)
		if err == nil {
			httpserver.WriteJSON(w, 200, settings)
		}
		return err
	}))
	mux.HandleFunc("POST "+path+"/test", admin(func(w http.ResponseWriter, r *http.Request, _ identity.User) error {
		var d networkproxy.Draft
		if !httpserver.DecodeJSON(w, r, &d) {
			return nil
		}
		start := time.Now()
		code, err := service.Test(r.Context(), d)
		if err != nil {
			return err
		}
		result := struct {
			OK        bool    `json:"ok"`
			Code      *string `json:"code"`
			LatencyMS int64   `json:"latency_ms"`
		}{OK: code == "", LatencyMS: time.Since(start).Milliseconds()}
		if !result.OK {
			result.Code = &code
		}
		httpserver.WriteJSON(w, 200, result)
		return nil
	}))
}
