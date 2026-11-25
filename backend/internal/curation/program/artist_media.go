package program

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

// 记录仍有机会补齐或升级当前资料的来源。其它来源照常回退。
type mediaFailure struct {
	rank int
	err  error
}

type mediaFailures []mediaFailure

func (f *mediaFailures) record(ctx context.Context, source string, rank int, err error) error {
	switch {
	case err == nil, mediaMiss(err), failureCode(err) == "source_disabled", errors.Is(err, sources.ErrInvalidKey):
		return nil
	case curation.IsSourceUnavailable(err):
		*f = append(*f, mediaFailure{rank, sourceFailure(ctx, source, err)})
		return nil
	default:
		return err
	}
}

// 有几个独立来源可以重试时，由最早恢复的那个唤醒队列。
func earlierRetry(first, next error) error {
	if first == nil {
		return next
	}
	var a, b *sources.QueuePause
	if errors.As(next, &b) && (!errors.As(first, &a) || b.RetryAt.Before(a.RetryAt)) {
		return next
	}
	return first
}

func (f mediaFailures) retryAbove(rank int) error {
	var retry error
	for _, failure := range f {
		if failure.rank > rank {
			retry = earlierRetry(retry, failure.err)
		}
	}
	return retry
}

func (w *Worker) artistLinks(ctx context.Context, e artistEvidence, p artistProviders, failures *mediaFailures) ([]sources.ArtistLinks, error) {
	if e.NoLink {
		return []sources.ArtistLinks{{Name: e.Name}}, nil
	}
	var links []sources.ArtistLinks
	needLinks := e.ImageRank < catalog.ArtistMediaLicensedRank && p.active["commons"] || e.BioRank < catalog.ArtistMediaLicensedRank && p.active["wikipedia"]
	for _, mbid := range e.MBIDs {
		links = append(links, sources.ArtistLinks{MBID: mbid})
	}
	for _, id := range e.WikidataIDs {
		links = append(links, sources.ArtistLinks{WikidataID: id})
	}
	for i, entry := range links {
		// 已指定 Q 号时，Commons 与维基百科以该条目为依据；其他来源仍可使用 MBID。
		if needLinks && (entry.MBID == "" || len(e.WikidataIDs) == 0) {
			for _, id := range []string{"musicbrainz", "wikidata"} {
				finder, ok := w.sources.ArtistLinkFinder(id)
				if !ok || !p.active[id] || (id == "musicbrainz" && entry.MBID == "") {
					continue
				}
				found, err := finder.ArtistLinks(ctx, entry)
				if err == nil {
					entry = found
				} else if err := failures.record(ctx, id, 1, err); err != nil {
					return nil, err
				}
			}
		}
		entry.Name = e.Name
		links[i] = entry
	}
	return links, nil
}

