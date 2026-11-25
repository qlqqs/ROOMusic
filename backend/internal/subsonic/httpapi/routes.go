package httpapi

import (
	"context"
	"errors"
	"net/http"

	"golang.org/x/sync/singleflight"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/subsonic/auth"
	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
)

type Handlers struct {
	browse          Browse
	artworkFlights  singleflight.Group
	binaryEndpoints map[string]func(http.ResponseWriter, *http.Request) error
	service         *auth.Service
	guard           *identity.Guard
	version         string
	endpoints       map[string]func(*http.Request) protocol.Response
}

type userKey struct{}

func Register(mux *http.ServeMux, service *auth.Service, guard *identity.Guard, version string, browse Browse) {
	if version == "" {
		version = "dev"
	}
	h := &Handlers{browse: browse, service: service, guard: guard, version: version}
	h.endpoints = map[string]func(*http.Request) protocol.Response{
		"ping": func(*http.Request) protocol.Response { return protocol.Response{} },
		"getLicense": func(*http.Request) protocol.Response {
			return protocol.Response{License: &protocol.License{Valid: true}}
		},
		"getOpenSubsonicExtensions": func(*http.Request) protocol.Response {
			return protocol.Response{Extensions: []protocol.Extension{
				{Name: "formPost", Versions: []int{1}},
				{Name: "songLyrics", Versions: []int{1}},
			}}
		},
		"getMusicFolders": func(*http.Request) protocol.Response {
			return protocol.Response{MusicFolders: &protocol.MusicFolders{Folders: []protocol.MusicFolder{{ID: 1, Name: "ROOMusic"}}}}
		},
		"getUser":           getUser,
		"setRating":         h.setRating,
		"getArtists":        h.getArtists,
		"getArtist":         h.getArtist,
		"getAlbum":          h.getAlbum,
		"getSong":           h.getSong,
		"getAlbumList2":     h.getAlbumList2,
		"getArtistInfo2":    h.getArtistInfo2,
		"getAlbumInfo2":     h.getAlbumInfo2,
		"search3":           h.search3,
		"getRandomSongs":    h.getRandomSongs,
		"getGenres":         h.getGenres,
		"star":              h.star,
		"unstar":            h.unstar,
		"getStarred2":       h.getStarred2,
		"getPlaylists":      h.getPlaylists,
		"getPlaylist":       h.getPlaylist,
		"createPlaylist":    h.createPlaylist,
		"updatePlaylist":    h.updatePlaylist,
		"deletePlaylist":    h.deletePlaylist,
		"getLyricsBySongId": h.getLyricsBySongID,
		"getLyrics":         h.getLyrics,
		"jukeboxControl":    h.jukeboxControl,
	}
	h.binaryEndpoints = map[string]func(http.ResponseWriter, *http.Request) error{"getCoverArt": h.getCoverArt}
	mux.HandleFunc("/rest/", h.rest)
	mux.HandleFunc("GET /api/v1/me/subsonic", h.me)
	mux.HandleFunc("PUT /api/v1/me/subsonic", h.setJukeboxZone)
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
	binary := h.binaryEndpoints[params.Endpoint]
	unknown := handle == nil && binary == nil
	if unknown {
		httpserver.SetSubsonicEndpoint(r, "unknown")
	} else {
		httpserver.SetSubsonicEndpoint(r, params.Endpoint)
	}
	if parseErr != nil || unknown {
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
	r = r.WithContext(context.WithValue(r.Context(), userKey{}, user))
	if binary != nil {
		if err := binary(w, r); err != nil {
			write(200, browseFailure(err))
		}
		return
	}
	write(200, handle(r))
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
