// Package fanarttv 根据 MusicBrainz 艺人 ID 选择点赞最多的头像。
package fanarttv

import (
	"cmp"
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

var bundledProjectKey string

func Declaration() sources.Declaration {
	optional := []string{"personal"}
	if bundledProjectKey != "" {
		optional = []string{"project", "personal"}
	}
	return sources.Declaration{ID: "fanarttv", Name: "Fanart.tv", Capabilities: []sources.Capability{sources.ArtistImageLookup}, DefaultEnabled: true, MinInterval: time.Second, Keys: []string{"project", "personal"}, OptionalKeys: optional}
}

type Options = sources.Options
type Client struct {
	baseURL  string
	requests *sources.Requester
	keys     func(context.Context) (map[string]string, error)
}

func New(opts Options) *Client {
	base := cmp.Or(strings.TrimRight(opts.BaseURL, "/"), "https://webservice.fanart.tv")
	opts.MaxRedirects = 5
	opts.AllowRedirect = sources.MediaHosts(opts.MockHost, "webservice.fanart.tv", "assets.fanart.tv")
	return &Client{base, sources.NewRequester(Declaration(), opts), opts.Keys}
}
func (c *Client) StartCacheCleanup(ctx context.Context) { c.requests.StartCacheCleanup(ctx) }

func (c *Client) headers(ctx context.Context, _ *url.URL) (map[string]string, error) {
	var keys map[string]string
	if c.keys != nil {
		var err error
		keys, err = c.keys(ctx)
		if err != nil {
			return nil, err
		}
	}
	project := cmp.Or(keys["project"], bundledProjectKey)
	if project == "" {
		return nil, &sources.InvalidKeyError{Name: "project"}
	}
	headers := map[string]string{"api-key": project}
	if personal := keys["personal"]; personal != "" {
		headers["client-key"] = personal
	}
	return headers, nil
}

func (c *Client) ArtistImage(ctx context.Context, links sources.ArtistLinks) (sources.ArtistImage, error) {
	if !uuid.Valid(links.MBID) {
		return sources.ArtistImage{}, sources.ErrInvalidQuery
	}
	id := strings.ToLower(links.MBID)
	var best struct{ ID, URL string }
	err := c.requests.GetAuthenticatedJSON(ctx, c.baseURL+"/v3/music/"+id, c.headers, "project", func(body []byte) (bool, bool) {
		var item struct {
			MBID        string `json:"mbid"`
			MBIDID      string `json:"mbid_id"`
			ArtistThumb []struct {
				ID, URL string
				Likes   json.Number
			}
		}
		if json.Unmarshal(body, &item) != nil || (item.MBID == "" && item.MBIDID == "" && item.ArtistThumb == nil) {
			return false, false
		}
		most := -1
		for _, thumb := range item.ArtistThumb {
			likes, _ := strconv.Atoi(thumb.Likes.String())
			if thumb.URL != "" && likes > most {
				most = likes
				best.ID = thumb.ID
				best.URL = thumb.URL
			}
		}
		return best.URL == "", true
	})
	if err != nil {
		return sources.ArtistImage{}, err
	}
	if best.URL == "" {
		return sources.ArtistImage{}, sources.ErrNotFound
	}
	data, err := c.requests.DownloadArtistImage(ctx, best.URL)
	return sources.ArtistImage{ID: cmp.Or(best.ID, sources.ImageURLID(best.URL)), Data: data, Attribution: sources.Attribution{Source: "fanarttv", SourceURL: "https://fanart.tv/artist/" + id + "/"}}, err
}
