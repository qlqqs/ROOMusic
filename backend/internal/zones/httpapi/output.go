package httpapi

import (
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/zones"
)

func (h *Handlers) claimOutput(w http.ResponseWriter, r *http.Request, userID string) {
	deviceID, ok := pathID(w, r, "device_id")
	if !ok {
		return
	}
	var input zones.ClaimOutputInput
	fields, ok := decodeInput(w, r, &input)
	if !ok {
		return
	}
	if !validClaimFields(fields) {
		invalidInput(w, r)
		return
	}
	result, err := h.service.ClaimOutput(r.Context(), userID, deviceID, input)
	respond(w, r, http.StatusOK, result, err)
}

func (h *Handlers) reportOutput(w http.ResponseWriter, r *http.Request, userID string) {
	sessionID, ok := pathID(w, r, "session_id")
	if !ok {
		return
	}
	var input zones.ReportOutputInput
	fields, ok := decodeInput(w, r, &input)
	if !ok {
		return
	}
	if !fields.only("kind", "stream_seq", "stream_seconds", "status", "fault") ||
		!fields.required("kind", "stream_seconds", "status") || !fields.present("stream_seq", "fault") {
		invalidInput(w, r)
		return
	}
	result, err := h.service.ReportOutput(r.Context(), userID, sessionID, input)
	respond(w, r, http.StatusOK, result, err)
}

func (h *Handlers) releaseOutput(w http.ResponseWriter, r *http.Request, userID string) {
	sessionID, ok := pathID(w, r, "session_id")
	if !ok {
		return
	}
	var input zones.ReleaseOutputInput
	fields, ok := decodeInput(w, r, &input)
	if !ok {
		return
	}
	if !fields.only("stream_seq", "stream_seconds") || !fields.required("stream_seconds") || !fields.present("stream_seq") {
		invalidInput(w, r)
		return
	}
	if err := h.service.ReleaseOutput(r.Context(), userID, sessionID, input); err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
