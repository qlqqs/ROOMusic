// Package httpapi 提供本人收藏与歌单的传输边界，业务规则和 SQL 由 personal 拥有。
package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

type Handlers struct {
	guard   *identity.Guard
	service *personal.Service
}

func New(guard *identity.Guard, service *personal.Service) *Handlers {
	return &Handlers{guard: guard, service: service}
}

// Register 只对匹配的写方法执行同源检查；方法错误复用平台 API fallback。
func (h *Handlers) Register(mux *http.ServeMux) {
	favorites := http.NewServeMux()
	queue := http.NewServeMux()
	h.route(queue, "GET /api/v1/me/favorites/tracks/queue", false, h.favoriteQueue)
	h.route(favorites, "GET /api/v1/me/favorites/tracks", false, h.favoriteTracks)
	h.route(favorites, "GET /api/v1/me/favorites/albums", false, h.favoriteAlbums)
	h.route(favorites, "POST /api/v1/me/favorites/lookup", true, h.lookup)
	for _, method := range []string{"PUT", "DELETE"} {
		h.route(favorites, method+" /api/v1/me/favorites/tracks/{track_id}", true, h.favoriteTrack)
		h.route(favorites, method+" /api/v1/me/favorites/albums/{group_id}", true, h.favoriteAlbum)
	}
	httpserver.RegisterAPIFallback(favorites, httpserver.APINotFoundHandler(favorites))
	httpserver.RegisterAPIFallback(queue, httpserver.APINotFoundHandler(queue))
	// 静态 queue 不得被 PUT/DELETE 的 track_id 通配符吞掉，否则 Allow 与 405 会错误。
	mux.Handle("/api/v1/me/favorites", favorites)
	mux.HandleFunc("/api/v1/me/favorites/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/me/favorites/tracks/queue" {
			queue.ServeHTTP(w, r)
			return
		}
		favorites.ServeHTTP(w, r)
	})
	// 静态反查路径与 {playlist_id}/items 在 ServeMux 中并非互为更具体，必须分开匹配，避免启动时路由冲突。
	playlists := http.NewServeMux()
	membership := http.NewServeMux()
	h.route(membership, "GET /api/v1/me/playlists/containing-track/{track_id}", false, h.containingTrack)
	h.route(playlists, "GET /api/v1/me/playlists", false, h.playlists)
	h.route(playlists, "GET /api/v1/me/playlists/{playlist_id}", false, h.playlist)
	h.route(playlists, "GET /api/v1/me/playlists/{playlist_id}/items", false, h.playlistItems)
	h.route(playlists, "GET /api/v1/me/playlists/{playlist_id}/queue", false, h.playlistQueue)
	for _, route := range []struct{ pattern, kind string }{
		{"POST /api/v1/me/playlists", "create"},
		{"PATCH /api/v1/me/playlists/{playlist_id}", "rename"},
		{"DELETE /api/v1/me/playlists/{playlist_id}", "delete"},
		{"POST /api/v1/me/playlists/{playlist_id}/items", "add_items"},
		{"DELETE /api/v1/me/playlists/{playlist_id}/items/{item_id}", "remove_item"},
		{"POST /api/v1/me/playlists/{playlist_id}/restore", "restore_item"},
		{"POST /api/v1/me/playlists/{playlist_id}/items/{item_id}/move", "move_item"},
	} {
		h.route(playlists, route.pattern, true, func(w http.ResponseWriter, r *http.Request, user string) { h.execute(w, r, user, route.kind) })
	}
	httpserver.RegisterAPIFallback(playlists, httpserver.APINotFoundHandler(playlists))
	httpserver.RegisterAPIFallback(membership, httpserver.APINotFoundHandler(membership))
	mux.Handle("/api/v1/me/playlists", playlists)
	mux.HandleFunc("/api/v1/me/playlists/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/me/playlists/containing-track/") {
			membership.ServeHTTP(w, r)
			return
		}
		playlists.ServeHTTP(w, r)
	})
}
func (h *Handlers) route(mux *http.ServeMux, pattern string, write bool, handler func(http.ResponseWriter, *http.Request, string)) {
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if write && !h.guard.RequireSameOrigin(w, r) {
			return
		}
		user, err := h.guard.Authenticate(r)
		if err != nil {
			h.guard.WriteUnauthenticated(w, r)
			return
		}
		handler(w, r, user.ID)
	})
}
func respond(w http.ResponseWriter, r *http.Request, result any, err error) {
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, result)
}
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := 503, "database_unavailable", "个人音乐暂时不可用"
	switch {
	case errors.Is(err, personal.ErrInvalidInput):
		status, code, message = 400, "invalid_input", "请求参数无效"
	case errors.Is(err, personal.ErrNotFound):
		status, code, message = 404, "not_found", "内容不存在"
	case errors.Is(err, personal.ErrRevisionConflict):
		status, code, message = 409, "revision_conflict", "歌单已在其他页面修改，请刷新后再试。"
	case errors.Is(err, personal.ErrFavoritesChanged):
		status, code, message = 409, "favorites_changed", "收藏已更新，请刷新后再试。"
	case errors.Is(err, personal.ErrIdempotencyConflict):
		status, code, message = 409, "idempotency_conflict", "幂等键已用于其他请求"
	case errors.Is(err, personal.ErrPlaylistLimit):
		status, code, message = 409, "playlist_limit_reached", "歌单数量已达上限"
	case errors.Is(err, personal.ErrTrackLimit):
		status, code, message = 409, "playlist_track_limit_reached", "歌单曲目数量已达上限，请先移除部分曲目。"
	}
	httpserver.WriteError(w, r, status, code, message)
}
