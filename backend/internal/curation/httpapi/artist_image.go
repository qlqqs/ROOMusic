package httpapi

import (
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

const artistImageLimit = 16 << 20

func (h *Handlers) putArtistImage(w http.ResponseWriter, r *http.Request, cmd curation.ArtistEditRequest) {
	data, revision, err := readArtistImage(w, r)
	if err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			httpserver.WriteError(w, r, http.StatusRequestEntityTooLarge, "invalid_input", "图片超过大小限制")
		} else {
			httpserver.WriteError(w, r, 400, "invalid_input", "需要提供艺人版本号与图片文件")
		}
		return
	}
	cmd.ExpectedRevision = revision
	result, err := h.writer.PutArtistImage(r.Context(), cmd, data)
	h.respond(w, r, result.Response, err)
}

func readArtistImage(w http.ResponseWriter, r *http.Request) ([]byte, int64, error) {
	controller := http.NewResponseController(w)
	if err := controller.SetReadDeadline(time.Now().Add(time.Minute)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return nil, 0, err
	}
	defer controller.SetReadDeadline(time.Time{})
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	r.Body = http.MaxBytesReader(w, r.Body, artistImageLimit+(1<<20))
	stop := context.AfterFunc(ctx, func() { _ = r.Body.Close() })
	defer stop()
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, 0, err
	}
	var image []byte
	var revision int64
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, 0, err
		}
		switch part.FormName() {
		case "expected_revision":
			if part.FileName() != "" || revision != 0 {
				return nil, 0, errors.New("艺人版本字段无效")
			}
			data, err := io.ReadAll(io.LimitReader(part, 21))
			if err != nil {
				return nil, 0, err
			}
			if len(data) > 20 {
				return nil, 0, errors.New("艺人版本号过长")
			}
			revision, err = strconv.ParseInt(string(data), 10, 64)
			if err != nil || revision < 1 {
				return nil, 0, errors.New("艺人版本号无效")
			}
		case "image":
			if image != nil || part.FileName() == "" {
				return nil, 0, errors.New("图片文件字段无效")
			}
			image, err = readImagePart(part)
			if err != nil {
				return nil, 0, err
			}
		default:
			return nil, 0, errors.New("未知上传字段")
		}
		if err := part.Close(); err != nil {
			return nil, 0, err
		}
	}
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		return nil, 0, err
	}
	if revision < 1 || len(image) == 0 {
		return nil, 0, errors.New("缺少艺人版本或图片")
	}
	return image, revision, ctx.Err()
}

func readImagePart(part *multipart.Part) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(part, artistImageLimit+1))
	if len(data) > artistImageLimit {
		return nil, &http.MaxBytesError{Limit: artistImageLimit}
	}
	return data, err
}
