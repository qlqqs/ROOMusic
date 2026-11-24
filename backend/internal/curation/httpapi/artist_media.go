package httpapi

import (
	"net/http"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func (h *Handlers) registerArtistMedia(mux *http.ServeMux) {
	const base = "/api/v1/artists/{id}"
	mux.HandleFunc("PUT "+base+"/image", h.artistCommand(true, h.putArtistImage))
	mux.HandleFunc("PUT "+base+"/bio", h.artistCommand(true, h.putArtistBio))
	mux.HandleFunc("PUT "+base+"/links", h.artistCommand(true, h.putArtistLink))
	mux.HandleFunc("POST "+base+"/link-preview", h.artistCommand(false, h.previewArtistLink))
	mux.HandleFunc("POST "+base+"/media/reject", h.artistCommand(true, h.rejectArtistMedia))
}

func (h *Handlers) artistCommand(write bool, handle func(http.ResponseWriter, *http.Request, curation.ArtistEditRequest)) http.HandlerFunc {
	return h.admin(func(w http.ResponseWriter, r *http.Request, user identity.User) {
		id := strings.ToLower(r.PathValue("id"))
		if !uuid.Valid(id) {
			httpserver.WriteError(w, r, 400, "invalid_id", "艺人标识无效")
			return
		}
		cmd := curation.ArtistEditRequest{ArtistID: id, ActorID: user.ID, RequestID: r.Header.Get("X-Request-ID")}
		if write {
			key, ok := operations.NormalizeKey(r.Header.Get("Idempotency-Key"))
			if !ok {
				httpserver.WriteError(w, r, 400, "invalid_input", "请求需要 Idempotency-Key（UUID）")
				return
			}
			cmd.Key = key
		}
		handle(w, r, cmd)
	})
}

func (h *Handlers) putArtistBio(w http.ResponseWriter, r *http.Request, cmd curation.ArtistEditRequest) {
	var body struct {
		ExpectedRevision int64  `json:"expected_revision"`
		Text             string `json:"text"`
		Lang             string `json:"lang"`
	}
	if _, ok := httpserver.DecodeCommand(w, r, &body); !ok {
		return
	}
	cmd.ExpectedRevision = body.ExpectedRevision
	result, err := h.writer.PutArtistBio(r.Context(), cmd, body.Text, body.Lang)
	h.respond(w, r, result.Response, err)
}

func (h *Handlers) rejectArtistMedia(w http.ResponseWriter, r *http.Request, cmd curation.ArtistEditRequest) {
	var body struct {
		ExpectedRevision int64  `json:"expected_revision"`
		Field            string `json:"field"`
	}
	if _, ok := httpserver.DecodeCommand(w, r, &body); !ok {
		return
	}
	cmd.ExpectedRevision = body.ExpectedRevision
	result, err := h.writer.RejectArtistMedia(r.Context(), cmd, body.Field)
	h.respond(w, r, result.Response, err)
}

func (h *Handlers) previewArtistLink(w http.ResponseWriter, r *http.Request, cmd curation.ArtistEditRequest) {
	var body struct {
		URL string `json:"url"`
	}
	if _, ok := httpserver.DecodeCommand(w, r, &body); !ok {
		return
	}
	result, err := h.writer.PreviewArtistLink(r.Context(), h.sources, cmd.ArtistID, body.URL)
	h.respond(w, r, result, err)
}

func (h *Handlers) putArtistLink(w http.ResponseWriter, r *http.Request, cmd curation.ArtistEditRequest) {
	var body struct {
		ExpectedRevision int64  `json:"expected_revision"`
		Source           string `json:"source"`
		ExternalID       string `json:"external_id"`
	}
	if _, ok := httpserver.DecodeCommand(w, r, &body); !ok {
		return
	}
	result, err := h.writer.ApplyArtistLink(r.Context(), h.sources, curation.ArtistLinkRequest{
		ArtistID: cmd.ArtistID, ActorID: cmd.ActorID, Key: cmd.Key, RequestID: cmd.RequestID,
		ExpectedRevision: body.ExpectedRevision, ExternalSource: body.Source, ExternalID: body.ExternalID,
	})
	h.respond(w, r, result, err)
}
