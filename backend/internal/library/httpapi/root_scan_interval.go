package httpapi

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

func (handlers *Handlers) saveRootScanInterval(w http.ResponseWriter, r *http.Request) {
	if !handlers.guard.RequireSameOrigin(w, r) {
		return
	}
	if _, ok := handlers.guard.RequireAdmin(w, r); !ok {
		return
	}
	id, ok := rootIDFromPath(w, r)
	if !ok {
		return
	}
	minutes, ok := decodeRootInterval(w, r)
	if !ok {
		httpserver.WriteError(w, r, 400, "invalid_input", "定时扫描间隔无效")
		return
	}
	if err := handlers.service.SavePollInterval(r.Context(), id, minutes); err != nil {
		handlers.writeRootOperationError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, 200, map[string]int{"poll_interval_minutes": minutes})
}

func decodeRootInterval(w http.ResponseWriter, r *http.Request) (int, bool) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return 0, false
	}
	key, err := decoder.Token()
	if err != nil || key != "poll_interval_minutes" {
		return 0, false
	}
	var minutes int
	if err := decoder.Decode(&minutes); err != nil {
		return 0, false
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return 0, false
	}
	_, err = decoder.Token()
	return minutes, err == io.EOF
}
