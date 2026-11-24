// Package wikipedia 读取中文或英文条目的纯文本导语。
package wikipedia

import (
	"cmp"
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/sources"
)

func Declaration() sources.Declaration {
	return sources.Declaration{ID: "wikipedia", Name: "Wikipedia", Capabilities: []sources.Capability{sources.ArtistBioLookup}, DefaultEnabled: true, MinInterval: time.Second}
}

type Client struct {
	baseURL string
	zh, en  *sources.Requester
}
type Options = sources.Options

func New(opts Options) *Client {
	opts.MaxRedirects = 5
	opts.AllowRedirect = sources.MediaHosts(opts.MockHost, "zh.wikipedia.org")
	zh := sources.NewWikimediaRequester(Declaration(), opts, "zh-cn")
	opts.AllowRedirect = sources.MediaHosts(opts.MockHost, "en.wikipedia.org")
	return &Client{strings.TrimRight(opts.BaseURL, "/"), zh, sources.NewWikimediaRequester(Declaration(), opts, "")}
}

func (c *Client) StartCacheCleanup(ctx context.Context) { c.zh.StartCacheCleanup(ctx) }

func (c *Client) ArtistBio(ctx context.Context, links sources.ArtistLinks, lang string) (sources.ArtistBio, error) {
	title, requests := links.WikipediaZH, c.zh
	switch lang {
	case "zh":
	case "en":
		title, requests = links.WikipediaEN, c.en
	default:
		return sources.ArtistBio{}, sources.ErrInvalidQuery
	}
	if title == "" {
		return sources.ArtistBio{}, sources.ErrNotFound
	}
	official := "https://" + lang + ".wikipedia.org"
	base := official
	if c.baseURL != "" {
		base = c.baseURL + "/" + lang
	}
	address := base + "/api/rest_v1/page/summary/" + url.PathEscape(title)
	var bio sources.ArtistBio
	err := requests.GetJSON(ctx, address, func(body []byte) (bool, bool) {
		var item struct {
			Title       string
			Type        string
			Extract     *string
			Lang        string
			ContentURLs struct{ Desktop struct{ Page string } } `json:"content_urls"`
		}
		if json.Unmarshal(body, &item) != nil || item.Extract == nil {
			return false, false
		}
		text := sources.BioText(*item.Extract)
		if text == "" || item.Type == "disambiguation" || (item.Lang != "" && item.Lang != lang) {
			return true, true
		}
		page := official + "/wiki/" + url.PathEscape(strings.ReplaceAll(cmp.Or(item.Title, title), " ", "_"))
		if u, e := url.Parse(item.ContentURLs.Desktop.Page); e == nil && sources.MediaHosts(sources.MockHost{}, lang+".wikipedia.org")(u) {
			page = u.String()
		}
		bio = sources.ArtistBio{ID: page, Text: text, Lang: lang, Attribution: sources.Attribution{Source: "wikipedia", SourceURL: page, License: "CC BY-SA 4.0", LicenseURL: "https://creativecommons.org/licenses/by-sa/4.0/"}}
		return false, true
	})
	if err == nil && bio.Text == "" {
		err = sources.ErrNotFound
	}
	return bio, err
}
