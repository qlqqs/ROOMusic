package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func (h *Handlers) releaseMergePreview(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.guard.RequireAdmin(w, r); !ok {
		return
	}
	sources := r.URL.Query()["source"]
	if len(sources) != 1 {
		h.writeOperationError(w, r, operations.Invalid("invalid_input", "请选择一个要并入的发行版本"))
		return
	}
	preview, err := h.governance.PreviewReleaseMerge(r.Context(), strings.ToLower(r.PathValue("id")), strings.ToLower(sources[0]))
	var failure *operations.Failure
	switch {
	case errors.As(err, &failure):
		h.writeOperationError(w, r, failure)
	case errors.Is(err, catalog.ErrQuery):
		httpserver.WriteError(w, r, http.StatusServiceUnavailable, "database_unavailable", "服务暂不可用")
	case err != nil:
		httpserver.WriteError(w, r, http.StatusServiceUnavailable, "database_error", "无法读取发行合并预览")
	default:
		httpserver.WriteJSON(w, http.StatusOK, preview)
	}
}

func (h *Handlers) mergeReleases(w http.ResponseWriter, r *http.Request) {
	if !h.guard.RequireSameOrigin(w, r) {
		return
	}
	actor, ok := h.guard.RequireAdmin(w, r)
	if !ok {
		return
	}
	key, ok := operations.NormalizeKey(r.Header.Get("Idempotency-Key"))
	if !ok {
		h.writeOperationError(w, r, operations.Invalid("invalid_input", "请求需要 Idempotency-Key（UUID）"))
		return
	}
	var body struct {
		SourceID string `json:"source_release_id"`
		Token    string `json:"token"`
	}
	if !httpserver.DecodeJSON(w, r, &body) {
		return
	}
	change := governance.ReleaseMergeRequest{TargetID: strings.ToLower(r.PathValue("id")), SourceID: strings.ToLower(body.SourceID), Token: body.Token}
	if !uuid.Valid(change.TargetID) || !uuid.Valid(change.SourceID) || change.TargetID == change.SourceID || change.Token == "" {
		h.writeOperationError(w, r, operations.Invalid("invalid_input", "合并需要两个不同的发行版本和预览令牌"))
		return
	}
	result, err := operations.Run(r.Context(), h.database, operations.Spec{
		ActorID: actor.ID, Type: operations.TypeReleaseMerge, Key: key, Fingerprint: change.Fingerprint(), RequestID: r.Header.Get("X-Request-ID"),
	}, governance.ReleaseMergeExecutor(change))
	if err != nil {
		h.writeOperationError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, result.Response)
}
