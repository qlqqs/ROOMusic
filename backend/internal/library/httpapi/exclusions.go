package httpapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/library"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

const maxRootExclusions = 100

func (handlers *Handlers) addExclusion(w http.ResponseWriter, r *http.Request) {
	handlers.changeExclusion(w, r, false)
}

func (handlers *Handlers) removeExclusion(w http.ResponseWriter, r *http.Request) {
	handlers.changeExclusion(w, r, true)
}

func (handlers *Handlers) changeExclusion(w http.ResponseWriter, r *http.Request, remove bool) {
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
	var input struct {
		Path string `json:"path"`
	}
	operationType := "exclude"
	if remove {
		operationType = "unexclude"
		input.Path = r.URL.Query().Get("path")
		if err := library.ValidateExclusionRelativePath(input.Path); err != nil {
			httpserver.WriteError(w, r, 400, "invalid_input", "排除路径无效")
			return
		}
	} else if !httpserver.DecodeJSON(w, r, &input) {
		return
	}
	key := operationKey(r)
	fingerprint := operationFingerprint(map[string]any{"root_id": id, "path": input.Path})
	tx := handlers.beginRootOperation(w, r, actor.ID, operationType, key, fingerprint)
	if tx == nil {
		return
	}
	defer tx.Rollback()

	// 不同幂等键也先锁住目录，再单独读取排除集合，避免并发添加突破上限。
	var rootPath string
	err := tx.QueryRowContext(r.Context(), "SELECT path FROM library_roots WHERE id=$1::uuid FOR UPDATE", id).Scan(&rootPath)
	if err == sql.ErrNoRows {
		httpserver.WriteError(w, r, 404, "not_found", "目录不存在")
		return
	}
	if err != nil {
		httpserver.WriteError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	relativePath := input.Path
	if !remove {
		relativePath, err = library.ValidateExclusionPath(input.Path, rootPath, handlers.allowedRoots)
		if err != nil {
			httpserver.WriteError(w, r, 400, "invalid_library_path", "打不开这个文件夹")
			return
		}
	}
	result, err := scanRootResponse(tx.QueryRowContext(r.Context(), rootResponseQuery+" WHERE r.id=$1::uuid GROUP BY r.id", id))
	if err != nil {
		httpserver.WriteError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	index, exists := slices.BinarySearch(result.ExcludedPaths, relativePath)
	if !remove && !exists && len(result.ExcludedPaths) >= maxRootExclusions {
		httpserver.WriteError(w, r, 409, "exclusion_limit", "不扫描的文件夹太多了")
		return
	}
	if remove && exists {
		_, err = tx.ExecContext(r.Context(), "DELETE FROM library_root_exclusions WHERE root_id=$1::uuid AND relative_path=$2", id, relativePath)
		result.ExcludedPaths = slices.Delete(result.ExcludedPaths, index, index+1)
	} else if !remove && !exists {
		_, err = tx.ExecContext(r.Context(), "INSERT INTO library_root_exclusions(root_id,relative_path) VALUES($1::uuid,$2)", id, relativePath)
		result.ExcludedPaths = slices.Insert(result.ExcludedPaths, index, relativePath)
	}
	if err != nil {
		httpserver.WriteError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}

	// 排除项不改变目录版本；成功的空操作也记录收据，供相同请求稳定回放。
	body, err := json.Marshal(result)
	if err != nil {
		httpserver.WriteError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO library_root_operations
		(id,root_id,actor_id,operation_type,status,idempotency_key,fingerprint,result_revision,before_state,after_state,response,request_id)
		VALUES($1,$2::uuid,$3::uuid,$4,'succeeded',$5,$6,$7,'{}',jsonb_build_object('relative_path',$8::text),$9::jsonb,$10)`,
		uuid.New(), id, actor.ID, operationType, key, fingerprint, result.Revision, relativePath, string(body), r.Header.Get("X-Request-ID"))
	if err != nil {
		httpserver.WriteError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	if err := tx.Commit(); err != nil {
		httpserver.WriteError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	httpserver.WriteJSON(w, 200, result)
}
