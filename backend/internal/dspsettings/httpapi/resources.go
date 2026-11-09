package httpapi

import (
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/dspsettings"
)

func (h *Handlers) search(w http.ResponseWriter, r *http.Request, _ string) {
	items, err := h.service.SearchOPRA(r.URL.Query().Get("q"))
	respond(w, r, http.StatusOK, struct {
		Items []dspsettings.ProductMatch `json:"items"`
	}{items}, err)
}

func (h *Handlers) product(w http.ResponseWriter, r *http.Request, _ string) {
	result, err := h.service.OPRAProduct(r.PathValue("id"))
	respond(w, r, http.StatusOK, result, err)
}

func (h *Handlers) importPEQ(w http.ResponseWriter, r *http.Request, _ string) {
	fields, ok := decodeInput(w, r, "text")
	if !ok {
		return
	}
	var text string
	if !fields.field("text", &text) {
		invalidInput(w, r)
		return
	}
	result, err := h.service.ImportPEQ(text)
	respond(w, r, http.StatusOK, result, err)
}

func (h *Handlers) response(w http.ResponseWriter, r *http.Request, _ string) {
	fields, ok := decodeInput(w, r, "peq", "sample_rate")
	if !ok {
		return
	}
	document, err := fields.peq()
	if err != nil {
		invalidInput(w, r)
		return
	}
	var rate *int
	if _, exists := fields["sample_rate"]; exists {
		var value int
		if !fields.field("sample_rate", &value) {
			invalidInput(w, r)
			return
		}
		rate = &value
	}
	result, err := h.service.PEQResponse(document, rate)
	respond(w, r, http.StatusOK, result, err)
}
