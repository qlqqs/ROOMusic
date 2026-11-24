package curation

import (
	"context"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/sources"
)

type guardedArtistMedia struct{ guardedFinders }

func (s *Sources) ArtistLinkFinder(id string) (sources.ArtistLinkFinder, bool) {
	r, ok := s.lookup(id)
	if !ok || r.ArtistLinks == nil || !slices.Contains(r.Declaration.Capabilities, sources.ArtistLookup) {
		return nil, false
	}
	return guardedArtistMedia{guardedFinders{r, s}}, true
}

func (s *Sources) ArtistImageFinder(id string) (sources.ArtistImageFinder, bool) {
	r, ok := s.lookup(id)
	if !ok || r.ArtistImages == nil || !slices.Contains(r.Declaration.Capabilities, sources.ArtistImageLookup) {
		return nil, false
	}
	return guardedArtistMedia{guardedFinders{r, s}}, true
}

func (s *Sources) ArtistBioFinder(id string) (sources.ArtistBioFinder, bool) {
	r, ok := s.lookup(id)
	if !ok || r.ArtistBios == nil || !slices.Contains(r.Declaration.Capabilities, sources.ArtistBioLookup) {
		return nil, false
	}
	return guardedArtistMedia{guardedFinders{r, s}}, true
}

func (g guardedArtistMedia) ArtistLinks(ctx context.Context, links sources.ArtistLinks) (sources.ArtistLinks, error) {
	ctx, err := g.background(ctx)
	if err != nil {
		return links, err
	}
	return g.registration.ArtistLinks.ArtistLinks(ctx, links)
}

func (g guardedArtistMedia) ArtistImage(ctx context.Context, links sources.ArtistLinks) (sources.ArtistImage, error) {
	return withSourceKeys(ctx, g.guardedFinders, func(ctx context.Context) (sources.ArtistImage, error) {
		return g.registration.ArtistImages.ArtistImage(ctx, links)
	})
}

func (g guardedArtistMedia) ArtistBio(ctx context.Context, links sources.ArtistLinks, lang string) (sources.ArtistBio, error) {
	return withSourceKeys(ctx, g.guardedFinders, func(ctx context.Context) (sources.ArtistBio, error) {
		return g.registration.ArtistBios.ArtistBio(ctx, links, lang)
	})
}
