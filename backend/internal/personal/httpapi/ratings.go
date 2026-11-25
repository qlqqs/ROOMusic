package httpapi

import "net/http"

func (h *Handlers) registerRatings(mux *http.ServeMux) {
	for _, route := range []struct{ path, kind, parameter string }{
		{"/api/v1/me/ratings/tracks/{track_id}", "track", "track_id"},
		{"/api/v1/me/ratings/albums/{group_id}", "album", "group_id"},
		{"/api/v1/me/ratings/artists/{artist_id}", "artist", "artist_id"},
	} {
		for _, method := range []string{"PUT", "DELETE"} {
			h.route(mux, method+" "+route.path, true, func(w http.ResponseWriter, r *http.Request, user string) {
				h.setRating(w, r, user, route.kind, route.parameter)
			})
		}
	}
	h.route(mux, "POST /api/v1/me/ratings/lookup", true, h.lookupRatings)
}

func (h *Handlers) setRating(w http.ResponseWriter, r *http.Request, user, kind, parameter string) {
	id, ok := pathID(w, r, parameter)
	if !ok {
		return
	}
	if _, ok := query(w, r); !ok {
		return
	}
	var rating int
	if r.Method == http.MethodPut {
		input, ok := decodeObject(w, r, false, "rating")
		if !ok {
			return
		}
		if !input.field("rating", &rating) || rating < 1 || rating > 5 {
			invalidInput(w, r)
			return
		}
	} else if _, ok := decodeObject(w, r, true); !ok {
		return
	}
	result, err := h.service.SetRating(r.Context(), user, kind, id, rating)
	respond(w, r, result, err)
}

func (h *Handlers) lookupRatings(w http.ResponseWriter, r *http.Request, user string) {
	tracks, groups, artists, ok := lookupIDs(w, r)
	if !ok {
		return
	}
	result, err := h.service.LookupRatings(r.Context(), user, tracks, groups, artists)
	respond(w, r, result, err)
}
