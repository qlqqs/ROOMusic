package governance

import (
	"context"
	"testing"
)

func TestClaimHintsBatchBounds(t *testing.T) {
	store := NewStore(nil)
	got, err := store.LoadClaimHintsBatch(context.Background(), nil)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty batch = %#v, %v", got, err)
	}
	for _, ids := range [][]string{{"invalid"}, make([]string, 101)} {
		if _, err := store.LoadClaimHintsBatch(context.Background(), ids); err == nil {
			t.Fatal("invalid batch accepted")
		}
	}
}
