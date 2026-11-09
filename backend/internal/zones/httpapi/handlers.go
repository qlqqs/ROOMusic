// Package httpapi 提供播放区的鉴权、输入解码、实时与音频流交付；业务与存储由 zones 拥有。
package httpapi

import (
	"errors"
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

type Handlers struct {
	guard   *identity.Guard
	service *zones.Service
}

func New(guard *identity.Guard, service *zones.Service) *Handlers {
	return &Handlers{guard: guard, service: service}
}

// Register 挂载本人播放区的控制、输出端与实时接口。
func (h *Handlers) Register(mux *http.ServeMux) {
	h.route(mux, "GET /api/v1/me/playback", false, h.snapshot)
	h.route(mux, "GET /api/v1/me/playback/events", false, h.events)
	h.route(mux, "HEAD /api/v1/me/playback/events", false, h.rejectStreamHead)
	h.route(mux, "GET /api/v1/me/playback/zones/{zone_id}/streams/{seq}", false, h.stream)
	h.route(mux, "HEAD /api/v1/me/playback/zones/{zone_id}/streams/{seq}", false, h.rejectStreamHead)
	h.route(mux, "POST /api/v1/me/playback/devices/{device_id}/output", true, h.claimOutput)
	h.route(mux, "POST /api/v1/me/playback/outputs/{session_id}/reports", true, h.reportOutput)
	h.route(mux, "POST /api/v1/me/playback/outputs/{session_id}/release", true, h.releaseOutput)
	h.route(mux, "GET /api/v1/me/playback/zones/{zone_id}/queue", false, h.queue)
	h.route(mux, "POST /api/v1/me/playback/devices", true, h.registerDevice)
	h.route(mux, "PATCH /api/v1/me/playback/devices/{device_id}", true, h.renameDevice)
	h.route(mux, "DELETE /api/v1/me/playback/devices/{device_id}", true, h.deleteDevice)
	h.route(mux, "PUT /api/v1/me/playback/devices/{device_id}/volume", true, h.setVolume)
	h.route(mux, "POST /api/v1/me/playback/zones/{zone_id}/commands", true, h.command)
}

func (h *Handlers) route(mux *http.ServeMux, pattern string, write bool, handler func(http.ResponseWriter, *http.Request, string)) {
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if write && !h.guard.RequireSameOrigin(w, r) {
			return
		}
		user, err := h.guard.Authenticate(r)
		if err != nil {
			h.guard.WriteUnauthenticated(w, r)
			return
		}
		handler(w, r, user.ID)
	})
}

// HEAD 只鉴权并拒绝，不订阅事件，也不消耗只能取一次的音频流。
func (h *Handlers) rejectStreamHead(w http.ResponseWriter, r *http.Request, _ string) {
	w.Header().Set("Allow", "GET")
	httpserver.WriteError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "接口不支持该请求方法")
}

func (h *Handlers) snapshot(w http.ResponseWriter, r *http.Request, userID string) {
	result, err := h.service.Snapshot(r.Context(), userID)
	respond(w, r, http.StatusOK, result, err)
}

func (h *Handlers) queue(w http.ResponseWriter, r *http.Request, userID string) {
	zoneID, ok := pathID(w, r, "zone_id")
	if !ok {
		return
	}
	result, err := h.service.ReadQueue(r.Context(), userID, zoneID)
	respond(w, r, http.StatusOK, result, err)
}

func (h *Handlers) registerDevice(w http.ResponseWriter, r *http.Request, userID string) {
	var input zones.RegisterDeviceInput
	fields, ok := decodeInput(w, r, &input)
	if !ok {
		return
	}
	if !fields.only("name", "kind", "volume", "muted", "settings") ||
		!fields.required("name", "kind", "volume", "muted") || !validSettings(fields, false) {
		invalidInput(w, r)
		return
	}
	result, err := h.service.RegisterDevice(r.Context(), userID, input)
	respond(w, r, http.StatusCreated, result, err)
}

