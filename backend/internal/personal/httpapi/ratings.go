package httpapi

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

func (h *Handlers) registerRatings(mux *http.ServeMux) {
	const trackPath = "/api/v1/me/ratings/tracks"
	tracks := http.NewServeMux()
	queue := http.NewServeMux()
	h.route(tracks, "GET "+trackPath, false, h.ratedTracks)
	h.route(queue, "GET "+trackPath+"/queue", false, h.ratedTrackQueue)
	h.route(mux, "GET /api/v1/me/ratings/albums", false, h.ratedAlbums)
	h.route(mux, "GET /api/v1/me/ratings/artists", false, h.ratedArtists)
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		h.route(tracks, method+" "+trackPath+"/{track_id}", true, func(w http.ResponseWriter, r *http.Request, user string) {
			h.setRating(w, r, user, "track", "track_id")
		})
	}
	httpserver.RegisterAPIFallback(tracks, httpserver.APINotFoundHandler(tracks))
	httpserver.RegisterAPIFallback(queue, httpserver.APINotFoundHandler(queue))
	// 队列独立匹配，确保 PUT、DELETE 等方法在此路径收到准确的 405。
	mux.Handle(trackPath, tracks)
	mux.Handle(trackPath+"/", tracks)
	mux.Handle(trackPath+"/queue", queue)

	for _, route := range []struct{ path, kind, parameter string }{
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

func (h *Handlers) ratedTracks(w http.ResponseWriter, r *http.Request, user string) {
	p, ok := ratingQuery(w, r, true)
	if !ok {
		return
	}
	result, err := h.service.RatedTracks(r.Context(), user, p)
	respond(w, r, result, err)
}

func (h *Handlers) ratedAlbums(w http.ResponseWriter, r *http.Request, user string) {
	p, ok := ratingQuery(w, r, true)
	if !ok {
		return
	}
	result, err := h.service.RatedAlbums(r.Context(), user, p)
	respond(w, r, result, err)
}

func (h *Handlers) ratedArtists(w http.ResponseWriter, r *http.Request, user string) {
	p, ok := ratingQuery(w, r, true)
	if !ok {
		return
	}
	result, err := h.service.RatedArtists(r.Context(), user, p)
	respond(w, r, result, err)
}

func (h *Handlers) ratedTrackQueue(w http.ResponseWriter, r *http.Request, user string) {
	p, ok := ratingQuery(w, r, false)
	if !ok {
		return
	}
	result, err := h.service.RatedTrackQueue(r.Context(), user, p.MinRating, p.Sort)
	respond(w, r, result, err)
}

func ratingQuery(w http.ResponseWriter, r *http.Request, paginated bool) (personal.RatedListParams, bool) {
	params := personal.RatedListParams{MinRating: 1, Sort: "rating"}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_query", "查询参数无效")
		return params, false
	}
	for key, entries := range values {
		allowed := key == "min_rating" || key == "sort" || paginated && (key == "page" || key == "page_size")
		if !allowed || len(entries) != 1 {
			httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_query", "查询参数无效")
			return params, false
		}
	}
	if raw, exists := values["min_rating"]; exists {
		rating, err := strconv.Atoi(raw[0])
		if err != nil || rating < 1 || rating > 5 {
			httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_rating", "最低评分须为 1 至 5 的整数")
			return params, false
		}
		params.MinRating = rating
	}
	if raw, exists := values["sort"]; exists {
		if raw[0] != "rating" && raw[0] != "recent" {
			httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_sort", "评分排序方式无效")
			return params, false
		}
		params.Sort = raw[0]
	}
	if paginated {
		page, size, err := httpserver.ParsePagination(r, 50, 100)
		if err != nil || values.Has("page") && values.Get("page") == "" || values.Has("page_size") && values.Get("page_size") == "" {
			httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_pagination", "分页参数无效")
			return params, false
		}
		params.Page = personal.Page{Number: page, Size: size}
	}
	return params, true
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
