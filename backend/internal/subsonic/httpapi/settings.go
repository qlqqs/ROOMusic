package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/subsonic/auth"
)

func unavailable(w http.ResponseWriter, r *http.Request) {
	httpserver.WriteError(w, r, 503, "database_unavailable", "服务暂不可用")
}

func (h *Handlers) me(w http.ResponseWriter, r *http.Request) {
	user, err := h.guard.Authenticate(r)
	if err != nil {
		h.guard.WriteUnauthenticated(w, r)
		return
	}
	state, err := h.service.Status(r.Context(), user)
	if err != nil {
		unavailable(w, r)
		return
	}
	httpserver.WriteJSON(w, 200, state)
}

func (h *Handlers) setJukeboxZone(w http.ResponseWriter, r *http.Request) {
	if !h.guard.RequireSameOrigin(w, r) {
		return
	}
	user, err := h.guard.Authenticate(r)
	if err != nil {
		h.guard.WriteUnauthenticated(w, r)
		return
	}
	zoneID, valid := jukeboxZoneInput(w, r)
	if !valid {
		httpserver.WriteError(w, r, 400, "invalid_input", "请提供有效的播放区标识或 null")
		return
	}
	state, err := h.service.SetJukeboxZone(r.Context(), user, zoneID)
	switch {
	case errors.Is(err, auth.ErrDisabled):
		httpserver.WriteError(w, r, 409, "subsonic_disabled", "Subsonic 接口已关闭")
	case errors.Is(err, auth.ErrJukeboxZoneNotFound):
		httpserver.WriteError(w, r, 404, "not_found", "播放区不存在")
	case err != nil:
		unavailable(w, r)
	default:
		httpserver.WriteJSON(w, 200, state)
	}
}

func jukeboxZoneInput(w http.ResponseWriter, r *http.Request) (*string, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil || !utf8.Valid(body) || httpserver.UniqueJSONObject(body) != nil {
		return nil, false
	}
	var fields map[string]json.RawMessage
	var id *string
	if json.Unmarshal(body, &fields) != nil || len(fields) != 1 || json.Unmarshal(fields["jukebox_zone_id"], &id) != nil {
		return nil, false
	}
	return id, id == nil || uuid.Valid(*id)
}

func (h *Handlers) confirm(w http.ResponseWriter, r *http.Request) {
	if !h.guard.RequireSameOrigin(w, r) {
		return
	}
	user, err := h.guard.Authenticate(r)
	if err != nil {
		h.guard.WriteUnauthenticated(w, r)
		return
	}
	var input struct {
		Password json.RawMessage `json:"password"`
	}
	if _, ok := httpserver.DecodeCommand(w, r, &input); !ok {
		return
	}
	var password *string
	if err := json.Unmarshal(input.Password, &password); err != nil || password == nil {
		httpserver.WriteError(w, r, 400, "invalid_input", "需要提供登录密码")
		return
	}
	cookie, _ := r.Cookie(identity.SessionCookieName)
	err = h.service.Confirm(r.Context(), user, identity.HashSessionToken(cookie.Value), *password)
	var throttled *identity.ThrottledError
	switch {
	case errors.Is(err, auth.ErrDisabled):
		httpserver.WriteError(w, r, 409, "subsonic_disabled", "Subsonic 接口已关闭")
	case errors.Is(err, identity.ErrWrongCurrentPassword):
		httpserver.WriteError(w, r, 403, "current_password_incorrect", "当前密码不正确")
	case errors.As(err, &throttled):
		w.Header().Set("Retry-After", strconv.FormatInt(identity.RetryAfterSeconds(throttled.RetryAfter), 10))
		httpserver.WriteError(w, r, 429, "too_many_attempts", "当前密码输错次数太多，请过几分钟再试")
	case errors.Is(err, identity.ErrSessionEnded):
		h.guard.WriteUnauthenticated(w, r)
	case err != nil:
		unavailable(w, r)
	default:
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	}
}

type settings struct {
	Enabled bool `json:"enabled"`
}

func (h *Handlers) admin(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.guard.RequireAdmin(w, r); !ok {
		return
	}
	enabled, err := h.service.Enabled(r.Context())
	if err != nil {
		unavailable(w, r)
		return
	}
	httpserver.WriteJSON(w, 200, settings{enabled})
}

func (h *Handlers) setEnabled(w http.ResponseWriter, r *http.Request) {
	if !h.guard.RequireSameOrigin(w, r) {
		return
	}
	if _, ok := h.guard.RequireAdmin(w, r); !ok {
		return
	}
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if _, ok := httpserver.DecodeCommand(w, r, &input); !ok {
		return
	}
	if input.Enabled == nil {
		httpserver.WriteError(w, r, 400, "invalid_input", "需要提供启用状态")
		return
	}
	if err := h.service.SetEnabled(r.Context(), *input.Enabled); err != nil {
		unavailable(w, r)
		return
	}
	httpserver.WriteJSON(w, 200, settings{*input.Enabled})
}
