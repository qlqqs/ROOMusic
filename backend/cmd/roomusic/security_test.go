package main

import (
	"crypto/sha256"
	"encoding/hex"
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