func (h *Handlers) renameDevice(w http.ResponseWriter, r *http.Request, userID string) {
	deviceID, ok := pathID(w, r, "device_id")
	if !ok {
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	fields, ok := decodeInput(w, r, &input)
	if !ok {
		return
	}
	if !fields.only("name") || !fields.required("name") {
		invalidInput(w, r)
		return
	}
	result, err := h.service.RenameDevice(r.Context(), userID, deviceID, input.Name)
	respond(w, r, http.StatusOK, result, err)
}

func (h *Handlers) deleteDevice(w http.ResponseWriter, r *http.Request, userID string) {
	deviceID, ok := pathID(w, r, "device_id")
	if !ok {
		return
	}
	if err := h.service.DeleteDevice(r.Context(), userID, deviceID); err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) setVolume(w http.ResponseWriter, r *http.Request, userID string) {
	deviceID, ok := pathID(w, r, "device_id")
	if !ok {
		return
	}
	var input struct {
		Volume float64 `json:"volume"`
		Muted  bool    `json:"muted"`
	}
	fields, ok := decodeInput(w, r, &input)
	if !ok {
		return
	}
	if !fields.only("volume", "muted") || !fields.required("volume", "muted") {
		invalidInput(w, r)
		return
	}
	result, err := h.service.SetVolume(r.Context(), userID, deviceID, input.Volume, input.Muted)
	respond(w, r, http.StatusOK, result, err)
}

func (h *Handlers) command(w http.ResponseWriter, r *http.Request, userID string) {
	zoneID, ok := pathID(w, r, "zone_id")
	if !ok {
		return
	}
	var input zones.Command
	fields, ok := decodeInput(w, r, &input)
	if !ok {
		return
	}
	if !validCommandFields(fields, input.Command) {
		invalidInput(w, r)
		return
	}
	result, err := h.service.Command(r.Context(), userID, zoneID, input)
	respond(w, r, http.StatusOK, result, err)
}

func respond(w http.ResponseWriter, r *http.Request, status int, result any, err error) {
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, status, result)
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := http.StatusServiceUnavailable, "database_unavailable", "播放区暂时不可用"
	switch {
	case errors.Is(err, zones.ErrUnsupportedProtocol):
		status, code, message = http.StatusBadRequest, "unsupported_protocol", "不支持这个输出端协议版本"
	case errors.Is(err, zones.ErrOutputSuperseded):
		status, code, message = http.StatusConflict, "output_superseded", "输出会话已被接替"
	case errors.Is(err, zones.ErrOutputUnknown):
		status, code, message = http.StatusNotFound, "output_unknown", "输出会话不存在"
	case errors.Is(err, zones.ErrStreamGone):
		status, code, message = http.StatusGone, "stream_gone", "音频流已失效"
	case errors.Is(err, zones.ErrInvalidInput):
		status, code, message = http.StatusBadRequest, "invalid_input", "请求参数无效"
	case errors.Is(err, zones.ErrNotFound):
		status, code, message = http.StatusNotFound, "not_found", "播放区或设备不存在"
	case errors.Is(err, zones.ErrTrackNotFound):
		status, code, message = http.StatusNotFound, "track_not_found", "曲目不存在"
	case errors.Is(err, zones.ErrQueueFull):
		status, code, message = http.StatusConflict, "queue_full", "队列已满"
	case errors.Is(err, zones.ErrZoneOffline):
		status, code, message = http.StatusConflict, "zone_offline", "播放设备已离线"
	case errors.Is(err, zones.ErrDeviceOnline):
		status, code, message = http.StatusConflict, "device_online", "播放设备仍在线"
	case errors.Is(err, zones.ErrVolumeFixed):
		status, code, message = http.StatusConflict, "volume_fixed", "这台设备不能调音量"
	case errors.Is(err, zones.ErrRendererUnavailable):
		code, message = "renderer_unavailable", "播放暂时不可用"
	}
	httpserver.WriteError(w, r, status, code, message)
}
