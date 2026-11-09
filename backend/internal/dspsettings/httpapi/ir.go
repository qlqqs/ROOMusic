package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/qlqq/roomusic/backend/internal/dspsettings"
	"github.com/qlqq/roomusic/backend/internal/dspsettings/irload"
)

// IRDeleter 必须由 zones 服务实现；这里不能绕过引用清理直接删除仓储。
type IRDeleter interface {
	DeleteIR(context.Context, string, string) error
}
type Option func(*Handlers)

func WithIRDeleter(deleter IRDeleter) Option { return func(h *Handlers) { h.irDeleter = deleter } }

func (h *Handlers) listIRs(w http.ResponseWriter, r *http.Request, user string) {
	items, err := h.service.ListIRs(r.Context(), user)
	respond(w, r, http.StatusOK, struct {
		Items []dspsettings.IRFile `json:"items"`
	}{items}, err)
}
func (h *Handlers) renameIR(w http.ResponseWriter, r *http.Request, user string) {
	id, ok := presetID(w, r)
	if !ok {
		return
	}
	fields, ok := decodeInput(w, r, "name")
	if !ok {
		return
	}
	var name string
	if !fields.field("name", &name) {
		invalidInput(w, r)
		return
	}
	f, err := h.service.RenameIR(r.Context(), user, id, name)
	respond(w, r, http.StatusOK, f, err)
}
func (h *Handlers) deleteIR(w http.ResponseWriter, r *http.Request, user string) {
	id, ok := presetID(w, r)
	if !ok {
		return
	}
	if h.irDeleter == nil {
		writeError(w, r, errors.New("IR 删除用例未装配"))
		return
	}
	if err := h.irDeleter.DeleteIR(r.Context(), user, id); err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) uploadIR(w http.ResponseWriter, r *http.Request, user string) {
	// 读取期限仅覆盖此上传体，不能缩短播放长响应的全局写期限。
	controller := http.NewResponseController(w)
	if err := controller.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		writeError(w, r, err)
		return
	}
	defer controller.SetReadDeadline(time.Time{})
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	r.Body = http.MaxBytesReader(w, r.Body, irload.MaxFileBytes+(1<<20))
	body := r.Body
	stop := context.AfterFunc(ctx, func() { _ = body.Close() })
	defer stop()
	reader, err := r.MultipartReader()
	if err != nil {
		invalidInput(w, r)
		return
	}
	dir, err := os.MkdirTemp("", "roomusic-ir-upload-")
	if err != nil {
		writeError(w, r, err)
		return
	}
	defer os.RemoveAll(dir)
	var file *os.File
	defer func() {
		if file != nil {
			_ = file.Close()
		}
	}()
	var name, fileName string
	seen := map[string]bool{}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			uploadError(w, r, err)
			return
		}
		field := part.FormName()
		if seen[field] || (field != "file" && field != "name") {
			invalidInput(w, r)
			return
		}
		seen[field] = true
		if field == "name" {
			if part.FileName() != "" {
				invalidInput(w, r)
				return
			}
			data, err := io.ReadAll(io.LimitReader(part, 4097))
			if err != nil {
				uploadError(w, r, err)
				return
			}
			if len(data) > 4096 {
				invalidInput(w, r)
				return
			}
			name = string(data)
		} else {
			fileName = part.FileName()
			if fileName == "" {
				invalidInput(w, r)
				return
			}
			file, err = os.CreateTemp(dir, "source-")
			if err != nil {
				writeError(w, r, err)
				return
			}
			n, err := io.Copy(file, io.LimitReader(part, irload.MaxFileBytes+1))
			if n > irload.MaxFileBytes {
				writeError(w, r, irload.ErrTooLarge)
				return
			}
			if err != nil {
				uploadError(w, r, err)
				return
			}
		}
		if err := part.Close(); err != nil {
			uploadError(w, r, err)
			return
		}
	}
	// 消耗有限尾部，防止最后一个 boundary 后的载荷绕过请求总量限制。
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		uploadError(w, r, err)
		return
	}
	if !seen["name"] || file == nil {
		invalidInput(w, r)
		return
	}
	if err := ctx.Err(); err != nil {
		uploadError(w, r, err)
		return
	}
	stop()
	cancel()
	if err := controller.SetReadDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		writeError(w, r, err)
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		writeError(w, r, err)
		return
	}
	processing, cancelProcessing := context.WithTimeout(r.Context(), irload.ProcessingTimeout)
	defer cancelProcessing()
	result, err := h.service.UploadIR(processing, user, name, fileName, file)
	respond(w, r, http.StatusCreated, result, err)
}

func uploadError(w http.ResponseWriter, r *http.Request, err error) {
	var large *http.MaxBytesError
	if errors.As(err, &large) {
		writeError(w, r, irload.ErrTooLarge)
		return
	}
	writeError(w, r, dspsettings.ErrInvalidInput)
}
