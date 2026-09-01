package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

// frontendAssets is populated by the production build in this package's web
// directory. The development server remains the preferred local loop.
//
//go:embed web
var frontendAssets embed.FS

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	config, configErr := loadServerConfig()
	if configErr != nil {
		logger.Error("roomusic configuration invalid", "event", "platform.config.invalid", "error", configErr)
		os.Exit(1)
	}
	database, databaseErr := openDatabase(context.Background(), config.DatabaseURL)
	if databaseErr != nil {
		logger.Error("roomusic database unavailable", "event", "platform.database.unavailable", "error", databaseErr)
	}
	if database != nil {
		defer database.connection.Close()
	}
	var requestHandler http.Handler = buildHandler(logger, database)
	if database != nil {
		requestHandler = buildApplicationHandler(config, database, logger)
	}
	server := &http.Server{
		Addr:              config.Address,
		Handler:           requestHandler,
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
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		requestID := request.Header.Get("X-Request-ID")
		if !requestIDPattern.MatchString(requestID) {
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
