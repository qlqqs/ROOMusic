// Package httpapi 提供局域网设备的管理员设置、手动登记与账号分配接口。
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/devices/airplay"
	"github.com/qlqq/roomusic/backend/internal/devices/cast"
	"github.com/qlqq/roomusic/backend/internal/devices/upnp"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

type Handlers struct {
	guard   *identity.Guard
	service *upnp.Service
}

func New(guard *identity.Guard, service *upnp.Service) *Handlers {
	return &Handlers{guard: guard, service: service}
}

func (h *Handlers) Register(mux *http.ServeMux) {
	const base = "/api/v1/admin/upnp"
	mux.HandleFunc("GET "+base+"/settings", h.admin(h.settings))
	mux.HandleFunc("PUT "+base+"/settings", h.admin(h.updateSettings))
	mux.HandleFunc("GET "+base+"/devices", h.admin(h.list))
	mux.HandleFunc("POST "+base+"/devices", h.admin(h.add))
	mux.HandleFunc("DELETE "+base+"/devices/{udn}", h.admin(h.remove))
	mux.HandleFunc("POST "+base+"/devices/{udn}/pair/start", h.admin(h.startPairing))
	mux.HandleFunc("POST "+base+"/devices/{udn}/pair/finish", h.admin(h.finishPairing))
	mux.HandleFunc("PUT "+base+"/devices/{udn}/format", h.admin(h.format))
	mux.HandleFunc("PUT "+base+"/devices/{udn}/grants", h.admin(h.grants))
}

func (h *Handlers) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !h.guard.RequireSameOrigin(w, r) {
			return
		}
		if _, ok := h.guard.RequireAdmin(w, r); ok {
			next(w, r)
		}
	}
}

func decode(w http.ResponseWriter, r *http.Request, body any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(body) != nil || decoder.Decode(new(any)) != io.EOF {
		httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_json", "请求格式无效")
		return false
	}
	return true
}

func respond(w http.ResponseWriter, r *http.Request, result any, err error) {
	if err == nil {
		if result == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		httpserver.WriteJSON(w, http.StatusOK, result)
		return
	}
	status, code, message := http.StatusServiceUnavailable, "database_unavailable", "服务暂不可用"
	switch {
	case errors.Is(err, upnp.ErrAirPlayDisabled):
		status, code, message = 409, "airplay_disabled", upnp.ErrAirPlayDisabled.Error()
	case errors.Is(err, upnp.ErrCastDisabled):
		status, code, message = 409, "cast_disabled", upnp.ErrCastDisabled.Error()
	case errors.Is(err, cast.ErrAddress), errors.Is(err, airplay.ErrAddress):
		status, code, message = 400, "invalid_address", err.Error()
	case errors.Is(err, upnp.ErrDisabled):
		status, code, message = 409, "upnp_disabled", upnp.ErrDisabled.Error()
	case errors.Is(err, upnp.ErrUnknownDevice):
		status, code, message = 404, "not_found", upnp.ErrUnknownDevice.Error()
	case errors.Is(err, upnp.ErrManualDevice):
		status, code, message = 404, "not_found", upnp.ErrManualDevice.Error()
	case errors.Is(err, upnp.ErrPairingBusy):
		status, code, message = 409, "pairing_busy", err.Error()
	case errors.Is(err, upnp.ErrPairingExpired):
		status, code, message = 409, "pairing_expired", err.Error()
	case errors.Is(err, upnp.ErrPairingRejected):
		status, code, message = 422, "pairing_rejected", err.Error()
	case errors.Is(err, upnp.ErrPairingInput), errors.Is(err, upnp.ErrAirPlayFormat), errors.Is(err, upnp.ErrSlimProtoFormat), errors.Is(err, upnp.ErrFormat), errors.Is(err, upnp.ErrKind):
		status, code, message = 400, "invalid_input", err.Error()
	case errors.Is(err, upnp.ErrGrantUser):
		status, code, message = 400, "invalid_input", upnp.ErrGrantUser.Error()
	case errors.Is(err, upnp.ErrDeviceAddress):
		status, code, message = 400, "invalid_address", "需要局域网设备 IP 或同主机的 HTTP 描述地址"
	case errors.Is(err, upnp.ErrProbe):
		status, code, message = 422, "device_unreachable", upnp.ErrProbe.Error()
	case errors.Is(err, upnp.ErrDiscovery):
		status, code, message = 503, "discovery_unavailable", upnp.ErrDiscovery.Error()
	}
	httpserver.WriteError(w, r, status, code, message)
}

func (h *Handlers) settings(w http.ResponseWriter, r *http.Request) {
	v, err := h.service.Settings(r.Context())
	respond(w, r, v, err)
}

func (h *Handlers) updateSettings(w http.ResponseWriter, r *http.Request) {
	var body upnp.SettingsUpdate
	if !decode(w, r, &body) {
		return
	}
	if body == (upnp.SettingsUpdate{}) {
		httpserver.WriteError(w, r, 400, "invalid_input", "需要指定设备开关或共享开关")
		return
	}
	v, err := h.service.UpdateSettings(r.Context(), body)
	respond(w, r, v, err)
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.Devices(r.Context())
	respond(w, r, map[string]any{"items": items}, err)
}

func (h *Handlers) add(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Address string `json:"address"`
		Kind    string `json:"kind"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Kind == "" {
		body.Kind = "upnp"
	}
	item, err := h.service.Add(r.Context(), body.Kind, body.Address)
	respond(w, r, item, err)
}

func (h *Handlers) remove(w http.ResponseWriter, r *http.Request) {
	respond(w, r, nil, h.service.Remove(r.Context(), r.PathValue("udn")))
}

func (h *Handlers) grants(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserIDs []string `json:"user_ids"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.UserIDs == nil {
		respond(w, r, nil, upnp.ErrGrantUser)
		return
	}
	ids, err := h.service.SetGrants(r.Context(), r.PathValue("udn"), body.UserIDs)
	respond(w, r, map[string]any{"user_ids": ids}, err)
}

func (h *Handlers) format(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("udn")
	var format upnp.DeviceFormat
	if strings.HasPrefix(key, "slim:") {
		var body struct {
			Codec         string `json:"codec"`
			MaxSampleRate *int   `json:"max_sample_rate"`
			Volume        string `json:"volume"`
		}
		if !decode(w, r, &body) {
			return
		}
		format.Codec, format.Volume = body.Codec, body.Volume
		if body.MaxSampleRate != nil {
			if *body.MaxSampleRate <= 0 {
				respond(w, r, nil, upnp.ErrSlimProtoFormat)
				return
			}
			format.MaxSampleRate = *body.MaxSampleRate
		}
	} else {
		var body struct {
			Codec         string `json:"codec"`
			MaxSampleRate int    `json:"max_sample_rate"`
			MaxBitDepth   int    `json:"max_bit_depth"`
		}
		if !decode(w, r, &body) {
			return
		}
		format = upnp.DeviceFormat{Codec: body.Codec, MaxSampleRate: body.MaxSampleRate, MaxBitDepth: body.MaxBitDepth}
	}
	result, err := h.service.SetFormat(r.Context(), key, format)
	respond(w, r, result, err)
}

func (h *Handlers) startPairing(w http.ResponseWriter, r *http.Request) {
	respond(w, r, nil, h.service.BeginAirPlayPIN(r.Context(), r.PathValue("udn")))
}

func (h *Handlers) finishPairing(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PIN string `json:"pin"`
	}
	if !decode(w, r, &body) {
		return
	}
	respond(w, r, nil, h.service.FinishAirPlayPIN(r.Context(), r.PathValue("udn"), body.PIN))
}
