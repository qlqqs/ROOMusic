// Package httpapi 提供管家的设置、探测、会话流与管理员接口。
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/steward/secret"
	"github.com/qlqq/roomusic/backend/internal/steward/token"
)

// Handlers 持有管家接口依赖及本进程内按用户隔离的运行取消登记。
type Handlers struct {
	guard            *identity.Guard
	checkCredentials func(*http.Request, identity.User) (bool, error)
	store            *runtime.Store
	secrets          *secret.Store
	runner           *runtime.Runner
	logger           *slog.Logger
	tokens           *token.Store
	mu               sync.Mutex
	running          map[runKey]context.CancelFunc
}

type runKey struct{ userID, conversationID string }

// New 构造接口，不执行 I/O；依赖由 app 装配并共享。
func New(guard *identity.Guard, store *runtime.Store, secrets *secret.Store, runner *runtime.Runner, logger *slog.Logger) *Handlers {
	return &Handlers{checkCredentials: func(r *http.Request, user identity.User) (bool, error) {
		current, err := guard.Authenticate(r)
		return err == nil && current.ID == user.ID && current.Role == user.Role, nil
	}, guard: guard, store: store, secrets: secrets, runner: runner, logger: logger,
		running: make(map[runKey]context.CancelFunc)}
}

// WithCredentialCheck 注入能区分凭据失效与查询错误的长流复查；查询错误会跳过本次复查。
func (h *Handlers) WithCredentialCheck(check func(*http.Request, identity.User) (bool, error)) *Handlers {
	h.checkCredentials = check
	return h
}

// Register 注册版本化接口，所有写方法均复用身份模块的同源校验。
func (h *Handlers) Register(mux *http.ServeMux) {
	if h.tokens != nil {
		h.registerTokens(mux)
	}
	h.route(mux, "GET /api/v1/me/steward/settings", false, h.getSettings)
	h.route(mux, "PUT /api/v1/me/steward/settings", false, h.putSettings)
	h.route(mux, "POST /api/v1/me/steward/settings/probe", false, h.probe)
	h.route(mux, "GET /api/v1/me/steward/conversations", false, h.listConversations)
	h.route(mux, "POST /api/v1/me/steward/conversations", false, h.createConversation)
	h.route(mux, "GET /api/v1/me/steward/conversations/{id}", false, h.getConversation)
	h.route(mux, "DELETE /api/v1/me/steward/conversations/{id}", false, h.deleteConversation)
	h.route(mux, "PATCH /api/v1/me/steward/conversations/{id}", false, h.renameConversation)
	h.route(mux, "POST /api/v1/me/steward/conversations/{id}/messages", false, h.sendMessage)
	h.route(mux, "POST /api/v1/me/steward/conversations/{id}/plans/{plan_id}/decision", false, h.decidePlan)
	h.route(mux, "POST /api/v1/me/steward/conversations/{id}/continue", false, h.continueConversation)
	h.route(mux, "POST /api/v1/me/steward/conversations/{id}/cancel", false, h.cancelConversation)
	h.route(mux, "GET /api/v1/admin/steward/settings", true, h.getInstanceSettings)
	h.route(mux, "PUT /api/v1/admin/steward/settings", true, h.putInstanceSettings)
	h.route(mux, "GET /api/v1/admin/steward/egress-allowlist", true, h.listAllowlist)
	h.route(mux, "POST /api/v1/admin/steward/egress-allowlist", true, h.addAllowlist)
	h.route(mux, "DELETE /api/v1/admin/steward/egress-allowlist", true, h.deleteAllowlist)
}

func (h *Handlers) route(mux *http.ServeMux, pattern string, admin bool, handler func(http.ResponseWriter, *http.Request, identity.User)) {
	write := !strings.HasPrefix(pattern, "GET ")
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if write && !h.guard.RequireSameOrigin(w, r) {
			return
		}
		user, err := h.guard.Authenticate(r)
		if err != nil {
			h.guard.WriteUnauthenticated(w, r)
			return
		}
		if admin && user.Role != "admin" {
			httpserver.WriteError(w, r, 403, "forbidden", "需要管理员权限")
			return
		}
		if user.Role != "admin" {
			enabled, err := h.store.GetMembersEnabled(r.Context())
			if err != nil {
				h.writeStoreError(w, r, err)
				return
			}
			if !enabled {
				writeProblem(w, r, disabledProblem)
				return
			}
		}
		handler(w, r, user)
	})
}

func (h *Handlers) registerRun(key runKey, cancel context.CancelFunc) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.running[key]; exists {
		return false
	}
	h.running[key] = cancel
	return true
}

func (h *Handlers) unregisterRun(key runKey) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.running, key)
}
