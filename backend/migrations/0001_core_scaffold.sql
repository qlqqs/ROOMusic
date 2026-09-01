-- Core 0 的首个迁移仅建立迁移可用性记录；业务表由对应纵向切片添加。
CREATE TABLE IF NOT EXISTS schema_migrations (
    version BIGINT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO schema_migrations (version)
VALUES (1)
ON CONFLICT (version) DO NOTHING;
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthHandlerReportsHealthyProcess(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	responseRecorder := httptest.NewRecorder()

	healthHandler(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, responseRecorder.Code)
	}
	if responseRecorder.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("unexpected health response: %s", responseRecorder.Body.String())
	}
}

func TestReadinessHandlerDoesNotClaimReadyBeforeDatabaseWiring(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	responseRecorder := httptest.NewRecorder()

	readinessHandler(responseRecorder, request)

	if responseRecorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, responseRecorder.Code)
	}
}
.PHONY: backend-test backend-build frontend-install frontend-check frontend-build

backend-test:
	cd backend && go test ./...

backend-build:
	cd backend && go build ./...

frontend-install:
	cd frontend && npm install

frontend-check:
	cd frontend && npm run lint && npm run typecheck && npm run test

frontend-build:
	cd frontend && npm run build
