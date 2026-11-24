// Package commons 按 Wikidata 的文件名读取带许可的标准宽度缩略图。
package commons

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
	return sources.Declaration{ID: "commons", Name: "Wikimedia Commons", Capabilities: []sources.Capability{sources.ArtistImageLookup}, DefaultEnabled: true, MinInterval: time.Second}
}

type Options = sources.Options
type Client struct {
	baseURL  string
	requests *sources.Requester
}

func New(opts Options) *Client {
	base := cmp.Or(strings.TrimRight(opts.BaseURL, "/"), "https://commons.wikimedia.org")
	opts.MaxRedirects = 5
	opts.AllowRedirect = sources.MediaHosts(opts.MockHost, "commons.wikimedia.org", "upload.wikimedia.org", "thumb.wikimedia.org")
	return &Client{base + "/w/api.php", sources.NewWikimediaRequester(Declaration(), opts, "")}
}
func (c *Client) StartCacheCleanup(ctx context.Context) { c.requests.StartCacheCleanup(ctx) }

type imageInfo struct {
	ThumbURL, DescriptionURL string
	ExtMetadata              map[string]struct{ Value string }
}

func (c *Client) ArtistImage(ctx context.Context, links sources.ArtistLinks) (sources.ArtistImage, error) {
	if links.CommonsFile == "" {
		return sources.ArtistImage{}, sources.ErrNotFound
	}
	title := "File:" + links.CommonsFile
	q := url.Values{"action": {"query"}, "titles": {title}, "prop": {"imageinfo"}, "iiprop": {"url|size|mime|extmetadata"}, "iiurlwidth": {"960"}, "iiextmetadatafilter": {"Artist|Attribution|Credit|LicenseShortName|LicenseUrl"}, "formatversion": {"2"}, "format": {"json"}}
	var info imageInfo
	err := c.requests.GetJSON(ctx, c.baseURL+"?"+q.Encode(), func(body []byte) (bool, bool) {
		var result struct {
			Query struct {
				Pages []struct {
					ImageInfo []imageInfo
					Title     string
				}
			}
		}
		if json.Unmarshal(body, &result) != nil || result.Query.Pages == nil {
			return false, false
		}
		for _, page := range result.Query.Pages {
			if len(page.ImageInfo) > 0 {
				info = page.ImageInfo[0]
				title = cmp.Or(page.Title, title)
				break
			}
		}
		return info.ThumbURL == "", true
	})
	if err != nil {
		return sources.ArtistImage{}, err
	}
	if info.ThumbURL == "" {
		return sources.ArtistImage{}, sources.ErrNotFound
	}
	page := "https://commons.wikimedia.org/wiki/" + url.PathEscape(strings.ReplaceAll(title, " ", "_"))
	if u, e := url.Parse(info.DescriptionURL); e == nil && sources.MediaHosts(sources.MockHost{}, "commons.wikimedia.org")(u) {
		page = u.String()
	}
	author := sources.PlainText(cmp.Or(info.ExtMetadata["Artist"].Value, info.ExtMetadata["Attribution"].Value, info.ExtMetadata["Credit"].Value))
	license := sources.PlainText(info.ExtMetadata["LicenseShortName"].Value)
	licenseURL := strings.TrimSpace(info.ExtMetadata["LicenseUrl"].Value)
	if strings.HasPrefix(licenseURL, "//") {
		licenseURL = "https:" + licenseURL
	}
	if u, e := url.Parse(licenseURL); e != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		licenseURL = ""
	}
	image := sources.ArtistImage{ID: sources.ImageURLID(info.ThumbURL), Attribution: sources.Attribution{Source: "commons", SourceURL: page, Author: author, License: license, LicenseURL: licenseURL}}
	if license == "" {
		return sources.ArtistImage{}, sources.ErrNotFound
	}
	image.Data, err = c.requests.DownloadArtistImage(ctx, info.ThumbURL)
	return image, err
}
