package catalog

import (
	"context"
	"testing"
)

func TestTrackCollectionStatsInputBounds(t *testing.T) {
	store := NewStore(nil)
	for _, ids := range [][]string{nil, {}, {"invalid", ""}} {
		if got, err := store.LoadTrackCollectionStats(context.Background(), ids); err != nil || got != (TrackCollectionStats{}) {
			t.Fatalf("empty stats: %+v %v", got, err)
		}
	}
	if _, err := store.LoadTrackCollectionStats(context.Background(), personalTestIDs(10001)); err == nil {
		t.Fatal("accepted over-limit stats batch")
	}
}
