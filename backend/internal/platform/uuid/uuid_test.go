package uuid

import "testing"

func TestRESTIdentifiersAreValidatedBeforeDatabaseCasts(t *testing.T) {
	if !Valid("01234567-89ab-cdef-0123-456789abcdef") {
		t.Fatal("valid UUID-shaped identifier was rejected")
	}
	for _, value := range []string{"", "release-1", "01234567-89ab-cdef-0123-456789abcdeg", "0123456789abcdef0123456789abcdef"} {
		if Valid(value) {
			t.Errorf("malformed identifier %q was accepted", value)
		}
	}
}

func TestNewReturnsValidVersion4Identifiers(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		value := New()
		if !Valid(value) || value[14] != '4' || seen[value] {
			t.Fatalf("New() returned %q (valid=%v, duplicate=%v)", value, Valid(value), seen[value])
		}
		seen[value] = true
	}
}
