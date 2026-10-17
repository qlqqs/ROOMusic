package httpapi

import (
	"errors"
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

// listReleaseClaims 返回待认领的旧版本的一页（管理员）：已不可见、但留有人工修改的版本，按最后一次被看到的时间倒序。
// 检查顺序是 401、403、400（分页 invalid_pagination）、200。
func (handlers *Handlers) listReleaseClaims(responseWriter http.ResponseWriter, request *http.Request) {
	if _, ok := handlers.guard.RequireAdmin(responseWriter, request); !ok {
		return
	}
	page, pageSize, err := httpserver.ParsePagination(request, defaultReleasePageSize, maxReleaseRows)
	if err != nil {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_pagination", "分页参数无效")
		return
	}
	result, err := handlers.store.ListClaims(request.Context(), catalog.ClaimListParams{Page: page, PageSize: pageSize})
	if errors.Is(err, catalog.ErrQuery) {
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "服务暂不可用")
		return
	}
	if err != nil {
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取待认领的版本")
		return
	}
	httpserver.WriteJSON(responseWriter, http.StatusOK, map[string]any{
		"items":      result.Items,
		"pagination": map[string]any{"page": page, "page_size": pageSize, "total": result.Total},
	})
}

// releaseClaim 返回一个待认领的旧版本的详情（管理员）：留着的修改明细、核对人的用户名与推荐的新版本。
// 检查顺序是 401、403、400（非 UUID）、404（不存在，或不满足待认领的判定）、200。
func (handlers *Handlers) releaseClaim(responseWriter http.ResponseWriter, request *http.Request) {
	if _, ok := handlers.guard.RequireAdmin(responseWriter, request); !ok {
		return
	}
	releaseID, ok := releaseIDFromPath(responseWriter, request)
	if !ok {
		return
	}
	detail, err := handlers.store.LoadClaim(request.Context(), releaseID)
	if err == nil {
		var names map[string]string
		if names, err = handlers.guard.Usernames(request.Context(), detail.ReviewerIDs()); err == nil {
			detail.SetReviewerNames(names)
		}
	}
	switch {
	case errors.Is(err, catalog.ErrReleaseNotFound):
		httpserver.WriteError(responseWriter, request, http.StatusNotFound, "not_found", "发行版本不存在")
	case errors.Is(err, catalog.ErrQuery):
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "服务暂不可用")
	case err != nil:
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取待认领的版本")
	default:
		httpserver.WriteJSON(responseWriter, http.StatusOK, detail)
	}
}

// releaseClaimHints 返回把这个可见版本列为推荐的待认领旧版本（管理员，最多 3 个），供专辑详情提示“可能接替了已不可见的《…》”。
// 检查顺序是 401、403、400（非 UUID）、404（不存在或不可见）、200；没有提示时 items 是空数组。
func (handlers *Handlers) releaseClaimHints(responseWriter http.ResponseWriter, request *http.Request) {
	if _, ok := handlers.guard.RequireAdmin(responseWriter, request); !ok {
		return
	}
	releaseID, ok := releaseIDFromPath(responseWriter, request)
	if !ok {
		return
	}
	hints, err := handlers.store.LoadClaimHints(request.Context(), releaseID)
	switch {
	case errors.Is(err, catalog.ErrReleaseNotFound):
		httpserver.WriteError(responseWriter, request, http.StatusNotFound, "not_found", "发行版本不存在")
	case errors.Is(err, catalog.ErrQuery):
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "服务暂不可用")
	case err != nil:
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取认领提示")
	default:
		httpserver.WriteJSON(responseWriter, http.StatusOK, map[string]any{"items": hints})
	}
}
