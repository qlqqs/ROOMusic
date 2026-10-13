package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// releaseIDFromPath 校验路由里的发行 ID 并规范成小写，使指纹和记录不受大小写影响；无效时写出 400 并返回 false。
func releaseIDFromPath(responseWriter http.ResponseWriter, request *http.Request) (string, bool) {
	releaseID := request.PathValue("id")
	if !uuid.Valid(releaseID) {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_id", "发行版本标识无效")
		return "", false
	}
	return strings.ToLower(releaseID), true
}

// writeOperationError 把 operations.Run 或执行器的错误写成统一的错误信封；500 与 503 在这里记录日志，响应不含内部细节。
func (handlers *Handlers) writeOperationError(responseWriter http.ResponseWriter, request *http.Request, err error) {
	status, code, message := operations.Problem(err)
	if status >= http.StatusInternalServerError {
		handlers.logger.Error("governed operation failed", "request_id", request.Header.Get("X-Request-ID"), "status", status, "error", err)
	}
	httpserver.WriteError(responseWriter, request, status, code, message)
}

// releaseCorrections 返回发行的修正状态（管理员）：当前人工修改版本号与八个可修正字段的扫描值、修正、有效值与不确定标记。
// 检查顺序是 401、403、400（非 UUID）、404（不存在或没有可见曲目）、200。
func (handlers *Handlers) releaseCorrections(responseWriter http.ResponseWriter, request *http.Request) {
	if _, ok := handlers.guard.RequireAdmin(responseWriter, request); !ok {
		return
	}
	releaseID, ok := releaseIDFromPath(responseWriter, request)
	if !ok {
		return
	}
	state, err := handlers.store.LoadCorrectionState(request.Context(), releaseID)
	switch {
	case errors.Is(err, catalog.ErrReleaseNotFound):
		httpserver.WriteError(responseWriter, request, http.StatusNotFound, "not_found", "发行版本不存在")
	case errors.Is(err, catalog.ErrQuery):
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "服务暂不可用")
	case err != nil:
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取修正状态")
	default:
		httpserver.WriteJSON(responseWriter, http.StatusOK, state)
	}
}

// correctRelease 修正发行的字段（管理员、同源、Idempotency-Key）。请求体是预期版本号与每个字段的修改
// （set、null 或 clear）；检查顺序是同源 403、401/403、400（ID、幂等键、请求体）、之后由 operations.Run 在
// 同一个事务里完成幂等、版本核对、写修正、写操作记录。成功返回新的修正状态加 operation_id，同键同义重放返回同一份响应。
func (handlers *Handlers) correctRelease(responseWriter http.ResponseWriter, request *http.Request) {
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
		ExpectedRevision int64                                  `json:"expected_revision"`
		Changes          map[string]catalog.RawCorrectionChange `json:"changes"`
	}
	if !httpserver.DecodeJSON(responseWriter, request, &body) {
		return
	}
	if body.ExpectedRevision < 1 {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_input", "版本号无效")
		return
	}
	changes, err := catalog.ParseCorrectionChanges(body.Changes)
	if err != nil {
		handlers.writeOperationError(responseWriter, request, err)
		return
	}
	correction := catalog.CorrectionRequest{ReleaseID: releaseID, ExpectedRevision: body.ExpectedRevision, Changes: changes}
	result, err := operations.Run(request.Context(), handlers.database, operations.Spec{
		ActorID: actor.ID, Type: operations.TypeReleaseCorrection, Key: key,
		Fingerprint: correction.Fingerprint(), RequestID: request.Header.Get("X-Request-ID"),
	}, catalog.CorrectionExecutor(correction))
	if err != nil {
		handlers.writeOperationError(responseWriter, request, err)
		return
	}
	httpserver.WriteJSON(responseWriter, http.StatusOK, result.Response)
}
