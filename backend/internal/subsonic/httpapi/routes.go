package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/subsonic/auth"
	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
)

type Handlers struct {
	ratings   *personal.Service
	service   *auth.Service
	guard     *identity.Guard
	version   string
	endpoints map[string]func(*http.Request) protocol.Response
}

type userKey struct{}

func Register(mux *http.ServeMux, service *auth.Service, ratings *personal.Service, guard *identity.Guard, version string) {
	if version == "" {
		version = "dev"
	}
	h := &Handlers{service: service, ratings: ratings, guard: guard, version: version}
	h.endpoints = map[string]func(*http.Request) protocol.Response{
		"ping": func(*http.Request) protocol.Response { return protocol.Response{} },
		"getLicense": func(*http.Request) protocol.Response {
			return protocol.Response{License: &protocol.License{Valid: true}}
		},
		"getOpenSubsonicExtensions": func(*http.Request) protocol.Response {
			return protocol.Response{Extensions: []protocol.Extension{{Name: "formPost", Versions: []int{1}}}}
		},
		"getMusicFolders": func(*http.Request) protocol.Response {
			return protocol.Response{MusicFolders: &protocol.MusicFolders{Folders: []protocol.MusicFolder{{ID: 1, Name: "ROOMusic"}}}}
		},
		"getUser":   getUser,
		"setRating": h.setRating,
	}
	mux.HandleFunc("/rest/", h.rest)
	mux.HandleFunc("GET /api/v1/me/subsonic", h.me)
	mux.HandleFunc("POST /api/v1/me/subsonic/confirm-password", h.confirm)
	mux.HandleFunc("GET /api/v1/admin/subsonic", h.admin)
	mux.HandleFunc("PUT /api/v1/admin/subsonic", h.setEnabled)
}

func (h *Handlers) rest(w http.ResponseWriter, r *http.Request) {
	params, parseErr := protocol.Parse(w, r)
	write := func(status int, response protocol.Response) {
		protocol.Write(w, params.Values.Get("f"), h.version, status, response)
	}
	handle := h.endpoints[params.Endpoint]
	if handle == nil {
		httpserver.SetSubsonicEndpoint(r, "unknown")
	} else {
		httpserver.SetSubsonicEndpoint(r, params.Endpoint)
	}
	if parseErr != nil || handle == nil {
		if err := h.service.RequireEnabled(r.Context()); err != nil {
			write(200, authFailure(err))
		} else if parseErr != nil {
			write(200, protocol.Response{Error: parseErr})
		} else {
			write(404, protocol.Failure(protocol.General, "Unknown endpoint"))
		}
		return
	}
	user, err := h.service.Authenticate(r.Context(), params, identity.SourceOf(r.RemoteAddr))
	if err != nil {
		write(200, authFailure(err))
		return
	}
	httpserver.SetActor(r, user.ID)
	write(200, handle(r.WithContext(context.WithValue(r.Context(), userKey{}, user))))
}

func authFailure(err error) protocol.Response {
	var throttled *identity.ThrottledError
	switch {
	case errors.Is(err, auth.ErrMissingCredentials):
		return protocol.Failure(protocol.MissingParameter, "Missing required parameter")
	case errors.Is(err, auth.ErrDisabled), errors.Is(err, identity.ErrInvalidCredentials), errors.As(err, &throttled):
		return protocol.Failure(protocol.InvalidCredentials, "Wrong username or password")
	default:
		return protocol.Failure(protocol.General, "Service unavailable")
	}
}

func getUser(r *http.Request) protocol.Response {
	user := r.Context().Value(userKey{}).(identity.User)
	name := r.Form.Get("username")
	if name == "" {
		return protocol.Failure(protocol.MissingParameter, "Missing username")
	}
	if name != user.Username {
		return protocol.Failure(protocol.Forbidden, "Permission denied")
	}
	return protocol.Response{User: &protocol.User{Username: name, DownloadRole: true, JukeboxRole: true, StreamRole: true, Folders: []int{1}}}
}
