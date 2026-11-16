package httpapi

import (
	"io"
	"net/http"
	"time"
)

// responseLifetime 沿用分区事件接口的写期限与 watcher 回收方式。
// 只观察连接上下文；主动取消模型运行仍需向有效连接发送 done(cancelled)。
func responseLifetime(w http.ResponseWriter, r *http.Request) func() {
	finished, watched := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watched)
		select {
		case <-finished:
			return
		case <-r.Context().Done():
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now())
		}
	}()
	return func() { close(finished); <-watched }
}

func writeAndFlush(w http.ResponseWriter, payload []byte) error {
	// 慢客户端也必须有界退出，主动取消不能永远卡在一次 Write 中。
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
	defer controller.SetWriteDeadline(time.Time{})
	n, err := w.Write(payload)
	if err != nil {
		return err
	}
	if n != len(payload) {
		return io.ErrShortWrite
	}
	return controller.Flush()
}

func streamingHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Del("Content-Length")
}
