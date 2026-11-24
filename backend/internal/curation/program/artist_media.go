package program

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

// 单个源暂时不可用时仍试较低优先级的源。总闸门、取消及数据库错误中止本轮。
func artistSourceError(err error, retry *bool) error {
	switch {
	case err == nil, mediaMiss(err), failureCode(err) == "source_disabled", errors.Is(err, sources.ErrInvalidKey):
		return nil
	case errors.Is(err, sources.ErrTryLater), errors.Is(err, sources.ErrUnavailable), errors.Is(err, sources.ErrRateLimited):
		*retry = true
		return nil
	}
	return err
}

func (w *Worker) artistLinks(ctx context.Context, e artistEvidence, p artistProviders) ([]sources.ArtistLinks, bool, error) {
	var links []sources.ArtistLinks
	var retry bool
	needLinks := !e.Image && p.active["commons"] || !e.Bio && p.active["wikipedia"]
	for _, mbid := range e.MBIDs {
		entry := sources.ArtistLinks{MBID: mbid}
		if needLinks {
			for _, id := range []string{"musicbrainz", "wikidata"} {
				finder, ok := w.sources.ArtistLinkFinder(id)
				if !ok || !p.active[id] {
					continue
				}
				found, err := finder.ArtistLinks(ctx, entry)
				if err == nil {
					entry = found
				} else if err := artistSourceError(err, &retry); err != nil {
					return nil, false, err
				}
			}
		}
		links = append(links, entry)
	}
	return links, retry, nil
}

func (e artistEvidence) suppressed(kind, provider, externalID string) bool {
	hash := sha256.Sum256([]byte(provider + "\x00" + externalID))
	return slices.Contains(e.Markers, fmt.Sprintf("%s:%x", kind, hash))
}

func (u *unit) writeArtist(ctx context.Context, provider, step string, apply func(string, string) (curation.ExternalApplied, error)) (bool, error) {
	if err := u.w.sources.Check(ctx, provider); err != nil {
		return false, err
	}
	var result curation.ExternalApplied
	err := u.writeInBatch(ctx, step, func(batch, key string) error {
		var err error
		result, err = apply(batch, key)
		return err
	})
	if failureCode(err) == "evidence_changed" {
		return false, errReleaseChanged
	}
	if err != nil {
		return false, err
	}
	return result.OperationID != "" || result.Skipped == "occupied", nil
}

func (w *Worker) artistImage(ctx context.Context, u *unit, e artistEvidence, p artistProviders, links []sources.ArtistLinks) (filled, retry bool, err error) {
	for _, id := range []string{"fanarttv", "theaudiodb", "commons"} {
		f, ok := w.sources.ArtistImageFinder(id)
		if !ok || !p.active[id] {
			continue
		}
		for _, link := range links {
			if id == "commons" && link.CommonsFile == "" {
				continue
			}
			img, err := f.ArtistImage(ctx, link)
			if err == nil && img.ID != "" && !e.suppressed("artist_image", id, img.ID) {
				filled, err = u.writeArtist(ctx, id, "image/"+id+"/"+img.ID, func(batch, key string) (curation.ExternalApplied, error) {
					return w.writer.ApplyArtistImage(ctx, curation.ArtistImageRequest{ArtistID: e.ID, MBID: link.MBID, Image: img, BatchID: batch, Key: key})
				})
				if filled {
					return true, false, nil
				}
				// 图片字节可能损坏；校验失败后仍可试用下一来源。
				if failureCode(err) == "invalid_input" {
					continue
				}
			}
			if err := artistSourceError(err, &retry); err != nil {
				return false, false, err
			}
		}
	}
	return false, retry, nil
}

func (w *Worker) artistBio(ctx context.Context, u *unit, e artistEvidence, p artistProviders, links []sources.ArtistLinks) (filled, retry bool, err error) {
	for _, lang := range []string{"zh", "en"} {
		for _, id := range []string{"wikipedia", "theaudiodb"} {
			f, ok := w.sources.ArtistBioFinder(id)
			if !ok || !p.active[id] {
				continue
			}
			for _, link := range links {
				if id == "wikipedia" && (lang == "zh" && link.WikipediaZH == "" || lang == "en" && link.WikipediaEN == "") {
					continue
				}
				bio, err := f.ArtistBio(ctx, link, lang)
				if err == nil && bio.ID != "" && !e.suppressed("artist_bio", id, bio.ID) {
					filled, err = u.writeArtist(ctx, id, "bio/"+id+"/"+bio.ID, func(batch, key string) (curation.ExternalApplied, error) {
						return w.writer.ApplyArtistBio(ctx, curation.ArtistBioRequest{ArtistID: e.ID, MBID: link.MBID, Bio: bio, BatchID: batch, Key: key})
					})
					if filled {
						return true, false, nil
					}
					if failureCode(err) == "invalid_input" {
						continue
					}
				}
				if err := artistSourceError(err, &retry); err != nil {
					return false, false, err
				}
			}
		}
	}
	return false, retry, nil
}

func (w *Worker) enrichArtist(ctx context.Context, u *unit, e *artistEvidence, p artistProviders) (bool, error) {
	links, linksRetry, err := w.artistLinks(ctx, *e, p)
	if err != nil {
		return false, err
	}
	var imageRetry, bioRetry bool
	if !e.Image {
		e.Image, imageRetry, err = w.artistImage(ctx, u, *e, p, links)
		if err != nil {
			return false, err
		}
	}
	if !e.Bio {
		e.Bio, bioRetry, err = w.artistBio(ctx, u, *e, p, links)
		if err != nil {
			return false, err
		}
	}
	return imageRetry || bioRetry || linksRetry && (!e.Image && p.active["commons"] || !e.Bio && p.active["wikipedia"]), nil
}
