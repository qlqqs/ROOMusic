package httpapi

import (
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

// reviewReleaseIssue 核对或取消核对发行的一类问题（管理员、同源、Idempotency-Key）：review 把它标记为已核对（带页面读到的依据令牌），
// unreview 取消核对。请求体含预期版本号；检查顺序是同源 403、401/403、400（ID、幂等键、请求体）、之后由 operations.Run 在同一个事务里
// 完成幂等、版本核对、比较依据、写核对行、写操作记录。成功返回问题现在是否有核对、新的版本号与 operation_id；同键同义重放返回同一份响应。
func (handlers *Handlers) reviewReleaseIssue(responseWriter http.ResponseWriter, request *http.Request) {
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
		ExpectedRevision int64   `json:"expected_revision"`
		Issue            string  `json:"issue"`
		Action           string  `json:"action"`
		Basis            *string `json:"basis"`
	}
	if !httpserver.DecodeJSON(responseWriter, request, &body) {
		return
	}
	message := ""
	switch {
	case body.ExpectedRevision < 1:
		message = "版本号无效"
	case !catalog.ReviewableIssue(body.Issue):
		message = "这类问题不能标记为已核对"
	case body.Action == catalog.IssueReview && (body.Basis == nil || *body.Basis == ""):
		message = "核对需要问题的依据"
	case body.Action == catalog.IssueUnreview && body.Basis != nil:
		message = "取消核对时不需要依据"
	case body.Action != catalog.IssueReview && body.Action != catalog.IssueUnreview:
		message = "核对动作无效"
	}
	if message != "" {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_input", message)
		return
	}
	change := catalog.IssueReviewRequest{ReleaseID: releaseID, ExpectedRevision: body.ExpectedRevision, Issue: body.Issue, Action: body.Action}
	if body.Basis != nil {
		change.Basis = *body.Basis
	}
	result, err := operations.Run(request.Context(), handlers.database, operations.Spec{
		ActorID: actor.ID, Type: operations.TypeReleaseIssueReview, Key: key,
		Fingerprint: change.Fingerprint(), RequestID: request.Header.Get("X-Request-ID"),
	}, catalog.IssueReviewExecutor(change))
	if err != nil {
		handlers.writeOperationError(responseWriter, request, err)
		return
	}
	httpserver.WriteJSON(responseWriter, http.StatusOK, result.Response)
}
