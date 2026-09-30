package httpapi

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"time"

	"github.com/qlqq/roomusic/backend/internal/library"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func (handlers *Handlers) listRoots(responseWriter http.ResponseWriter, request *http.Request) {
	if _, ok := handlers.guard.RequireAdmin(responseWriter, request); !ok {
		return
	}
	rows, err := handlers.database.QueryContext(request.Context(), "SELECT id::text,path,status,revision,created_at,updated_at FROM library_roots ORDER BY path")
	if err != nil {
		httpserver.WriteError(responseWriter, request, 503, "database_unavailable", "服务暂不可用")
		return
	}
	defer rows.Close()
	roots := []map[string]any{}
	for rows.Next() {
		var id, path, status string
		var revision int64
		var created, updated time.Time
		if scanErr := rows.Scan(&id, &path, &status, &revision, &created, &updated); scanErr != nil {
			httpserver.WriteError(responseWriter, request, 503, "database_error", "无法读取目录")
			return
		}
		roots = append(roots, map[string]any{"id": id, "path": filepath.Base(path), "name": filepath.Base(path), "status": status, "revision": revision, "created_at": created, "updated_at": updated})
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		httpserver.WriteError(responseWriter, request, 503, "database_error", "无法读取目录")
		return
	}
	httpserver.WriteJSON(responseWriter, 200, map[string]any{"items": roots})
}

func (handlers *Handlers) addRoot(responseWriter http.ResponseWriter, request *http.Request) {
	if !handlers.guard.RequireSameOrigin(responseWriter, request) {
		return
	}
	if _, ok := handlers.guard.RequireAdmin(responseWriter, request); !ok {
		return
	}
	actor, _ := handlers.guard.Authenticate(request)
	key := operationKey(request)
	var input struct {
		Path string `json:"path"`
	}
	if !httpserver.DecodeJSON(responseWriter, request, &input) {
		return
	}
	safePath, err := library.ValidatePath(input.Path, handlers.allowedRoots)
	if err != nil {
		httpserver.WriteError(responseWriter, request, 400, "invalid_library_path", "目录不在允许的音乐根目录内")
		return
	}
	fingerprint := operationFingerprint(map[string]any{"path": safePath})
	tx := handlers.beginRootOperation(responseWriter, request, actor.ID, "create", key, fingerprint)
	if tx == nil {
		return
	}
	defer tx.Rollback()
	id := uuid.New()
	var createdID string
	var rev int64
	var status string
	err = tx.QueryRowContext(request.Context(), "INSERT INTO library_roots(id,path) VALUES($1::uuid,$2) ON CONFLICT(path) DO UPDATE SET path=EXCLUDED.path RETURNING id::text,revision,status", id, safePath).Scan(&createdID, &rev, &status)
	if err != nil {
		httpserver.WriteError(responseWriter, request, 500, "database_error", "无法注册目录")
		return
	}
	result := map[string]any{"id": createdID, "name": filepath.Base(safePath), "status": status, "revision": rev}
	body, _ := json.Marshal(result)
	_, err = tx.ExecContext(request.Context(), `INSERT INTO library_root_operations(id,root_id,actor_id,operation_type,status,idempotency_key,fingerprint,result_revision,before_state,after_state,response,request_id) VALUES($1,$2::uuid,$3::uuid,'create','succeeded',$4,$5,$6,'{}',jsonb_build_object('status',$9::text,'revision',$6::bigint),$7::jsonb,$8)`, uuid.New(), createdID, actor.ID, key, fingerprint, rev, string(body), request.Header.Get("X-Request-ID"), status)
	if err != nil {
		handlers.writeRootOperationPersistenceError(responseWriter, request, err)
		return
	}
	if err = tx.Commit(); err != nil {
		httpserver.WriteError(responseWriter, request, 503, "database_unavailable", "服务暂不可用")
		return
	}
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(201)
	_, _ = responseWriter.Write(body)
}
