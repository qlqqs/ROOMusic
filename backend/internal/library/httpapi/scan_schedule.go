package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/library"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

func (handlers *Handlers) loadScanSchedule(w http.ResponseWriter, r *http.Request) {
	if _, ok := handlers.guard.RequireAdmin(w, r); !ok {
		return
	}
	settings, err := handlers.service.LoadScanSchedule(r.Context())
	if err != nil {
		httpserver.WriteError(w, r, http.StatusServiceUnavailable, "database_unavailable", "无法读取定时扫描设置")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, settings)
}

func (handlers *Handlers) saveScanSchedule(w http.ResponseWriter, r *http.Request) {
	if !handlers.guard.RequireSameOrigin(w, r) {
		return
	}
	if _, ok := handlers.guard.RequireAdmin(w, r); !ok {
		return
	}
	settings, err := decodeScanSchedule(w, r)
	if err == nil {
		settings, err = handlers.service.SaveScanSchedule(r.Context(), settings)
	}
	switch {
	case errors.Is(err, library.ErrInvalidScanSchedule):
		httpserver.WriteError(w, r, http.StatusBadRequest, "invalid_input", "定时扫描设置无效")
	case err != nil:
		httpserver.WriteError(w, r, http.StatusServiceUnavailable, "database_unavailable", "无法保存定时扫描设置")
	default:
		httpserver.WriteJSON(w, http.StatusOK, settings)
	}
}

// decodeScanSchedule 严格接收两个必填字段，拒绝重复键、null 和尾随 JSON。
func decodeScanSchedule(w http.ResponseWriter, r *http.Request) (library.ScanSchedule, error) {
	var settings library.ScanSchedule
	if r.Body == nil {
		return settings, library.ErrInvalidScanSchedule
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return settings, library.ErrInvalidScanSchedule
	}
	var enabled *bool
	var hour *int
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return settings, library.ErrInvalidScanSchedule
		}
		switch key {
		case "enabled":
			if enabled != nil {
				return settings, library.ErrInvalidScanSchedule
			}
			if err := decoder.Decode(&enabled); err != nil || enabled == nil {
				return settings, library.ErrInvalidScanSchedule
			}
		case "hour":
			if hour != nil {
				return settings, library.ErrInvalidScanSchedule
			}
			if err := decoder.Decode(&hour); err != nil || hour == nil || *hour < 0 || *hour > 23 {
				return settings, library.ErrInvalidScanSchedule
			}
		default:
			return settings, library.ErrInvalidScanSchedule
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') || enabled == nil || hour == nil {
		return settings, library.ErrInvalidScanSchedule
	}
	if _, err := decoder.Token(); err != io.EOF {
		return settings, library.ErrInvalidScanSchedule
	}
	settings.Enabled, settings.Hour = *enabled, *hour
	return settings, nil
}
