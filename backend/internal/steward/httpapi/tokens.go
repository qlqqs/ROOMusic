package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/token"
)

// WithTokens 注入个人访问令牌存储，由 app 装配。
func (h *Handlers) WithTokens(store *token.Store) *Handlers {
	h.tokens = store
	return h
}

type tokenResponse struct {
	// ID 是令牌标识，不是认证凭据。
	ID string `json:"id"`
	// Name 是用户填写的名称。
	Name string `json:"name"`
	// Scope 是 read 或 read_write。
	Scope token.Scope `json:"scope"`
	// Tail 是明文末四位。
	Tail string `json:"tail"`
	// CreatedAt 是签发时间。
	CreatedAt time.Time `json:"created_at"`
	// ExpiresAt 为空表示不过期。
	ExpiresAt *time.Time `json:"expires_at"`
	// LastUsedAt 为空表示尚未使用。
	LastUsedAt *time.Time `json:"last_used_at"`
}

func tokenView(entry token.Entry) tokenResponse {
	return tokenResponse{ID: entry.ID, Name: entry.Name, Scope: entry.Scope, Tail: entry.Tail,
		CreatedAt: entry.CreatedAt, ExpiresAt: entry.ExpiresAt, LastUsedAt: entry.LastUsedAt}
}

func (h *Handlers) registerTokens(mux *http.ServeMux) {
	h.tokenRoute(mux, "GET /api/v1/me/steward/tokens", h.listTokens)
	h.tokenRoute(mux, "POST /api/v1/me/steward/tokens", h.issueToken)
	h.tokenRoute(mux, "DELETE /api/v1/me/steward/tokens/{id}", h.revokeToken)
}

func (h *Handlers) tokenRoute(mux *http.ServeMux, pattern string, handler func(http.ResponseWriter, *http.Request, identity.User)) {
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
		// 成员开关关闭后仍可查看及吊销已有令牌，签发时才拒绝。
		handler(w, r, user)
	})
}

func (h *Handlers) tokensAvailable(w http.ResponseWriter, r *http.Request, user identity.User) (bool, bool) {
	if user.Role == "admin" {
		return true, true
	}
	enabled, err := h.store.GetMembersEnabled(r.Context())
	if err != nil {
		h.writeStoreError(w, r, err)
		return false, false
	}
	return enabled, true
}

func (h *Handlers) listTokens(w http.ResponseWriter, r *http.Request, user identity.User) {
	available, ok := h.tokensAvailable(w, r, user)
	if !ok {
		return
	}
	entries, err := h.tokens.List(r.Context(), user.ID)
	if err != nil {
		h.writeTokenError(w, r, err)
		return
	}
	tokens := make([]tokenResponse, 0, len(entries))
	for _, entry := range entries {
		tokens = append(tokens, tokenView(entry))
	}
	httpserver.WriteJSON(w, http.StatusOK, struct {
		// Available 表示当前用户能否使用 MCP 和签发令牌。
		Available bool `json:"available"`
		// Tokens 仅包含未吊销的本人令牌。
		Tokens []tokenResponse `json:"tokens"`
	}{available, tokens})
}

func (h *Handlers) issueToken(w http.ResponseWriter, r *http.Request, user identity.User) {
	available, ok := h.tokensAvailable(w, r, user)
	if !ok {
		return
	}
	if !available {
		writeProblem(w, r, disabledProblem)
		return
	}
	var input struct {
		Name      string      `json:"name"`
		Scope     token.Scope `json:"scope"`
		ExpiresAt *time.Time  `json:"expires_at"`
	}
	if !decodeInput(w, r, &input) {
		return
	}
	entry, plaintext, err := h.tokens.Issue(r.Context(), user.ID, token.IssueInput{Name: input.Name, Scope: input.Scope, ExpiresAt: input.ExpiresAt})
	if err != nil {
		h.writeTokenError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, struct {
		tokenResponse
		// Token 只在本次签发响应里返回。
		Token string `json:"token"`
	}{tokenView(entry), plaintext})
}

func (h *Handlers) revokeToken(w http.ResponseWriter, r *http.Request, user identity.User) {
	id := r.PathValue("id")
	if !uuid.Valid(id) {
		httpserver.WriteError(w, r, 400, "invalid_id", "令牌标识无效")
		return
	}
	if err := h.tokens.Revoke(r.Context(), user.ID, strings.ToLower(id)); err != nil {
		h.writeTokenError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) writeTokenError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, token.ErrInvalidInput):
		invalidInput(w, r)
	case errors.Is(err, token.ErrLimitReached):
		httpserver.WriteError(w, r, 409, "token_limit_reached", "未吊销的访问令牌最多 20 个")
	case errors.Is(err, token.ErrNotFound):
		httpserver.WriteError(w, r, 404, "not_found", "令牌不存在")
	default:
		h.logFailure(r, databaseProblem.code)
		writeProblem(w, r, databaseProblem)
	}
}
