package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

func parseGlobalSearchQuery(request *http.Request) (string, error) {
	query := request.URL.Query().Get("q")
	if !utf8.ValidString(query) || strings.IndexFunc(query, func(r rune) bool { return unicode.IsControl(r) && !unicode.IsSpace(r) }) >= 0 {
		return "", fmt.Errorf("invalid query encoding or control character")
	}
	query = strings.TrimSpace(query)
	if len(query) > 200 {
		return "", fmt.Errorf("query too long")
	}
	return query, nil
}

func parseSearchLimit(request *http.Request) (int, error) {
	values, present := request.URL.Query()["limit"]
	if !present {
		return 5, nil
	}
	if len(values) == 0 {
		return 0, fmt.Errorf("invalid limit")
	}
	limit, err := strconv.Atoi(values[0])
	if err != nil || limit < 1 || limit > 8 {
		return 0, fmt.Errorf("invalid limit")
	}
	return limit, nil
}

func (handlers *Handlers) search(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if _, err := handlers.guard.Authenticate(r); err != nil {
		handlers.guard.WriteUnauthenticated(w, r)
		return
	}
	kind := r.URL.Query().Get("type")
	switch kind {
	case "albums", "tracks":
	case "artists":
		httpserver.WriteError(w, r, http.StatusBadRequest, "unsupported_search_type", "暂不支持搜索艺人")
		return
	default:
		httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_search_type", "搜索类型无效")
		return
	}
	query, err := parseGlobalSearchQuery(r)
	if err != nil {
		httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_query", "搜索条件无效")
		return
	}
	page, pageSize, err := httpserver.ParsePagination(r, defaultReleasePageSize, maxReleaseRows)
	if err != nil {
		httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_pagination", "分页参数无效")
		return
	}
	result, err := handlers.store.Search(r.Context(), catalog.SearchParams{Query: query, Type: kind, Page: page, PageSize: pageSize})
	if err != nil {
		handlers.writeSearchError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, result)
}

func (handlers *Handlers) searchSuggestions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if _, err := handlers.guard.Authenticate(r); err != nil {
		handlers.guard.WriteUnauthenticated(w, r)
		return
	}
	query, err := parseGlobalSearchQuery(r)
	if err != nil {
		httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_query", "搜索条件无效")
		return
	}
	limit, err := parseSearchLimit(r)
	if err != nil {
		httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_limit", "建议数量无效")
		return
	}
	result, err := handlers.store.SearchSuggestions(r.Context(), query, limit)
	if err != nil {
		handlers.writeSearchError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, result)
}

func (handlers *Handlers) catalogStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if _, err := handlers.guard.Authenticate(r); err != nil {
		handlers.guard.WriteUnauthenticated(w, r)
		return
	}
	state, err := handlers.store.LoadCatalogState(r.Context())
	if err != nil {
		handlers.writeSearchError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, state)
}

func (handlers *Handlers) writeSearchError(w http.ResponseWriter, r *http.Request, err error) {
	handlers.logger.ErrorContext(r.Context(), "目录搜索读取失败", "error", err)
	httpserver.WriteError(w, r, http.StatusServiceUnavailable, "database_unavailable", "暂时无法读取搜索结果")
}