func mediaBasis(link sources.ArtistLinks) (curation.ArtistMediaLink, string) {
	if link.MBID != "" {
		return curation.ArtistMediaLink{Source: "musicbrainz", ExternalID: link.MBID}, ""
	}
	if link.WikidataID != "" {
		return curation.ArtistMediaLink{Source: "wikidata", ExternalID: link.WikidataID}, ""
	}
	return curation.ArtistMediaLink{}, link.Name
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

func (w *Worker) artistImage(ctx context.Context, u *unit, e artistEvidence, p artistProviders, links []sources.ArtistLinks, failures *mediaFailures) (int, error) {
	for _, id := range append([]string{"fanarttv", "theaudiodb", "commons"}, w.sources.PluginIDs(sources.ArtistImageLookup)...) {
		f, ok := w.sources.ArtistImageFinder(id)
		if !ok || !p.active[id] || catalog.ArtistMediaRank(id) <= e.ImageRank {
			continue
		}
		for _, link := range links {
			if (id == "commons" && link.CommonsFile == "") || ((id == "fanarttv" || id == "theaudiodb") && link.MBID == "") {
				continue
			}
			img, err := f.ArtistImage(ctx, link)
			if err == nil && img.ID != "" && !e.suppressed("artist_image", id, img.ID) {
				basis, name := mediaBasis(link)
				var filled bool
				filled, err = u.writeArtist(ctx, id, "image/"+id+"/"+img.ID, func(batch, key string) (curation.ExternalApplied, error) {
					return w.writer.ApplyArtistImage(ctx, curation.ArtistImageRequest{ArtistID: e.ID, Link: basis, Name: name, Image: img, BatchID: batch, Key: key})
				})
				if filled {
					return catalog.ArtistMediaRank(id), nil
				}
				// 图片字节可能损坏；校验失败后仍可试用下一来源。
				if failureCode(err) == "invalid_input" {
					continue
				}
			}
			if err := failures.record(ctx, id, catalog.ArtistMediaRank(id), err); err != nil {
				return 0, err
			}
		}
	}
	return e.ImageRank, nil
}

func (w *Worker) artistBio(ctx context.Context, u *unit, e artistEvidence, p artistProviders, links []sources.ArtistLinks, failures *mediaFailures) (int, error) {
	for _, group := range [][]string{{"wikipedia", "theaudiodb"}, w.sources.PluginIDs(sources.ArtistBioLookup)} {
		for _, lang := range []string{"zh", "en"} {
			for _, id := range group {
				f, ok := w.sources.ArtistBioFinder(id)
				if !ok || !p.active[id] || catalog.ArtistMediaRank(id) <= e.BioRank {
					continue
				}
				for _, link := range links {
					if (id == "wikipedia" && (lang == "zh" && link.WikipediaZH == "" || lang == "en" && link.WikipediaEN == "")) || (id == "theaudiodb" && link.MBID == "") {
						continue
					}
					bio, err := f.ArtistBio(ctx, link, lang)
					if err == nil && bio.ID != "" && !e.suppressed("artist_bio", id, bio.ID) {
						basis, name := mediaBasis(link)
						var filled bool
						filled, err = u.writeArtist(ctx, id, "bio/"+id+"/"+bio.ID, func(batch, key string) (curation.ExternalApplied, error) {
							return w.writer.ApplyArtistBio(ctx, curation.ArtistBioRequest{ArtistID: e.ID, Link: basis, Name: name, Bio: bio, BatchID: batch, Key: key})
						})
						if filled {
							return catalog.ArtistMediaRank(id), nil
						}
						if failureCode(err) == "invalid_input" {
							continue
						}
					}
					if err := failures.record(ctx, id, catalog.ArtistMediaRank(id), err); err != nil {
						return 0, err
					}
				}
			}
		}
	}
	return e.BioRank, nil
}

func (w *Worker) enrichArtist(ctx context.Context, u *unit, e *artistEvidence, p artistProviders) error {
	var linkFailures, imageFailures, bioFailures mediaFailures
	links, err := w.artistLinks(ctx, *e, p, &linkFailures)
	if err != nil {
		return err
	}
	if e.ImageRank < catalog.ArtistMediaLicensedRank {
		e.ImageRank, err = w.artistImage(ctx, u, *e, p, links, &imageFailures)
		if err != nil {
			return err
		}
	}
	if e.BioRank < catalog.ArtistMediaLicensedRank {
		e.BioRank, err = w.artistBio(ctx, u, *e, p, links, &bioFailures)
		if err != nil {
			return err
		}
	}
	retry := earlierRetry(imageFailures.retryAbove(e.ImageRank), bioFailures.retryAbove(e.BioRank))
	if e.ImageRank < catalog.ArtistMediaLicensedRank && p.active["commons"] || e.BioRank < catalog.ArtistMediaLicensedRank && p.active["wikipedia"] {
		retry = earlierRetry(retry, linkFailures.retryAbove(0))
	}
	return retry
}
