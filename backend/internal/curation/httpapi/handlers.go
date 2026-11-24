// Package httpapi 提供自动整理的管理员读取、开关与撤销接口。
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

type Handlers struct {
	model       curation.ModelStatus
	exceptions  curation.ExceptionResolver
	program     curation.ProgramStatus
	ruleVersion int16
	sources     *curation.Sources
	writer      *curation.Writer
	reverter    *curation.Reverter
	guard       *identity.Guard
	logger      *slog.Logger
}

func New(writer *curation.Writer, reverter *curation.Reverter, sources *curation.Sources, program curation.ProgramStatus, model curation.ModelStatus, exceptions curation.ExceptionResolver, ruleVersion int16, guard *identity.Guard, logger *slog.Logger) *Handlers {
	return &Handlers{model: model, exceptions: exceptions, sources: sources, writer: writer, reverter: reverter, program: program, ruleVersion: ruleVersion, guard: guard, logger: logger}
}

func (h *Handlers) Register(mux *http.ServeMux) {
	h.registerArtistMedia(mux)
	const base = "/api/v1/admin/curation"
	mux.HandleFunc("GET "+base+"/sources", h.admin(h.listSources))
	mux.HandleFunc("PUT "+base+"/sources/{id}", h.admin(h.setSource))
	mux.HandleFunc("PUT "+base+"/sources/{id}/keys/{name}", h.admin(h.setSourceKey))
	mux.HandleFunc("DELETE "+base+"/sources/{id}/keys/{name}", h.admin(h.clearSourceKey))
	mux.HandleFunc("GET "+base+"/progress", h.admin(h.progress))
	mux.HandleFunc("GET "+base+"/exceptions", h.admin(h.listExceptions))
	mux.HandleFunc("POST "+base+"/exceptions/{id}/accept", h.admin(h.acceptException))
	mux.HandleFunc("POST "+base+"/exceptions/{id}/decline", h.admin(h.declineException))
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
	if curation.IsSourceUnavailable(err) {
		status, code, message = http.StatusServiceUnavailable, "source_unavailable", "外部资料源暂不可用"
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
	if err != nil {
		h.respond(w, r, nil, err)
		return
	}
	model, err := h.model.ModelStatus(r.Context())
	h.respond(w, r, struct {
		curation.Progress
		Model curation.ModelProgress `json:"model"`
	}{p, model}, err)
}

func (h *Handlers) settings(w http.ResponseWriter, r *http.Request, _ identity.User) {
	s, err := h.writer.Settings(r.Context())
	h.respond(w, r, s, err)
}

func (h *Handlers) setSettings(w http.ResponseWriter, r *http.Request, actor identity.User) {
	var body struct {
		Enabled         *bool           `json:"enabled"`
		Paused          *bool           `json:"paused"`
		Model           json.RawMessage `json:"model"`
		DailyTokenLimit json.RawMessage `json:"daily_token_limit"`
	}
	if !decode(w, r, &body) {
		return
	}
	var patch curation.ModelSettingsPatch
	if body.Model != nil && (json.Unmarshal(body.Model, &patch.Model) != nil || patch.Model == nil || strings.IndexByte(*patch.Model, 0) >= 0) {
		h.respond(w, r, nil, operations.Invalid("invalid_input", "模型名称无效"))
		return
	}
	patch.SetDailyTokenLimit = body.DailyTokenLimit != nil
	if patch.SetDailyTokenLimit && json.Unmarshal(body.DailyTokenLimit, &patch.DailyTokenLimit) != nil {
		h.respond(w, r, nil, operations.Invalid("invalid_input", "每日用量上限无效"))
		return
	}
	s, err := h.writer.UpdateModelSettings(r.Context(), actor.ID, body.Enabled, body.Paused, patch)
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

func (h *Handlers) setSourceKey(w http.ResponseWriter, r *http.Request, actor identity.User) {
	var body struct {
		Key string `json:"key"`
	}
	if !decode(w, r, &body) {
		return
	}
	item, err := h.sources.SetKey(r.Context(), r.PathValue("id"), r.PathValue("name"), actor.ID, body.Key)
	h.respondSourceKey(w, r, item, err)
}

func (h *Handlers) clearSourceKey(w http.ResponseWriter, r *http.Request, actor identity.User) {
	item, err := h.sources.ClearKey(r.Context(), r.PathValue("id"), r.PathValue("name"), actor.ID)
	h.respondSourceKey(w, r, item, err)
}

func (h *Handlers) respondSourceKey(w http.ResponseWriter, r *http.Request, item curation.SourceSettings, err error) {
	if err == nil {
		h.logger.InfoContext(r.Context(), "外部源密钥设置已更新", "event", "curation.source.key_changed", "module", "curation", "source", item.ID, "key_name", r.PathValue("name"), "configured", r.Method == http.MethodPut)
	}
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

func (h *Handlers) listExceptions(w http.ResponseWriter, r *http.Request, _ identity.User) {
	page, size, err := httpserver.ParsePagination(r, 50, 100)
	if err != nil {
		h.respond(w, r, nil, operations.Invalid("invalid_input", "分页参数无效"))
		return
	}
	status := r.URL.Query().Get("status")
	if !r.URL.Query().Has("status") {
		status = "open"
	}
	items, total, err := h.writer.ListExceptions(r.Context(), status, page, size)
	h.respond(w, r, map[string]any{"items": items, "total": total, "page": page, "page_size": size}, err)
}

func (h *Handlers) acceptException(w http.ResponseWriter, r *http.Request, actor identity.User) {
	e, err := h.exceptions.Accept(r.Context(), r.PathValue("id"), actor.ID, r.Header.Get("X-Request-ID"))
	h.respond(w, r, map[string]any{"exception": e, "operation_ids": e.OperationIDs}, err)
}

func (h *Handlers) declineException(w http.ResponseWriter, r *http.Request, actor identity.User) {
	e, err := h.exceptions.Decline(r.Context(), r.PathValue("id"), actor.ID)
	h.respond(w, r, map[string]any{"exception": e}, err)
}
