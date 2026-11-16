// Package mcp 将个人访问令牌和管家工具合同适配为无状态 MCP HTTP 服务。
package mcp

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/steward/token"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

// TokenValidator 只校验 MCP 凭据，不接受登录会话。
type TokenValidator interface {
	Validate(context.Context, string) (token.Principal, error)
}

// MemberSettings 提供本次请求的成员开关。
type MemberSettings interface {
	GetMembersEnabled(context.Context) (bool, error)
}

// Handler 在 SDK 之前执行认证、失败限速及来源检查。
type Handler struct {
	guard     *identity.Guard
	tokens    TokenValidator
	settings  MemberSettings
	registry  *tool.Registry
	logger    *slog.Logger
	throttle  *identity.SourceThrottle
	transport http.Handler
}

type principalKey struct{}

// New 构造独立于登录限速的 MCP 入口；工具注册表应在装配前完成登记。
func New(guard *identity.Guard, tokens TokenValidator, settings MemberSettings, registry *tool.Registry, logger *slog.Logger) *Handler {
	h := &Handler{guard: guard, tokens: tokens, settings: settings, registry: registry, logger: logger,
		throttle: identity.NewSourceThrottle(nil)}
	h.transport = sdk.NewStreamableHTTPHandler(func(r *http.Request) *sdk.Server {
		principal := r.Context().Value(principalKey{}).(token.Principal)
		return h.serverFor(r.Context(), principal, r.Header.Get("X-Request-ID"))
	}, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 1 << 20})
	return h
}

// ServeHTTP 每个请求都重新校验令牌、当前用户及成员开关，Cookie 不参与认证。
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.guard.RequireOriginIfPresent(w, r) {
		return
	}
	source := identity.SourceOf(r.RemoteAddr)
	if err := h.throttle.Check(source); err != nil {
		h.writeThrottled(w, r, source, err)
		return
	}
	scheme, plaintext, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || plaintext == "" {
		h.unauthenticated(w, r, source)
		return
	}
	principal, err := h.tokens.Validate(r.Context(), plaintext)
	if errors.Is(err, token.ErrUnauthenticated) {
		h.unauthenticated(w, r, source)
		return
	}
	if err != nil {
		h.databaseUnavailable(w, r)
		return
	}
	httpserver.SetActor(r, principal.User.ID)
	if principal.User.Role != "admin" {
		enabled, err := h.settings.GetMembersEnabled(r.Context())
		if err != nil {
			h.databaseUnavailable(w, r)
			return
		}
		if !enabled {
			httpserver.WriteError(w, r, 403, "steward_disabled", "管理员已关闭成员使用管家")
			return
		}
	}
	h.transport.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, principal)))
}

func (h *Handler) unauthenticated(w http.ResponseWriter, r *http.Request, source string) {
	if err := h.throttle.Failed(source); err != nil {
		h.writeThrottled(w, r, source, err)
		return
	}
	w.Header().Set("WWW-Authenticate", "Bearer")
	httpserver.WriteError(w, r, 401, "unauthorized", "访问令牌无效")
}

func (h *Handler) writeThrottled(w http.ResponseWriter, r *http.Request, source string, err error) {
	throttled := err.(*identity.ThrottledError)
	seconds := int(math.Ceil(throttled.RetryAfter.Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	if throttled.Started {
		h.logger.WarnContext(r.Context(), "MCP 认证失败来源已暂停", "event", "steward.mcp.auth.throttled",
			"message", "MCP 认证失败来源已暂停", "module", "steward", "request_id", r.Header.Get("X-Request-ID"),
			"source", source, "retry_after_seconds", seconds)
	}
	httpserver.WriteError(w, r, 429, "too_many_attempts", "认证尝试过多，请稍后再试")
}

func (h *Handler) databaseUnavailable(w http.ResponseWriter, r *http.Request) {
	h.logger.ErrorContext(r.Context(), "MCP 认证数据暂时不可用", "event", "steward.request.failed",
		"message", "MCP 认证数据暂时不可用", "module", "steward", "request_id", r.Header.Get("X-Request-ID"),
		"error_code", "database_unavailable")
	httpserver.WriteError(w, r, 503, "database_unavailable", "管家数据暂时不可用")
}
