package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

const maxRootExclusions = 100

// AddExclusion 校验真实子目录并添加排除项；已有项仍记录成功收据。
func (service *Service) AddExclusion(ctx context.Context, operation RootOperation, id, path string, allowedRoots []string) (RootOperationResult, error) {
	return service.changeExclusion(ctx, operation, id, path, allowedRoots, false)
}

// RemoveExclusion 移除相对路径排除项，不要求对应目录仍然存在。
func (service *Service) RemoveExclusion(ctx context.Context, operation RootOperation, id, path string) (RootOperationResult, error) {
	return service.changeExclusion(ctx, operation, id, path, nil, true)
}

func (service *Service) changeExclusion(ctx context.Context, operation RootOperation, id, path string, allowedRoots []string, remove bool) (RootOperationResult, error) {

	if !uuid.Valid(id) {
		return RootOperationResult{}, &RootOperationError{Code: "invalid_id", Message: "目录标识无效"}
	}
	id = strings.ToLower(id)
	operationType := "exclude"
	if remove {
		operationType = "unexclude"
		if err := ValidateExclusionRelativePath(path); err != nil {
			return RootOperationResult{}, &RootOperationError{Code: "invalid_input", Message: "排除路径无效"}
		}
	}
	key := strings.TrimSpace(operation.IdempotencyKey)
	fingerprint := operationFingerprint(map[string]any{"root_id": id, "path": path})
	tx, replay, err := service.beginRootOperation(ctx, operation.ActorID, operationType, key, fingerprint)
	if err != nil || tx == nil {
		return replay, err
	}
	defer tx.Rollback()

	// 不同幂等键也先锁住目录，再单独读取排除集合，避免并发添加突破上限。
	var rootPath string
	err = tx.QueryRowContext(ctx, "SELECT path FROM library_roots WHERE id=$1::uuid FOR UPDATE", id).Scan(&rootPath)
	if err == sql.ErrNoRows {
		return RootOperationResult{}, &RootOperationError{Code: "not_found", Message: "目录不存在"}
	}
	if err != nil {
		return RootOperationResult{}, &RootOperationError{Code: "database_unavailable", Message: "服务暂不可用", Err: err}
	}
	relativePath := path
	if !remove {
		relativePath, err = ValidateExclusionPath(path, rootPath, allowedRoots)
		if err != nil {
			return RootOperationResult{}, &RootOperationError{Code: "invalid_library_path", Message: "打不开这个文件夹", Err: err}
		}
	}
	result, err := scanRootResponse(tx.QueryRowContext(ctx, rootResponseQuery+" WHERE r.id=$1::uuid GROUP BY r.id", id))
	if err != nil {
		return RootOperationResult{}, &RootOperationError{Code: "database_unavailable", Message: "服务暂不可用", Err: err}
	}
	index, exists := slices.BinarySearch(result.ExcludedPaths, relativePath)
	if !remove && !exists && len(result.ExcludedPaths) >= maxRootExclusions {
		return RootOperationResult{}, &RootOperationError{Code: "exclusion_limit", Message: "不扫描的文件夹太多了"}
	}
	if remove && exists {
		_, err = tx.ExecContext(ctx, "DELETE FROM library_root_exclusions WHERE root_id=$1::uuid AND relative_path=$2", id, relativePath)
		result.ExcludedPaths = slices.Delete(result.ExcludedPaths, index, index+1)
	} else if !remove && !exists {
		_, err = tx.ExecContext(ctx, "INSERT INTO library_root_exclusions(root_id,relative_path) VALUES($1::uuid,$2)", id, relativePath)
		result.ExcludedPaths = slices.Insert(result.ExcludedPaths, index, relativePath)
	}
	if err != nil {
		return RootOperationResult{}, &RootOperationError{Code: "database_unavailable", Message: "服务暂不可用", Err: err}
	}

	// 排除项不改变目录版本；成功的空操作也记录收据，供相同请求稳定回放。
	body, err := json.Marshal(result)
	if err != nil {
		return RootOperationResult{}, &RootOperationError{Code: "database_unavailable", Message: "服务暂不可用", Err: err}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO library_root_operations
		(id,root_id,actor_id,operation_type,status,idempotency_key,fingerprint,result_revision,before_state,after_state,response,request_id)
		VALUES($1,$2::uuid,$3::uuid,$4,'succeeded',$5,$6,$7,'{}',jsonb_build_object('relative_path',$8::text),$9::jsonb,$10)`,
		uuid.New(), id, operation.ActorID, operationType, key, fingerprint, result.Revision, relativePath, string(body), operation.RequestID)
	if err != nil {
		return RootOperationResult{}, &RootOperationError{Code: "database_unavailable", Message: "服务暂不可用", Err: err}
	}
	if err := tx.Commit(); err != nil {
		return RootOperationResult{}, &RootOperationError{Code: "database_unavailable", Message: "服务暂不可用", Err: err}
	}
	return RootOperationResult{Response: body}, nil
}
