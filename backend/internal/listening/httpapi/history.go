package httpapi

import (
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func (handlers *Handlers) tracks(w http.ResponseWriter, r *http.Request) {
	user, err := handlers.guard.Authenticate(r)
	if err != nil {
		handlers.guard.WriteUnauthenticated(w, r)
		return
	}
	page, size, err := httpserver.ParsePagination(r, 50, 100)
	if err != nil {
		httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_pagination", "分页参数无效")
		return
	}
	result, err := handlers.service.Tracks(r.Context(), user.ID, page, size)
	if err != nil {
		httpserver.WriteError(w, r, http.StatusServiceUnavailable, "database_unavailable", "无法读取播放历史")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, result)
}

func (handlers *Handlers) queue(w http.ResponseWriter, r *http.Request) {
	user, err := handlers.guard.Authenticate(r)
	if err != nil {
		handlers.guard.WriteUnauthenticated(w, r)
		return
	}
	result, err := handlers.service.Queue(r.Context(), user.ID)
	if err != nil {
		httpserver.WriteError(w, r, http.StatusServiceUnavailable, "database_unavailable", "无法读取播放历史")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, result)
}

func (handlers *Handlers) deleteTrack(w http.ResponseWriter, r *http.Request) {
	if !handlers.guard.RequireSameOrigin(w, r) {
		return
	}
	user, err := handlers.guard.Authenticate(r)
	if err != nil {
		handlers.guard.WriteUnauthenticated(w, r)
		return
	}
	id := r.PathValue("track_id")
	if !uuid.Valid(id) {
		httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_id", "曲目 ID 无效")
		return
	}
	deleted, err := handlers.service.DeleteTrack(r.Context(), user.ID, id)
	if err != nil {
		httpserver.WriteError(w, r, http.StatusServiceUnavailable, "database_unavailable", "无法移除播放历史")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]int64{"deleted": deleted})
}
