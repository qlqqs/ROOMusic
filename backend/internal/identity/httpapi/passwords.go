package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

func (handlers *Handlers) changePassword(w http.ResponseWriter, r *http.Request) {
	if !handlers.guard.RequireSameOrigin(w, r) {
		return
	}
	user, err := handlers.guard.Authenticate(r)
	if err != nil {
		handlers.guard.WriteUnauthenticated(w, r)
		return
	}
	var input struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !httpserver.DecodeJSON(w, r, &input) {
		return
	}
	cookie, _ := r.Cookie(identity.SessionCookieName)
	if err := identity.ChangeOwnPassword(r.Context(), handlers.database, handlers.throttle, user.ID, identity.HashSessionToken(cookie.Value), input.CurrentPassword, input.NewPassword); err != nil {
		handlers.writePasswordError(w, r, err, "无法修改密码")
		return
	}
	httpserver.WriteJSON(w, 200, map[string]bool{"password_changed": true})
}

func (handlers *Handlers) resetPassword(w http.ResponseWriter, r *http.Request) {
	if !handlers.guard.RequireSameOrigin(w, r) {
		return
	}
	actor, ok := handlers.guard.RequireAdmin(w, r)
	if !ok {
		return
	}
	id, ok := userIDFromPath(w, r)
	if !ok {
		return
	}
	if strings.EqualFold(id, actor.ID) {
		httpserver.WriteError(w, r, 403, "self_password_reset", "修改自己的密码需要提供当前密码")
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if !httpserver.DecodeJSON(w, r, &input) {
		return
	}
	if _, err := identity.ResetPassword(r.Context(), handlers.database, id, input.Password); err != nil {
		handlers.writePasswordError(w, r, err, "无法重置密码")
		return
	}
	httpserver.WriteJSON(w, 200, map[string]bool{"password_reset": true})
}

func (handlers *Handlers) writePasswordError(w http.ResponseWriter, r *http.Request, err error, message string) {
	var throttled *identity.ThrottledError
	var hashError *identity.PasswordHashError
	switch {
	case errors.As(err, &throttled):
		writeThrottledError(w, r, throttled, "当前密码输错次数太多，请过几分钟再试")
	case errors.Is(err, identity.ErrInvalidPassword):
		httpserver.WriteError(w, r, 400, "invalid_input", identity.PasswordRuleMessage)
	case errors.Is(err, identity.ErrWrongCurrentPassword):
		httpserver.WriteError(w, r, 403, "current_password_incorrect", "当前密码不正确")
	case errors.Is(err, identity.ErrSessionEnded):
		handlers.guard.WriteUnauthenticated(w, r)
	case errors.Is(err, identity.ErrUserNotFound):
		httpserver.WriteError(w, r, 404, "not_found", "用户不存在")
	case errors.As(err, &hashError):
		httpserver.WriteError(w, r, 500, "internal_error", message)
	default:
		httpserver.WriteError(w, r, 503, "database_unavailable", message)
	}
}
