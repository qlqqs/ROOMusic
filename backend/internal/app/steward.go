package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	governancetools "github.com/qlqq/roomusic/backend/internal/catalog/governance/stewardtools"
	catalogtools "github.com/qlqq/roomusic/backend/internal/catalog/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/library"
	librarytools "github.com/qlqq/roomusic/backend/internal/library/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/listening"
	listeningtools "github.com/qlqq/roomusic/backend/internal/listening/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/operations"
	operationtools "github.com/qlqq/roomusic/backend/internal/operations/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/personal"
	personaltools "github.com/qlqq/roomusic/backend/internal/personal/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	stewardapi "github.com/qlqq/roomusic/backend/internal/steward/httpapi"
	stewardmcp "github.com/qlqq/roomusic/backend/internal/steward/mcp"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/steward/secret"
	"github.com/qlqq/roomusic/backend/internal/steward/token"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
	"github.com/qlqq/roomusic/backend/internal/zones"
	zonestools "github.com/qlqq/roomusic/backend/internal/zones/stewardtools"
)

// registerSteward 在接受请求前恢复会话状态；失败时记录日志并让管家接口返回 503。
func registerSteward(lifecycle context.Context, mux *http.ServeMux, database *sql.DB, guard *identity.Guard, dataDirectory string, logger *slog.Logger, scans *library.Service, kinds []operations.Kind, playback *zones.Service, history *listening.Service, personalLibrary *personal.Service) {
	store := runtime.NewStore(database).WithOperationLookup(func(ctx context.Context, tx *sql.Tx, userID, key string) (runtime.RecordedOperation, error) {
		result, err := operations.LookupResult(ctx, tx, userID, key)
		if errors.Is(err, operations.ErrNotFound) {
			return runtime.RecordedOperation{}, nil
		}
		return runtime.RecordedOperation{ID: result.OperationID, Output: result.Response}, err
	})
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
			mux.HandleFunc("/api/v1/mcp", unavailable)
			return
		}
		logger.Info("管家会话状态已恢复", "event", "steward.recovery.completed", "message", "管家会话状态已恢复", "module", "steward", "conversations", count)
	}
	secrets := secret.New(filepath.Join(dataDirectory, "steward", "instance.key"))
	registry := tool.NewRegistry()
	definitions := catalogtools.StewardTools(catalog.NewStore(database), governance.NewStore(database))
	definitions = append(definitions, governancetools.StewardTools(database, artistResolutionAdapter{})...)
	definitions = append(definitions, operationtools.RevertTools(database, kinds...)...)
	definitions = append(definitions, librarytools.StewardTools(scans)...)
	definitions = append(definitions, operationtools.StewardTools(database, kinds...)...)
	definitions = append(definitions, zonestools.StewardTools(playback, catalog.NewStore(database))...)
	definitions = append(definitions, listeningtools.StewardTools(history)...)
	definitions = append(definitions, personaltools.StewardTools(personalLibrary)...)
	for _, definition := range definitions {
		if definition.Scope == tool.ScopeShared {
			handler := definition.Handler
			definition.Handler = func(ctx context.Context, caller tool.Caller, input json.RawMessage) (json.RawMessage, error) {
				if caller.ReviewID != "" {
					ctx = operations.WithReviewOrigin(ctx, caller.ReviewID, caller.ReviewDecision)
				}
				return handler(ctx, caller, input)
			}
		}
		if err := registry.Register(definition); err != nil {
			// 工具合同是编译期装配；重复名称或非法 schema 属于程序错误。
			panic(err)
		}
	}
	runner := runtime.NewRunner(store, registry, logger).WithReviewEvidence(func(ctx context.Context, caller tool.Caller, item runtime.PlanItem) (runtime.ReviewEvidenceFacts, error) {
		if !catalogtools.SupportsReviewEvidence(item.Tool) {
			return runtime.ReviewEvidenceFacts{}, runtime.ErrReviewEvidenceUnsupported
		}
		targets, err := catalogtools.ReadReviewTargets(ctx, catalog.NewStore(database), governance.NewStore(database), item.Input)
		if err != nil {
			return runtime.ReviewEvidenceFacts{}, err
		}
		filters := make([]operations.Target, 0, len(targets))
		for _, target := range targets {
			filters = append(filters, operations.Target{Type: target.Type, ID: target.ID})
		}
		if len(filters) == 0 {
			return runtime.ReviewEvidenceFacts{}, runtime.ErrReviewUnavailable
		}
		// 最近操作限定为公开列表第一页的 20 条；不外发操作正文或操作者。
		page, err := operations.List(ctx, database, operations.ListParams{Targets: filters, Page: 1, PageSize: 20})
		if err != nil {
			return runtime.ReviewEvidenceFacts{}, err
		}
		raw, err := json.Marshal(targets)
		return runtime.ReviewEvidenceFacts{Targets: raw, RecentOperationCount: len(page.Items)}, err
	})
	tokens := token.NewStore(database, guard)
	mux.Handle("/api/v1/mcp", stewardmcp.New(guard, tokens, store, registry, logger))
	stewardapi.New(guard, store, secrets, runner, logger).WithTokens(tokens).WithCredentialCheck(func(r *http.Request, user identity.User) (bool, error) {
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
