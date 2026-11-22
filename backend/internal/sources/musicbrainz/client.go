// Package musicbrainz 提供 MusicBrainz 的实体查询和发行、录音、艺人查找。
package musicbrainz

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

func Declaration() sources.Declaration {
	return sources.Declaration{ID: "musicbrainz", Name: "MusicBrainz", Capabilities: []sources.Capability{sources.ReleaseLookup, sources.RecordingLookup, sources.ArtistLookup}, DefaultEnabled: true, MinInterval: time.Second}
}

type Options = sources.Options
type Client struct {
	baseURL  string
	requests *sources.Requester
}

func New(opts Options) *Client {
	base := strings.TrimRight(opts.BaseURL, "/")
	if base == "" {
		base = "https://musicbrainz.org/ws/2"
	}
	return &Client{baseURL: base, requests: sources.NewRequester(Declaration(), opts)}
}

func (c *Client) BaseURL() string                       { return c.baseURL }
func (c *Client) Limiter() *sources.Limiter             { return c.requests.Limiter() }
func (c *Client) StartCacheCleanup(ctx context.Context) { c.requests.StartCacheCleanup(ctx) }

func (c *Client) get(ctx context.Context, path string, params url.Values, decode func([]byte) (bool, bool)) error {
	params.Set("fmt", "json")
	return c.requests.GetJSON(ctx, c.baseURL+"/"+path+"?"+params.Encode(), decode)
}

func (c *Client) Lookup(ctx context.Context, entity, mbid string, inc []string) (Entity, error) {
	if !uuid.Valid(mbid) {
		return Entity{}, sources.ErrInvalidQuery
	}
	params := url.Values{}
	if len(inc) > 0 {
		params.Set("inc", strings.Join(inc, " "))
	}
	var result Entity
	err := c.get(ctx, entity+"/"+strings.ToLower(mbid), params, func(body []byte) (bool, bool) {
		var item Entity
		if json.Unmarshal(body, &item) != nil || item.ID == "" {
			return false, false
		}
		if entity == "release" && slices.Contains(inc, "recordings") && slices.Contains(inc, "discids") {
			if item.Media == nil {
				return false, false
			}
			for _, medium := range item.Media {
				if medium.Tracks == nil {
					return false, false
				}
			}
		}
		result = item
		return false, true
	})
	return result, err
}

func (c *Client) Search(ctx context.Context, entity, query string, limit int) ([]Entity, error) {
	params := url.Values{"query": {query}, "limit": {strconv.Itoa(limit)}}
	return c.entities(ctx, entity, params, entity+"s", limit)
}

func (c *Client) entities(ctx context.Context, path string, params url.Values, key string, limit int) ([]Entity, error) {
	var results []Entity
	err := c.get(ctx, path, params, func(body []byte) (bool, bool) {
		var fields map[string]json.RawMessage
		if json.Unmarshal(body, &fields) != nil {
			return false, false
		}
		var items []Entity
		if json.Unmarshal(fields[key], &items) != nil || !validEntities(items) {
			return false, false
		}
		if limit > 0 && len(items) > limit {
			items = items[:limit]
		}
		results = items
		return len(items) == 0, true
	})
	return results, err
}

func validEntities(items []Entity) bool {
	if items == nil {
		return false
	}
	for _, item := range items {
		if item.ID == "" {
			return false
		}
	}
	return true
}
