// Package theaudiodb 按 MusicBrainz 艺人 ID 读取头像及中英文简介。
package theaudiodb

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
	return sources.Declaration{ID: "theaudiodb", Name: "TheAudioDB", Capabilities: []sources.Capability{sources.ArtistImageLookup, sources.ArtistBioLookup}, DefaultEnabled: true, MinInterval: 2 * time.Second, Keys: []string{"api_key"}}
}

type Options = sources.Options
type Client struct {
	baseURL  string
	requests *sources.Requester
	keys     func(context.Context) (map[string]string, error)
}

func New(opts Options) *Client {
	base := cmp.Or(strings.TrimRight(opts.BaseURL, "/"), "https://www.theaudiodb.com")
	opts.MaxRedirects = 5
	opts.AllowRedirect = sources.MediaHosts(opts.MockHost, "www.theaudiodb.com", "theaudiodb.com", "r2.theaudiodb.com")
	return &Client{base, sources.NewRequester(Declaration(), opts), opts.Keys}
}
func (c *Client) StartCacheCleanup(ctx context.Context) { c.requests.StartCacheCleanup(ctx) }

type artist struct{ IDArtist, StrMusicBrainzID, StrArtistThumb, StrBiographyCN, StrBiography string }

func (c *Client) prepare(ctx context.Context, u *url.URL) (map[string]string, error) {
	var keys map[string]string
	if c.keys != nil {
		var err error
		keys, err = c.keys(ctx)
		if err != nil {
			return nil, err
		}
	}
	key := keys["api_key"]
	if key == "" {
		return nil, &sources.InvalidKeyError{Name: "api_key"}
	}
	path := strings.TrimSuffix(u.EscapedPath(), "/artist-mb.php") + "/" + url.PathEscape(key) + "/artist-mb.php"
	u.Path, _ = url.PathUnescape(path)
	u.RawPath = path
	return nil, nil
}

func (c *Client) artist(ctx context.Context, id string) (artist, error) {
	if !uuid.Valid(id) {
		return artist{}, sources.ErrInvalidQuery
	}
	address := c.baseURL + "/api/v1/json/artist-mb.php?i=" + strings.ToLower(id)
	var found artist
	err := c.requests.GetAuthenticatedJSON(ctx, address, c.prepare, "api_key", func(body []byte) (bool, bool) {
		var result struct{ Artists json.RawMessage }
		if json.Unmarshal(body, &result) != nil || result.Artists == nil {
			return false, false
		}
		var list []artist
		if json.Unmarshal(result.Artists, &list) != nil {
			return false, false
		}
		for _, entry := range list {
			if entry.IDArtist != "" && (entry.StrMusicBrainzID == "" || strings.EqualFold(entry.StrMusicBrainzID, id)) {
				found = entry
				break
			}
		}
		return found.IDArtist == "", true
	})
	if err == nil && found.IDArtist == "" {
		err = sources.ErrNotFound
	}
	return found, err
}

func (a artist) attribution() sources.Attribution {
	return sources.Attribution{Source: "theaudiodb", SourceURL: "https://www.theaudiodb.com/artist/" + url.PathEscape(a.IDArtist)}
}

func (c *Client) ArtistImage(ctx context.Context, links sources.ArtistLinks) (sources.ArtistImage, error) {
	item, err := c.artist(ctx, links.MBID)
	if err != nil {
		return sources.ArtistImage{}, err
	}
	if item.StrArtistThumb == "" {
		return sources.ArtistImage{}, sources.ErrNotFound
	}
	data, err := c.requests.DownloadArtistImage(ctx, item.StrArtistThumb)
	return sources.ArtistImage{ID: sources.ImageURLID(item.StrArtistThumb), Attribution: item.attribution(), Data: data}, err
}

func (c *Client) ArtistBio(ctx context.Context, links sources.ArtistLinks, lang string) (sources.ArtistBio, error) {
	if lang != "zh" && lang != "en" {
		return sources.ArtistBio{}, sources.ErrInvalidQuery
	}
	item, err := c.artist(ctx, links.MBID)
	if err != nil {
		return sources.ArtistBio{}, err
	}
	value := item.StrBiography
	if lang == "zh" {
		value = item.StrBiographyCN
	}
	text := sources.BioText(value)
	if text == "" {
		return sources.ArtistBio{}, sources.ErrNotFound
	}
	return sources.ArtistBio{ID: item.IDArtist, Attribution: item.attribution(), Text: text, Lang: lang}, nil
}
