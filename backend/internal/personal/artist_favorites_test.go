package personal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

type artistCatalogStub struct {
	resolve   func(context.Context, []string) (map[string]string, error)
	summaries func(context.Context, []string) (map[string]catalog.ArtistSummary, error)
}

func (s artistCatalogStub) ResolveArtists(ctx context.Context, ids []string) (map[string]string, error) {
	return s.resolve(ctx, ids)
}
func (s artistCatalogStub) LoadArtistSummaries(ctx context.Context, ids []string) (map[string]catalog.ArtistSummary, error) {
	return s.summaries(ctx, ids)
}

func TestArtistFavoritesPreparingAndLegacyLookup(t *testing.T) {
	probe := &readTxDriverProbe{}
	service := NewService(openReadTxProbe(t, probe))
	calls := 0
	service.artistCatalogFor = func(*sql.Tx) ArtistCatalog {
		return artistCatalogStub{resolve: func(_ context.Context, ids []string) (map[string]string, error) {
			calls++
			return nil, catalog.ErrArtistCatalogPreparing
		}}
	}
	ctx := context.Background()
	user := "10000000-0000-4000-8000-000000000001"
	for _, groups := range [][][]string{nil, {nil}, {[]string{}}} {
		got, err := service.LookupFavorites(ctx, user, nil, nil, groups...)
		if err != nil || got.Artists == nil || len(got.Artists) != 0 || calls != 0 {
			t.Fatalf("legacy lookup: %+v %v calls=%d", got, err, calls)
		}
	}
	if _, err := service.LookupFavorites(ctx, user, nil, nil, []string{user}); !errors.Is(err, ErrArtistCatalogPreparing) {
		t.Fatalf("lookup preparing: %v", err)
	}
	if _, err := service.FavoriteArtists(ctx, user, Page{Number: 1, Size: 50}); !errors.Is(err, ErrArtistCatalogPreparing) {
		t.Fatalf("empty collection preparing: %v", err)
	}
	if calls != 2 {
		t.Fatalf("readiness calls=%d", calls)
	}
	if probe.commits != 3 || probe.rollbacks != 2 {
		t.Fatalf("transactions: %+v", probe)
	}
}

func TestArtistFavoritesInputAndErrorClassification(t *testing.T) {
	service := NewService(nil)
	ctx := context.Background()
	for _, groups := range [][][]string{{nil, nil}, {{"invalid"}}, {make([]string, 1001)}} {
		if _, err := service.LookupFavorites(ctx, "", nil, nil, groups...); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid lookup: %v", err)
		}
	}
	if _, err := service.SetFavorite(ctx, "", "artist", "invalid", true); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid target: %v", err)
	}
	if _, err := service.FavoriteArtists(ctx, "", Page{Number: 1, Size: 101}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid page: %v", err)
	}
	wrapped := fmt.Errorf("context: %w", catalog.ErrArtistCatalogPreparing)
	if !errors.Is(artistCatalogError(wrapped, false), ErrArtistCatalogPreparing) || !errors.Is(artistCatalogError(wrapped, true), ErrArtistCatalogNotReady) {
		t.Fatal("read/write readiness errors differ")
	}
	failure := errors.New("database failure")
	if artistCatalogError(failure, false) != failure {
		t.Fatal("database error replaced")
	}
}

func TestArtistResolutionMappingBatches(t *testing.T) {
	mapping := map[string]string{}
	for i := 0; i < 2001; i++ {
		mapping[fmt.Sprintf("abcdef00-0000-4000-8000-%012d", i)] = "10000000-0000-4000-8000-000000000001"
	}
	batches, err := artistResolutionBatches(mapping)
	if err != nil || len(batches) != 3 || len(batches[0].originals) != 1000 || len(batches[1].originals) != 1000 || len(batches[2].originals) != 1 {
		t.Fatalf("batches: %d %v", len(batches), err)
	}
	previous := ""
	for _, batch := range batches {
		for i, id := range batch.originals {
			if id <= previous || batch.resolved[i] != mapping[id] {
				t.Fatal("mapping lost or unstable ordering")
			}
			previous = id
		}
	}
	id := "abcdef00-0000-4000-8000-000000000001"
	target := "10000000-0000-4000-8000-000000000001"
	batches, err = artistResolutionBatches(map[string]string{id: target, strings.ToUpper(id): target})
	if err != nil || len(batches) != 1 || len(batches[0].originals) != 1 {
		t.Fatalf("normalization: %+v %v", batches, err)
	}
	for _, invalid := range []map[string]string{{"bad": target}, {id: "bad"}, {id: target, strings.ToUpper(id): id}} {
		if _, err := artistResolutionBatches(invalid); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid mapping: %v", err)
		}
	}
	if err := RefreshArtistResolution(context.Background(), nil, nil); err != nil {
		t.Fatalf("empty refresh: %v", err)
	}
}
