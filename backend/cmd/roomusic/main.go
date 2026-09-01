package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"
)

// frontendAssets is populated by the production build when assets are copied
// into backend/web. The development server remains the preferred local loop.
//go:embed web
var frontendAssets embed.FS

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	server := &http.Server{
		Addr:              configuredAddress(),
		Handler:           buildHandler(logger),
		ReadHeaderTimeout: 5 * time.Second,
	}

	logger.Info("roomusic server starting", "address", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("roomusic server stopped", "error", err)
		os.Exit(1)
	}
}

func configuredAddress() string {
	if address := os.Getenv("ROOMUSIC_HTTP_ADDR"); address != "" {
		return address
	}
	return ":8080"
}

func buildHandler(logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthHandler)
	mux.HandleFunc("GET /readyz", readinessHandler)
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
	fileServer := http.FileServer(http.FS(frontendAssets))
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/" {
			request.URL.Path = "/web/index.html"
		} else {
			request.URL.Path = "/web" + request.URL.Path
		}
		fileServer.ServeHTTP(responseWriter, request)
	})
}

func requestIDMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		requestID := request.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = fmt.Sprintf("req-%d", time.Now().UnixNano())
		}
		responseWriter.Header().Set("X-Request-ID", requestID)
		logger.Info("http request", "request_id", requestID, "method", request.Method, "path", request.URL.Path)
		next.ServeHTTP(responseWriter, request)
	})
}

func writeJSON(responseWriter http.ResponseWriter, statusCode int, value any) {
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(statusCode)
	if err := json.NewEncoder(responseWriter).Encode(value); err != nil {
		return
	}
}
