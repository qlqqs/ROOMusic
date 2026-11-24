package wikidata

import (
	"cmp"
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type claim[T any] struct {
	Rank     string
	Mainsnak struct{ DataValue struct{ Value T } }
}

type term struct{ Value string }

const artistLanguages = "zh|en|mul|zh-hans|zh-cn|zh-hant|zh-tw|ja"

type artistEntity struct {
	ID                   string
	Missing              json.RawMessage
	Labels, Descriptions map[string]term
	Aliases              map[string][]term
	Claims               struct {
		P434 []claim[string]
		P31  []claim[struct{ ID string }]
	}
}

func (e artistEntity) artist() sources.WikidataArtist {
	a := sources.WikidataArtist{WikidataID: e.ID}
	languages := strings.Split(artistLanguages, "|")
	for _, lang := range languages {
		a.Name = cmp.Or(a.Name, strings.TrimSpace(e.Labels[lang].Value))
		a.Description = cmp.Or(a.Description, strings.TrimSpace(e.Descriptions[lang].Value))
	}
	a.Name = cmp.Or(a.Name, e.ID)
	for _, lang := range languages {
		if name := strings.TrimSpace(e.Labels[lang].Value); name != "" && name != a.Name && !slices.Contains(a.Aliases, name) {
			a.Aliases = append(a.Aliases, name)
		}
		for _, alias := range e.Aliases[lang] {
			if name := strings.TrimSpace(alias.Value); name != "" && name != a.Name && !slices.Contains(a.Aliases, name) {
				a.Aliases = append(a.Aliases, name)
			}
		}
	}
	for _, c := range e.Claims.P31 {
		id := c.Mainsnak.DataValue.Value.ID
		if c.Rank != "deprecated" && sources.ValidWikidataID(id) && !slices.Contains(a.InstanceOf, id) {
			a.InstanceOf = append(a.InstanceOf, id)
		}
	}
	for _, c := range e.Claims.P434 {
		id := strings.ToLower(c.Mainsnak.DataValue.Value)
		if c.Rank == "deprecated" || !uuid.Valid(id) {
			continue
		}
		if a.MBID != "" && a.MBID != id {
			a.MBID = ""
			break
		}
		a.MBID = id
	}
	return a
}

func (c *Client) ArtistByID(ctx context.Context, id string) (sources.WikidataArtist, error) {
	if !sources.ValidWikidataID(id) {
		return sources.WikidataArtist{}, sources.ErrInvalidQuery
	}
	q := url.Values{"action": {"wbgetentities"}, "ids": {id}, "props": {"labels|aliases|descriptions|claims"}, "languages": {artistLanguages}, "format": {"json"}}
	var artist sources.WikidataArtist
	err := c.requests.GetJSON(ctx, c.baseURL+"?"+q.Encode(), func(body []byte) (bool, bool) {
		var result struct{ Entities map[string]artistEntity }
		if json.Unmarshal(body, &result) != nil {
			return false, false
		}
		entry, ok := result.Entities[id]
		if !ok {
			return false, false
		}
		if entry.Missing != nil {
			return true, true
		}
		if entry.ID != id {
			return false, false
		}
		artist = entry.artist()
		return false, true
	})
	if err == nil && artist.WikidataID == "" {
		err = sources.ErrNotFound
	}
	return artist, err
}
