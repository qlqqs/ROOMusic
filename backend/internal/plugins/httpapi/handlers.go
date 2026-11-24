// Package httpapi 提供管理员安装和删除数据源插件的入口。
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/plugins"
)

func Register(mux *http.ServeMux, guard *identity.Guard, s *plugins.Store) {
	admin := func(fn func(*http.Request) (any, error), status int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" && r.Method != "HEAD" && !guard.RequireSameOrigin(w, r) {
				return
			}
			if _, ok := guard.RequireAdmin(w, r); !ok {
				return
			}
			if s == nil {
				httpserver.WriteError(w, r, 503, "plugins_unavailable", "插件宿主不可用")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, plugins.MaxPackage+(64<<10))
			out, err := fn(r)
			switch {
			case errors.Is(err, plugins.ErrInvalid):
				httpserver.WriteError(w, r, 400, "invalid_plugin", err.Error())
			case errors.Is(err, plugins.ErrNotFound):
				httpserver.WriteError(w, r, 404, "not_found", "插件不存在")
			case err != nil:
				httpserver.WriteError(w, r, 503, "plugins_unavailable", "插件操作失败")
			case status == 204:
				w.WriteHeader(status)
			default:
				httpserver.WriteJSON(w, status, out)
			}
		}
	}
	const base = "/api/v1/admin/plugins"
	mux.HandleFunc("POST "+base+"/preview", admin(func(r *http.Request) (any, error) {
		var reader io.Reader = r.Body
		media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if media == "multipart/form-data" {
			mr, err := r.MultipartReader()
			if err != nil {
				return nil, plugins.ErrInvalid
			}
			part, err := mr.NextPart()
			if err != nil || part.FormName() != "file" {
				return nil, plugins.ErrInvalid
			}
			defer part.Close()
			reader = part
		}
		b, err := io.ReadAll(io.LimitReader(reader, plugins.MaxPackage+1))
		if err != nil {
			return nil, plugins.ErrInvalid
		}
		return s.Preview(r.Context(), b)
	}, 200))
	mux.HandleFunc("POST "+base, admin(func(r *http.Request) (any, error) {
		var b struct {
			Token string `json:"preview_token"`
		}
		d := json.NewDecoder(io.LimitReader(r.Body, 4096))
		d.DisallowUnknownFields()
		if d.Decode(&b) != nil || d.Decode(new(any)) != io.EOF {
			return nil, plugins.ErrInvalid
		}
		return s.Install(r.Context(), b.Token)
	}, 201))
	mux.HandleFunc("GET "+base, admin(func(r *http.Request) (any, error) {
		items, err := s.List(r.Context())
		return map[string]any{"items": items}, err
	}, 200))
	mux.HandleFunc("DELETE "+base+"/{id}", admin(func(r *http.Request) (any, error) { return nil, s.Delete(r.Context(), r.PathValue("id")) }, 204))
}
