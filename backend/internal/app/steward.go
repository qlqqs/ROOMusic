package app

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	catalogtools "github.com/qlqq/roomusic/backend/internal/catalog/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/library"
	librarytools "github.com/qlqq/roomusic/backend/internal/library/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/operations"
	operationtools "github.com/qlqq/roomusic/backend/internal/operations/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	stewardapi "github.com/qlqq/roomusic/backend/internal/steward/httpapi"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/steward/secret"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

// registerSteward 在接受请求前恢复会话状态；失败时记录日志并让管家接口返回 503。
func registerSteward(lifecycle context.Context, mux *http.ServeMux, database *sql.DB, guard *identity.Guard, dataDirectory string, logger *slog.Logger, scans *library.Service, kinds []operations.Kind) {
	store := runtime.NewStore(database)
	if database != nil {
		ctx, cancel := context.WithTimeout(lifecycle, 10*time.Second)
		count, err := store.ResetRunningConversations(ctx)
		cancel()
		if err != nil {
			logger.Error("管家会话状态恢复失败", "event", "steward.recovery.failed", "message", "管家会话状态恢复失败", "module", "steward")
			unavailable := func(w http.ResponseWriter, r *http.Request) {
				httpserver.WriteError(w, r, http.StatusServiceUnavailable, "database_unavailable", "管家会话状态恢复失败")
			}
			for _, prefix := range []string{"/api/v1/me/steward", "/api/v1/admin/steward"} {
				mux.HandleFunc(prefix, unavailable)
				mux.HandleFunc(prefix+"/", unavailable)
			}
			return
		}
		logger.Info("管家会话状态已恢复", "event", "steward.recovery.completed", "message", "管家会话状态已恢复", "module", "steward", "conversations", count)
	}
	secrets := secret.New(filepath.Join(dataDirectory, "steward", "instance.key"))
	registry := tool.NewRegistry()
	definitions := catalogtools.StewardTools(catalog.NewStore(database), governance.NewStore(database))
	definitions = append(definitions, librarytools.StewardTools(scans)...)
	definitions = append(definitions, operationtools.StewardTools(database, kinds...)...)
	for _, definition := range definitions {
		if err := registry.Register(definition); err != nil {
			// 工具合同是编译期装配；重复名称或非法 schema 属于程序错误。
			panic(err)
		}
	}
	runner := runtime.NewRunner(store, registry, logger)
	stewardapi.New(guard, store, secrets, runner, logger).WithCredentialCheck(func(r *http.Request, user identity.User) (bool, error) {
		cookie, err := r.Cookie(identity.SessionCookieName)
		if err != nil || cookie.Value == "" {
			return false, nil
		}
		// 现有 Guard 会丢弃查询错误分类；长流复查保留错误，以便数据库暂时不可用时下次再查。
		var valid bool
		err = database.QueryRowContext(r.Context(), `SELECT EXISTS (
 SELECT 1 FROM sessions s JOIN users u ON u.id=s.user_id
 WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at > NOW()
 AND u.disabled_at IS NULL AND u.id=$2::uuid AND u.role=$3)`, identity.HashSessionToken(cookie.Value), user.ID, user.Role).Scan(&valid)
		return valid, err
	}).Register(mux)
}
