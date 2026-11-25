package musicbrainz

import (
	"context"
	"net/url"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/sources"
)

func (c *Client) ArtistLinks(ctx context.Context, links sources.ArtistLinks) (sources.ArtistLinks, error) {
	entity, err := c.artistEntity(ctx, links.MBID)
	if err != nil {
		return links, err
	}
	for _, rel := range entity.Relations {
		u, err := url.Parse(rel.URL.Resource)
		if err != nil {
			continue
		}
		title, ok := strings.CutPrefix(u.Path, "/wiki/")
		if !ok || title == "" {
			continue
		}
		switch strings.ToLower(u.Hostname()) {
		case "wikidata.org", "www.wikidata.org":
			if rel.Type == "wikidata" && sources.ValidWikidataID(title) {
				links.WikidataID = title
			}
		case "zh.wikipedia.org":
			if rel.Type == "wikipedia" {
				links.WikipediaZH = title
			}
		case "en.wikipedia.org":
			if rel.Type == "wikipedia" {
				links.WikipediaEN = title
			}
		}
	}
	return links, nil
}
