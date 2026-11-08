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

type rootResponse struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Path          string    `json:"path"`
	Status        string    `json:"status"`
	Revision      int64     `json:"revision"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	ExcludedPaths []string  `json:"excluded_paths"`
}

// 数组先转 JSON 再解码，避免 database/sql 把 PostgreSQL text[] 当作原生切片扫描。
const rootResponseQuery = `SELECT r.id::text,r.path,r.status,r.revision,r.created_at,r.updated_at,
	COALESCE(to_json(array_agg(e.relative_path ORDER BY e.relative_path COLLATE "C")
		FILTER (WHERE e.relative_path IS NOT NULL)), '[]'::json)
	FROM library_roots r LEFT JOIN library_root_exclusions e ON e.root_id=r.id`

func scanRootResponse(row interface{ Scan(...any) error }) (rootResponse, error) {
	var root rootResponse
	var path string
	var exclusions []byte
	if err := row.Scan(&root.ID, &path, &root.Status, &root.Revision, &root.CreatedAt, &root.UpdatedAt, &exclusions); err != nil {
		return rootResponse{}, err
	}
	if err := json.Unmarshal(exclusions, &root.ExcludedPaths); err != nil {
		return rootResponse{}, err
	}
	root.Path = filepath.Base(path)
	root.Name = root.Path
	return root, nil
}

func (handlers *Handlers) listRoots(responseWriter http.ResponseWriter, request *http.Request) {
	if _, ok := handlers.guard.RequireAdmin(responseWriter, request); !ok {
		return
	}
	rows, err := handlers.database.QueryContext(request.Context(), rootResponseQuery+" GROUP BY r.id ORDER BY r.path")
	if err != nil {
		httpserver.WriteError(responseWriter, request, 503, "database_unavailable", "服务暂不可用")
		return
	}
	defer rows.Close()
	roots := []rootResponse{}
	for rows.Next() {
		root, scanErr := scanRootResponse(rows)
		if scanErr != nil {
			httpserver.WriteError(responseWriter, request, 503, "database_unavailable", "服务暂不可用")
			return
		}
		roots = append(roots, root)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		httpserver.WriteError(responseWriter, request, 503, "database_unavailable", "服务暂不可用")
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
