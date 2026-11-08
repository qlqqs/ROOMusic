package httpapi

import (
	"errors"
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

func (h *Handlers) catalogStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if _, err := h.guard.Authenticate(r); err != nil {
		h.guard.WriteUnauthenticated(w, r)
		return
	}
	stats, err := catalog.ReadCatalogStats(r.Context(), h.database)
	if err != nil {
		if errors.Is(err, catalog.ErrQuery) {
			httpserver.WriteError(w, r, http.StatusServiceUnavailable, "database_unavailable", "服务暂不可用")
			return
		}
		h.logger.ErrorContext(r.Context(), "资料库统计读取失败", "error", err)
		httpserver.WriteError(w, r, http.StatusServiceUnavailable, "database_unavailable", "暂时无法读取资料库统计")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, stats)
}
