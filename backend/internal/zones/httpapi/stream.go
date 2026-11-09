package httpapi

import (
	"context"
	"io"
	"net/http"
	"strconv"
)

func (h *Handlers) stream(w http.ResponseWriter, r *http.Request, userID string) {
	zoneID, ok := pathID(w, r, "zone_id")
	if !ok {
		return
	}
	seq, err := strconv.ParseInt(r.PathValue("seq"), 10, 64)
	if err != nil || seq < 0 {
		invalidInput(w, r)
		return
	}
	stream, err := h.service.TakeStream(r.Context(), userID, zoneID, seq)
	if err != nil {
		writeError(w, r, err)
		return
	}
	r, finish := h.responseLifetime(w, r, nil)
	defer finish()
	streamingHeaders(w, stream.MimeType)
	w.Header().Set("Accept-Ranges", "none")
	w.WriteHeader(http.StatusOK)
	// 首次 Flush 同时禁止 net/http 为短流推断 Content-Length；Range 请求也始终返回 200。
	if err := http.NewResponseController(w).Flush(); err != nil {
		return
	}
	copyStream(r.Context(), w, stream.Body)
}

type streamRead struct {
	data []byte
	err  error
}

func copyStream(ctx context.Context, w http.ResponseWriter, body io.Reader) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	reads := make(chan streamRead)
	next := make(chan struct{})
	go readStream(ctx, body, next, reads)
	for {
		select {
		case <-ctx.Done():
			return
		case next <- struct{}{}:
		}
		select {
		case <-ctx.Done():
			return
		case read := <-reads:
			if ctx.Err() != nil {
				return
			}
			if len(read.data) > 0 && writeAndFlush(w, read.data) != nil {
				return
			}
			if read.err != nil {
				return
			}
		}
	}
}

// 每条已取流只有一个 reader 和一个 32 KiB 缓冲；写出完成才允许下一次读，保持背压。
// io.Reader 无取消接口，取消时 handler 直接返回，不关闭会话；在途 Read 等 zones 关闭会话后 EOF 退出。
func readStream(ctx context.Context, body io.Reader, next <-chan struct{}, reads chan<- streamRead) {
	buffer := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			return
		case <-next:
		}
		n, err := body.Read(buffer)
		select {
		case <-ctx.Done():
			return
		case reads <- streamRead{data: buffer[:n], err: err}:
		}
		if err != nil {
			return
		}
	}
}
