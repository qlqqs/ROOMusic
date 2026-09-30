package httpapi

import (
	"errors"
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// listReleaseGroups 与 /releases 使用同一组参数、默认值、上限与错误码；组的可见性、搜索与 attention 语义见 catalog.ListReleaseGroups。
func (handlers *Handlers) listReleaseGroups(responseWriter http.ResponseWriter, request *http.Request) {
	if _, err := handlers.guard.Authenticate(request); err != nil {
		handlers.guard.WriteUnauthenticated(responseWriter, request)
		return
	}
	page, pageSize, err := parsePagination(request)
	if err != nil {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_pagination", "分页参数无效")
		return
	}
	searchQuery, err := parseReleaseSearch(request)
	if err != nil {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_query", "搜索条件无效")
		return
	}
	attentionRequired, err := parseReleaseAttention(request)
	if err != nil {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_attention", "检查筛选条件无效")
		return
	}

	result, err := handlers.store.ListReleaseGroups(request.Context(), catalog.ListParams{
		Search: searchQuery, AttentionRequired: attentionRequired, Page: page, PageSize: pageSize,
	})
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
		"pagination": map[string]any{"page": page, "page_size": pageSize, "total": result.Total},
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
