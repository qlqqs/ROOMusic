package catalog

import (
	"context"
	"testing"
)

func TestScanAddedAlbumsPaginationBounds(t *testing.T) {
	store := NewStore(nil)
	for _, test := range [][2]int{{0, 20}, {1, 0}, {1, 101}, {int(^uint(0) >> 1), 100}} {
		if _, err := store.ListScanAddedAlbums(context.Background(), "unused", test[0], test[1]); err == nil {
			t.Fatalf("pagination accepted: %v", test)
		}
	}
}
