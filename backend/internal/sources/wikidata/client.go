// Package wikidata 按艺人 MBID 或已知条目读取 Commons 文件名和维基百科标题。
package wikidata

import (
	"cmp"
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

func Declaration() sources.Declaration {
	return sources.Declaration{ID: "wikidata", Name: "Wikidata", Capabilities: []sources.Capability{sources.ArtistLookup}, DefaultEnabled: true, MinInterval: time.Second}
}

type Client struct {
	baseURL  string
	requests *sources.Requester
}
type Options = sources.Options

func New(opts Options) *Client {
	base := cmp.Or(strings.TrimRight(opts.BaseURL, "/"), "https://www.wikidata.org")
	opts.MaxRedirects = 5
	opts.AllowRedirect = sources.MediaHosts(opts.MockHost, "www.wikidata.org", "wikidata.org")
	return &Client{base + "/w/api.php", sources.NewWikimediaRequester(Declaration(), opts, "")}
}

func (c *Client) StartCacheCleanup(ctx context.Context) { c.requests.StartCacheCleanup(ctx) }

func (c *Client) ArtistLinks(ctx context.Context, links sources.ArtistLinks) (sources.ArtistLinks, error) {
	id := links.WikidataID
	if id == "" {
		if !uuid.Valid(links.MBID) {
			return links, sources.ErrInvalidQuery
		}
		q := url.Values{"action": {"query"}, "list": {"search"}, "srsearch": {"haswbstatement:P434=" + strings.ToLower(links.MBID)}, "srlimit": {"2"}, "format": {"json"}}
		err := c.requests.GetJSON(ctx, c.baseURL+"?"+q.Encode(), func(body []byte) (bool, bool) {
			var response struct {
				Query struct{ Search []struct{ Title string } }
			}
			if json.Unmarshal(body, &response) != nil || response.Query.Search == nil {
				return false, false
			}
			if len(response.Query.Search) == 1 && sources.ValidWikidataID(response.Query.Search[0].Title) {
				id = response.Query.Search[0].Title
			}
			return id == "", true
		})
		if err != nil {
			return links, err
		}
		if id == "" {
			return links, sources.ErrNotFound
		}
	}
	if !sources.ValidWikidataID(id) {
		return links, sources.ErrInvalidQuery
	}
	q := url.Values{"action": {"wbgetentities"}, "ids": {id}, "props": {"claims|sitelinks"}, "sitefilter": {"zhwiki|enwiki"}, "format": {"json"}}
	err := c.requests.GetJSON(ctx, c.baseURL+"?"+q.Encode(), func(body []byte) (bool, bool) {
		var response struct {
			Entities map[string]struct {
				ID      string
				Missing json.RawMessage
				Claims  struct {
					P18 []struct {
						Rank     string
						Mainsnak struct{ DataValue struct{ Value string } }
					}
				}
				Sitelinks map[string]struct{ Title string }
			}
		}
		if json.Unmarshal(body, &response) != nil {
			return false, false
		}
		entry, found := response.Entities[id]
		if !found {
			return false, false
		}
		if entry.Missing != nil {
			return true, true
		}
		if entry.ID != id {
			return false, false
		}
		links.WikidataID = id
		for _, claim := range entry.Claims.P18 {
			name := strings.TrimSpace(claim.Mainsnak.DataValue.Value)
			if claim.Rank == "deprecated" || name == "" {
				continue
			}
			if claim.Rank == "preferred" {
				links.CommonsFile = name
				break
			}
			if links.CommonsFile == "" {
				links.CommonsFile = name
			}
		}
		links.WikipediaZH = cmp.Or(entry.Sitelinks["zhwiki"].Title, links.WikipediaZH)
		links.WikipediaEN = cmp.Or(entry.Sitelinks["enwiki"].Title, links.WikipediaEN)
		return links.CommonsFile == "" && links.WikipediaZH == "" && links.WikipediaEN == "", true
	})
	return links, err
}
