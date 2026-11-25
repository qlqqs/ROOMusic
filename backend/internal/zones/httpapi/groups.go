package httpapi

import "net/http"

func (h *Handlers) addGroupMember(w http.ResponseWriter, r *http.Request, userID string) {
	zoneID, ok := pathID(w, r, "zone_id")
	if !ok {
		return
	}
	var input struct {
		DeviceID string `json:"device_id"`
		Takeover bool   `json:"takeover"`
	}
	fields, ok := decodeInput(w, r, &input)
	if !ok {
		return
	}
	if !fields.only("device_id", "takeover") || !fields.required("device_id") || (fields["takeover"] != nil && !fields.required("takeover")) {
		invalidInput(w, r)
		return
	}
	result, err := h.service.AddGroupMember(r.Context(), userID, zoneID, input.DeviceID, input.Takeover)
	respond(w, r, http.StatusOK, result, err)
}

func (h *Handlers) removeGroupMember(w http.ResponseWriter, r *http.Request, userID string) {
	zoneID, ok := pathID(w, r, "zone_id")
	if !ok {
		return
	}
	deviceID, ok := pathID(w, r, "device_id")
	if !ok {
		return
	}
	if r.Body != nil && r.ContentLength != 0 {
		fields, ok := decodeInput(w, r, &struct{}{})
		if !ok {
			return
		}
		if len(fields) > 0 {
			invalidInput(w, r)
			return
		}
	}
	result, err := h.service.RemoveGroupMember(r.Context(), userID, zoneID, deviceID)
	respond(w, r, http.StatusOK, result, err)
}

func (h *Handlers) setGroupVolume(w http.ResponseWriter, r *http.Request, userID string) {
	zoneID, ok := pathID(w, r, "zone_id")
	if !ok {
		return
	}
	var input struct {
		Volume float64 `json:"volume"`
	}
	fields, ok := decodeInput(w, r, &input)
	if !ok {
		return
	}
	if !fields.only("volume") || !fields.required("volume") {
		invalidInput(w, r)
		return
	}
	result, err := h.service.SetGroupVolume(r.Context(), userID, zoneID, input.Volume)
	respond(w, r, http.StatusOK, result, err)
}
func (h *Handlers) setSyncDelay(w http.ResponseWriter, r *http.Request, userID string) {
	id, ok := pathID(w, r, "device_id")
	if !ok {
		return
	}
	var input struct {
		SyncDelayMS int `json:"sync_delay_ms"`
	}
	fields, ok := decodeInput(w, r, &input)
	if !ok {
		return
	}
	if !fields.only("sync_delay_ms") || !fields.required("sync_delay_ms") {
		invalidInput(w, r)
		return
	}
	result, err := h.service.SetSyncDelay(r.Context(), userID, id, input.SyncDelayMS)
	respond(w, r, http.StatusOK, result, err)
}
