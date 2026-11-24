package curation

import (
	"cmp"
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type ArtistLinkPreview struct {
	Source      string   `json:"source"`
	ExternalID  string   `json:"external_id"`
	Name        string   `json:"name"`
	Description *string  `json:"description"`
	MBID        *string  `json:"musicbrainz"`
	WikidataID  *string  `json:"wikidata"`
	NameMatch   bool     `json:"name_matches"`
	InstanceOf  []string `json:"-"`
	artistType  string
	entryID     string
}

func parseArtistLink(input string) (externalIdentity, error) {
	input = strings.TrimSpace(input)
	if uuid.Valid(input) {
		return externalIdentity{"musicbrainz", strings.ToLower(input)}, nil
	}
	if sources.ValidWikidataID(input) {
		return externalIdentity{"wikidata", input}, nil
	}
	if !strings.Contains(input, "://") {
		input = "https://" + input
	}
	u, err := url.Parse(input)
	if err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.User == nil {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 2 {
			switch strings.ToLower(u.Host) {
			case "musicbrainz.org", "beta.musicbrainz.org", "www.musicbrainz.org":
				if parts[0] == "artist" && uuid.Valid(parts[1]) {
					return externalIdentity{"musicbrainz", strings.ToLower(parts[1])}, nil
				}
			case "wikidata.org", "www.wikidata.org":
				if len(parts) == 2 && (parts[0] == "wiki" || parts[0] == "entity") && sources.ValidWikidataID(parts[1]) {
					return externalIdentity{"wikidata", parts[1]}, nil
				}
			}
		}
	}
	return externalIdentity{}, operations.Invalid("invalid_input", "请输入 MusicBrainz 艺人或 Wikidata 条目的链接或编号")
}

func artistLinkSourceError(err error) error {
	var failure *operations.Failure
	switch {
	case errors.Is(err, sources.ErrNotFound):
		return operations.NotFound("not_found", "外部条目不存在")
	case errors.Is(err, sources.ErrUnavailable), errors.Is(err, sources.ErrRateLimited), errors.Is(err, sources.ErrTryLater):
		return operations.Conflict("source_inactive", "外部资料源暂不可用")
	case errors.As(err, &failure):
		switch failure.Code {
		case "curation_disabled", "curation_paused", "scan_running", "source_disabled":
			return operations.Conflict("source_inactive", failure.Message)
		}
	}
	return err
}

func readArtistLink(ctx context.Context, gate *Sources, link externalIdentity) (ArtistLinkPreview, []string, error) {
	p := ArtistLinkPreview{Source: link.Source, ExternalID: link.ExternalID}
	if link.Source == "wikidata" {
		entry, err := gate.WikidataArtist(ctx, link.ExternalID)
		if err != nil {
			return p, nil, artistLinkSourceError(err)
		}
		p.entryID = entry.WikidataID
		p.Name, p.WikidataID, p.InstanceOf = entry.Name, &link.ExternalID, entry.InstanceOf
		if entry.Description != "" {
			p.Description = &entry.Description
		}
		if entry.MBID != "" {
			p.MBID = &entry.MBID
		}
		return p, entry.Aliases, nil
	}
	finder, ok := gate.ArtistFinder("musicbrainz")
	relations, hasRelations := gate.ArtistLinkFinder("musicbrainz")
	if !ok || !hasRelations {
		return p, nil, operations.Conflict("source_inactive", "MusicBrainz 暂不可用")
	}
	entry, err := finder.ArtistByID(ctx, link.ExternalID)
	if err != nil {
		return p, nil, artistLinkSourceError(err)
	}
	links, err := relations.ArtistLinks(ctx, sources.ArtistLinks{MBID: link.ExternalID})
	if err != nil {
		return p, nil, artistLinkSourceError(err)
	}
	p.artistType, p.entryID = entry.Type, entry.ID
	p.Name, p.MBID = cmp.Or(strings.TrimSpace(entry.Name), link.ExternalID), &link.ExternalID
	if entry.Disambiguation != "" {
		p.Description = &entry.Disambiguation
	}
	if links.WikidataID != "" {
		p.WikidataID = &links.WikidataID
	}
	aliases := make([]string, 0, len(entry.Aliases))
	for _, a := range entry.Aliases {
		aliases = append(aliases, a.Name)
	}
	return p, aliases, nil
}

func (w *Writer) PreviewArtistLink(ctx context.Context, gate *Sources, artistID, input string) (ArtistLinkPreview, error) {
	link, err := parseArtistLink(input)
	if err != nil {
		return ArtistLinkPreview{}, err
	}
	store := catalog.NewStore(w.db)
	summaries, err := store.LoadArtistSummaries(ctx, []string{artistID})
	if errors.Is(err, catalog.ErrArtistCatalogPreparing) {
		return ArtistLinkPreview{}, operations.Conflict("artist_catalog_not_ready", "艺人资料正在整理")
	}
	if err != nil {
		return ArtistLinkPreview{}, err
	}
	if len(summaries) == 0 {
		return ArtistLinkPreview{}, operations.NotFound("not_found", "艺人不存在")
	}
	p, aliases, err := readArtistLink(ctx, gate, link)
	if err != nil {
		return p, err
	}
	if err := textnorm.LoadEmbedded(); err != nil {
		return p, err
	}
	names := map[string]bool{}
	for _, s := range append(aliases, p.Name) {
		if key := textnorm.Text(s); key != "" {
			names[key] = true
		}
	}
	for _, s := range summaries {
		if names[textnorm.Text(s.Name)] {
			p.NameMatch = true
			return p, nil
		}
	}
	const size = 100
	for page := 1; ; page++ {
		local, err := store.ListArtistNames(ctx, artistID, page, size)
		if err != nil {
			return p, err
		}
		for _, n := range local.Items {
			if names[textnorm.Text(n.Name)] {
				p.NameMatch = true
				return p, nil
			}
		}
		if int64(page*size) >= local.Pagination.Total {
			return p, nil
		}
	}
}
