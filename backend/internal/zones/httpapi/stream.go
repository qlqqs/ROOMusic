package httpapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/zones"
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

func (h *Handlers) hlsMedia(w http.ResponseWriter, r *http.Request, userID string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	zoneID, ok := pathID(w, r, "zone_id")
	if !ok {
		return
	}
	seq, err := strconv.ParseInt(r.PathValue("seq"), 10, 64)
	if err != nil || seq < 0 {
		invalidInput(w, r)
		return
	}
	file := r.PathValue("file")
	var segment int64
	if file != "index.m3u8" && file != "init.mp4" {
		var valid bool
		segment, valid = hlsSegmentNumber(file)
		if !valid {
			writeError(w, r, zones.ErrSegmentMissing)
			return
		}
	}
	media, err := h.service.GetHLSMedia(r.Context(), userID, zoneID, seq)
	if err != nil {
		writeError(w, r, err)
		return
	}
	r, finish := h.responseLifetime(w, r, nil)
	defer finish()
	var body []byte
	switch file {
	case "index.m3u8":
		body, err = media.Playlist(r.Context())
	case "init.mp4":
		body, err = media.Init()
	default:
		body, err = media.Segment(segment)
	}
	if r.Context().Err() != nil {
		return
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	if file == "index.m3u8" {
		writeHLSPlaylist(w, r, body)
		return
	}
	w.Header().Set("Content-Type", "audio/mp4")
	http.ServeContent(hlsContentWriter{w}, r, file, time.Time{}, bytes.NewReader(body))
}

// ServeContent 的 Range 错误会清除缓存头，写出状态前恢复媒体的禁止缓存合同。
type hlsContentWriter struct{ http.ResponseWriter }

func (w hlsContentWriter) WriteHeader(status int) {
	w.Header().Set("Cache-Control", "no-store")
	w.ResponseWriter.WriteHeader(status)
}

// 文件名只接受 ASCII 十进制片号，不接受符号、路径或 int64 溢出。
func hlsSegmentNumber(file string) (int64, bool) {
	number, ok := strings.CutSuffix(file, ".m4s")
	if !ok || number == "" {
		return 0, false
	}
	for i := range number {
		if number[i] < '0' || number[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(number, 10, 64)
	return n, err == nil
}

func writeHLSPlaylist(w http.ResponseWriter, r *http.Request, body []byte) {
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Add("Vary", "Accept-Encoding")
	if acceptsGzip(r.Header.Values("Accept-Encoding")) {
		w.Header().Set("Content-Encoding", "gzip")
		if r.Method == http.MethodHead {
			return
		}
		compressed := gzip.NewWriter(w)
		if _, err := compressed.Write(body); err != nil {
			return
		}
		_ = compressed.Close()
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func acceptsGzip(values []string) bool {
	return strings.Contains(strings.ToLower(strings.Join(values, ",")), "gzip")
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
