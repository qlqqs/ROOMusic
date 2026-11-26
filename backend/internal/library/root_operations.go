package library

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash/fnv"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// RootOperation 标识已经鉴权的调用者、幂等键与关联请求；调用入口负责管理员权限检查。
type RootOperation struct {
	ActorID        string
	IdempotencyKey string
	RequestID      string
}

// RootOperationResult 保留原始收据和回放标记，供各入口复用同一操作结果。
type RootOperationResult struct {
	Response json.RawMessage
	Replayed bool
	Created  bool
}

// RootOperationError 提供稳定错误码和安全文案；Err 仅供内部诊断，不应返回给调用者。
// RecordFailure 标记原有目录操作记录失败日志，唯一约束冲突不记录该日志。
type RootOperationError struct {
	Code          string
	Message       string
	Err           error
	RecordFailure bool
}

func (e *RootOperationError) Error() string { return e.Message }
func (e *RootOperationError) Unwrap() error { return e.Err }

func rootOperationPersistenceError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return &RootOperationError{Code: "idempotency_conflict", Message: "幂等键已用于其他请求", Err: err}
	}
	return &RootOperationError{Code: "operation_failed", Message: "目录操作未能完成", Err: err, RecordFailure: true}
}

func operationFingerprint(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// rootOperationLockClass 是目录操作 advisory lock 的固定类别（ASCII 的 ROOT）。
// 两参数锁与迁移锁（单个 bigint 键）、扫描锁（SCAN 类别）互不共用键空间。
const rootOperationLockClass int32 = 0x524f4f54

// rootOperationLockKey 把同一 actor、操作类型和幂等键映射到同一把锁。碰撞只会让无关请求多排一次队，
// 是否重放仍由 beginRootOperation 中完整的 actor/类型/键查询决定。
func rootOperationLockKey(actorID, operationType, key string) int32 {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(actorID + ":" + operationType + ":" + key))
	return int32(hash.Sum32())
}

