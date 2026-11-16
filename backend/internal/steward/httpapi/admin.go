package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
)

type allowlistAddress struct {
	// Host 是不带端口或 IPv6 方括号的主机。
	Host string `json:"host"`
	// Port 是 1 至 65535 的端口号。
	Port int `json:"port"`
}

type allowlistResponse struct {
	// Host 是不带端口或 IPv6 方括号的主机。
	Host string `json:"host"`
	// Port 是 1 至 65535 的端口号。
	Port int `json:"port"`
	// AddedBy 是添加者的用户标识，用户已删除时为 null。
	AddedBy *string `json:"added_by"`
	// CreatedAt 是创建时间。
	CreatedAt time.Time `json:"created_at"`
}

func allowlistView(entry runtime.AllowlistEntry) allowlistResponse {
	return allowlistResponse{entry.Host, entry.Port, entry.AddedBy, entry.CreatedAt}
}

func (h *Handlers) getInstanceSettings(w http.ResponseWriter, r *http.Request, _ identity.User) {
	enabled, err := h.store.GetMembersEnabled(r.Context())
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, 200, map[string]bool{"members_enabled": enabled})
}

func (h *Handlers) putInstanceSettings(w http.ResponseWriter, r *http.Request, _ identity.User) {
	var input struct {
		// MembersEnabled 决定是否允许成员使用管家。
		MembersEnabled *bool `json:"members_enabled"`
	}
	if !decodeInput(w, r, &input) {
		return
	}
	if input.MembersEnabled == nil {
		invalidInput(w, r)
		return
	}
	if err := h.store.SetMembersEnabled(r.Context(), *input.MembersEnabled); err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, 200, map[string]bool{"members_enabled": *input.MembersEnabled})
}

func (h *Handlers) listAllowlist(w http.ResponseWriter, r *http.Request, _ identity.User) {
	entries, err := h.store.ListAllowlist(r.Context())
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	items := make([]allowlistResponse, 0, len(entries))
	for _, entry := range entries {
		items = append(items, allowlistView(entry))
	}
	httpserver.WriteJSON(w, 200, map[string]any{"items": items})
}

func allowlistInput(w http.ResponseWriter, r *http.Request) (allowlistAddress, bool) {
	var input allowlistAddress
	if !decodeInput(w, r, &input) {
		return input, false
	}
	input.Host = strings.TrimSpace(input.Host)
	if !validHost(input.Host) || input.Port < 1 || input.Port > 65535 {
		invalidInput(w, r)
		return input, false
	}
	input.Host = normalizeHost(input.Host)
	return input, true
}

func (h *Handlers) addAllowlist(w http.ResponseWriter, r *http.Request, user identity.User) {
	input, ok := allowlistInput(w, r)
	if !ok {
		return
	}
	entry, err := h.store.AddAllowlistEntry(r.Context(), user.ID, input.Host, input.Port)
	if err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, 200, allowlistView(entry))
}

func (h *Handlers) deleteAllowlist(w http.ResponseWriter, r *http.Request, _ identity.User) {
	input, ok := allowlistInput(w, r)
	if !ok {
		return
	}
	if err := h.store.DeleteAllowlistEntry(r.Context(), input.Host, input.Port); err != nil {
		h.writeStoreError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, 200, map[string]bool{"deleted": true})
}
