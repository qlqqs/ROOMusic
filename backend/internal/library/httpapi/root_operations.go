package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/library"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func (handlers *Handlers) writeRootOperationError(w http.ResponseWriter, r *http.Request, err error) {
	var operationError *library.RootOperationError
	if !errors.As(err, &operationError) {
		httpserver.WriteError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	if operationError.RecordFailure {
		handlers.logger.Error("record root operation", "request_id", r.Header.Get("X-Request-ID"), "error", operationError.Err)
	}
	status := http.StatusServiceUnavailable
	switch operationError.Code {
	case "invalid_input", "invalid_id", "invalid_library_path":
		status = http.StatusBadRequest
	case "not_found":
		status = http.StatusNotFound
	case "idempotency_conflict", "revision_conflict", "scan_running", "path_already_registered", "exclusion_limit":
		status = http.StatusConflict
	case "database_error", "operation_failed":
		status = http.StatusInternalServerError
	}
	httpserver.WriteError(w, r, status, operationError.Code, operationError.Message)
}

func (handlers *Handlers) writeRootOperationResult(w http.ResponseWriter, r *http.Request, result library.RootOperationResult, err error) {
	if err != nil {
		handlers.writeRootOperationError(w, r, err)
		return
	}
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	if result.Replayed || result.Created {
		w.Header().Set("Content-Type", "application/json")
		if result.Replayed {
			w.Header().Set("Cache-Control", "no-store")
		}
		w.WriteHeader(status)
		_, _ = w.Write(result.Response)
		return
	}
	httpserver.WriteJSON(w, status, result.Response)
}

func operationKey(r *http.Request) string { return strings.TrimSpace(r.Header.Get("Idempotency-Key")) }

// rootIDFromPath 校验路由中的目录 ID 并规范为小写，使指纹和响应不受大小写影响。
func rootIDFromPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !uuid.Valid(id) {
		httpserver.WriteError(w, r, 400, "invalid_id", "目录标识无效")
		return "", false
	}
	return strings.ToLower(id), true
}

func (handlers *Handlers) rootOperations(w http.ResponseWriter, r *http.Request) {
	if _, ok := handlers.guard.RequireAdmin(w, r); !ok {
		return
	}
	rows, err := handlers.database.QueryContext(r.Context(), `SELECT id::text,root_id::text,operation_type,status,result_revision,error_code,created_at FROM library_root_operations ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		httpserver.WriteError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, typ, status string
		var rootID, code sql.NullString
		var rev sql.NullInt64
		var created interface{}
		if err := rows.Scan(&id, &rootID, &typ, &status, &rev, &code, &created); err != nil {
			httpserver.WriteError(w, r, 503, "database_error", "无法读取操作历史")
			return
		}
		item := map[string]any{"id": id, "operation": typ, "status": status, "created_at": created}
		if rootID.Valid {
			item["root_id"] = rootID.String
		}
		if rev.Valid {
			item["revision"] = rev.Int64
		}
		if code.Valid {
			item["error_code"] = code.String
		}
		items = append(items, item)
	}
	httpserver.WriteJSON(w, 200, map[string]any{"items": items})
}

func (handlers *Handlers) updateRoot(w http.ResponseWriter, r *http.Request) {
	if !handlers.guard.RequireSameOrigin(w, r) {
		return
	}
	actor, ok := handlers.guard.RequireAdmin(w, r)
	if !ok {
		return
	}
	id, ok := rootIDFromPath(w, r)
	if !ok {
		return
	}
	key := operationKey(r)
	var in struct {
		Status           string `json:"status"`
		ExpectedRevision int64  `json:"expected_revision"`
	}
	if !httpserver.DecodeJSON(w, r, &in) {
		return
	}
	if in.Status != "disabled" || in.ExpectedRevision < 1 {
		httpserver.WriteError(w, r, 400, "invalid_input", "状态或 revision 无效")
		return
	}
	result, err := handlers.service.DisableRoot(r.Context(), library.RootOperation{ActorID: actor.ID, IdempotencyKey: key, RequestID: r.Header.Get("X-Request-ID")}, id, in.ExpectedRevision)
	handlers.writeRootOperationResult(w, r, result, err)
}

func (handlers *Handlers) restoreRoot(w http.ResponseWriter, r *http.Request) {
	if !handlers.guard.RequireSameOrigin(w, r) {
		return
	}
	actor, ok := handlers.guard.RequireAdmin(w, r)
	if !ok {
		return
	}
	id, ok := rootIDFromPath(w, r)
	if !ok {
		return
	}
	key := operationKey(r)
	var in struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if !httpserver.DecodeJSON(w, r, &in) {
		return
	}
	if in.ExpectedRevision < 1 {
		httpserver.WriteError(w, r, 400, "invalid_input", "revision 无效")
		return
	}
	result, err := handlers.service.RestoreRoot(r.Context(), library.RootOperation{ActorID: actor.ID, IdempotencyKey: key, RequestID: r.Header.Get("X-Request-ID")}, id, in.ExpectedRevision)
	handlers.writeRootOperationResult(w, r, result, err)
}

func (handlers *Handlers) relocateRoot(w http.ResponseWriter, r *http.Request) {
	if !handlers.guard.RequireSameOrigin(w, r) {
		return
	}
	actor, ok := handlers.guard.RequireAdmin(w, r)
	if !ok {
		return
	}
	id, ok := rootIDFromPath(w, r)
	if !ok {
		return
	}
	key := operationKey(r)
	if key == "" {
		httpserver.WriteError(w, r, 400, "invalid_input", "请求需要 Idempotency-Key")
		return
	}
	var in struct {
		Path             string `json:"path"`
		ExpectedRevision int64  `json:"expected_revision"`
	}
	if !httpserver.DecodeJSON(w, r, &in) {
		return
	}
	if in.Path == "" || in.ExpectedRevision < 1 {
		httpserver.WriteError(w, r, 400, "invalid_input", "路径或 revision 无效")
		return
	}
	result, err := handlers.service.RelocateRoot(r.Context(), library.RootOperation{ActorID: actor.ID, IdempotencyKey: key, RequestID: r.Header.Get("X-Request-ID")}, id, in.ExpectedRevision, in.Path, handlers.allowedRoots)
	handlers.writeRootOperationResult(w, r, result, err)
}
