package httpapi

import (
	"errors"
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// listReleaseGroups 复用发行列表的搜索、筛选和分页，并单独校验组排序。
func (handlers *Handlers) listReleaseGroups(responseWriter http.ResponseWriter, request *http.Request) {
	if _, err := handlers.guard.Authenticate(request); err != nil {
		handlers.guard.WriteUnauthenticated(responseWriter, request)
		return
	}
	params, ok := parseReleaseListParams(responseWriter, request)
	if !ok {
		return
	}

	params.Sort, ok = parseReleaseGroupSort(responseWriter, request)
	if !ok {
		return
	}

	var result catalog.ReleaseGroupPage
	var err error
	if params.Sort == catalog.ReleaseGroupSortAddedAtDesc {
		result, err = catalog.ReadReleaseGroupsByAddedAt(request.Context(), handlers.database, params)
	} else {
		result, err = handlers.store.ListReleaseGroups(request.Context(), params)
	}
	if errors.Is(err, catalog.ErrQuery) {
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "服务暂不可用")
		return
	}
	if err != nil {
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取发行组")
		return
	}
	httpserver.WriteJSON(responseWriter, http.StatusOK, map[string]any{
		"items":      result.Items,
		"pagination": map[string]any{"page": params.Page, "page_size": params.PageSize, "total": result.Total},
	})
}

func (handlers *Handlers) releaseGroupDetail(responseWriter http.ResponseWriter, request *http.Request) {
	if _, err := handlers.guard.Authenticate(request); err != nil {
		handlers.guard.WriteUnauthenticated(responseWriter, request)
		return
	}
	groupID := request.PathValue("id")
	if !uuid.Valid(groupID) {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_id", "发行组标识无效")
		return
	}
	detail, err := handlers.store.LoadReleaseGroup(request.Context(), groupID)
	if errors.Is(err, catalog.ErrReleaseGroupNotFound) {
		httpserver.WriteError(responseWriter, request, http.StatusNotFound, "not_found", "发行组不存在")
		return
	}
	if err != nil {
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "无法读取发行组")
		return
	}
	httpserver.WriteJSON(responseWriter, http.StatusOK, detail)
}

// releaseGroupComparison 返回一张专辑各个可见版本的对比数据（所有登录成员）：成员顺序与组详情一致，最多 100 个，
// release_count 是完整数量。组不存在或没有可见成员是 404；读取失败按组详情的约定返回 503。
func (handlers *Handlers) releaseGroupComparison(responseWriter http.ResponseWriter, request *http.Request) {
	if _, err := handlers.guard.Authenticate(request); err != nil {
		handlers.guard.WriteUnauthenticated(responseWriter, request)
		return
	}
	groupID := request.PathValue("id")
	if !uuid.Valid(groupID) {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_id", "发行组标识无效")
		return
	}
	comparison, err := handlers.store.LoadReleaseComparison(request.Context(), groupID)
	if errors.Is(err, catalog.ErrReleaseGroupNotFound) {
		httpserver.WriteError(responseWriter, request, http.StatusNotFound, "not_found", "发行组不存在")
		return
	}
	if err != nil {
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "无法读取版本对比")
		return
	}
	httpserver.WriteJSON(responseWriter, http.StatusOK, comparison)
}
