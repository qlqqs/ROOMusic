// Package httpapi 提供当前成员的 DSP 资源接口，不持有播放区状态。
package httpapi

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/dspsettings"
	"github.com/qlqq/roomusic/backend/internal/dspsettings/irload"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

type Handlers struct {
	guard     *identity.Guard
	service   *dspsettings.Service
	irDeleter IRDeleter
}

func New(guard *identity.Guard, service *dspsettings.Service, options ...Option) *Handlers {
	h := &Handlers{guard: guard, service: service}
	for _, option := range options {
		option(h)
	}
	return h
}

func (h *Handlers) Register(mux *http.ServeMux) {
	h.route(mux, "GET /api/v1/me/dsp/ir-files", false, h.listIRs)
	h.route(mux, "POST /api/v1/me/dsp/ir-files", true, h.uploadIR)
	h.route(mux, "PATCH /api/v1/me/dsp/ir-files/{id}", true, h.renameIR)
	h.route(mux, "DELETE /api/v1/me/dsp/ir-files/{id}", true, h.deleteIR)
	h.route(mux, "GET /api/v1/me/dsp/opra/search", false, h.search)
	h.route(mux, "GET /api/v1/me/dsp/opra/products/{id}", false, h.product)
	h.route(mux, "POST /api/v1/me/dsp/peq/import", true, h.importPEQ)
	h.route(mux, "POST /api/v1/me/dsp/peq/response", true, h.response)
	h.route(mux, "GET /api/v1/me/dsp/peq-presets", false, h.presets)
	h.route(mux, "POST /api/v1/me/dsp/peq-presets", true, h.createPreset)
	h.route(mux, "PATCH /api/v1/me/dsp/peq-presets/{id}", true, h.updatePreset)
	h.route(mux, "DELETE /api/v1/me/dsp/peq-presets/{id}", true, h.deletePreset)
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

func respond(w http.ResponseWriter, r *http.Request, status int, result any, err error) {
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, status, result)
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := http.StatusInternalServerError, "internal", "DSP 设置暂时不可用"
	var cfg *irload.CFGError
	switch {
	case errors.As(err, &cfg):
		status, code, message = http.StatusBadRequest, "ir_cfg_invalid", fmt.Sprintf("cfg 第 %d 行看不懂", cfg.Line)
	case errors.Is(err, irload.ErrTooLarge):
		status, code, message = http.StatusRequestEntityTooLarge, "ir_too_large", "IR 文件或解码结果太大"
	case errors.Is(err, irload.ErrFormatUnsupported):
		status, code, message = http.StatusBadRequest, "ir_format_unsupported", "不支持这种 IR 格式"
	case errors.Is(err, irload.ErrDecodeFailed):
		status, code, message = http.StatusBadRequest, "ir_decode_failed", "IR 文件无法解码"
	case errors.Is(err, dspsettings.ErrIRLimitReached):
		status, code, message = http.StatusConflict, "ir_limit_reached", "IR 文件数量已达上限"
	case errors.Is(err, dspsettings.ErrInvalidInput), errors.Is(err, zones.ErrInvalidInput):
		status, code, message = http.StatusBadRequest, "invalid_input", "请求参数无效"
	case errors.Is(err, dspsettings.ErrNotFound), errors.Is(err, zones.ErrNotFound):
		status, code, message = http.StatusNotFound, "not_found", "内容不存在"
	case errors.Is(err, dspsettings.ErrImportEmpty):
		status, code, message = http.StatusBadRequest, "peq_import_empty", "没有可导入的滤波段"
	case errors.Is(err, dspsettings.ErrPresetNameTaken):
		status, code, message = http.StatusConflict, "peq_preset_name_taken", "预设名称已存在"
	case errors.Is(err, dspsettings.ErrPresetLimit):
		status, code, message = http.StatusConflict, "peq_preset_limit", "预设数量已达上限"
	}
	httpserver.WriteError(w, r, status, code, message)
}
