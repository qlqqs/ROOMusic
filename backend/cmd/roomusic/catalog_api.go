package main

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func parseReleaseSearch(request *http.Request) (string, error) {
	query := strings.TrimSpace(request.URL.Query().Get("q"))
	if len(query) > 200 {
		return "", fmt.Errorf("query too long")
	}
	return query, nil
}

func parseReleaseAttention(request *http.Request) (bool, error) {
	values, present := request.URL.Query()["attention"]
	if !present {
		return false, nil
	}
	if len(values) != 1 || values[0] != "required" {
		return false, fmt.Errorf("unsupported attention filter")
	}
	return true, nil
}

func parsePagination(request *http.Request) (int, int, error) {
	page, pageSize := 1, 50
	var err error
	if pageValue := request.URL.Query().Get("page"); pageValue != "" {
		page, err = strconv.Atoi(pageValue)
		if err != nil || page < 1 {
			return 0, 0, fmt.Errorf("invalid page")
		}
	}
	if pageSizeValue := request.URL.Query().Get("page_size"); pageSizeValue != "" {
		pageSize, err = strconv.Atoi(pageSizeValue)
		if err != nil || pageSize < 1 || pageSize > maxReleaseRows {
			return 0, 0, fmt.Errorf("invalid page size")
		}
	}
	if page-1 > int(^uint(0)>>1)/pageSize {
		return 0, 0, fmt.Errorf("page offset exceeds bound")
	}
	return page, pageSize, nil
}

// 页大小上限只约束请求参数；Store 不重复限制。
const maxReleaseRows = 100

func (application *roomusicApplication) listReleases(responseWriter http.ResponseWriter, request *http.Request) {
	if _, err := application.authenticatedUser(request); err != nil {
		application.writeAuthenticationError(responseWriter, request)
		return
	}
	page, pageSize, err := parsePagination(request)
	if err != nil {
		writeAPIError(responseWriter, request, http.StatusBadRequest, "invalid_pagination", "分页参数无效")
		return
	}
	searchQuery, err := parseReleaseSearch(request)
	if err != nil {
		writeAPIError(responseWriter, request, http.StatusBadRequest, "invalid_query", "搜索条件无效")
		return
	}
	attentionRequired, err := parseReleaseAttention(request)
	if err != nil {
		writeAPIError(responseWriter, request, http.StatusBadRequest, "invalid_attention", "检查筛选条件无效")
		return
	}

	result, err := catalog.NewStore(application.database.connection).ListReleases(request.Context(), catalog.ListParams{
		Search: searchQuery, AttentionRequired: attentionRequired, Page: page, PageSize: pageSize,
	})
	if errors.Is(err, catalog.ErrQuery) {
		writeAPIError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "服务暂不可用")
		return
	}
	if err != nil {
		writeAPIError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取发行版本")
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"items":      result.Items,
		"pagination": map[string]any{"page": page, "page_size": pageSize, "total": result.Total},
	})
}

func (application *roomusicApplication) releaseDetail(responseWriter http.ResponseWriter, request *http.Request) {
	if _, err := application.authenticatedUser(request); err != nil {
		application.writeAuthenticationError(responseWriter, request)
		return
	}
	releaseID := request.PathValue("id")
	if !uuid.Valid(releaseID) {
		writeAPIError(responseWriter, request, http.StatusBadRequest, "invalid_id", "发行版本标识无效")
		return
	}

	store := catalog.NewStore(application.database.connection)
	summary, err := store.LoadReleaseSummary(request.Context(), releaseID)
	if err != nil {
		application.writeReleaseLoadError(responseWriter, request, err)
		return
	}
	header, err := store.LoadReleaseHeader(request.Context(), releaseID)
	if err != nil {
		writeAPIError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "无法读取发行版本详情")
		return
	}
	media, err := store.LoadReleaseMedia(request.Context(), releaseID)
	if err != nil {
		writeAPIError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取发行版本详情")
		return
	}
	credits, err := store.LoadReleaseCredits(request.Context(), releaseID)
	if err != nil {
		writeAPIError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取发行版本署名")
		return
	}
	evidence, err := store.LoadReleaseEvidenceSummary(request.Context(), releaseID)
	if err != nil {
		writeAPIError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取整理证据")
		return
	}
	writeJSON(responseWriter, http.StatusOK, catalog.ReleaseDetail{
		ReleaseSummary: summary,
		CandidateKind:  header.CandidateKind,
		Genre:          header.Genre,
		CatalogNumber:  header.CatalogNumber,
		Edition:        header.Edition,
		Label:          header.Label,
		Barcode:        header.Barcode,
		Media:          media,
		Credits:        credits,
		Evidence:       evidence,
	})
}

func (application *roomusicApplication) writeReleaseLoadError(responseWriter http.ResponseWriter, request *http.Request, err error) {
	if errors.Is(err, catalog.ErrReleaseNotFound) {
		writeAPIError(responseWriter, request, http.StatusNotFound, "not_found", "发行版本不存在")
		return
	}
	writeAPIError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "无法读取发行版本")
}

func (application *roomusicApplication) releaseEvidence(responseWriter http.ResponseWriter, request *http.Request) {
	if _, ok := application.requireAdmin(responseWriter, request); !ok {
		return
	}
	releaseID := request.PathValue("id")
	if !uuid.Valid(releaseID) {
		writeAPIError(responseWriter, request, http.StatusBadRequest, "invalid_id", "发行版本标识无效")
		return
	}
	store := catalog.NewStore(application.database.connection)
	exists, err := store.ReleaseExists(request.Context(), releaseID)
	if err != nil {
		writeAPIError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "无法读取整理证据")
		return
	}
	if !exists {
		writeAPIError(responseWriter, request, http.StatusNotFound, "not_found", "发行版本不存在")
		return
	}

	result, err := store.LoadReleaseEvidence(request.Context(), releaseID)
	if err != nil {
		writeAPIError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取整理证据")
		return
	}
	writeJSON(responseWriter, http.StatusOK, result)
}
