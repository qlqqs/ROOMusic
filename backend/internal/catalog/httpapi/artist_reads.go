package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func (h *Handlers) authenticateArtistRead(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	if _, err := h.guard.Authenticate(r); err != nil {
		h.guard.WriteUnauthenticated(w, r)
		return false
	}
	return true
}

func artistReadID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !uuid.Valid(id) {
		httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_id", "艺人标识无效")
		return "", false
	}
	return strings.ToLower(id), true
}

func artistReadPagination(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	page, size, err := httpserver.ParsePagination(r, defaultReleasePageSize, maxReleaseRows)
	if err != nil {
		httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_pagination", "分页参数无效")
		return 0, 0, false
	}
	return page, size, true
}

func artistReadEnum(r *http.Request, key, fallback string, allowed ...string) (string, error) {
	values, present := r.URL.Query()[key]
	if !present {
		return fallback, nil
	}
	if len(values) != 1 {
		return "", fmt.Errorf("repeated %s", key)
	}
	for _, value := range allowed {
		if values[0] == value {
			return value, nil
		}
	}
	return "", fmt.Errorf("invalid %s", key)
}

func parseArtistListParams(w http.ResponseWriter, r *http.Request) (catalog.ArtistListParams, bool) {
	var p catalog.ArtistListParams
	var err error
	page, size, ok := artistReadPagination(w, r)
	if !ok {
		return p, false
	}
	p.Page, p.PageSize = page, size
	p.Query, err = parseGlobalSearchQuery(r)
	if err != nil {
		httpserver.WriteError(w, r, 400, "invalid_query", "搜索条件无效")
		return p, false
	}
	p.Role, err = artistReadEnum(r, "role", "", "release_artist", "album_artist", "track_artist", "lyricist", "composer", "arranger", "conductor", "performer", "producer")
	if err != nil {
		httpserver.WriteError(w, r, 400, "invalid_artist_role", "署名角色无效")
		return p, false
	}
	initials := []string{"kana", "#"}
	for c := 'A'; c <= 'Z'; c++ {
		initials = append(initials, string(c))
	}
	p.Initial, err = artistReadEnum(r, "initial", "", initials...)
	if err != nil {
		httpserver.WriteError(w, r, 400, "invalid_artist_initial", "首字母分组无效")
		return p, false
	}
	p.Sort, err = artistReadEnum(r, "sort", "name", "name", "album_count")
	if err != nil {
		httpserver.WriteError(w, r, 400, "invalid_artist_sort", "艺人排序无效")
		return p, false
	}
	return p, true
}

func (h *Handlers) listArtists(w http.ResponseWriter, r *http.Request) {
	if !h.authenticateArtistRead(w, r) {
		return
	}
	p, ok := parseArtistListParams(w, r)
	if !ok {
		return
	}
	result, err := h.store.ListArtists(r.Context(), p)
	if err != nil {
		h.writeArtistReadError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, 200, result)
}
func (h *Handlers) artistDetail(w http.ResponseWriter, r *http.Request) {
	if !h.authenticateArtistRead(w, r) {
		return
	}
	id, ok := artistReadID(w, r)
	if !ok {
		return
	}
	result, err := h.store.LoadArtist(r.Context(), id)
	if err != nil {
		h.writeArtistReadError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, 200, result)
}
func (h *Handlers) artistNames(w http.ResponseWriter, r *http.Request) {
	if !h.authenticateArtistRead(w, r) {
		return
	}
	id, ok := artistReadID(w, r)
	if !ok {
		return
	}
	page, size, ok := artistReadPagination(w, r)
	if !ok {
		return
	}
	result, err := h.store.ListArtistNames(r.Context(), id, page, size)
	if err != nil {
		h.writeArtistReadError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, 200, result)
}
func parseArtistAlbumOptions(w http.ResponseWriter, r *http.Request) (catalog.ArtistAlbumOptions, bool) {
	var option catalog.ArtistAlbumOptions
	var err error
	option.Participation, err = artistReadEnum(r, "participation", "", "any")
	if err != nil {
		httpserver.WriteError(w, r, 400, "invalid_artist_participation", "专辑参与范围无效")
		return option, false
	}
	if len(r.URL.Query()["q"]) > 1 {
		httpserver.WriteError(w, r, 400, "invalid_query", "搜索条件无效")
		return option, false
	}
	option.Query, err = parseGlobalSearchQuery(r)
	if err != nil {
		httpserver.WriteError(w, r, 400, "invalid_query", "搜索条件无效")
		return option, false
	}
	return option, true
}

func (h *Handlers) artistAlbums(w http.ResponseWriter, r *http.Request) {
	if !h.authenticateArtistRead(w, r) {
		return
	}
	id, ok := artistReadID(w, r)
	if !ok {
		return
	}
	page, size, ok := artistReadPagination(w, r)
	if !ok {
		return
	}
	option, ok := parseArtistAlbumOptions(w, r)
	if !ok {
		return
	}
	result, err := h.store.ListArtistAlbums(r.Context(), id, page, size, option)
	if err != nil {
		h.writeArtistReadError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, 200, result)
}
func (h *Handlers) artistTracks(w http.ResponseWriter, r *http.Request) {
	if !h.authenticateArtistRead(w, r) {
		return
	}
	id, ok := artistReadID(w, r)
	if !ok {
		return
	}
	page, size, ok := artistReadPagination(w, r)
	if !ok {
		return
	}
	role, err := artistReadEnum(r, "role", "", "track_artist", "lyricist", "composer", "arranger", "conductor", "performer", "producer")
	if err != nil || role == "" {
		httpserver.WriteError(w, r, 400, "invalid_artist_role", "署名角色无效")
		return
	}
	result, err := h.store.ListArtistTracks(r.Context(), id, role, page, size)
	if err != nil {
		h.writeArtistReadError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, 200, result)
}
func parseArtistQueue(r *http.Request) (string, *string, error) {
	order, err := artistReadEnum(r, "order", "album", "album", "random")
	if err != nil {
		return "", nil, err
	}
	values, present := r.URL.Query()["seed"]
	if order == "random" || present {
		if len(values) != 1 || !uuid.Valid(values[0]) {
			return order, nil, fmt.Errorf("invalid seed")
		}
		seed := strings.ToLower(values[0])
		if order == "random" {
			return order, &seed, nil
		}
	}
	return order, nil, nil
}
func (h *Handlers) artistQueue(w http.ResponseWriter, r *http.Request) {
	if !h.authenticateArtistRead(w, r) {
		return
	}
	id, ok := artistReadID(w, r)
	if !ok {
		return
	}
	order, seed, err := parseArtistQueue(r)
	if err != nil {
		code, message := "invalid_seed", "随机种子无效"
		if order == "" {
			code, message = "invalid_queue_order", "队列排序无效"
		}
		httpserver.WriteError(w, r, 400, code, message)
		return
	}
	result, err := h.store.LoadArtistQueue(r.Context(), id, order, seed)
	if err != nil {
		h.writeArtistReadError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, 200, result)
}
func (h *Handlers) writeArtistReadError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, catalog.ErrArtistCatalogPreparing) {
		w.Header().Set("Retry-After", "3")
		httpserver.WriteError(w, r, 503, "artist_catalog_preparing", "艺人信息正在整理，请稍后再试。")
		return
	}
	if errors.Is(err, catalog.ErrArtistNotFound) {
		httpserver.WriteError(w, r, 404, "not_found", "艺人不存在")
		return
	}
	h.logger.ErrorContext(r.Context(), "艺人读取失败", "error", err)
	httpserver.WriteError(w, r, 503, "database_unavailable", "暂时无法读取艺人信息")
}
