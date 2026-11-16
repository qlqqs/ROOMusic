package httpapi

import (
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/steward/memory"
)

// WithMemories 注入与工具、运行时共享的本人记忆存储。
func (h *Handlers) WithMemories(memories *memory.Store) *Handlers {
	h.memories = memories
	return h
}

func (h *Handlers) registerMemories(mux *http.ServeMux) {
	h.routeWithMemberAccess(mux, "GET /api/v1/me/steward/memories", false, true, h.listMemories)
	h.routeWithMemberAccess(mux, "DELETE /api/v1/me/steward/memories", false, true, h.deleteMemories)
	h.routeWithMemberAccess(mux, "DELETE /api/v1/me/steward/memories/{id}", false, true, h.deleteMemories)
	h.route(mux, "POST /api/v1/me/steward/memories", false, h.saveMemory)
	h.route(mux, "PATCH /api/v1/me/steward/memories/{id}", false, h.saveMemory)
}

func (h *Handlers) listMemories(w http.ResponseWriter, r *http.Request, user identity.User) {
	result, err := h.memories.List(r.Context(), user.ID)
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, 200, result)
}

func (h *Handlers) saveMemory(w http.ResponseWriter, r *http.Request, user identity.User) {
	var input struct {
		Content string `json:"content"`
	}
	if !decodeInput(w, r, &input) {
		return
	}
	var id *string
	status := 201
	if r.Method == http.MethodPatch {
		value := r.PathValue("id")
		id = &value
		status = 200
	}
	item, err := h.memories.Remember(r.Context(), user.ID, id, input.Content)
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, status, item)
}

func (h *Handlers) deleteMemories(w http.ResponseWriter, r *http.Request, user identity.User) {
	var id *string
	if value := r.PathValue("id"); value != "" {
		id = &value
	}
	if err := h.memories.Forget(r.Context(), user.ID, id); err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
