// Package httpapi 提供管理员备份接口；文件与备份策略由 backup 持有。
package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/backup"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/buildinfo"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

type Handlers struct {
	service   *backup.Service
	db        *sql.DB
	guard     *identity.Guard
	lifecycle context.Context
}

func New(service *backup.Service, db *sql.DB, guard *identity.Guard, lifecycle context.Context) *Handlers {
	return &Handlers{service: service, db: db, guard: guard, lifecycle: lifecycle}
}
func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/system/version", h.version)
	mux.HandleFunc("GET /api/v1/system/backups", h.overview)
	mux.HandleFunc("POST /api/v1/system/backups", h.start)
	mux.HandleFunc("GET /api/v1/system/backups/{name}", h.download)
}
func (h *Handlers) version(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.guard.RequireAdmin(w, r); !ok {
		return
	}
	info := buildinfo.Current()
	var version, commit *string
	if info.Version != "" {
		version = &info.Version
	}
	if info.Commit != "" {
		commit = &info.Commit
	}
	httpserver.WriteJSON(w, 200, struct {
		Version *string `json:"version"`
		Commit  *string `json:"commit"`
	}{version, commit})
}
func (h *Handlers) overview(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.guard.RequireAdmin(w, r); !ok {
		return
	}
	overview, err := h.service.Overview(r.Context())
	if err != nil {
		httpserver.WriteError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	httpserver.WriteJSON(w, 200, overview)
}
func (h *Handlers) start(w http.ResponseWriter, r *http.Request) {
	if !h.guard.RequireSameOrigin(w, r) {
		return
	}
	if _, ok := h.guard.RequireAdmin(w, r); !ok {
		return
	}
	status, started, err := h.service.StartManual(r.Context(), h.db)
	if errors.Is(err, backup.ErrBusy) {
		httpserver.WriteError(w, r, 409, "backup_busy", "另一份备份正在进行，稍后再试")
		return
	}
	if err != nil {
		httpserver.WriteError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	code := 200
	if started {
		code = 202
	}
	httpserver.WriteJSON(w, code, struct {
		Status backup.Status `json:"status"`
	}{status})
}
func (h *Handlers) download(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.guard.RequireAdmin(w, r); !ok {
		return
	}
	name := r.PathValue("name")
	file, info, err := h.service.OpenBackup(name)
	if err != nil {
		httpserver.WriteError(w, r, 404, "backup_not_found", "备份不存在")
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	reader := &lifecycleReader{ReadSeeker: file, lifecycle: h.lifecycle, request: r.Context()}
	http.ServeContent(w, r, name, info.ModTime(), reader)
}

type lifecycleReader struct {
	io.ReadSeeker
	lifecycle, request context.Context
}

func (r *lifecycleReader) Read(p []byte) (int, error) {
	if err := r.lifecycle.Err(); err != nil {
		return 0, err
	}
	if err := r.request.Err(); err != nil {
		return 0, err
	}
	n, err := r.ReadSeeker.Read(p)
	if canceled := r.lifecycle.Err(); canceled != nil {
		return 0, canceled
	}
	if canceled := r.request.Err(); canceled != nil {
		return 0, canceled
	}
	return n, err
}
