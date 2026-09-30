package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestPasswordHashUsesBcryptAndVerifies(t *testing.T) {
	passwordHash, err := HashPassword("a sufficiently long password")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if !strings.HasPrefix(passwordHash, "$2") {
		t.Fatalf("expected bcrypt hash, got %q", passwordHash)
	}
	if !VerifyPassword(passwordHash, "a sufficiently long password") {
		t.Fatal("bcrypt hash did not verify")
	}
	if VerifyPassword(passwordHash, "wrong password") {
		t.Fatal("wrong password verified")
	}
}

func TestVerifyPasswordSupportsLegacyHash(t *testing.T) {
	salt := "legacy-salt"
	digest := sha256.Sum256([]byte(salt + "legacy password"))
	legacyHash := "sha256$" + salt + "$" + hex.EncodeToString(digest[:])
	if !VerifyPassword(legacyHash, "legacy password") {
		t.Fatal("legacy hash did not verify")
	}
}
