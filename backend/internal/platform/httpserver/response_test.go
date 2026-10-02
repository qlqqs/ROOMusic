package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteJSONNeverAllowsCaching(t *testing.T) {
	success := httptest.NewRecorder()
	WriteJSON(success, http.StatusOK, map[string]string{"status": "ok"})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/anything", nil)
	failure := httptest.NewRecorder()
	WriteError(failure, request, http.StatusNotFound, "not_found", "不存在")

	for name, response := range map[string]*httptest.ResponseRecorder{"success": success, "error envelope": failure} {
		if got := response.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("%s: Cache-Control = %q, want no-store", name, got)
		}
		if got := response.Header().Get("Content-Type"); got != "application/json" {
			t.Fatalf("%s: Content-Type = %q", name, got)
		}
	}
}

func TestHealthEndpointsAreNotCacheable(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"healthz":       HealthHandler,
		"readyz ready":  ReadyHandler(func() bool { return true }),
		"readyz absent": ReadyHandler(func() bool { return false }),
	} {
		response := httptest.NewRecorder()
		handler(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if got := response.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("%s: Cache-Control = %q, want no-store", name, got)
		}
	}
}
