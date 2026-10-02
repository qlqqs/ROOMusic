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

func TestValidPasswordLengthCountsUTF8Bytes(t *testing.T) {
	cases := []struct {
		name     string
		password string
		valid    bool
	}{
		{"empty", "", false},
		{"11 bytes", strings.Repeat("a", 11), false},
		{"12 bytes", strings.Repeat("a", 12), true},
		{"72 bytes", strings.Repeat("a", 72), true},
		{"73 bytes", strings.Repeat("a", 73), false},
		{"3 CJK characters are 9 bytes", strings.Repeat("字", 3), false},
		{"4 CJK characters are 12 bytes", strings.Repeat("字", 4), true},
		{"24 CJK characters are 72 bytes", strings.Repeat("字", 24), true},
		{"25 CJK characters are 75 bytes", strings.Repeat("字", 25), false},
	}
	for _, testCase := range cases {
		if got := ValidPasswordLength(testCase.password); got != testCase.valid {
			t.Errorf("%s: ValidPasswordLength = %v, want %v", testCase.name, got, testCase.valid)
		}
	}
}

// bcrypt 对超过 72 字节的输入返回错误；MaxPasswordBytes 必须与之一致，否则过长的密码会变成 500。
func TestMaxPasswordBytesMatchesBcryptLimit(t *testing.T) {
	if _, err := HashPassword(strings.Repeat("a", MaxPasswordBytes)); err != nil {
		t.Fatalf("hash a password of MaxPasswordBytes: %v", err)
	}
	if _, err := HashPassword(strings.Repeat("a", MaxPasswordBytes+1)); err == nil {
		t.Fatal("bcrypt accepted a password longer than MaxPasswordBytes; raise MaxPasswordBytes or revisit the rule")
	}
}
