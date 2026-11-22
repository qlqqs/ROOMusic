package curation

import (
	"context"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/sources"
)

type guardedFinders struct {
	registration SourceRegistration
	gate         *Sources
}

func (g guardedFinders) background(ctx context.Context) (context.Context, error) {
	permit := func(ctx context.Context) error { return g.gate.Check(ctx, g.registration.Declaration.ID) }
	if err := permit(ctx); err != nil {
		return nil, err
	}
	return sources.WithBackground(ctx, permit), nil
}

type guardedRelease struct{ guardedFinders }

func (s *Sources) ReleaseFinder(sourceID string) (sources.ReleaseFinder, bool) {
	r, ok := s.lookup(sourceID)
	if !ok || r.Releases == nil || !slices.Contains(r.Declaration.Capabilities, sources.ReleaseLookup) {
		return nil, false
	}
	return guardedRelease{guardedFinders{registration: r, gate: s}}, true
}

func (g guardedRelease) ReleaseByID(ctx context.Context, id string) (sources.Release, error) {
	ctx, err := g.background(ctx)
	if err != nil {
		return sources.Release{}, err
	}
	return g.registration.Releases.ReleaseByID(ctx, id)
}

func (g guardedRelease) ReleasesByBarcode(ctx context.Context, barcode string) ([]sources.Release, error) {
	ctx, err := g.background(ctx)
	if err != nil {
		return nil, err
	}
	return g.registration.Releases.ReleasesByBarcode(ctx, barcode)
}

func (g guardedRelease) ReleasesByCatalog(ctx context.Context, catalogNumber, label string) ([]sources.Release, error) {
	ctx, err := g.background(ctx)
	if err != nil {
		return nil, err
	}
	return g.registration.Releases.ReleasesByCatalog(ctx, catalogNumber, label)
}

func (g guardedRelease) ReleasesByDisc(ctx context.Context, toc sources.DiscTOC) (sources.DiscMatch, error) {
	ctx, err := g.background(ctx)
	if err != nil {
		return sources.DiscMatch{}, err
	}
	return g.registration.Releases.ReleasesByDisc(ctx, toc)
}

func (g guardedRelease) SearchReleases(ctx context.Context, query sources.ReleaseQuery) ([]sources.Release, error) {
	ctx, err := g.background(ctx)
	if err != nil {
		return nil, err
	}
	return g.registration.Releases.SearchReleases(ctx, query)
}

type guardedRecording struct{ guardedFinders }

func (s *Sources) RecordingFinder(sourceID string) (sources.RecordingFinder, bool) {
	r, ok := s.lookup(sourceID)
	if !ok || r.Recordings == nil || !slices.Contains(r.Declaration.Capabilities, sources.RecordingLookup) {
		return nil, false
	}
	return guardedRecording{guardedFinders{registration: r, gate: s}}, true
}

func (g guardedRecording) RecordingByID(ctx context.Context, id string) (sources.Recording, error) {
	ctx, err := g.background(ctx)
	if err != nil {
		return sources.Recording{}, err
	}
	return g.registration.Recordings.RecordingByID(ctx, id)
}

func (g guardedRecording) RecordingsByISRC(ctx context.Context, isrc string) ([]sources.Recording, error) {
	ctx, err := g.background(ctx)
	if err != nil {
		return nil, err
	}
	return g.registration.Recordings.RecordingsByISRC(ctx, isrc)
}

func (g guardedRecording) SearchRecordings(ctx context.Context, query sources.RecordingQuery) ([]sources.Recording, error) {
	ctx, err := g.background(ctx)
	if err != nil {
		return nil, err
	}
	return g.registration.Recordings.SearchRecordings(ctx, query)
}

type guardedArtist struct{ guardedFinders }

func (s *Sources) ArtistFinder(sourceID string) (sources.ArtistFinder, bool) {
	r, ok := s.lookup(sourceID)
	if !ok || r.Artists == nil || !slices.Contains(r.Declaration.Capabilities, sources.ArtistLookup) {
		return nil, false
	}
	return guardedArtist{guardedFinders{registration: r, gate: s}}, true
}

func (g guardedArtist) ArtistByID(ctx context.Context, id string) (sources.Artist, error) {
	ctx, err := g.background(ctx)
	if err != nil {
		return sources.Artist{}, err
	}
	return g.registration.Artists.ArtistByID(ctx, id)
}

func (g guardedArtist) SearchArtists(ctx context.Context, name string) ([]sources.Artist, error) {
	ctx, err := g.background(ctx)
	if err != nil {
		return nil, err
	}
	return g.registration.Artists.SearchArtists(ctx, name)
}
