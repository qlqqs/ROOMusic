// Package httpapi 是 Release 目录的 REST 传输层：列表、详情、证据、按组读取、问题、封面读取与曲目歌词读取。
// handler 只做参数解析、鉴权和响应映射；查询与投影在 catalog，封面读取在 catalog/artwork。
package httpapi

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/artwork"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// Handlers 汇集目录相关的 HTTP handler：读取与发行信息的受治理写入。
type Handlers struct {
	database   *sql.DB
	guard      *identity.Guard
	store      *catalog.Store
	governance *governance.Store
	artwork    *artwork.Store
	logger     *slog.Logger
}

// New 返回目录 handler；dataDirectory 是受管封面所在的数据目录，logger 记录写入失败（500 与 503）的细节，可以为 nil。
func New(database *sql.DB, guard *identity.Guard, dataDirectory string, logger *slog.Logger) *Handlers {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Handlers{database: database, guard: guard, store: catalog.NewStore(database), governance: governance.NewStore(database), artwork: artwork.NewStore(dataDirectory), logger: logger}
}

// Register 在 mux 上注册目录路由。
func (handlers *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/releases", handlers.listReleases)
	mux.HandleFunc("GET /api/v1/releases/{id}/evidence", handlers.releaseEvidence)
	mux.HandleFunc("GET /api/v1/releases/{id}/corrections", handlers.releaseCorrections)
	mux.HandleFunc("PATCH /api/v1/releases/{id}/corrections", handlers.correctRelease)
	mux.HandleFunc("POST /api/v1/releases/{id}/group", handlers.changeReleaseGroup)
	mux.HandleFunc("GET /api/v1/releases/{id}/issues", handlers.releaseIssues)
	mux.HandleFunc("POST /api/v1/releases/{id}/issue-review", handlers.reviewReleaseIssue)
	mux.HandleFunc("GET /api/v1/releases/{id}/claim-preview", handlers.claimPreview)
	mux.HandleFunc("POST /api/v1/releases/{id}/claim", handlers.claimRelease)
	mux.HandleFunc("GET /api/v1/releases/{id}/claim-hints", handlers.releaseClaimHints)
	mux.HandleFunc("GET /api/v1/release-claims", handlers.listReleaseClaims)
	mux.HandleFunc("GET /api/v1/release-claims/{id}", handlers.releaseClaim)
	mux.HandleFunc("GET /api/v1/releases/{id}", handlers.releaseDetail)
	mux.HandleFunc("GET /api/v1/release-issues", handlers.listReleaseIssues)
	mux.HandleFunc("GET /api/v1/release-groups", handlers.listReleaseGroups)
	mux.HandleFunc("GET /api/v1/release-groups/recently-added", handlers.recentlyAddedReleaseGroups)
	mux.HandleFunc("GET /api/v1/release-groups/{id}", handlers.releaseGroupDetail)
	mux.HandleFunc("GET /api/v1/release-groups/{id}/comparison", handlers.releaseGroupComparison)
	mux.HandleFunc("POST /api/v1/release-groups/{id}/default", handlers.setReleaseGroupDefault)
	mux.HandleFunc("GET /api/v1/artworks/{id}", handlers.artworkResource)
	mux.HandleFunc("GET /api/v1/tracks/{id}/lyrics", handlers.trackLyrics)
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

// 目录列表的页大小：默认 50、上限 100。上限只约束请求参数，Store 不重复限制。
const (
	defaultReleasePageSize = 50
	maxReleaseRows         = 100
)

// parseReleaseListParams 解析 /releases 与 /release-groups 共用的搜索、筛选和分页参数。组排序单独解析。
// 参数非法时写出 400 错误并返回 false。
func parseReleaseListParams(responseWriter http.ResponseWriter, request *http.Request) (catalog.ListParams, bool) {
	page, pageSize, err := httpserver.ParsePagination(request, defaultReleasePageSize, maxReleaseRows)
	if err != nil {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_pagination", "分页参数无效")
		return catalog.ListParams{}, false
	}
	searchQuery, err := parseReleaseSearch(request)
	if err != nil {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_query", "搜索条件无效")
		return catalog.ListParams{}, false
	}
	attentionRequired, err := parseReleaseAttention(request)
	if err != nil {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_attention", "检查筛选条件无效")
		return catalog.ListParams{}, false
	}
	return catalog.ListParams{Search: searchQuery, AttentionRequired: attentionRequired, Page: page, PageSize: pageSize}, true
}

func (handlers *Handlers) listReleases(responseWriter http.ResponseWriter, request *http.Request) {
	if _, err := handlers.guard.Authenticate(request); err != nil {
		handlers.guard.WriteUnauthenticated(responseWriter, request)
		return
	}
	params, ok := parseReleaseListParams(responseWriter, request)
	if !ok {
		return
	}

	result, err := handlers.store.ListReleases(request.Context(), params)
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
		"pagination": map[string]any{"page": params.Page, "page_size": params.PageSize, "total": result.Total},
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
		ReleaseSummary:  summary,
		CandidateKind:   header.CandidateKind,
		Genre:           header.Genre,
		CatalogNumber:   header.CatalogNumber,
		Edition:         header.Edition,
		Label:           header.Label,
		Barcode:         header.Barcode,
		Media:           media,
		Credits:         credits,
		Evidence:        evidence,
		Revision:        header.Revision,
		CorrectedFields: header.CorrectedFields,
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

// trackLyrics 返回曲目的内嵌歌词：所有登录成员可读；检查顺序是 401、400（非 UUID）、404（不存在或非 present）、200。
// 没有可显示的歌词时 lyrics 为 null，不是 404。
func (handlers *Handlers) trackLyrics(responseWriter http.ResponseWriter, request *http.Request) {
	if _, err := handlers.guard.Authenticate(request); err != nil {
		handlers.guard.WriteUnauthenticated(responseWriter, request)
		return
	}
	trackID := request.PathValue("id")
	if !uuid.Valid(trackID) {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_id", "曲目标识无效")
		return
	}
	lyrics, err := handlers.store.LoadTrackLyrics(request.Context(), trackID)
	if errors.Is(err, catalog.ErrTrackNotFound) {
		httpserver.WriteError(responseWriter, request, http.StatusNotFound, "not_found", "曲目不存在")
		return
	}
	if err != nil {
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "无法读取歌词")
		return
	}
	httpserver.WriteJSON(responseWriter, http.StatusOK, lyrics)
}

func (handlers *Handlers) artworkResource(w http.ResponseWriter, r *http.Request) {
	if _, err := handlers.guard.Authenticate(r); err != nil {
		handlers.guard.WriteUnauthenticated(w, r)
		return
	}
	// 先鉴权、再确认资源存在，最后才比较校验值：未登录与不存在的资源不会因为带着匹配的 ETag 得到 304。
	resource, err := handlers.artwork.Lookup(r.Context(), handlers.database, r.PathValue("id"))
	if err != nil {
		httpserver.WriteError(w, r, http.StatusNotFound, "not_found", "封面不存在")
		return
	}
	// 资源标识是内容哈希加扩展名，同一个标识的字节永远不变，直接作为强校验值。private, no-cache 让浏览器每次显示前
	// 都带着凭据重新验证：内容没变只花一次 304（不读文件），退出登录后验证得到 401，旧封面不再显示。
	if httpserver.IfNoneMatchHits(r, resource.ID) {
		setArtworkCacheHeaders(w, resource)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	data, err := handlers.artwork.Read(resource)
	if err != nil {
		httpserver.WriteError(w, r, http.StatusNotFound, "not_found", "封面不存在")
		return
	}
	setArtworkCacheHeaders(w, resource)
	w.Header().Set("Content-Type", resource.MIMEType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// setArtworkCacheHeaders 写出封面 200 与 304 共用的缓存头（304 必须带上与 200 相同的 ETag 与 Cache-Control）。
func setArtworkCacheHeaders(w http.ResponseWriter, resource artwork.Resource) {
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", httpserver.EntityTag(resource.ID))
}
