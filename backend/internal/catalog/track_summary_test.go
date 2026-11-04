package catalog

import (
	"database/sql"
	"testing"
)

func TestPositiveAudioInt(t *testing.T) {
	for _, value := range []sql.NullInt64{{}, {Int64: 0, Valid: true}, {Int64: -1, Valid: true}} {
		if got := positiveAudioInt(value); got != nil {
			t.Fatalf("unknown fact became %d", *got)
		}
	}
	if got := positiveAudioInt(sql.NullInt64{Int64: 24, Valid: true}); got == nil || *got != 24 {
		t.Fatalf("positive fact: %v", got)
	}
}
