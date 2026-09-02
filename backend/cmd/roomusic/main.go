package main

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"
)

// frontendAssets is populated by the production build in this package's web
// directory. The development server remains the preferred local loop.
//
//go:embed web
var frontendAssets embed.FS

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	applicationContext, stopApplication := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopApplication()
	config, configErr := loadServerConfig()
	if configErr != nil {
		logger.Error("roomusic configuration invalid", "event", "platform.config.invalid", "error", configErr)
		os.Exit(1)
	}
	database, databaseErr := openDatabase(applicationContext, config.DatabaseURL)
	if databaseErr != nil {
		logger.Error("roomusic database unavailable", "event", "platform.database.unavailable", "error", databaseErr)
	}
	var requestHandler http.Handler = buildHandler(logger, database)
	var application *roomusicApplication
	if database != nil {
		application, requestHandler = buildApplication(applicationContext, config, database, logger)
	}
	server := &http.Server{
		Addr:              config.Address,
		Handler:           requestHandler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	logger.Info("roomusic server starting", "address", server.Addr)
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.ListenAndServe()
	}()
	var serveErr error
	select {
	case <-applicationContext.Done():
	case serveErr = <-serveErrors:
		stopApplication()
	}
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownContext); err != nil {
		logger.Error("roomusic HTTP shutdown failed", "event", "platform.http.shutdown_failed", "error", err)
	}
	if application != nil {
		if err := application.waitForScans(shutdownContext); err != nil {
			logger.Error("roomusic scan shutdown timed out", "event", "library.scan.shutdown_timeout", "error", err)
		}
	}
	if database != nil {
		if err := database.connection.Close(); err != nil {
			logger.Error("roomusic database close failed", "event", "platform.database.close_failed", "error", err)
		}
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		logger.Error("roomusic server stopped", "error", serveErr)
		os.Exit(1)
	}
}

func configuredAddress() string {
	if address := os.Getenv("ROOMUSIC_HTTP_ADDR"); address != "" {
		return address
	}
	return ":8080"
}

func buildHandler(logger *slog.Logger, database *databaseState) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthHandler)
	mux.HandleFunc("GET /readyz", func(responseWriter http.ResponseWriter, _ *http.Request) {
		statusCode, status := readinessStatus(database)
		writeJSON(responseWriter, statusCode, map[string]string{"status": status})
	})
	mux.Handle("/", productionFrontendHandler())

	return requestIDMiddleware(logger, mux)
}

func healthHandler(responseWriter http.ResponseWriter, _ *http.Request) {
	writeJSON(responseWriter, http.StatusOK, map[string]string{"status": "ok"})
}

func readinessHandler(responseWriter http.ResponseWriter, _ *http.Request) {
	// Database wiring is intentionally the next vertical slice. Until then the
	// service must not claim readiness for the business application.
	writeJSON(responseWriter, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
}

func productionFrontendHandler() http.Handler {
	webAssets, err := fs.Sub(frontendAssets, "web")
	if err != nil {
		panic(fmt.Errorf("load embedded frontend assets: %w", err))
	}
	fileServer := http.FileServer(http.FS(webAssets))
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		requestedPath := strings.TrimPrefix(path.Clean(request.URL.Path), "/")
		if requestedPath == "." || requestedPath == "" {
			serveEmbeddedIndex(responseWriter, webAssets)
			return
		}
		if _, statError := fs.Stat(webAssets, requestedPath); statError != nil {
			if strings.HasPrefix(requestedPath, "assets/") {
				http.NotFound(responseWriter, request)
				return
			}
			serveEmbeddedIndex(responseWriter, webAssets)
			return
		}
		request.URL.Path = "/" + requestedPath
		fileServer.ServeHTTP(responseWriter, request)
	})
}

func serveEmbeddedIndex(responseWriter http.ResponseWriter, webAssets fs.FS) {
	indexHTML, err := fs.ReadFile(webAssets, "index.html")
	if err != nil {
		http.Error(responseWriter, "frontend unavailable", http.StatusServiceUnavailable)
		return
	}
	responseWriter.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = responseWriter.Write(indexHTML)
}

func requestIDMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
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
			if routeTemplate == "" {
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

func writeJSON(responseWriter http.ResponseWriter, statusCode int, value any) {
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(statusCode)
	if err := json.NewEncoder(responseWriter).Encode(value); err != nil {
		return
	}
}
