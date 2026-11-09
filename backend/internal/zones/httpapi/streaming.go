package httpapi

import (
	"context"
	"io"
	"net/http"
	"time"
)

// responseLifetime 只中断 HTTP 读写，不接管渲染会话。额外的 done 用于订阅溢出。
// 取消时的写期限让慢客户端的 Write/Flush 也能返回；不支持期限的测试 writer 仍靠上下文退出循环。
func (h *Handlers) responseLifetime(w http.ResponseWriter, r *http.Request, done <-chan struct{}) (*http.Request, func()) {
	ctx, cancel := context.WithCancel(r.Context())
	finished := make(chan struct{})
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		select {
		case <-finished:
			return
		case <-ctx.Done():
		case <-h.service.Done():
		case <-done:
		}
		cancel()
		// 网络期限必须使用系统墙钟，不能使用用于业务测试的注入时钟。
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now())
	}()
	return r.WithContext(ctx), func() {
		close(finished)
		// 等待 watcher，保证 handler 返回后不再访问 ResponseWriter。
		<-watched
		cancel()
	}
}

func writeAndFlush(w http.ResponseWriter, payload []byte) error {
	n, err := w.Write(payload)
	if err != nil {
		return err
	}
	if n != len(payload) {
		return io.ErrShortWrite
	}
	// ResponseController 优先调用已有中间件的 FlushError，并保留其错误。
	return http.NewResponseController(w).Flush()
}

func streamingHeaders(w http.ResponseWriter, mimeType string) {
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Del("Content-Length")
}
