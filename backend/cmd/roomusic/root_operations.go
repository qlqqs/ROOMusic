package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

func (a *roomusicApplication) writeRootOperationPersistenceError(w http.ResponseWriter, r *http.Request, err error) {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		writeAPIError(w, r, http.StatusConflict, "idempotency_conflict", "幂等键已用于其他请求")
		return
	}
	a.logger.Error("record root operation", "request_id", r.Header.Get("X-Request-ID"), "error", err)
	writeAPIError(w, r, http.StatusInternalServerError, "operation_failed", "目录操作未能完成")
}

func operationKey(r *http.Request) string { return strings.TrimSpace(r.Header.Get("Idempotency-Key")) }

func operationFingerprint(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (a *roomusicApplication) replayOperation(w http.ResponseWriter, r *http.Request, actor, typ, key, fingerprint string) (bool, int) {
	if key == "" {
		writeAPIError(w, r, 400, "invalid_input", "请求需要 Idempotency-Key")
		return true, 0
	}
	var oldFingerprint string
	var response []byte
	var status string
	err := a.database.connection.QueryRowContext(r.Context(), `SELECT fingerprint,response,status FROM library_root_operations WHERE actor_id=$1::uuid AND operation_type=$2 AND idempotency_key=$3`, actor, typ, key).Scan(&oldFingerprint, &response, &status)
	if err == sql.ErrNoRows {
		return false, 0
	}
	if err != nil {
		writeAPIError(w, r, 503, "database_unavailable", "服务暂不可用")
		return true, 0
	}
	if oldFingerprint != fingerprint {
		writeAPIError(w, r, 409, "idempotency_conflict", "幂等键已用于其他请求")
		return true, 0
	}
	code := http.StatusOK
	if status == "succeeded" && typ == "create" {
		code = http.StatusCreated
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(response)
	return true, code
}

func (a *roomusicApplication) listRootsGoverned(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	rows, err := a.database.connection.QueryContext(r.Context(), "SELECT id::text,path,status,revision,created_at,updated_at FROM library_roots ORDER BY path")
	if err != nil {
		writeAPIError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, path, status string
		var rev int64
		var created, updated interface{}
		if err := rows.Scan(&id, &path, &status, &rev, &created, &updated); err != nil {
			writeAPIError(w, r, 503, "database_error", "无法读取目录")
			return
		}
		items = append(items, map[string]any{"id": id, "path": filepathBase(path), "status": status, "revision": rev, "created_at": created, "updated_at": updated})
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func filepathBase(path string) string {
	i := strings.LastIndexAny(path, "/\\")
	if i >= 0 {
		return path[i+1:]
	}
	return path
}

func (a *roomusicApplication) rootOperations(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	rows, err := a.database.connection.QueryContext(r.Context(), `SELECT id::text,root_id::text,operation_type,status,result_revision,error_code,created_at FROM library_root_operations ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		writeAPIError(w, r, 503, "database_unavailable", "服务暂不可用")
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
			writeAPIError(w, r, 503, "database_error", "无法读取操作历史")
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
	writeJSON(w, 200, map[string]any{"items": items})
}

func (a *roomusicApplication) updateRoot(w http.ResponseWriter, r *http.Request) {
	if !a.requireSameOrigin(w, r) {
		return
	}
	actor, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	key := operationKey(r)
	var in struct {
		Status           string `json:"status"`
		ExpectedRevision int64  `json:"expected_revision"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Status != "disabled" || in.ExpectedRevision < 1 {
		writeAPIError(w, r, 400, "invalid_input", "状态或 revision 无效")
		return
	}
	id := r.PathValue("id")
	fp := operationFingerprint(map[string]any{"status": in.Status, "expected_revision": in.ExpectedRevision})
	if done, _ := a.replayOperation(w, r, actor.ID, "disable", key, fp); done {
		return
	}
	tx, err := a.database.connection.BeginTx(r.Context(), nil)
	if err != nil {
		writeAPIError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	defer tx.Rollback()
	var path, status string
	var rev int64
	err = tx.QueryRowContext(r.Context(), "SELECT path,status,revision FROM library_roots WHERE id=$1::uuid FOR UPDATE", id).Scan(&path, &status, &rev)
	if err == sql.ErrNoRows {
		writeAPIError(w, r, 404, "not_found", "目录不存在")
		return
	}
	if err != nil {
		writeAPIError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	if rev != in.ExpectedRevision {
		writeAPIError(w, r, 409, "revision_conflict", "目录版本已变化")
		return
	}
	if status != "active" {
		writeAPIError(w, r, 409, "revision_conflict", "目录状态已变化")
		return
	}
	newRev := rev + 1
	_, err = tx.ExecContext(r.Context(), "UPDATE library_roots SET status='disabled',revision=$2,updated_at=NOW() WHERE id=$1::uuid", id, newRev)
	if err != nil {
		writeAPIError(w, r, 500, "operation_failed", "无法停用目录")
		return
	}
	result := map[string]any{"id": id, "path": filepathBase(path), "status": "disabled", "revision": newRev}
	body, _ := json.Marshal(result)
	_, err = tx.ExecContext(r.Context(), `INSERT INTO library_root_operations(id,root_id,actor_id,operation_type,status,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id) VALUES($1,$2::uuid,$3::uuid,'disable','succeeded',$4,$5,$6,$7,jsonb_build_object('status',$8::text,'revision',$6::bigint),jsonb_build_object('status','disabled','revision',$7::bigint),$9::jsonb,$10)`, createIdentifier(), id, actor.ID, key, fp, rev, newRev, status, string(body), r.Header.Get("X-Request-ID"))
	if err != nil {
		a.writeRootOperationPersistenceError(w, r, err)
		return
	}
	if err = tx.Commit(); err != nil {
		writeAPIError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	writeJSON(w, 200, result)
}

func (a *roomusicApplication) restoreRoot(w http.ResponseWriter, r *http.Request) {
	if !a.requireSameOrigin(w, r) {
		return
	}
	actor, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	key := operationKey(r)
	var in struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.ExpectedRevision < 1 {
		writeAPIError(w, r, 400, "invalid_input", "revision 无效")
		return
	}
	id := r.PathValue("id")
	fp := operationFingerprint(map[string]any{"expected_revision": in.ExpectedRevision})
	if done, _ := a.replayOperation(w, r, actor.ID, "restore", key, fp); done {
		return
	}
	tx, err := a.database.connection.BeginTx(r.Context(), nil)
	if err != nil {
		writeAPIError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	defer tx.Rollback()
	var path, status string
	var rev int64
	err = tx.QueryRowContext(r.Context(), "SELECT path,status,revision FROM library_roots WHERE id=$1::uuid FOR UPDATE", id).Scan(&path, &status, &rev)
	if err == sql.ErrNoRows {
		writeAPIError(w, r, 404, "not_found", "目录不存在")
		return
	}
	if err != nil {
		writeAPIError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	if rev != in.ExpectedRevision || status != "disabled" {
		writeAPIError(w, r, 409, "revision_conflict", "目录版本或状态已变化")
		return
	}
	var beforeStatus string
	var beforeRevision int64
	err = tx.QueryRowContext(r.Context(), `SELECT before_state->>'status',(before_state->>'revision')::bigint FROM library_root_operations WHERE root_id=$1::uuid AND operation_type='disable' AND status='succeeded' AND result_revision=$2 ORDER BY created_at DESC LIMIT 1`, id, rev).Scan(&beforeStatus, &beforeRevision)
	if err != nil || beforeStatus != "active" || beforeRevision != rev-1 {
		writeAPIError(w, r, 409, "revision_conflict", "找不到可恢复的目录状态")
		return
	}
	newRev := rev + 1
	if _, err = tx.ExecContext(r.Context(), "UPDATE library_roots SET status='active',revision=$2,updated_at=NOW() WHERE id=$1::uuid", id, newRev); err != nil {
		writeAPIError(w, r, 500, "operation_failed", "无法恢复目录")
		return
	}
	result := map[string]any{"id": id, "path": filepathBase(path), "status": "active", "revision": newRev}
	body, _ := json.Marshal(result)
	_, err = tx.ExecContext(r.Context(), `INSERT INTO library_root_operations(id,root_id,actor_id,operation_type,status,idempotency_key,fingerprint,expected_revision,result_revision,before_state,after_state,response,request_id) VALUES($1,$2::uuid,$3::uuid,'restore','succeeded',$4,$5,$6,$7,jsonb_build_object('status','disabled','revision',$6::bigint),jsonb_build_object('status',$8::text,'revision',$7::bigint),$9::jsonb,$10)`, createIdentifier(), id, actor.ID, key, fp, rev, newRev, beforeStatus, string(body), r.Header.Get("X-Request-ID"))
	if err != nil {
		a.writeRootOperationPersistenceError(w, r, err)
		return
	}
	if err = tx.Commit(); err != nil {
		writeAPIError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	writeJSON(w, 200, result)
}
