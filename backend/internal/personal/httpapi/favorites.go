package httpapi

import "net/http"

func (h *Handlers) favoriteArtists(w http.ResponseWriter, r *http.Request, user string) {
	page, ok := parsePage(w, r, 50, 100, 0)
	if !ok {
		return
	}
	result, err := h.service.FavoriteArtists(r.Context(), user, page)
	respond(w, r, result, err)
}
func (h *Handlers) favoriteArtist(w http.ResponseWriter, r *http.Request, user string) {
	h.setFavorite(w, r, user, "artist", "artist_id")
}

func (h *Handlers) favoriteTracks(w http.ResponseWriter, r *http.Request, user string) {
	page, ok := parsePage(w, r, 50, 100, 0)
	if !ok {
		return
	}
	result, err := h.service.FavoriteTracks(r.Context(), user, page)
	respond(w, r, result, err)
}
func (h *Handlers) favoriteAlbums(w http.ResponseWriter, r *http.Request, user string) {
	page, ok := parsePage(w, r, 50, 100, 0)
	if !ok {
		return
	}
	result, err := h.service.FavoriteAlbums(r.Context(), user, page)
	respond(w, r, result, err)
}
func (h *Handlers) favoriteQueue(w http.ResponseWriter, r *http.Request, user string) {
	if _, ok := query(w, r); !ok {
		return
	}
	result, err := h.service.FavoriteQueue(r.Context(), user)
	respond(w, r, result, err)
}
func (h *Handlers) favoriteTrack(w http.ResponseWriter, r *http.Request, user string) {
	h.setFavorite(w, r, user, "track", "track_id")
}
func (h *Handlers) favoriteAlbum(w http.ResponseWriter, r *http.Request, user string) {
	h.setFavorite(w, r, user, "album", "group_id")
}
func (h *Handlers) setFavorite(w http.ResponseWriter, r *http.Request, user, kind, parameter string) {
	id, ok := pathID(w, r, parameter)
	if !ok {
		return
	}
	if _, ok = query(w, r); !ok {
		return
	}
	if _, ok = decodeObject(w, r, true); !ok {
		return
	}
	result, err := h.service.SetFavorite(r.Context(), user, kind, id, r.Method == http.MethodPut)
	respond(w, r, result, err)
}
func (h *Handlers) lookup(w http.ResponseWriter, r *http.Request, user string) {
	if _, ok := query(w, r); !ok {
		return
	}
	input, ok := decodeObject(w, r, false, "track_ids", "group_ids", "artist_ids")
	if !ok {
		return
	}
	tracks, ok := input.ids("track_ids", false, 10000)
	if !ok {
		invalidInput(w, r)
		return
	}
	groups, ok := input.ids("group_ids", false, 1000)
	if !ok {
		invalidInput(w, r)
		return
	}
	artists, ok := input.ids("artist_ids", false, 1000)
	if !ok {
		invalidInput(w, r)
		return
	}
	result, err := h.service.LookupFavorites(r.Context(), user, tracks, groups, artists)
	respond(w, r, result, err)
}
