package httpapi

import (
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

func (handlers *Handlers) analysisProgress(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if _, err := handlers.guard.Authenticate(r); err != nil {
		handlers.guard.WriteUnauthenticated(w, r)
		return
	}
	result, err := handlers.progress.LoadProgress(r.Context())
	if err != nil {
		handlers.logger.ErrorContext(r.Context(), "音频分析进度读取失败", "error", err)
		httpserver.WriteError(w, r, http.StatusServiceUnavailable, "database_unavailable", "暂时无法读取音频分析进度")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, result)
}
