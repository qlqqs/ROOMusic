package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// setReleaseGroupDefault 设定或清除一张专辑的默认版本（管理员、同源、Idempotency-Key）：release_id 是 UUID 表示把这个版本设为
// 默认版本，null 表示改回自动选择。请求体含专辑的预期版本号；检查顺序是同源 403、401/403、400（ID、幂等键、请求体）、
// 之后由 operations.Run 在同一个事务里完成幂等、版本核对、更新默认版本、写操作记录。
// 成功返回专辑、现在已存的默认版本、新的版本号与 operation_id；同键同义重放返回同一份响应。
func (handlers *Handlers) setReleaseGroupDefault(responseWriter http.ResponseWriter, request *http.Request) {
	if !handlers.guard.RequireSameOrigin(responseWriter, request) {
		return
	}
	actor, ok := handlers.guard.RequireAdmin(responseWriter, request)
	if !ok {
		return
	}
	groupID := request.PathValue("id")
	if !uuid.Valid(groupID) {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_id", "发行组标识无效")
		return
	}
	key, ok := operations.NormalizeKey(request.Header.Get("Idempotency-Key"))
	if !ok {
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_input", "请求需要 Idempotency-Key（UUID）")
		return
	}
	// release_id 必须出现：UUID 表示设定，null 表示改回自动选择；缺少、不是字符串或不是 UUID 都是无效输入，
	// 不能把“漏写”当成“改回自动选择”。
	var body struct {
		ExpectedRevision int64           `json:"expected_revision"`
		ReleaseID        json.RawMessage `json:"release_id"`
	}
	if !httpserver.DecodeJSON(responseWriter, request, &body) {
		return
	}
	releaseID, valid := parseDefaultRelease(body.ReleaseID)
	if body.ExpectedRevision < 1 || !valid {
		message := "版本号无效"
		if body.ExpectedRevision >= 1 {
			message = "默认版本需要是版本标识（UUID），或 null（改回自动选择）"
		}
		httpserver.WriteError(responseWriter, request, http.StatusBadRequest, "invalid_input", message)
		return
	}
	change := governance.GroupDefaultRequest{GroupID: strings.ToLower(groupID), ExpectedRevision: body.ExpectedRevision, ReleaseID: releaseID}
	result, err := operations.Run(request.Context(), handlers.database, operations.Spec{
		ActorID: actor.ID, Type: operations.TypeReleaseGroupDefault, Key: key,
		Fingerprint: change.Fingerprint(), RequestID: request.Header.Get("X-Request-ID"),
	}, governance.GroupDefaultExecutor(change))
	if err != nil {
		handlers.writeOperationError(responseWriter, request, err)
		return
	}
	httpserver.WriteJSON(responseWriter, http.StatusOK, result.Response)
}

// parseDefaultRelease 解析请求体里的 release_id：UUID（返回小写）、null（返回空字符串，表示改回自动选择）；
// 字段缺失、不是字符串或不是 UUID 时 valid 为 false。
func parseDefaultRelease(raw json.RawMessage) (releaseID string, valid bool) {
	trimmed := bytes.TrimSpace(raw)
	switch {
	case len(trimmed) == 0:
		return "", false
	case bytes.Equal(trimmed, []byte("null")):
		return "", true
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err != nil || !uuid.Valid(text) {
		return "", false
	}
	return strings.ToLower(text), true
}
