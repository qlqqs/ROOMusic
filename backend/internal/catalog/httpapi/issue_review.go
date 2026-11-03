package httpapi

import (
	"errors"
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
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
	case !governance.ReviewableIssue(body.Issue):
		message = "这类问题不能标记为已核对"
	case body.Action == governance.IssueReview && (body.Basis == nil || *body.Basis == ""):
		message = "核对需要问题的依据"
	case body.Action == governance.IssueUnreview && body.Basis != nil:
		message = "取消核对时不需要依据"
	case body.Action != governance.IssueReview && body.Action != governance.IssueUnreview:
		message = "核对动作无效"
	}
	if message != "" {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_input", message)
		return
	}
	change := governance.IssueReviewRequest{ReleaseID: releaseID, ExpectedRevision: body.ExpectedRevision, Issue: body.Issue, Action: body.Action}
	if body.Basis != nil {
		change.Basis = *body.Basis
	}
	result, err := operations.Run(request.Context(), handlers.database, operations.Spec{
		ActorID: actor.ID, Type: operations.TypeReleaseIssueReview, Key: key,
		Fingerprint: change.Fingerprint(), RequestID: request.Header.Get("X-Request-ID"),
	}, governance.IssueReviewExecutor(change))
	if err != nil {
		handlers.writeOperationError(responseWriter, request, err)
		return
	}
	httpserver.WriteJSON(responseWriter, http.StatusOK, result.Response)
}

// listReleaseIssues 返回有问题的可见版本的一页（管理员）：status 是 open（默认）或 reviewed，category 是 fields、grouping、
// missing_tracks（不给是全部）；另给筛选项上的数字。检查顺序是 401、403、400（分页 invalid_pagination、筛选 invalid_filter）、200。
func (handlers *Handlers) listReleaseIssues(responseWriter http.ResponseWriter, request *http.Request) {
	if _, ok := handlers.guard.RequireAdmin(responseWriter, request); !ok {
		return
	}
	page, pageSize, err := httpserver.ParsePagination(request, defaultReleasePageSize, maxReleaseRows)
	if err != nil {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_pagination", "分页参数无效")
		return
	}
	query := request.URL.Query()
	status, validStatus := governance.ParseIssueListStatus(query["status"])
	category, validCategory := governance.ParseIssueListCategory(query["category"])
	if !validStatus || !validCategory {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_filter", "问题筛选条件无效")
		return
	}
	result, err := handlers.governance.ListIssues(request.Context(), governance.IssueListParams{Status: status, Category: category, Page: page, PageSize: pageSize})
	if errors.Is(err, catalog.ErrQuery) {
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "服务暂不可用")
		return
	}
	if err != nil {
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取问题列表")
		return
	}
	httpserver.WriteJSON(responseWriter, http.StatusOK, map[string]any{
		"items":      result.Items,
		"summary":    result.Summary,
		"pagination": map[string]any{"page": page, "page_size": pageSize, "total": result.Total},
	})
}

// releaseIssues 返回一个可见版本的全部问题（管理员）：拿不准的字段、归组冲突与缺失曲目，可以核对的问题带依据令牌与核对状态，
// 核对人的用户名在这里批量补上。检查顺序是 401、403、400（非 UUID）、404（不存在或不可见）、200。
func (handlers *Handlers) releaseIssues(responseWriter http.ResponseWriter, request *http.Request) {
	if _, ok := handlers.guard.RequireAdmin(responseWriter, request); !ok {
		return
	}
	releaseID, ok := releaseIDFromPath(responseWriter, request)
	if !ok {
		return
	}
	issues, err := handlers.governance.LoadReleaseIssues(request.Context(), releaseID)
	if err == nil {
		var names map[string]string
		if names, err = handlers.guard.Usernames(request.Context(), issues.ReviewerIDs()); err == nil {
			issues.SetReviewerNames(names)
		}
	}
	switch {
	case errors.Is(err, catalog.ErrReleaseNotFound):
		httpserver.WriteError(responseWriter, request, http.StatusNotFound, "not_found", "发行版本不存在")
	case errors.Is(err, catalog.ErrQuery):
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "服务暂不可用")
	case err != nil:
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_error", "无法读取这个版本的问题")
	default:
		httpserver.WriteJSON(responseWriter, http.StatusOK, issues)
	}
}
