package httpapi

import (
	"net/http"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// changeReleaseGroup 调整发行所在的组（管理员、同源、Idempotency-Key）：join 把这个版本归入 group_id 指定的另一张专辑，
// detach 把它移出为独立专辑。请求体含预期版本号；检查顺序是同源 403、401/403、400（ID、幂等键、请求体）、
// 之后由 operations.Run 在同一个事务里完成幂等、版本核对、更新 group_id、写操作记录。
// 成功返回发行现在所在的组、原来的组、新的版本号与 operation_id；同键同义重放返回同一份响应。
func (handlers *Handlers) changeReleaseGroup(responseWriter http.ResponseWriter, request *http.Request) {
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
		Action           string `json:"action"`
		GroupID          string `json:"group_id"`
	}
	if !httpserver.DecodeJSON(responseWriter, request, &body) {
		return
	}
	switch {
	case body.ExpectedRevision < 1:
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_input", "版本号无效")
		return
	case body.Action == catalog.GroupJoin && !uuid.Valid(body.GroupID):
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_input", "目标专辑标识无效")
		return
	case body.Action == catalog.GroupDetach && body.GroupID != "":
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_input", "移出为独立专辑时不需要目标专辑")
		return
	case body.Action != catalog.GroupJoin && body.Action != catalog.GroupDetach:
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_input", "归组动作无效")
		return
	}
	change := catalog.GroupChangeRequest{ReleaseID: releaseID, ExpectedRevision: body.ExpectedRevision, Action: body.Action, GroupID: strings.ToLower(body.GroupID)}
	result, err := operations.Run(request.Context(), handlers.database, operations.Spec{
		ActorID: actor.ID, Type: operations.TypeReleaseGroupChange, Key: key,
		Fingerprint: change.Fingerprint(), RequestID: request.Header.Get("X-Request-ID"),
	}, catalog.GroupChangeExecutor(change))
	if err != nil {
		handlers.writeOperationError(responseWriter, request, err)
		return
	}
	httpserver.WriteJSON(responseWriter, http.StatusOK, result.Response)
}
