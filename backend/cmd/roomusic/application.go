package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type roomusicApplication struct {
	config      serverConfig
	database    *databaseState
	logger      *slog.Logger
	scanContext context.Context
	scanWorkers sync.WaitGroup
	scanMutex   sync.Mutex
	runningScan string
}
type apiError struct {
	Error     apiErrorDetail `json:"error"`
	RequestID string         `json:"request_id"`
}
type apiErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeAPIError(responseWriter http.ResponseWriter, request *http.Request, status int, code, message string) {
	requestID := responseWriter.Header().Get("X-Request-ID")
	writeJSON(responseWriter, status, apiError{Error: apiErrorDetail{Code: code, Message: message}, RequestID: requestID})
}

func buildApplicationHandler(config serverConfig, database *databaseState, logger *slog.Logger) http.Handler {
	_, handler := buildApplication(context.Background(), config, database, logger)
	return handler
}

func buildApplication(scanContext context.Context, config serverConfig, database *databaseState, logger *slog.Logger) (*roomusicApplication, http.Handler) {
	if scanContext == nil {
		scanContext = context.Background()
	}
	application := &roomusicApplication{config: config, database: database, logger: logger, scanContext: scanContext}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthHandler)
	mux.HandleFunc("GET /readyz", func(responseWriter http.ResponseWriter, request *http.Request) {
		statusCode, status := readinessStatus(database)
		writeJSON(responseWriter, statusCode, map[string]string{"status": status})
	})
	mux.HandleFunc("GET /api/v1/setup/status", application.setupStatus)
	mux.HandleFunc("POST /api/v1/setup/admin", application.setupAdmin)
	mux.HandleFunc("POST /api/v1/auth/login", application.login)
	mux.HandleFunc("POST /api/v1/auth/logout", application.logout)
	mux.HandleFunc("GET /api/v1/auth/me", application.me)
	mux.HandleFunc("GET /api/v1/users", application.listUsers)
	mux.HandleFunc("POST /api/v1/users", application.createUser)
	mux.HandleFunc("PATCH /api/v1/users/{id}", application.updateUser)
	mux.HandleFunc("POST /api/v1/users/{id}/sessions/revoke", application.revokeUserSessions)
	mux.HandleFunc("GET /api/v1/library-roots", application.listRoots)
	mux.HandleFunc("POST /api/v1/library-roots", application.addRoot)
	mux.HandleFunc("PATCH /api/v1/library-roots/{id}", application.updateRoot)
	mux.HandleFunc("POST /api/v1/library-roots/{id}/restore", application.restoreRoot)
	mux.HandleFunc("GET /api/v1/library-root-operations", application.rootOperations)
	mux.HandleFunc("POST /api/v1/scans", application.startScan)
	mux.HandleFunc("GET /api/v1/scans/active", application.activeScan)
	mux.HandleFunc("GET /api/v1/scans/{id}", application.scanStatus)
	mux.HandleFunc("POST /api/v1/scans/{id}/cancel", application.cancelScan)
	mux.HandleFunc("GET /api/v1/scans/{id}/diagnostics", application.diagnostics)
	mux.HandleFunc("GET /api/v1/releases", application.listReleases)
	mux.HandleFunc("GET /api/v1/releases/{id}/evidence", application.releaseEvidence)
	mux.HandleFunc("GET /api/v1/releases/{id}", application.releaseDetail)
	mux.HandleFunc("GET /api/v1/artworks/{id}", application.artworkResource)
	mux.Handle("/", productionFrontendHandler())
	return application, requestIDMiddleware(logger, mux)
}

