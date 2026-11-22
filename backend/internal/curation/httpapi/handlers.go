// Package httpapi 提供自动整理的管理员读取、开关与撤销接口。
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

type Handlers struct {
	program     curation.ProgramStatus
	ruleVersion int16
	sources     *curation.Sources
	writer      *curation.Writer
	reverter    *curation.Reverter
	guard       *identity.Guard
	logger      *slog.Logger
}

func New(writer *curation.Writer, reverter *curation.Reverter, sources *curation.Sources, program curation.ProgramStatus, ruleVersion int16, guard *identity.Guard, logger *slog.Logger) *Handlers {
	return &Handlers{sources: sources, writer: writer, reverter: reverter, program: program, ruleVersion: ruleVersion, guard: guard, logger: logger}
}

func (h *Handlers) Register(mux *http.ServeMux) {
	const base = "/api/v1/admin/curation"
	mux.HandleFunc("GET "+base+"/sources", h.admin(h.listSources))
	mux.HandleFunc("PUT "+base+"/sources/{id}", h.admin(h.setSource))
	mux.HandleFunc("GET "+base+"/progress", h.admin(h.progress))
	mux.HandleFunc("GET "+base+"/settings", h.admin(h.settings))
	mux.HandleFunc("PUT "+base+"/settings", h.admin(h.setSettings))
	mux.HandleFunc("GET "+base+"/batches", h.admin(h.batches))
	mux.HandleFunc("GET "+base+"/batches/{id}", h.admin(h.batch))
	mux.HandleFunc("POST "+base+"/reverts", h.admin(h.revert))
	mux.HandleFunc("GET "+base+"/suppressions", h.admin(h.suppressions))
	mux.HandleFunc("DELETE "+base+"/suppressions/{id}", h.admin(h.clearSuppression))
}

func (h *Handlers) admin(handle func(http.ResponseWriter, *http.Request, identity.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !h.guard.RequireSameOrigin(w, r) {
			return
		}
		actor, ok := h.guard.RequireAdmin(w, r)
		if ok {
			handle(w, r, actor)
		}
	}
}

func (h *Handlers) respond(w http.ResponseWriter, r *http.Request, result any, err error) {
	if err == nil {
		if result == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		httpserver.WriteJSON(w, http.StatusOK, result)
		return
	}
	status, code, message := operations.Problem(err)
	var failure *operations.Failure
	if !errors.As(err, &failure) && !errors.Is(err, operations.ErrOperationFailed) {
		status, code, message = http.StatusServiceUnavailable, "database_unavailable", "服务暂不可用"
	}
	if status >= 500 {
		h.logger.ErrorContext(r.Context(), "自动整理请求失败", "request_id", r.Header.Get("X-Request-ID"), "error", err)
	}
	httpserver.WriteError(w, r, status, code, message)
}

func decode(w http.ResponseWriter, r *http.Request, body any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(body) != nil || decoder.Decode(new(any)) != io.EOF {
		httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return false
	}
	return true
}

func (h *Handlers) progress(w http.ResponseWriter, r *http.Request, _ identity.User) {
	p, err := h.writer.Progress(r.Context(), h.ruleVersion, h.program)
	h.respond(w, r, p, err)
}

func (h *Handlers) settings(w http.ResponseWriter, r *http.Request, _ identity.User) {
	s, err := h.writer.Settings(r.Context())
	h.respond(w, r, s, err)
}

func (h *Handlers) setSettings(w http.ResponseWriter, r *http.Request, actor identity.User) {
	var body struct {
		Enabled *bool `json:"enabled"`
		Paused  *bool `json:"paused"`
	}
	if !decode(w, r, &body) {
		return
	}
	s, err := h.writer.UpdateSettings(r.Context(), actor.ID, body.Enabled, body.Paused)
	h.respond(w, r, s, err)
}

func decodeEnabled(w http.ResponseWriter, r *http.Request, message string) (bool, bool) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if !decode(w, r, &body) {
		return false, false
	}
	if body.Enabled == nil {
		httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_input", message)
		return false, false
	}
	return *body.Enabled, true
}

func (h *Handlers) listSources(w http.ResponseWriter, r *http.Request, _ identity.User) {
	items, err := h.sources.List(r.Context())
	h.respond(w, r, map[string]any{"items": items}, err)
}

func (h *Handlers) setSource(w http.ResponseWriter, r *http.Request, actor identity.User) {
	enabled, ok := decodeEnabled(w, r, "需要指定是否开启外部源")
	if !ok {
		return
	}
	item, err := h.sources.SetEnabled(r.Context(), r.PathValue("id"), actor.ID, enabled)
	h.respond(w, r, item, err)
}

func pagination(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	page, size, err := httpserver.ParsePagination(r, 50, 100)
	if err != nil {
		httpserver.WriteError(w, r, 400, "invalid_pagination", "分页参数无效")
		return 0, 0, false
	}
	return page, size, true
}

func pageResponse(items any, page, size int, total int64) any {
	return map[string]any{"items": items, "pagination": map[string]any{"page": page, "page_size": size, "total": total}}
}

func (h *Handlers) batches(w http.ResponseWriter, r *http.Request, _ identity.User) {
	page, size, ok := pagination(w, r)
	if !ok {
		return
	}
	items, total, err := h.writer.Batches(r.Context(), page, size)
	h.respond(w, r, pageResponse(items, page, size, total), err)
}

func (h *Handlers) batch(w http.ResponseWriter, r *http.Request, _ identity.User) {
	b, err := h.writer.Batch(r.Context(), r.PathValue("id"))
	h.respond(w, r, b, err)
}

func (h *Handlers) revert(w http.ResponseWriter, r *http.Request, actor identity.User) {
	key, ok := operations.NormalizeKey(r.Header.Get("Idempotency-Key"))
	if !ok {
		httpserver.WriteError(w, r, 400, "invalid_input", "请求需要 Idempotency-Key（UUID）")
		return
	}
	var body struct {
		Scope  curation.Scope `json:"scope"`
		Cursor string         `json:"cursor"`
	}
	if !decode(w, r, &body) {
		return
	}
	result, err := h.reverter.RevertScope(r.Context(), actor.ID, body.Scope, body.Cursor, key, r.Header.Get("X-Request-ID"))
	h.respond(w, r, result, err)
}

func (h *Handlers) suppressions(w http.ResponseWriter, r *http.Request, _ identity.User) {
	page, size, ok := pagination(w, r)
	if !ok {
		return
	}
	items, total, err := h.writer.Suppressions(r.Context(), r.URL.Query().Get("release_id"), page, size)
	h.respond(w, r, pageResponse(items, page, size, total), err)
}

func (h *Handlers) clearSuppression(w http.ResponseWriter, r *http.Request, _ identity.User) {
	h.respond(w, r, nil, h.writer.ClearSuppression(r.Context(), r.PathValue("id")))
}
