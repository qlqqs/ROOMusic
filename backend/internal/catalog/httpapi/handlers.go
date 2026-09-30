// Package httpapi 是 Release 目录的 REST 传输层：列表、详情、证据与封面读取。
// handler 只做参数解析、鉴权和响应映射；查询与投影在 catalog，封面读取在 catalog/artwork。
package httpapi

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/artwork"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// Handlers 汇集目录读取相关的 HTTP handler。
type Handlers struct {
	database *sql.DB
	guard    *identity.Guard
	store    *catalog.Store
	artwork  *artwork.Store
}

// New 返回目录 handler；dataDirectory 是受管封面所在的数据目录。
func New(database *sql.DB, guard *identity.Guard, dataDirectory string) *Handlers {
	return &Handlers{database: database, guard: guard, store: catalog.NewStore(database), artwork: artwork.NewStore(dataDirectory)}
}

// Register 在 mux 上注册目录读取路由。
func (handlers *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/releases", handlers.listReleases)
	mux.HandleFunc("GET /api/v1/releases/{id}/evidence", handlers.releaseEvidence)
	mux.HandleFunc("GET /api/v1/releases/{id}", handlers.releaseDetail)
	mux.HandleFunc("GET /api/v1/artworks/{id}", handlers.artworkResource)
}

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

func (handlers *Handlers) listReleases(responseWriter http.ResponseWriter, request *http.Request) {
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

	result, err := handlers.store.ListReleases(request.Context(), catalog.ListParams{
		Search: searchQuery, AttentionRequired: attentionRequired, Page: page, PageSize: pageSize,
	})
	if errors.Is(err, catalog.ErrQuery) {
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "服务暂不可用")
		return
	}
	if err != nil {
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取发行版本")
		return
	}
	httpserver.WriteJSON(responseWriter, http.StatusOK, map[string]any{
		"items":      result.Items,
		"pagination": map[string]any{"page": page, "page_size": pageSize, "total": result.Total},
	})
}

func (handlers *Handlers) releaseDetail(responseWriter http.ResponseWriter, request *http.Request) {
	if _, err := handlers.guard.Authenticate(request); err != nil {
		handlers.guard.WriteUnauthenticated(responseWriter, request)
		return
	}
	releaseID := request.PathValue("id")
	if !uuid.Valid(releaseID) {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_id", "发行版本标识无效")
		return
	}

	store := handlers.store
	summary, err := store.LoadReleaseSummary(request.Context(), releaseID)
	if err != nil {
		handlers.writeReleaseLoadError(responseWriter, request, err)
		return
	}
	header, err := store.LoadReleaseHeader(request.Context(), releaseID)
	if err != nil {
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "无法读取发行版本详情")
		return
	}
	media, err := store.LoadReleaseMedia(request.Context(), releaseID)
	if err != nil {
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取发行版本详情")
		return
	}
	credits, err := store.LoadReleaseCredits(request.Context(), releaseID)
	if err != nil {
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取发行版本署名")
		return
	}
	evidence, err := store.LoadReleaseEvidenceSummary(request.Context(), releaseID)
	if err != nil {
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取整理证据")
		return
	}
	httpserver.WriteJSON(responseWriter, http.StatusOK, catalog.ReleaseDetail{
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

func (handlers *Handlers) writeReleaseLoadError(responseWriter http.ResponseWriter, request *http.Request, err error) {
	if errors.Is(err, catalog.ErrReleaseNotFound) {
		httpserver.WriteError(responseWriter, request, http.StatusNotFound, "not_found", "发行版本不存在")
		return
	}
	httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "无法读取发行版本")
}

func (handlers *Handlers) releaseEvidence(responseWriter http.ResponseWriter, request *http.Request) {
	if _, ok := handlers.guard.RequireAdmin(responseWriter, request); !ok {
		return
	}
	releaseID := request.PathValue("id")
	if !uuid.Valid(releaseID) {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_id", "发行版本标识无效")
		return
	}
	store := handlers.store
	exists, err := store.ReleaseExists(request.Context(), releaseID)
	if err != nil {
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "无法读取整理证据")
		return
	}
	if !exists {
		httpserver.WriteError(responseWriter, request, http.StatusNotFound, "not_found", "发行版本不存在")
		return
	}

	result, err := store.LoadReleaseEvidence(request.Context(), releaseID)
	if err != nil {
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取整理证据")
		return
	}
	httpserver.WriteJSON(responseWriter, http.StatusOK, result)
}

func (handlers *Handlers) artworkResource(w http.ResponseWriter, r *http.Request) {
	if _, err := handlers.guard.Authenticate(r); err != nil {
		handlers.guard.WriteUnauthenticated(w, r)
		return
	}
	data, mime, err := handlers.artwork.Load(r.Context(), handlers.database, r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, r, http.StatusNotFound, "not_found", "封面不存在")
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
