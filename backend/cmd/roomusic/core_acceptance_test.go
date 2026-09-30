package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReleaseSearchParsingTrimsAndEscapesWildcards(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/releases?q=%20%25Rock_%20", nil)
	query, err := parseReleaseSearch(request)
	if err != nil || query != "%Rock_" {
		t.Fatalf("unexpected parsed query %q, %v", query, err)
	}
	longQuery := strings.Repeat("x", 201)
	if _, err := parseReleaseSearch(httptest.NewRequest(http.MethodGet, "/api/v1/releases?q="+longQuery, nil)); err == nil {
		t.Fatal("overlong search query was accepted")
	}
}

func TestProductionFrontendServesRootFallbackAndAsset404(t *testing.T) {
	handler := productionFrontendHandler()
	for _, requestPath := range []string{"/", "/releases/example"} {
		responseRecorder := httptest.NewRecorder()
		handler.ServeHTTP(responseRecorder, httptest.NewRequest(http.MethodGet, requestPath, nil))
		if responseRecorder.Code != http.StatusOK {
			t.Fatalf("expected frontend path %q to return 200, got %d", requestPath, responseRecorder.Code)
		}
		if !bytes.Contains(responseRecorder.Body.Bytes(), []byte("ROOMusic")) {
			t.Fatalf("frontend path %q did not serve the embedded index", requestPath)
		}
	}

	responseRecorder := httptest.NewRecorder()
	handler.ServeHTTP(responseRecorder, httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil))
	if responseRecorder.Code != http.StatusNotFound {
		t.Fatalf("missing production asset returned %d instead of 404", responseRecorder.Code)
	}
}

func TestAPIFallbackReturnsJSONErrors(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	applicationHandler := buildApplicationHandler(serverConfig{}, nil, logger)
	degradedHandler := buildHandler(logger, nil)
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
			var body apiError
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
		"application": buildApplicationHandler(serverConfig{}, nil, logger),
		"degraded":    buildHandler(logger, nil),
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