func (application *roomusicApplication) waitForScans(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		application.scanWorkers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (application *roomusicApplication) setupStatus(responseWriter http.ResponseWriter, request *http.Request) {
	var completed bool
	err := application.database.connection.QueryRowContext(request.Context(), "SELECT EXISTS (SELECT 1 FROM setup_state)").Scan(&completed)
	if err != nil {
		writeAPIError(responseWriter, request, 503, "database_unavailable", "服务暂不可用")
		return
	}
	writeJSON(responseWriter, 200, map[string]bool{"setup_required": !completed})
}

func (application *roomusicApplication) setupAdmin(responseWriter http.ResponseWriter, request *http.Request) {
	if !application.requireSameOrigin(responseWriter, request) {
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(responseWriter, request, &input) || len(input.Password) < 12 || strings.TrimSpace(input.Username) == "" {
		writeAPIError(responseWriter, request, 400, "invalid_input", "用户名不能为空，密码至少需要 12 个字符")
		return
	}
	passwordHash, err := hashPassword(input.Password)
	if err != nil {
		writeAPIError(responseWriter, request, 500, "internal_error", "无法创建管理员")
		return
	}
	tx, err := application.database.connection.BeginTx(request.Context(), nil)
	if err != nil {
		writeAPIError(responseWriter, request, 503, "database_unavailable", "服务暂不可用")
		return
	}
	defer tx.Rollback()
	var setupExists bool
	if err = tx.QueryRowContext(request.Context(), "SELECT EXISTS (SELECT 1 FROM setup_state)").Scan(&setupExists); err != nil || setupExists {
		writeAPIError(responseWriter, request, 409, "setup_closed", "管理员初始化已完成")
		return
	}
	userID := createIdentifier()
	if _, err = tx.ExecContext(request.Context(), "INSERT INTO users (id, username, password_hash, role) VALUES ($1::uuid,$2,$3,'admin')", userID, input.Username, passwordHash); err != nil {
		writeAPIError(responseWriter, request, 409, "setup_closed", "管理员初始化已完成")
		return
	}
	if _, err = tx.ExecContext(request.Context(), "INSERT INTO setup_state (completed_at) VALUES (NOW())"); err != nil {
		writeAPIError(responseWriter, request, 409, "setup_closed", "管理员初始化已完成")
		return
	}
	if err = tx.Commit(); err != nil {
		writeAPIError(responseWriter, request, 409, "setup_closed", "管理员初始化已完成")
		return
	}
	writeJSON(responseWriter, 201, map[string]string{"username": input.Username, "role": "admin"})
}

func decodeJSON(responseWriter http.ResponseWriter, request *http.Request, destination any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(responseWriter, request.Body, 1<<20)).Decode(destination); err != nil {
		writeAPIError(responseWriter, request, 400, "invalid_json", "请求格式无效")
		return false
	}
	return true
}

func (application *roomusicApplication) login(responseWriter http.ResponseWriter, request *http.Request) {
	if !application.requireSameOrigin(responseWriter, request) {
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(responseWriter, request, &input) {
		return
	}
	var userID, passwordHash, role string
	err := application.database.connection.QueryRowContext(request.Context(), "SELECT id::text,password_hash,role FROM users WHERE username=$1 AND disabled_at IS NULL", input.Username).Scan(&userID, &passwordHash, &role)
	if err != nil || !verifyPassword(passwordHash, input.Password) {
		writeAPIError(responseWriter, request, 401, "invalid_credentials", "用户名或密码错误")
		return
	}
	token := generateSessionToken()
	_, err = application.database.connection.ExecContext(request.Context(), "INSERT INTO sessions (token_hash,user_id,expires_at) VALUES ($1,$2::uuid,NOW()+INTERVAL '24 hours')", hashSessionToken(token), userID)
	if err != nil {
		writeAPIError(responseWriter, request, 503, "database_unavailable", "服务暂不可用")
		return
	}
	application.setSessionCookie(responseWriter, token)
	writeJSON(responseWriter, 200, map[string]string{"username": input.Username, "role": role})
}

func (application *roomusicApplication) logout(responseWriter http.ResponseWriter, request *http.Request) {
	if !application.requireSameOrigin(responseWriter, request) {
		return
	}
	if cookie, err := request.Cookie(sessionCookieName); err == nil {
		_, _ = application.database.connection.ExecContext(request.Context(), "UPDATE sessions SET revoked_at=NOW() WHERE token_hash=$1", hashSessionToken(cookie.Value))
	}
	http.SetCookie(responseWriter, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true, Secure: application.config.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	writeJSON(responseWriter, 200, map[string]bool{"logged_out": true})
}
func (application *roomusicApplication) me(responseWriter http.ResponseWriter, request *http.Request) {
	user, err := application.currentUser(request)
	if err != nil {
		application.writeAuthenticationError(responseWriter, request)
		return
	}
	writeJSON(responseWriter, 200, map[string]string{"username": user.Username, "role": user.Role})
}

func (application *roomusicApplication) listRoots(responseWriter http.ResponseWriter, request *http.Request) {
	if _, ok := application.requireAdmin(responseWriter, request); !ok {
		return
	}
	rows, err := application.database.connection.QueryContext(request.Context(), "SELECT id::text,path,status,revision,created_at,updated_at FROM library_roots ORDER BY path")
	if err != nil {
		writeAPIError(responseWriter, request, 503, "database_unavailable", "服务暂不可用")
		return
	}
	defer rows.Close()
	roots := []map[string]any{}
	for rows.Next() {
		var id, path, status string
		var revision int64
		var created, updated time.Time
		if scanErr := rows.Scan(&id, &path, &status, &revision, &created, &updated); scanErr != nil {
			writeAPIError(responseWriter, request, 503, "database_error", "无法读取目录")
			return
		}
		roots = append(roots, map[string]any{"id": id, "path": filepath.Base(path), "name": filepath.Base(path), "status": status, "revision": revision, "created_at": created, "updated_at": updated})
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		writeAPIError(responseWriter, request, 503, "database_error", "无法读取目录")
		return
	}
	writeJSON(responseWriter, 200, map[string]any{"items": roots})
}

func (application *roomusicApplication) addRoot(responseWriter http.ResponseWriter, request *http.Request) {
	if !application.requireSameOrigin(responseWriter, request) {
		return
	}
	if _, ok := application.requireAdmin(responseWriter, request); !ok {
		return
	}
	actor, _ := application.currentUser(request)
	key := operationKey(request)
	var input struct {
		Path string `json:"path"`
	}
	if !decodeJSON(responseWriter, request, &input) {
		return
	}
	safePath, err := validateLibraryPath(input.Path, application.config.AllowedLibraryRoots)
	if err != nil {
		writeAPIError(responseWriter, request, 400, "invalid_library_path", "目录不在允许的音乐根目录内")
		return
	}
	fingerprint := operationFingerprint(map[string]any{"path": safePath})
	if done, _ := application.replayOperation(responseWriter, request, actor.ID, "create", key, fingerprint); done {
		return
	}
	tx, err := application.database.connection.BeginTx(request.Context(), nil)
	if err != nil {
		writeAPIError(responseWriter, request, 503, "database_unavailable", "服务暂不可用")
		return
	}
	defer tx.Rollback()
	id := createIdentifier()
	var createdID string
	var rev int64
	var status string
	err = tx.QueryRowContext(request.Context(), "INSERT INTO library_roots(id,path) VALUES($1::uuid,$2) ON CONFLICT(path) DO UPDATE SET path=EXCLUDED.path RETURNING id::text,revision,status", id, safePath).Scan(&createdID, &rev, &status)
	if err != nil {
		writeAPIError(responseWriter, request, 500, "database_error", "无法注册目录")
		return
	}
	result := map[string]any{"id": createdID, "name": filepath.Base(safePath), "status": status, "revision": rev}
	body, _ := json.Marshal(result)
	_, err = tx.ExecContext(request.Context(), `INSERT INTO library_root_operations(id,root_id,actor_id,operation_type,status,idempotency_key,fingerprint,result_revision,before_state,after_state,response,request_id) VALUES($1,$2::uuid,$3::uuid,'create','succeeded',$4,$5,$6,'{}',jsonb_build_object('status',$9::text,'revision',$6::bigint),$7::jsonb,$8)`, createIdentifier(), createdID, actor.ID, key, fingerprint, rev, string(body), request.Header.Get("X-Request-ID"), status)
	if err != nil {
		application.writeRootOperationPersistenceError(responseWriter, request, err)
		return
	}
	if err = tx.Commit(); err != nil {
		writeAPIError(responseWriter, request, 503, "database_unavailable", "服务暂不可用")
		return
	}
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(201)
	_, _ = responseWriter.Write(body)
}

func validateLibraryPath(input string, allowedRoots []string) (string, error) {
	clean, err := filepath.Abs(filepath.Clean(input))
	if err != nil {
		return "", err
	}
	// Registration must not silently follow a directory symlink. The lexical
	// path and its real path differ whenever any component is a symlink.
	real, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", err
	}
	if filepath.Clean(real) != clean {
		return "", fmt.Errorf("directory symlink is not allowed")
	}
	info, err := os.Stat(real)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("not directory")
	}
	for _, allowed := range allowedRoots {
		allowedReal, evalErr := filepath.EvalSymlinks(allowed)
		if evalErr == nil && (real == allowedReal || strings.HasPrefix(real, allowedReal+string(filepath.Separator))) {
			return real, nil
		}
	}
	return "", fmt.Errorf("outside allowlist")
}

func (application *roomusicApplication) artworkResource(w http.ResponseWriter, r *http.Request) {
	if _, err := application.authenticatedUser(r); err != nil {
		application.writeAuthenticationError(w, r)
		return
	}
	var key, mime string
	if err := application.database.connection.QueryRowContext(r.Context(), "SELECT storage_key,mime_type FROM release_artworks WHERE storage_key=$1", r.PathValue("id")).Scan(&key, &mime); err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "封面不存在")
		return
	}
	if filepath.Base(key) != key {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "封面不存在")
		return
	}
	b, err := os.ReadFile(filepath.Join(application.config.DataDirectory, "artwork", key))
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not_found", "封面不存在")
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

