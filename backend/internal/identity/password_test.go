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

func TestValidPassword(t *testing.T) {
	cases := []struct {
		name     string
		password string
		valid    bool
	}{
		{"empty", "", false},
		{"11 ASCII characters", strings.Repeat("a", 11), false},
		{"12 ASCII characters", strings.Repeat("a", 12), true},
		{"72 ASCII characters", strings.Repeat("a", 72), true},
		{"73 ASCII characters", strings.Repeat("a", 73), false},
		{"Chinese", strings.Repeat("字", 4), false},
		{"Chinese at 72 bytes", strings.Repeat("字", 24), false},
		{"mixed Chinese", "abcdefghijk字", false},
		{"fullwidth", "abcdefghijkＡ", false},
		{"emoji", "abcdefghijk😀", false},
		{"tab", "abcdefghijk\t", false},
		{"newline", "abcdefghijk\n", false},
		{"carriage return", "abcdefghijk\r", false},
		{"NUL", "abcdefghijk\x00", false},
		{"below printable ASCII", "abcdefghijk\x1f", false},
		{"DEL", "abcdefghijk\x7f", false},
		{"non-ASCII byte", "abcdefghijk\x80", false},
		{"spaces", " Abc123!@#~  ", true},
		{"only spaces", strings.Repeat(" ", 12), true},
		{"symbols", "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := ValidPassword(testCase.password); got != testCase.valid {
				t.Errorf("ValidPassword = %v, want %v", got, testCase.valid)
			}
		})
	}
}

// bcrypt 对超过 72 字节的输入返回错误；MaxPasswordLength 必须与之一致，否则过长的密码会变成 500。
func TestMaxPasswordLengthMatchesBcryptLimit(t *testing.T) {
	if _, err := HashPassword(strings.Repeat("a", MaxPasswordLength)); err != nil {
		t.Fatalf("hash a password of MaxPasswordLength: %v", err)
	}
	if _, err := HashPassword(strings.Repeat("a", MaxPasswordLength+1)); err == nil {
		t.Fatal("bcrypt accepted a password longer than MaxPasswordLength; raise MaxPasswordLength or revisit the rule")
	}
}
