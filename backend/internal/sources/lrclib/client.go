// Package lrclib 提供 LRCLIB 的歌词精确查询与候选搜索。
package lrclib

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/sources"
)

func Declaration() sources.Declaration {
	return sources.Declaration{ID: "lrclib", Name: "LRCLIB", Capabilities: []sources.Capability{sources.LyricsLookup}, DefaultEnabled: true, MinInterval: time.Second}
}

type Options = sources.Options
type Client struct {
	baseURL  string
	requests *sources.Requester
}

func New(opts Options) *Client {
	base := strings.TrimRight(opts.BaseURL, "/")
	if base == "" {
		base = "https://lrclib.net"
	}
	opts.MaxRedirects = 0
	return &Client{baseURL: base, requests: sources.NewRequester(Declaration(), opts)}
}

func (c *Client) StartCacheCleanup(ctx context.Context) { c.requests.StartCacheCleanup(ctx) }

func params(query sources.LyricsQuery) url.Values {
	values := url.Values{"track_name": {strings.TrimSpace(query.Title)}}
	if artist := strings.TrimSpace(query.Artist); artist != "" {
		values.Set("artist_name", artist)
	}
	if album := strings.TrimSpace(query.Album); album != "" {
		values.Set("album_name", album)
	}
	return values
}

type record struct {
	ID                               *int64
	TrackName, ArtistName, AlbumName string
	Duration                         float64
	Instrumental                     bool
	SyncedLyrics, PlainLyrics        string
}

func (r record) lyrics() sources.Lyrics {
	return sources.Lyrics{
		ID: strconv.FormatInt(*r.ID, 10), Title: r.TrackName, Artist: r.ArtistName, Album: r.AlbumName,
		DurationSeconds: r.Duration, Instrumental: r.Instrumental,
		Synced: r.SyncedLyrics, Plain: r.PlainLyrics,
	}
}

func (c *Client) LyricsFor(ctx context.Context, query sources.LyricsQuery) (sources.Lyrics, error) {
	values := params(query)
	if values.Get("track_name") == "" || values.Get("artist_name") == "" {
		return sources.Lyrics{}, sources.ErrInvalidQuery
	}
	if query.DurationSeconds >= 1 && query.DurationSeconds <= 3600 {
		values.Set("duration", strconv.Itoa(query.DurationSeconds))
	}
	var result sources.Lyrics
	err := c.requests.GetJSON(ctx, c.baseURL+"/api/get?"+values.Encode(), func(body []byte) (bool, bool) {
		var item record
		if json.Unmarshal(body, &item) != nil || item.ID == nil {
			return false, false
		}
		result = item.lyrics()
		return false, true
	})
	return result, err
}

func (c *Client) SearchLyrics(ctx context.Context, query sources.LyricsQuery) ([]sources.Lyrics, error) {
	values := params(query)
	if values.Get("track_name") == "" {
		return nil, sources.ErrInvalidQuery
	}
	var results []sources.Lyrics
	err := c.requests.GetJSON(ctx, c.baseURL+"/api/search?"+values.Encode(), func(body []byte) (bool, bool) {
		var items []record
		if json.Unmarshal(body, &items) != nil || items == nil {
			return false, false
		}
		const limit = 20
		candidates := make([]sources.Lyrics, 0, min(limit, len(items)))
		for _, item := range items {
			if item.ID == nil {
				return false, false
			}
			if len(candidates) < limit {
				candidates = append(candidates, item.lyrics())
			}
		}
		results = candidates
		return len(results) == 0, true
	})
	return results, err
}
