// Package coverartarchive 按发行或发行组查找封面并下载主封面。
package coverartarchive

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"mime"
	"net/url"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

// 与 catalog/artwork 的单张封面大小上限一致。
const maxImageBytes = 16 << 20

func Declaration() sources.Declaration {
	return sources.Declaration{ID: "coverartarchive", Name: "Cover Art Archive", Capabilities: []sources.Capability{sources.CoverArt}, DefaultEnabled: true, MinInterval: time.Second}
}

type Options struct {
	MockHost                 sources.MockHost
	DataDirectory, BaseURL   string
	Logger                   *slog.Logger
	NewClient                sources.ClientFactory
	ManifestLimiter, Limiter *sources.Limiter
	Now                      func() time.Time
	After                    func(time.Duration) <-chan time.Time
}

type Client struct {
	baseURL          string
	manifest, images *sources.Requester
}

func New(opts Options) *Client {
	base := strings.TrimRight(opts.BaseURL, "/")
	if base == "" {
		base = "https://coverartarchive.org"
	}
	allowedHost := func(u *url.URL) bool { return officialHost(u) || opts.MockHost.Matches(u) }
	requests := sources.Options{
		MockHost: opts.MockHost, DataDirectory: opts.DataDirectory, Logger: opts.Logger, NewClient: opts.NewClient,
		Now: opts.Now, After: opts.After, Limiter: opts.Limiter,
		MaxRedirects: 5, AllowRedirect: allowedHost,
	}
	images := sources.NewRequester(Declaration(), requests)
	requests.Limiter = opts.ManifestLimiter
	if requests.Limiter == nil {
		requests.Limiter = images.Limiter()
	}
	requests.AllowRedirect = func(u *url.URL) bool { return allowedHost(u) && strings.HasSuffix(u.Path, "/index.json") }
	return &Client{baseURL: base, manifest: sources.NewRequester(Declaration(), requests), images: images}
}

func officialHost(u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	return host == "coverartarchive.org" || host == "archive.org" || strings.HasSuffix(host, ".archive.org")
}

func (c *Client) BaseURL() string                       { return c.baseURL }
func (c *Client) ManifestLimiter() *sources.Limiter     { return c.manifest.Limiter() }
func (c *Client) StartCacheCleanup(ctx context.Context) { c.manifest.StartCacheCleanup(ctx) }

func (c *Client) Covers(ctx context.Context, target sources.CoverTarget) ([]sources.CoverImage, error) {
	if !uuid.Valid(target.ID) {
		return nil, sources.ErrInvalidQuery
	}
	var kind string
	switch target.Kind {
	case "release":
		kind = "release"
	case "release_group":
		kind = "release-group"
	default:
		return nil, sources.ErrInvalidQuery
	}
	address := c.baseURL + "/" + kind + "/" + strings.ToLower(target.ID) + "/"
	var images []sources.CoverImage
	err := c.manifest.GetJSON(ctx, address, func(body []byte) (bool, bool) {
		var ok bool
		images, ok = decodeManifest(body)
		return false, ok // 清单 200 缓存三十天，404 缓存一天。
	})
	if errors.Is(err, sources.ErrNotFound) {
		return []sources.CoverImage{}, nil
	}
	return images, err
}

func (c *Client) FrontCover(ctx context.Context, target sources.CoverTarget) (sources.CoverFile, error) {
	images, err := c.Covers(ctx, target)
	if err != nil {
		return sources.CoverFile{}, err
	}
	image := sources.FrontImage(images)
	if image.ID == "" {
		return sources.CoverFile{}, sources.ErrNotFound
	}
	if image.ReleaseID == "" {
		if target.Kind != "release" {
			return sources.CoverFile{}, sources.ErrNotFound
		}
		image.ReleaseID = strings.ToLower(target.ID)
	}
	original := c.baseURL + "/release/" + image.ReleaseID + "/" + image.ID
	for _, size := range []string{"1200", "500", "250", "original"} {
		address := original
		if size != "original" {
			address += "-" + size
		}
		data, contentType, err := c.images.GetBytes(ctx, address, "image/*", maxImageBytes)
		if errors.Is(err, sources.ErrNotFound) {
			continue
		}
		if err != nil {
			return sources.CoverFile{}, err
		}
		if size == "original" {
			mediaType, _, _ := mime.ParseMediaType(contentType)
			if mediaType == "application/pdf" || bytes.HasPrefix(data, []byte("%PDF")) {
				return sources.CoverFile{}, sources.ErrNotFound
			}
		}
		return sources.CoverFile{Image: image, Size: size, Data: data}, nil
	}
	return sources.CoverFile{}, sources.ErrNotFound
}
