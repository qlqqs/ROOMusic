package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// claimRelease 把旧版本上的人工修改一次迁给新版本（管理员、同源、Idempotency-Key）；{id} 是新版本。请求体含两个版本的预期版本号、
// 旧版本的 ID 与页面预览的令牌。检查顺序是同源 403、401/403、400（两个 ID、两者相同、幂等键、请求体）、之后由 operations.Run 在同一个事务里
// 完成幂等、加锁、版本核对、比较预览、迁移、写操作记录。成功返回新版本认领之后所在的组、原来的组、两个新版本号、各类的去向与 operation_id；
// 同键同义重放返回同一份响应。
func (handlers *Handlers) claimRelease(responseWriter http.ResponseWriter, request *http.Request) {
	if !handlers.guard.RequireSameOrigin(responseWriter, request) {
		return
	}
	actor, ok := handlers.guard.RequireAdmin(responseWriter, request)
	if !ok {
		return
	}
	releaseID, ok := releaseIDFromPath(responseWriter, request)
	if !ok {
		return
	}
	key, ok := operations.NormalizeKey(request.Header.Get("Idempotency-Key"))
	if !ok {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_input", "请求需要 Idempotency-Key（UUID）")
		return
	}
	var body struct {
		ExpectedRevision int64  `json:"expected_revision"`
		SourceReleaseID  string `json:"source_release_id"`
		SourceRevision   int64  `json:"source_revision"`
		Preview          string `json:"preview"`
	}
	if !httpserver.DecodeJSON(responseWriter, request, &body) {
		return
	}
	message := ""
	switch {
	case body.ExpectedRevision < 1 || body.SourceRevision < 1:
		message = "版本号无效"
	case !uuid.Valid(body.SourceReleaseID):
		message = "旧版本标识无效"
	case strings.EqualFold(body.SourceReleaseID, releaseID):
		message = "不能把一个版本认领给它自己"
	case body.Preview == "":
		message = "认领需要预览的令牌"
	}
	if message != "" {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_input", message)
		return
	}
	change := governance.ClaimRequest{
		ReleaseID: releaseID, ExpectedRevision: body.ExpectedRevision,
		SourceID: strings.ToLower(body.SourceReleaseID), SourceRevision: body.SourceRevision, Preview: body.Preview,
	}
	result, err := operations.Run(request.Context(), handlers.database, operations.Spec{
		ActorID: actor.ID, Type: operations.TypeReleaseClaim, Key: key,
		Fingerprint: change.Fingerprint(), RequestID: request.Header.Get("X-Request-ID"),
	}, governance.ClaimExecutor(change))
	if err != nil {
		handlers.writeOperationError(responseWriter, request, err)
		return
	}
	httpserver.WriteJSON(responseWriter, http.StatusOK, result.Response)
}

// claimPreview 返回把旧版本认领给新版本的预览（管理员）：逐项去向与预览令牌；{id} 是新版本，查询参数 source 是旧版本。
// 检查顺序是 401、403、400（两个 ID、两者相同）、404（新版本不可见或不存在、旧版本不存在）、409 not_claimable、200。
func (handlers *Handlers) claimPreview(responseWriter http.ResponseWriter, request *http.Request) {
	if _, ok := handlers.guard.RequireAdmin(responseWriter, request); !ok {
		return
	}
	releaseID, ok := releaseIDFromPath(responseWriter, request)
	if !ok {
		return
	}
	sources := request.URL.Query()["source"]
	if len(sources) != 1 || !uuid.Valid(sources[0]) {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_id", "旧版本标识无效")
		return
	}
	sourceID := strings.ToLower(sources[0])
	if sourceID == releaseID {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_input", "不能把一个版本认领给它自己")
		return
	}
	preview, err := handlers.governance.PreviewClaim(request.Context(), sourceID, releaseID)
	var failure *operations.Failure
	switch {
	case errors.Is(err, catalog.ErrReleaseNotFound):
		httpserver.WriteError(responseWriter, request, http.StatusNotFound, "not_found", "发行版本不存在")
	case errors.As(err, &failure):
		handlers.writeOperationError(responseWriter, request, failure)
	case errors.Is(err, catalog.ErrQuery):
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "服务暂不可用")
	case err != nil:
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取认领预览")
	default:
		httpserver.WriteJSON(responseWriter, http.StatusOK, preview)
	}
}
