package curation

import (
	"context"
	"errors"
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
	base := guardedRelease{guardedFinders{registration: r, gate: s}}
	if pages, ok := r.Releases.(ReleasePageFinder); ok {
		return guardedReleasePages{guardedRelease: base, pages: pages}, true
	}
	return base, true
}

type guardedReleasePages struct {
	guardedRelease
	pages ReleasePageFinder
}

func (g guardedReleasePages) SearchReleasePage(ctx context.Context, query ReleaseSearch) (ReleasePage, error) {
	ctx, err := g.background(ctx)
	if err != nil {
		return ReleasePage{}, err
	}
	return g.pages.SearchReleasePage(ctx, query)
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
	base := guardedRecording{guardedFinders{registration: r, gate: s}}
	if pages, ok := r.Recordings.(RecordingPageFinder); ok {
		return guardedRecordingPages{guardedRecording: base, pages: pages}, true
	}
	return base, true
}

type guardedRecordingPages struct {
	guardedRecording
	pages RecordingPageFinder
}

func (g guardedRecordingPages) SearchRecordingPage(ctx context.Context, query RecordingSearch) (RecordingPage, error) {
	ctx, err := g.background(ctx)
	if err != nil {
		return RecordingPage{}, err
	}
	return g.pages.SearchRecordingPage(ctx, query)
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
	base := guardedArtist{guardedFinders{registration: r, gate: s}}
	if pages, ok := r.Artists.(ArtistPageFinder); ok {
		return guardedArtistPages{guardedArtist: base, pages: pages}, true
	}
	return base, true
}

type guardedArtistPages struct {
	guardedArtist
	pages ArtistPageFinder
}

func (g guardedArtistPages) SearchArtistPage(ctx context.Context, query string) (ArtistPage, error) {
	ctx, err := g.background(ctx)
	if err != nil {
		return ArtistPage{}, err
	}
	return g.pages.SearchArtistPage(ctx, query)
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

type guardedCover struct{ guardedFinders }

func (s *Sources) CoverFinder(sourceID string) (sources.CoverFinder, bool) {
	r, ok := s.lookup(sourceID)
	if !ok || r.Covers == nil || !slices.Contains(r.Declaration.Capabilities, sources.CoverArt) {
		return nil, false
	}
	return guardedCover{guardedFinders{registration: r, gate: s}}, true
}

func (g guardedCover) Covers(ctx context.Context, target sources.CoverTarget) ([]sources.CoverImage, error) {
	return withSourceKeys(ctx, g.guardedFinders, func(ctx context.Context) ([]sources.CoverImage, error) {
		return g.registration.Covers.Covers(ctx, target)
	})
}

func (g guardedCover) FrontCover(ctx context.Context, target sources.CoverTarget) (sources.CoverFile, error) {
	return withSourceKeys(ctx, g.guardedFinders, func(ctx context.Context) (sources.CoverFile, error) {
		return g.registration.Covers.FrontCover(ctx, target)
	})
}

type guardedLyrics struct{ guardedFinders }

func (s *Sources) LyricsFinder(sourceID string) (sources.LyricsFinder, bool) {
	r, ok := s.lookup(sourceID)
	if !ok || r.Lyrics == nil || !slices.Contains(r.Declaration.Capabilities, sources.LyricsLookup) {
		return nil, false
	}
	return guardedLyrics{guardedFinders{registration: r, gate: s}}, true
}

func (g guardedLyrics) LyricsFor(ctx context.Context, query sources.LyricsQuery) (sources.Lyrics, error) {
	return withSourceKeys(ctx, g.guardedFinders, func(ctx context.Context) (sources.Lyrics, error) { return g.registration.Lyrics.LyricsFor(ctx, query) })
}

func (g guardedLyrics) SearchLyrics(ctx context.Context, query sources.LyricsQuery) ([]sources.Lyrics, error) {
	ctx, err := g.background(ctx)
	if err != nil {
		return nil, err
	}
	return g.registration.Lyrics.SearchLyrics(ctx, query)
}

type guardedFingerprint struct{ guardedFinders }

func (s *Sources) FingerprintFinder(sourceID string) (sources.FingerprintFinder, bool) {
	r, ok := s.lookup(sourceID)
	if !ok || r.Fingerprints == nil || !slices.Contains(r.Declaration.Capabilities, sources.FingerprintLookup) {
		return nil, false
	}
	return guardedFingerprint{guardedFinders{registration: r, gate: s}}, true
}

func (g guardedFingerprint) LookupFingerprint(ctx context.Context, fp sources.Fingerprint) ([]sources.FingerprintMatch, error) {
	return withSourceKeys(ctx, g.guardedFinders, func(ctx context.Context) ([]sources.FingerprintMatch, error) {
		return g.registration.Fingerprints.LookupFingerprint(ctx, fp)
	})
}

func withSourceKeys[T any](ctx context.Context, g guardedFinders, lookup func(context.Context) (T, error)) (T, error) {
	var zero T
	ctx, err := g.background(ctx)
	if err != nil {
		return zero, err
	}
	used := make(map[string]keyUse)
	rows, err := lookup(context.WithValue(ctx, keyUseContext{}, used))
	var invalid *sources.InvalidKeyError
	status := "valid"
	if errors.As(err, &invalid) {
		status = "invalid"
		d := g.registration.Declaration
		if _, adminKey := used[invalid.Name]; !adminKey && slices.Contains(d.OptionalKeys, invalid.Name) {
			g.gate.rejectedDefaults.Store(sourceKeyPurpose(d.ID, invalid.Name), true)
		}
	} else if err != nil {
		return rows, err
	}
	var names []string
	var ciphertexts [][]byte
	for _, name := range g.registration.Declaration.Keys {
		key, ok := used[name]
		if !ok || (invalid != nil && invalid.Name != name) || (invalid == nil && key.status != "unchecked") {
			continue
		}
		names = append(names, name)
		ciphertexts = append(ciphertexts, key.ciphertext)
	}
	if len(names) > 0 {
		if writeErr := g.gate.markKeys(ctx, g.registration.Declaration.ID, names, ciphertexts, status); writeErr != nil {
			return zero, writeErr
		}
	}
	return rows, err
}
