package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPasswordHashUsesBcryptAndVerifies(t *testing.T) {
	passwordHash, err := hashPassword("a sufficiently long password")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if !strings.HasPrefix(passwordHash, "$2") {
		t.Fatalf("expected bcrypt hash, got %q", passwordHash)
	}
	if !verifyPassword(passwordHash, "a sufficiently long password") {
		t.Fatal("bcrypt hash did not verify")
	}
	if verifyPassword(passwordHash, "wrong password") {
		t.Fatal("wrong password verified")
	}
}

func TestVerifyPasswordSupportsLegacyHash(t *testing.T) {
	salt := "legacy-salt"
	digest := sha256.Sum256([]byte(salt + "legacy password"))
	legacyHash := "sha256$" + salt + "$" + hex.EncodeToString(digest[:])
	if !verifyPassword(legacyHash, "legacy password") {
		t.Fatal("legacy hash did not verify")
	}
}

func TestRequestIDMiddlewareRejectsUnboundedInput(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set("X-Request-ID", strings.Repeat("x", 65))
	response := httptest.NewRecorder()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := requestIDMiddleware(logger, http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.WriteHeader(http.StatusNoContent)
	}))
	handler.ServeHTTP(response, request)
	if response.Header().Get("X-Request-ID") == strings.Repeat("x", 65) {
		t.Fatal("unbounded request ID was accepted")
	}
}

func TestRequestIDMiddlewareLogsCompletion(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/releases/{id}", func(w http.ResponseWriter, r *http.Request) {
		if got := r.PathValue("id"); got != "release-42" {
			t.Fatalf("path value = %q", got)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/releases/release-42?token=secret", strings.NewReader("password=secret"))
	request.Header.Set("X-Request-ID", "req-test")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-secret"})
	response := httptest.NewRecorder()
	requestIDMiddleware(logger, mux).ServeHTTP(response, request)
	if response.Code != http.StatusCreated || response.Body.String() != "ok" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if response.Header().Get("X-Request-ID") != "req-test" {
		t.Fatal("request ID response header missing")
	}
	var event map[string]any
	if err := json.Unmarshal(logs.Bytes(), &event); err != nil {
		t.Fatalf("decode log: %v (%s)", err, logs.String())
	}
	checks := map[string]any{
		"event":          "http.request.completed",
		"module":         "platform",
		"message":        "http request completed",
		"request_id":     "req-test",
		"method":         "GET",
		"route_template": "GET /api/v1/releases/{id}",
		"status":         float64(http.StatusCreated),
		"response_bytes": float64(2),
	}
	for key, want := range checks {
		if event[key] != want {
			t.Errorf("%s = %#v, want %#v", key, event[key], want)
		}
	}
	duration, ok := event["duration_ms"].(float64)
	if !ok || duration < 0 {
		t.Errorf("duration_ms = %#v, want non-negative number", event["duration_ms"])
	}
	if _, exists := event["actor_id"]; exists {
		t.Error("anonymous request unexpectedly has actor_id")
	}
	for _, secret := range []string{"token=secret", "password=secret", "session-secret"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("log contains sensitive value %q: %s", secret, logs.String())
		}
	}
}

func TestRequestIDMiddlewareLogsImplicitAndErrorResponses(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	requestIDMiddleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/implicit":
			_, _ = w.Write([]byte("hello"))
		case "/error":
			http.Error(w, "bad", http.StatusBadRequest)
		}
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/implicit", nil))
	requestIDMiddleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusBadRequest)
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/error", nil))
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d log events, want 2: %s", len(lines), logs.String())
	}
	for i, line := range lines {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode event %d: %v", i, err)
		}
		if event["event"] != "http.request.completed" {
			t.Errorf("event %d name = %#v", i, event["event"])
		}
		status := event["status"].(float64)
		if i == 0 && status != 200 {
			t.Errorf("implicit status = %v", status)
		}
		if i == 1 && status != 400 {
			t.Errorf("error status = %v", status)
		}
		if i == 1 && event["level"] != "WARN" {
			t.Errorf("error level = %#v", event["level"])
		}
	}
}

func TestRequestObservationActorID(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	obs := &requestObservation{}
	request = request.WithContext(context.WithValue(request.Context(), requestObservationKey{}, obs))
	requestIDMiddleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Context().Value(requestObservationKey{}).(*requestObservation).actorID = "user-123"
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(httptest.NewRecorder(), request)
	if !strings.Contains(logs.String(), `"actor_id":"user-123"`) {
		t.Fatalf("actor ID missing from log: %s", logs.String())
	}
}
