package token

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTokenGenerateAndHash(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		plaintext := Generate()
		if len(plaintext) != 47 || !strings.HasPrefix(plaintext, "rmt_") {
			t.Fatal("明文格式不正确")
		}
		decoded, err := base64.RawURLEncoding.DecodeString(plaintext[4:])
		if err != nil || len(decoded) != 32 || seen[plaintext] {
			t.Fatal("随机部分无效或重复")
		}
		seen[plaintext] = true
		expected := sha256.Sum256([]byte(plaintext))
		if !bytes.Equal(Hash(plaintext), expected[:]) || bytes.Equal(Hash(plaintext), Hash(plaintext+"x")) {
			t.Fatal("SHA-256 摘要不正确")
		}
	}
}

func TestTokenIssueRejectsInvalidInputBeforeStorage(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	for _, tc := range []struct {
		name  string
		input IssueInput
	}{
		{"empty", IssueInput{Name: "", Scope: ScopeRead}},
		{"whitespace", IssueInput{Name: " \t\n", Scope: ScopeRead}},
		{"65-runes", IssueInput{Name: strings.Repeat("名", 65), Scope: ScopeRead}},
		{"nul", IssueInput{Name: "a\x00b", Scope: ScopeRead}},
		{"utf8", IssueInput{Name: string([]byte{0xff}), Scope: ScopeRead}},
		{"scope-empty", IssueInput{Name: "合成", Scope: ""}},
		{"scope-unknown", IssueInput{Name: "合成", Scope: "write"}},
		{"expired", IssueInput{Name: "合成", Scope: ScopeRead, ExpiresAt: &past}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry, plaintext, err := NewStore(nil, nil).Issue(context.Background(), "unused", tc.input)
			if !errors.Is(err, ErrInvalidInput) || plaintext != "" || entry.ID != "" {
				t.Fatalf("无效输入返回 %v", err)
			}
		})
	}
}
