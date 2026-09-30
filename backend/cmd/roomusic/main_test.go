package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
)

func TestProductionFrontendServesRootFallbackAndAsset404(t *testing.T) {
	handler := httpserver.Frontend(frontendSub())
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
