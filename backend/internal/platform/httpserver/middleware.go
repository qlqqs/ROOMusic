package httpserver

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"time"
)

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)

// Middleware 为每个请求校验或生成 X-Request-ID 并回写，记录 http.request.completed 完成日志
// （路由模板、状态、字节数、耗时与可选 actor），并在 panic 时先记日志再继续抛出。
func Middleware(logger *slog.Logger, next http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		requestID := request.Header.Get("X-Request-ID")
		if !requestIDPattern.MatchString(requestID) {
			requestID = fmt.Sprintf("req-%d", time.Now().UnixNano())
		}
		request.Header.Set("X-Request-ID", requestID)
		responseWriter.Header().Set("X-Request-ID", requestID)
		observation := &requestObservation{}
		request = request.WithContext(context.WithValue(request.Context(), requestObservationKey{}, observation))
		routeTemplate := "<unmatched>"
		if mux, ok := next.(*http.ServeMux); ok {
			_, routeTemplate = mux.Handler(request)
			if routeTemplate == "" || isAPIFallbackPattern(routeTemplate) {
				routeTemplate = "<unmatched>"
			}
		}
		recorded := &recordingResponseWriter{ResponseWriter: responseWriter}
		started := time.Now()
		defer func() {
			level := slog.LevelInfo
			status := recorded.statusCode()
			if recovered := recover(); recovered != nil {
				status = http.StatusInternalServerError
				level = slog.LevelError
				logHTTPCompleted(logger, level, requestID, request.Method, routeTemplate, status, recorded.bytes, time.Since(started), observation.actorID)
				panic(recovered)
			}
			if status >= 500 {
				level = slog.LevelError
			} else if status >= 400 {
				level = slog.LevelWarn
			}
			logHTTPCompleted(logger, level, requestID, request.Method, routeTemplate, status, recorded.bytes, time.Since(started), observation.actorID)
		}()
		next.ServeHTTP(recorded, request)
	})
}

type requestObservationKey struct{}

type requestObservation struct {
	actorID string
}

// SetActor 把已认证的用户 ID 记录到请求观察里，供完成日志写入 actor_id。请求没有经过 Middleware 时什么也不做。
func SetActor(request *http.Request, actorID string) {
	if observation, ok := request.Context().Value(requestObservationKey{}).(*requestObservation); ok {
		observation.actorID = actorID
	}
}

func logHTTPCompleted(logger *slog.Logger, level slog.Level, requestID, method, routeTemplate string, status, responseBytes int, duration time.Duration, actorID string) {
	if duration < 0 {
		duration = 0
	}
	attrs := []slog.Attr{
		slog.String("event", "http.request.completed"),
		slog.String("module", "platform"),
		slog.String("message", "http request completed"),
		slog.String("request_id", requestID),
		slog.String("method", method),
		slog.String("route_template", routeTemplate),
		slog.Int("status", status),
		slog.Int("response_bytes", responseBytes),
		slog.Int64("duration_ms", duration.Milliseconds()),
	}
	if actorID != "" {
		attrs = append(attrs, slog.String("actor_id", actorID))
	}
	logger.LogAttrs(context.Background(), level, "http request completed", attrs...)
}

type recordingResponseWriter struct {
	http.ResponseWriter
	status  int
	bytes   int
	written bool
}

func (w *recordingResponseWriter) WriteHeader(statusCode int) {
	if statusCode >= 100 && statusCode < 200 {
		w.ResponseWriter.WriteHeader(statusCode)
		return
	}
	if w.written {
		return
	}
	w.status = statusCode
	w.written = true
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *recordingResponseWriter) Write(payload []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(payload)
	w.bytes += n
	return n, err
}

func (w *recordingResponseWriter) statusCode() int {
	if !w.written {
		return http.StatusOK
	}
	return w.status
}

func (w *recordingResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *recordingResponseWriter) Flush() {
	_ = w.FlushError()
}

func (w *recordingResponseWriter) FlushError() error {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
		return nil
	}
	return http.ErrNotSupported
}

func (w *recordingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

func (w *recordingResponseWriter) Push(target string, options *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, options)
}

func (w *recordingResponseWriter) ReadFrom(reader io.Reader) (int64, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	var n int64
	var err error
	if readerFrom, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		n, err = readerFrom.ReadFrom(reader)
	} else {
		n, err = io.Copy(w.ResponseWriter, reader)
	}
	w.bytes += int(n)
	return n, err
}