// beginRootOperation 在 READ COMMITTED 事务中按操作者、类型、幂等键加锁并查收据。
// 新操作返回事务；回放或错误返回空事务，并在返回前回滚以释放锁。
func (service *Service) beginRootOperation(ctx context.Context, actorID, operationType, key, fingerprint string) (*sql.Tx, RootOperationResult, error) {
	if key == "" {
		return nil, RootOperationResult{}, &RootOperationError{Code: "invalid_input", Message: "请求需要 Idempotency-Key"}
	}
	// 加锁后的查询必须看到前一个持锁事务已提交的记录，所以显式要求 READ COMMITTED。
	tx, err := service.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, RootOperationResult{}, &RootOperationError{Code: "database_unavailable", Message: "服务暂不可用", Err: err}
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1::int, $2::int)`, rootOperationLockClass, rootOperationLockKey(actorID, operationType, key)); err != nil {
		_ = tx.Rollback()
		return nil, RootOperationResult{}, &RootOperationError{Code: "database_unavailable", Message: "服务暂不可用", Err: err}
	}
	var recordedFingerprint, status string
	var response []byte
	err = tx.QueryRowContext(ctx, `SELECT fingerprint,response,status FROM library_root_operations WHERE actor_id=$1::uuid AND operation_type=$2 AND idempotency_key=$3`, actorID, operationType, key).Scan(&recordedFingerprint, &response, &status)
	if err == sql.ErrNoRows {
		return tx, RootOperationResult{}, nil
	}
	_ = tx.Rollback()
	if err != nil {
		return nil, RootOperationResult{}, &RootOperationError{Code: "database_unavailable", Message: "服务暂不可用", Err: err}
	}
	if recordedFingerprint != fingerprint {
		return nil, RootOperationResult{}, &RootOperationError{Code: "idempotency_conflict", Message: "幂等键已用于其他请求"}
	}
	return nil, RootOperationResult{Response: response, Replayed: true, Created: status == "succeeded" && operationType == "create"}, nil
}

func filepathBase(path string) string {
	i := strings.LastIndexAny(path, "/\\")
	if i >= 0 {
		return path[i+1:]
	}
	return path
}

// AddRoot 校验允许根并登记目录，与幂等收据在同一事务提交。
func (service *Service) AddRoot(ctx context.Context, operation RootOperation, path string, allowedRoots []string) (RootOperationResult, error) {
	key := strings.TrimSpace(operation.IdempotencyKey)
	safePath, err := ValidatePath(path, allowedRoots)
	if err != nil {
		return RootOperationResult{}, &RootOperationError{Code: "invalid_library_path", Message: "目录不在允许的音乐根目录内", Err: err}
	}
	fingerprint := operationFingerprint(map[string]any{"path": safePath})
	tx, replay, err := service.beginRootOperation(ctx, operation.ActorID, "create", key, fingerprint)
	if err != nil || tx == nil {
		return replay, err
	}
	defer tx.Rollback()
	id := uuid.New()
	var createdID string
	var rev int64
	var status string
	err = tx.QueryRowContext(ctx, "INSERT INTO library_roots(id,path) VALUES($1::uuid,$2) ON CONFLICT(path) DO UPDATE SET path=EXCLUDED.path RETURNING id::text,revision,status", id, safePath).Scan(&createdID, &rev, &status)
	if err != nil {
		return RootOperationResult{}, &RootOperationError{Code: "database_error", Message: "无法注册目录", Err: err}
	}
	result := map[string]any{"id": createdID, "name": filepath.Base(safePath), "status": status, "revision": rev}
	body, _ := json.Marshal(result)
	_, err = tx.ExecContext(ctx, `INSERT INTO library_root_operations(id,root_id,actor_id,operation_type,status,idempotency_key,fingerprint,result_revision,before_state,after_state,response,request_id) VALUES($1,$2::uuid,$3::uuid,'create','succeeded',$4,$5,$6,'{}',jsonb_build_object('status',$9::text,'revision',$6::bigint),$7::jsonb,$8)`, uuid.New(), createdID, operation.ActorID, key, fingerprint, rev, string(body), operation.RequestID, status)
	if err != nil {
		return RootOperationResult{}, rootOperationPersistenceError(err)
	}
	if err = tx.Commit(); err != nil {
		return RootOperationResult{}, &RootOperationError{Code: "database_unavailable", Message: "服务暂不可用", Err: err}
	}
	wakeWatch(service.watch.refresh)
	return RootOperationResult{Response: body, Created: true}, nil
}

// DisableRoot 沿用目录操作的校验、事务与幂等收据。
func (service *Service) DisableRoot(ctx context.Context, operation RootOperation, id string, expectedRevision int64) (RootOperationResult, error) {
	key := strings.TrimSpace(operation.IdempotencyKey)
	if !uuid.Valid(id) {
		return RootOperationResult{}, &RootOperationError{Code: "invalid_id", Message: "目录标识无效"}
	}
	id = strings.ToLower(id)
	if expectedRevision < 1 {
		return RootOperationResult{}, &RootOperationError{Code: "invalid_input", Message: "状态或 revision 无效"}
	}
	fp := operationFingerprint(map[string]any{"root_id": id, "status": "disabled", "expected_revision": expectedRevision})
	tx, replay, err := service.beginRootOperation(ctx, operation.ActorID, "disable", key, fp)
	if err != nil || tx == nil {
		return replay, err
	}
	defer tx.Rollback()
	var path, status string
	var rev int64
	err = tx.QueryRowContext(ctx, "SELECT path,status,revision FROM library_roots WHERE id=$1::uuid FOR UPDATE", id).Scan(&path, &status, &rev)
	if err == sql.ErrNoRows {
		return RootOperationResult{}, &RootOperationError{Code: "not_found", Message: "目录不存在"}
	}
	if err != nil {
		return RootOperationResult{}, &RootOperationError{Code: "database_unavailable", Message: "服务暂不可用", Err: err}
	}
	if rev != expectedRevision {
		return RootOperationResult{}, &RootOperationError{Code: "revision_conflict", Message: "目录版本已变化"}
	}
	if status != "active" {
		return RootOperationResult{}, &RootOperationError{Code: "revision_conflict", Message: "目录状态已变化"}
	}
	newRev := rev + 1
	_, err = tx.ExecContext(ctx, "UPDATE library_roots SET status='disabled',revision=$2,updated_at=NOW() WHERE id=$1::uuid", id, newRev)
	if err != nil {
		return RootOperationResult{}, &RootOperationError{Code: "operation_failed", Message: "无法停用目录", Err: err}
	}
	result := map[string]any{"id": id, "path": filepathBase(path), "status": "disabled", "revision": newRev}
	body, _ := json.Marshal(result)
	_, err = tx.ExecContext(ctx, `INSERT INTO library_root_operations(id,root_id,actor_id,operation_type,status,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id) VALUES($1,$2::uuid,$3::uuid,'disable','succeeded',$4,$5,$6,$7,jsonb_build_object('status',$8::text,'revision',$6::bigint),jsonb_build_object('status','disabled','revision',$7::bigint),$9::jsonb,$10)`, uuid.New(), id, operation.ActorID, key, fp, rev, newRev, status, string(body), operation.RequestID)
	if err != nil {
		return RootOperationResult{}, rootOperationPersistenceError(err)
	}
	if err = tx.Commit(); err != nil {
		return RootOperationResult{}, &RootOperationError{Code: "database_unavailable", Message: "服务暂不可用", Err: err}
	}
	wakeWatch(service.watch.refresh)
	return RootOperationResult{Response: body}, nil
}

// RestoreRoot 沿用目录操作的校验、事务与幂等收据。
func (service *Service) RestoreRoot(ctx context.Context, operation RootOperation, id string, expectedRevision int64) (RootOperationResult, error) {
	key := strings.TrimSpace(operation.IdempotencyKey)
	if !uuid.Valid(id) {
		return RootOperationResult{}, &RootOperationError{Code: "invalid_id", Message: "目录标识无效"}
	}
	id = strings.ToLower(id)
	if expectedRevision < 1 {
		return RootOperationResult{}, &RootOperationError{Code: "invalid_input", Message: "revision 无效"}
	}
	fp := operationFingerprint(map[string]any{"root_id": id, "expected_revision": expectedRevision})
	tx, replay, err := service.beginRootOperation(ctx, operation.ActorID, "restore", key, fp)
	if err != nil || tx == nil {
		return replay, err
	}
	defer tx.Rollback()
	var path, status string
	var rev int64
	err = tx.QueryRowContext(ctx, "SELECT path,status,revision FROM library_roots WHERE id=$1::uuid FOR UPDATE", id).Scan(&path, &status, &rev)
	if err == sql.ErrNoRows {
		return RootOperationResult{}, &RootOperationError{Code: "not_found", Message: "目录不存在"}
	}
	if err != nil {
		return RootOperationResult{}, &RootOperationError{Code: "database_unavailable", Message: "服务暂不可用", Err: err}
	}
	if rev != expectedRevision || status != "disabled" {
		return RootOperationResult{}, &RootOperationError{Code: "revision_conflict", Message: "目录版本或状态已变化"}
	}
	var beforeStatus string
	// 停用期间允许改路径，因此恢复依据不要求与当前 revision 紧邻。
	err = tx.QueryRowContext(ctx, `SELECT before_state->>'status' FROM library_root_operations WHERE root_id=$1::uuid AND operation_type='disable' AND status='succeeded' ORDER BY created_at DESC LIMIT 1`, id).Scan(&beforeStatus)
	if err != nil || beforeStatus != "active" {
		return RootOperationResult{}, &RootOperationError{Code: "revision_conflict", Message: "找不到可恢复的目录状态"}
	}
	newRev := rev + 1
	if _, err = tx.ExecContext(ctx, "UPDATE library_roots SET status='active',watch_baseline_at=NULL,last_poll_started_at=NULL,revision=$2,updated_at=NOW() WHERE id=$1::uuid", id, newRev); err != nil {
		return RootOperationResult{}, &RootOperationError{Code: "operation_failed", Message: "无法恢复目录", Err: err}
	}
	result := map[string]any{"id": id, "path": filepathBase(path), "status": "active", "revision": newRev}
	body, _ := json.Marshal(result)
	_, err = tx.ExecContext(ctx, `INSERT INTO library_root_operations(id,root_id,actor_id,operation_type,status,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id) VALUES($1,$2::uuid,$3::uuid,'restore','succeeded',$4,$5,$6,$7,jsonb_build_object('status','disabled','revision',$6::bigint),jsonb_build_object('status',$8::text,'revision',$7::bigint),$9::jsonb,$10)`, uuid.New(), id, operation.ActorID, key, fp, rev, newRev, beforeStatus, string(body), operation.RequestID)
	if err != nil {
		return RootOperationResult{}, rootOperationPersistenceError(err)
	}
	if err = tx.Commit(); err != nil {
		return RootOperationResult{}, &RootOperationError{Code: "database_unavailable", Message: "服务暂不可用", Err: err}
	}
	wakeWatch(service.watch.refresh)
	return RootOperationResult{Response: body}, nil
}

// RelocateRoot 沿用目录操作的校验、事务与幂等收据。
func (service *Service) RelocateRoot(ctx context.Context, operation RootOperation, id string, expectedRevision int64, path string, allowedRoots []string) (RootOperationResult, error) {
	key := strings.TrimSpace(operation.IdempotencyKey)
	if !uuid.Valid(id) {
		return RootOperationResult{}, &RootOperationError{Code: "invalid_id", Message: "目录标识无效"}
	}
	id = strings.ToLower(id)
	if key == "" {
		return RootOperationResult{}, &RootOperationError{Code: "invalid_input", Message: "请求需要 Idempotency-Key"}
	}
	if path == "" || expectedRevision < 1 {
		return RootOperationResult{}, &RootOperationError{Code: "invalid_input", Message: "路径或 revision 无效"}
	}
	safePath, err := ValidatePath(path, allowedRoots)
	if err != nil {
		return RootOperationResult{}, &RootOperationError{Code: "invalid_library_path", Message: "目录不在允许的音乐根目录内", Err: err}
	}
	fp := operationFingerprint(map[string]any{"root_id": id, "path": safePath, "expected_revision": expectedRevision})
	tx, replay, err := service.beginRootOperation(ctx, operation.ActorID, "relocate", key, fp)
	if err != nil || tx == nil {
		return replay, err
	}
	defer tx.Rollback()

	acquired, err := TryLockScanCoordination(ctx, tx)
	if err != nil {
		return RootOperationResult{}, &RootOperationError{Code: "database_unavailable", Message: "服务暂不可用", Err: err}
	}
	if !acquired {
		return RootOperationResult{}, &RootOperationError{Code: "scan_running", Message: "正在扫描，稍后再试"}
	}
	var previousPath string
	var rev int64
	err = tx.QueryRowContext(ctx, "SELECT path,revision FROM library_roots WHERE id=$1::uuid FOR UPDATE", id).Scan(&previousPath, &rev)
	if err == sql.ErrNoRows {
		return RootOperationResult{}, &RootOperationError{Code: "not_found", Message: "目录不存在"}
	}
	if err != nil {
		return RootOperationResult{}, &RootOperationError{Code: "database_unavailable", Message: "服务暂不可用", Err: err}
	}
	if rev != expectedRevision {
		return RootOperationResult{}, &RootOperationError{Code: "revision_conflict", Message: "目录版本已变化"}
	}
	var registered bool
	err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM library_roots WHERE path=$1 AND id<>$2::uuid)", safePath, id).Scan(&registered)
	if err != nil {
		return RootOperationResult{}, &RootOperationError{Code: "database_unavailable", Message: "服务暂不可用", Err: err}
	}
	if registered {
		return RootOperationResult{}, &RootOperationError{Code: "path_already_registered", Message: "这个文件夹已经添加过了"}
	}
	newRev := rev
	if safePath != previousPath {
		newRev++
		_, err = tx.ExecContext(ctx, "UPDATE library_roots SET path=$2,watch_baseline_at=NULL,last_poll_started_at=NULL,revision=$3,updated_at=NOW() WHERE id=$1::uuid", id, safePath, newRev)
		if err != nil {
			// 新增目录不持扫描锁；预查后发生的并发登记仍由路径唯一约束裁决。
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "library_roots_path_key" {
				return RootOperationResult{}, &RootOperationError{Code: "path_already_registered", Message: "这个文件夹已经添加过了"}
			}
			return RootOperationResult{}, &RootOperationError{Code: "database_unavailable", Message: "服务暂不可用", Err: err}
		}
	}
	// 在目录行锁之后独立读取排除项，沿用安全 basename 与有序相对路径投影。
	root, err := scanRootResponse(tx.QueryRowContext(ctx, rootResponseQuery+" WHERE r.id=$1::uuid GROUP BY r.id", id))
	if err != nil {
		return RootOperationResult{}, &RootOperationError{Code: "database_unavailable", Message: "服务暂不可用", Err: err}
	}
	result := map[string]any{"id": root.ID, "name": root.Name, "path": root.Path, "status": root.Status, "revision": root.Revision, "excluded_paths": root.ExcludedPaths}
	body, _ := json.Marshal(result)
	_, err = tx.ExecContext(ctx, `INSERT INTO library_root_operations
		(id,root_id,actor_id,operation_type,status,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id)
		VALUES($1,$2::uuid,$3::uuid,'relocate','succeeded',$4,$5,$6,$7,
		jsonb_build_object('path',$8::text,'revision',$6::bigint),jsonb_build_object('path',$9::text,'revision',$7::bigint),$10::jsonb,$11)`,
		uuid.New(), id, operation.ActorID, key, fp, rev, newRev, previousPath, safePath, string(body), operation.RequestID)
	if err != nil {
		return RootOperationResult{}, rootOperationPersistenceError(err)
	}
	if err = tx.Commit(); err != nil {
		return RootOperationResult{}, &RootOperationError{Code: "database_unavailable", Message: "服务暂不可用", Err: err}
	}
	wakeWatch(service.watch.refresh)
	return RootOperationResult{Response: body}, nil
}
