package httpapi

import (
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

func parseReleaseGroupSort(w http.ResponseWriter, r *http.Request) (catalog.ReleaseGroupSort, bool) {
	values, present := r.URL.Query()["sort"]
	if !present {
		return catalog.ReleaseGroupSortTitle, true
	}
	if len(values) == 1 && (values[0] == string(catalog.ReleaseGroupSortTitle) || values[0] == string(catalog.ReleaseGroupSortAddedAtDesc)) {
		return catalog.ReleaseGroupSort(values[0]), true
	}
	httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_sort", "排序参数无效")
	return "", false
}

func (handlers *Handlers) recentlyAddedReleaseGroups(w http.ResponseWriter, r *http.Request) {
	if _, err := handlers.guard.Authenticate(r); err != nil {
		handlers.guard.WriteUnauthenticated(w, r)
		return
	}
	// 此入口固定排序和上限，任何查询参数都不是合同的一部分；非法编码也不能被静默忽略。
	if r.URL.RawQuery != "" {
		httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_query", "最近新增不接受查询参数")
		return
	}
	items, err := catalog.ReadRecentlyAddedReleaseGroups(r.Context(), handlers.database)
	if err != nil {
		httpserver.WriteError(w, r, http.StatusServiceUnavailable, "database_unavailable", "无法读取最近新增")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}
