package httpapi

import (
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/dspsettings"
)

func (h *Handlers) presets(w http.ResponseWriter, r *http.Request, userID string) {
	items, err := h.service.ListPresets(r.Context(), userID)
	respond(w, r, http.StatusOK, struct {
		Items []dspsettings.Preset `json:"items"`
	}{items}, err)
}

func (h *Handlers) createPreset(w http.ResponseWriter, r *http.Request, userID string) {
	fields, ok := decodeInput(w, r, "name", "peq")
	if !ok {
		return
	}
	var name string
	if !fields.field("name", &name) {
		invalidInput(w, r)
		return
	}
	document, err := fields.peq()
	if err != nil {
		invalidInput(w, r)
		return
	}
	result, err := h.service.CreatePreset(r.Context(), userID, name, document)
	respond(w, r, http.StatusCreated, result, err)
}

func (h *Handlers) updatePreset(w http.ResponseWriter, r *http.Request, userID string) {
	id, ok := presetID(w, r)
	if !ok {
		return
	}
	fields, ok := decodeInput(w, r, "name", "peq")
	if !ok {
		return
	}
	patch := dspsettings.PresetPatch{}
	if _, exists := fields["name"]; exists {
		var name string
		if !fields.field("name", &name) {
			invalidInput(w, r)
			return
		}
		patch.Name = &name
	}
	if _, exists := fields["peq"]; exists {
		document, err := fields.peq()
		if err != nil {
			invalidInput(w, r)
			return
		}
		patch.PEQ = &document
	}
	result, err := h.service.UpdatePreset(r.Context(), userID, id, patch)
	respond(w, r, http.StatusOK, result, err)
}

func (h *Handlers) deletePreset(w http.ResponseWriter, r *http.Request, userID string) {
	id, ok := presetID(w, r)
	if !ok {
		return
	}
	if err := h.service.DeletePreset(r.Context(), userID, id); err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
