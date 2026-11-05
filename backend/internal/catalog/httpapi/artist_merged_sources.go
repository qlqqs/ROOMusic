package httpapi

import (
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

func (h *Handlers) artistMergedSources(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if _, ok := h.guard.RequireAdmin(w, r); !ok {
		return
	}
	id, ok := artistReadID(w, r)
	if !ok {
		return
	}
	result, err := governance.ReadArtistMergedSources(r.Context(), h.database, id)
	if err != nil {
		h.writeArtistReadError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, result)
}