func (application *roomusicApplication) listUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := application.requireAdmin(w, r); !ok {
		return
	}
	rows, err := application.database.connection.QueryContext(r.Context(), "SELECT id::text,username,role,disabled_at,created_at FROM users ORDER BY username")
	if err != nil {
		writeAPIError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, username, role string
		var disabled sql.NullTime
		var created time.Time
		if rows.Scan(&id, &username, &role, &disabled, &created) != nil {
			continue
		}
		items = append(items, map[string]any{"id": id, "username": username, "role": role, "disabled": disabled.Valid, "created_at": created})
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (application *roomusicApplication) createUser(w http.ResponseWriter, r *http.Request) {
	if !application.requireSameOrigin(w, r) {
		return
	}
	if _, ok := application.requireAdmin(w, r); !ok {
		return
	}
	var in struct{ Username, Password string }
	if !decodeJSON(w, r, &in) || strings.TrimSpace(in.Username) == "" || len(in.Password) < 12 {
		writeAPIError(w, r, 400, "invalid_input", "用户名不能为空，密码至少需要 12 个字符")
		return
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		writeAPIError(w, r, 500, "internal_error", "无法创建用户")
		return
	}
	id := createIdentifier()
	_, err = application.database.connection.ExecContext(r.Context(), "INSERT INTO users(id,username,password_hash,role) VALUES($1::uuid,$2,$3,'user')", id, in.Username, hash)
	if err != nil {
		writeAPIError(w, r, 409, "username_taken", "用户名已存在")
		return
	}
	writeJSON(w, 201, map[string]string{"id": id, "username": in.Username, "role": "user"})
}
func (application *roomusicApplication) updateUser(w http.ResponseWriter, r *http.Request) {
	if !application.requireSameOrigin(w, r) {
		return
	}
	_, ok := application.requireAdmin(w, r)
	if !ok {
		return
	}
	var in struct {
		Disabled *bool `json:"disabled"`
	}
	if !decodeJSON(w, r, &in) || in.Disabled == nil {
		writeAPIError(w, r, 400, "invalid_input", "缺少禁用状态")
		return
	}
	id := r.PathValue("id")
	tx, err := application.database.connection.BeginTx(r.Context(), nil)
	if err != nil {
		writeAPIError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	defer tx.Rollback()
	// Lock administrator rows first so concurrent last-admin checks share one order.
	adminRows, queryErr := tx.QueryContext(r.Context(), "SELECT id FROM users WHERE role='admin' AND disabled_at IS NULL FOR UPDATE")
	if queryErr != nil {
		writeAPIError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	admins := 0
	for adminRows.Next() {
		admins++
	}
	rowsErr := adminRows.Err()
	adminRows.Close()
	if rowsErr != nil {
		writeAPIError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	var role string
	var currentlyDisabled bool
	err = tx.QueryRowContext(r.Context(), "SELECT role, disabled_at IS NOT NULL FROM users WHERE id=$1::uuid FOR UPDATE", id).Scan(&role, &currentlyDisabled)
	if err == sql.ErrNoRows {
		writeAPIError(w, r, 404, "not_found", "用户不存在")
		return
	}
	if err != nil {
		writeAPIError(w, r, 503, "database_unavailable", "服务暂不可用")
		return
	}
	if *in.Disabled && !currentlyDisabled && role == "admin" {
		if admins <= 1 {
			writeAPIError(w, r, 409, "last_admin", "不能禁用最后一个管理员")
			return
		}
	}
	if *in.Disabled {
		var result sql.Result
		result, err = tx.ExecContext(r.Context(), "UPDATE users SET disabled_at=NOW() WHERE id=$1::uuid", id)
		if err == nil {
			if affected, affectedErr := result.RowsAffected(); affectedErr != nil {
				err = affectedErr
			} else if affected != 1 {
				err = sql.ErrNoRows
			}
		}
	} else {
		var result sql.Result
		result, err = tx.ExecContext(r.Context(), "UPDATE users SET disabled_at=NULL WHERE id=$1::uuid", id)
		if err == nil {
			if affected, affectedErr := result.RowsAffected(); affectedErr != nil {
				err = affectedErr
			} else if affected != 1 {
				err = sql.ErrNoRows
			}
		}
	}
	if err != nil {
		if err == sql.ErrNoRows {
			writeAPIError(w, r, 404, "not_found", "用户不存在")
			return
		}
		writeAPIError(w, r, 503, "database_unavailable", "无法更新用户")
		return
	}
	if *in.Disabled {
		if _, err = tx.ExecContext(r.Context(), "UPDATE sessions SET revoked_at=NOW() WHERE user_id=$1::uuid AND revoked_at IS NULL", id); err != nil {
			writeAPIError(w, r, 503, "database_unavailable", "无法撤销会话")
			return
		}
	}
	if err = tx.Commit(); err != nil {
		writeAPIError(w, r, 503, "database_unavailable", "无法更新用户")
		return
	}
	writeJSON(w, 200, map[string]bool{"disabled": *in.Disabled})
}
func (application *roomusicApplication) revokeUserSessions(w http.ResponseWriter, r *http.Request) {
	if !application.requireSameOrigin(w, r) {
		return
	}
	if _, ok := application.requireAdmin(w, r); !ok {
		return
	}
	_, err := application.database.connection.ExecContext(r.Context(), "UPDATE sessions SET revoked_at=NOW() WHERE user_id=$1::uuid AND revoked_at IS NULL", r.PathValue("id"))
	if err != nil {
		writeAPIError(w, r, 503, "database_unavailable", "无法撤销会话")
		return
	}
	writeJSON(w, 200, map[string]bool{"revoked": true})
}
