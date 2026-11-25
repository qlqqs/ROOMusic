package secret

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func secretDirectory(t *testing.T) string {
	t.Helper()
	base := filepath.Join("..", "..", "..", "..", "temp")
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(base, "steward-secret-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	return dir
}
func TestSecretCreatesPrivateKeyAndRoundTrips(t *testing.T) {
	path := filepath.Join(secretDirectory(t), "instance.key")
	s := New(path)
	plain := []byte("synthetic-key-1234")
	encrypted, err := s.Seal("user-a", "model", plain)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 || info.Size() != 32 {
		t.Fatalf("key mode=%v size=%d", info.Mode(), info.Size())
	}
	for _, store := range []*Store{s, New(path)} {
		got, err := store.Open("user-a", "model", encrypted)
		if err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("round trip = %q %v", got, err)
		}
	}
	second, err := s.Seal("user-a", "model", plain)
	if err != nil || bytes.Equal(encrypted, second) || bytes.Contains(encrypted, plain) {
		t.Fatal("ciphertext must use a fresh nonce and hide plaintext")
	}
}
func TestSecretBindingTamperAndReplacementHaveOneError(t *testing.T) {
	dir := secretDirectory(t)
	path := filepath.Join(dir, "instance.key")
	s := New(path)
	raw, err := s.Seal("ab", "c", []byte("synthetic-key"))
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Clone(raw)
	tampered[len(tampered)-1] ^= 1
	for _, tc := range []struct {
		name, user, purpose string
		data                []byte
		store               *Store
	}{{"user", "other", "c", raw, s}, {"purpose", "ab", "other", raw, s}, {"ambiguous_binding", "a", "bc", raw, s}, {"tampered", "ab", "c", tampered, s}, {"truncated", "ab", "c", raw[:3], s}, {"other_key", "ab", "c", raw, New(filepath.Join(dir, "other.key"))}} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.store.Open(tc.user, tc.purpose, tc.data)
			if !errors.Is(err, ErrCannotOpen) || len(got) != 0 {
				t.Fatalf("got %q %v", got, err)
			}
		})
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := New(path).Open("ab", "c", raw); !errors.Is(err, ErrCannotOpen) {
		t.Fatalf("lost key = %v", err)
	}
}
func TestLastFour(t *testing.T) {
	for _, tc := range []struct{ input, want string }{{"", ""}, {"abc", "abc"}, {"synthetic-1234", "1234"}, {"一二三四五", "二三四五"}} {
		if got := LastFour(tc.input); got != tc.want {
			t.Errorf("LastFour(%q)=%q want %q", tc.input, got, tc.want)
		}
	}
}

func TestSecretOpenMissingKeyNeverCreatesFile(t *testing.T) {
	original := filepath.Join(secretDirectory(t), "original.key")
	raw, err := New(original).Seal("synthetic-user", "model", []byte("synthetic-model-key"))
	if err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(secretDirectory(t), "missing-parent", "instance.key")
	for i := 0; i < 2; i++ {
		if _, err := New(missing).Open("synthetic-user", "model", raw); !errors.Is(err, ErrCannotOpen) {
			t.Fatalf("解密=%v", err)
		}
		if _, err := os.Stat(filepath.Dir(missing)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("解密创建了目录或文件：%v", err)
		}
	}
}
