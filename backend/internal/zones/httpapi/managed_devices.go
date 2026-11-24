package httpapi

import "net/http"

func (h *Handlers) activate(w http.ResponseWriter, r *http.Request, userID string) {
	id, ok := pathID(w, r, "device_id")
	if !ok {
		return
	}
	var input struct {
		Takeover bool `json:"takeover"`
	}
	fields, ok := decodeInput(w, r, &input)
	if !ok {
		return
	}
	if !fields.only("takeover") || (len(fields) > 0 && !fields.required("takeover")) {
		invalidInput(w, r)
		return
	}
	result, err := h.service.ActivateDevice(r.Context(), userID, id, input.Takeover)
	respond(w, r, http.StatusOK, result, err)
}

func (h *Handlers) sampleRate(w http.ResponseWriter, r *http.Request, userID string) {
	id, ok := pathID(w, r, "device_id")
	if !ok {
		return
	}
	var input struct {
		MaxSampleRate int `json:"max_sample_rate"`
	}
	fields, ok := decodeInput(w, r, &input)
	if !ok {
		return
	}
	if !fields.only("max_sample_rate") || !fields.required("max_sample_rate") {
		invalidInput(w, r)
		return
	}
	result, err := h.service.SetDeviceSampleRate(r.Context(), userID, id, input.MaxSampleRate)
	respond(w, r, http.StatusOK, result, err)
}
