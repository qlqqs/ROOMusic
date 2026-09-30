package app

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

func TestAPIFallbackReturnsJSONErrors(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	applicationHandler := New(nil, config.Server{}, nil, testFrontend(), logger).Handler()
	degradedHandler := Degraded(testFrontend(), logger)
	cases := []struct {
		name    string
		handler http.Handler
		method  string
		path    string
		status  int
		code    string
		allow   string
	}{
		{"unknown path", applicationHandler, http.MethodGet, "/api/v1/missing", http.StatusNotFound, "not_found", ""},
		{"unknown post", applicationHandler, http.MethodPost, "/api/nope", http.StatusNotFound, "not_found", ""},
		{"api root", applicationHandler, http.MethodGet, "/api", http.StatusNotFound, "not_found", ""},
		{"api root slash", applicationHandler, http.MethodGet, "/api/", http.StatusNotFound, "not_found", ""},
		{"wrong method", applicationHandler, http.MethodDelete, "/api/v1/releases/release-42", http.StatusMethodNotAllowed, "method_not_allowed", "GET, HEAD"},
		{"mixed methods", applicationHandler, http.MethodPut, "/api/v1/users", http.StatusMethodNotAllowed, "method_not_allowed", "GET, HEAD, POST"},
		{"degraded", degradedHandler, http.MethodGet, "/api/v1/releases", http.StatusServiceUnavailable, "database_unavailable", ""},
		{"degraded root", degradedHandler, http.MethodPost, "/api", http.StatusServiceUnavailable, "database_unavailable", ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			logs.Reset()
			request := httptest.NewRequest(testCase.method, testCase.path, nil)
			request.Header.Set("X-Request-ID", "req-fallback")
			response := httptest.NewRecorder()
			testCase.handler.ServeHTTP(response, request)
			if response.Code != testCase.status {
				t.Fatalf("status = %d, want %d (%s)", response.Code, testCase.status, response.Body.String())
			}
			if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
				t.Fatalf("content type = %q", contentType)
			}
			if allow := response.Header().Get("Allow"); allow != testCase.allow {
				t.Fatalf("Allow = %q, want %q", allow, testCase.allow)
			}
			var body httpserver.APIError
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v (%s)", err, response.Body.String())
			}
			if body.Error.Code != testCase.code || body.RequestID != "req-fallback" {
				t.Fatalf("body = %+v", body)
			}
			var event map[string]any
			if err := json.Unmarshal(logs.Bytes(), &event); err != nil {
				t.Fatalf("decode log: %v (%s)", err, logs.String())
			}
			if event["route_template"] != "<unmatched>" || event["status"] != float64(testCase.status) {
				t.Fatalf("log route_template = %#v, status = %#v", event["route_template"], event["status"])
			}
		})
	}
}

func TestAPIFallbackKeepsFrontendAndHealthRoutes(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	for name, handler := range map[string]http.Handler{
		"application": New(nil, config.Server{}, nil, testFrontend(), logger).Handler(),
		"degraded":    Degraded(testFrontend(), logger),
	} {
		for requestPath, want := range map[string]int{"/some/page": http.StatusOK, "/apiary": http.StatusOK, "/assets/missing.js": http.StatusNotFound, "/healthz": http.StatusOK} {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, requestPath, nil))
			if response.Code != want {
				t.Errorf("%s %s = %d, want %d", name, requestPath, response.Code, want)
			}
			if want == http.StatusOK && requestPath != "/healthz" && !bytes.Contains(response.Body.Bytes(), []byte("ROOMusic")) {
				t.Errorf("%s %s did not serve the embedded index", name, requestPath)
			}
		}
	}
}

func testFrontend() fs.FS {
	return fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>ROOMusic</title>")}}
}
