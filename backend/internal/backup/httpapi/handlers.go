// Package httpapi 提供管理员备份接口；文件与备份策略由 backup 持有。
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"unicode/utf8"

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
	mux.HandleFunc("DELETE /api/v1/system/backups/{name}", h.remove)
	mux.HandleFunc("POST /api/v1/system/backups/deletions", h.removeMany)
	mux.HandleFunc("PUT /api/v1/system/backups/retention", h.saveRetention)
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

func (h *Handlers) remove(w http.ResponseWriter, r *http.Request) {
	if !h.guard.RequireSameOrigin(w, r) {
		return
	}
	user, ok := h.guard.RequireAdmin(w, r)
	if !ok {
		return
	}
	err := h.service.DeleteBackup(r.Context(), r.PathValue("name"), user.ID)
	if err != nil {
		writeBackupMutationError(w, r, err)
		return
	}
	overview, err := h.service.Overview(r.Context())
	if err != nil {
		writeBackupMutationError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, 200, overview)
}

func (h *Handlers) removeMany(w http.ResponseWriter, r *http.Request) {
	if !h.guard.RequireSameOrigin(w, r) {
		return
	}
	user, ok := h.guard.RequireAdmin(w, r)
	if !ok {
		return
	}
	fields, ok := decodeBackupObject(w, r, "要删除的备份无效")
	if !ok {
		return
	}
	names, ok := decodeDeletionNames(fields["names"])
	if !ok {
		httpserver.WriteError(w, r, 400, "invalid_input", "要删除的备份无效")
		return
	}
	result, err := h.service.DeleteBackups(r.Context(), names, user.ID)
	if err != nil {
		writeBackupMutationError(w, r, err)
		return
	}
	overview, err := h.service.Overview(r.Context())
	if err != nil {
		writeBackupMutationError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, 200, struct {
		backup.DeleteResult
		Overview backup.Overview `json:"overview"`
	}{result, overview})
}

func (h *Handlers) saveRetention(w http.ResponseWriter, r *http.Request) {
	if !h.guard.RequireSameOrigin(w, r) {
		return
	}
	user, ok := h.guard.RequireAdmin(w, r)
	if !ok {
		return
	}
	fields, ok := decodeBackupObject(w, r, "清理设置无效")
	if !ok {
		return
	}
	rule, confirm, ok := decodeRetention(fields)
	if !ok {
		httpserver.WriteError(w, r, 400, "invalid_input", "清理设置无效")
		return
	}
	result, err := h.service.SaveRetention(r.Context(), rule, confirm, user.ID)
	if err != nil {
		writeBackupMutationError(w, r, err)
		return
	}
	if result.ConfirmationRequired {
		httpserver.WriteJSON(w, 200, struct {
			ConfirmationRequired bool `json:"confirmation_required"`
			Deletions            int  `json:"deletions"`
		}{true, result.Deletions})
		return
	}
	overview, err := h.service.Overview(r.Context())
	if err != nil {
		writeBackupMutationError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, 200, struct {
		ConfirmationRequired bool            `json:"confirmation_required"`
		Deleted              int             `json:"deleted"`
		Failed               int             `json:"failed"`
		Overview             backup.Overview `json:"overview"`
	}{false, result.Deleted, result.Failed, overview})
}

func writeBackupMutationError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, backup.ErrNotFound):
		httpserver.WriteError(w, r, 404, "backup_not_found", "备份不存在")
	case errors.Is(err, backup.ErrNotWritable):
		httpserver.WriteError(w, r, 503, "backup_directory_not_writable", "备份目录不能写入")
	default:
		httpserver.WriteError(w, r, 503, "database_unavailable", "服务暂不可用")
	}
}

// decodeBackupObject 将 JSON 语法错误与合法 JSON 的输入形状错误分开，拒绝尾随第二个值。
func decodeBackupObject(w http.ResponseWriter, r *http.Request, message string) (map[string]json.RawMessage, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil || !utf8.Valid(body) || !json.Valid(body) {
		httpserver.WriteError(w, r, 400, "invalid_json", "请求格式无效")
		return nil, false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		httpserver.WriteError(w, r, 400, "invalid_input", message)
		return nil, false
	}
	return fields, true
}

func decodeDeletionNames(raw json.RawMessage) ([]string, bool) {
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil || len(values) < 1 || len(values) > 500 {
		return nil, false
	}
	names := make([]string, 0, len(values))
	for _, value := range values {
		var name *string
		if json.Unmarshal(value, &name) != nil || name == nil {
			return nil, false
		}
		names = append(names, *name)
	}
	return names, true
}

func decodeRetention(fields map[string]json.RawMessage) (backup.Retention, bool, bool) {
	var rule backup.Retention
	if json.Unmarshal(fields["keep_days"], &rule.KeepDays) != nil ||
		json.Unmarshal(fields["keep_count"], &rule.KeepCount) != nil ||
		(rule.KeepDays != nil && (*rule.KeepDays < 1 || *rule.KeepDays > 3650)) ||
		(rule.KeepCount != nil && (*rule.KeepCount < 1 || *rule.KeepCount > 1000)) {
		return backup.Retention{}, false, false
	}
	confirm := false
	if raw, exists := fields["confirm"]; exists {
		var value *bool
		if json.Unmarshal(raw, &value) != nil || value == nil {
			return backup.Retention{}, false, false
		}
		confirm = *value
	}
	return rule, confirm, true
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
