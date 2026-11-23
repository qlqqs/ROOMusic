// Package acoustid 用声学指纹查询 AcoustID 对应的录音和发行。
package acoustid

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/qlqq/roomusic/backend/internal/sources"
)

var bundledClientKey string
var bundledKeyRejected atomic.Bool

func Declaration() sources.Declaration {
	keys := []string{"user"}
	if bundledClientKey == "" {
		keys = []string{"application", "user"}
	}
	return sources.Declaration{
		ID: "acoustid", Name: "AcoustID", Capabilities: []sources.Capability{sources.FingerprintLookup},
		DefaultEnabled: true, Keys: keys, MinInterval: 334 * time.Millisecond,
	}
}

type Options struct {
	DataDirectory, BaseURL string
	Logger                 *slog.Logger
	NewClient              sources.ClientFactory
	Limiter                *sources.Limiter
	Now                    func() time.Time
	After                  func(time.Duration) <-chan time.Time
	Keys                   func(context.Context) (map[string]string, error)
}

type Client struct {
	address          string
	requests         *sources.Requester
	keys             func(context.Context) (map[string]string, error)
	logger           *slog.Logger
	bundledClientKey string
}

func New(opts Options) *Client {
	base := strings.TrimRight(opts.BaseURL, "/")
	if base == "" {
		base = "https://api.acoustid.org"
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	requests := sources.NewRequester(Declaration(), sources.Options{
		DataDirectory: opts.DataDirectory, Logger: opts.Logger, NewClient: opts.NewClient, Limiter: opts.Limiter, Now: opts.Now, After: opts.After,
	})
	return &Client{
		address:          base + "/v2/lookup",
		requests:         requests,
		keys:             opts.Keys,
		logger:           logger,
		bundledClientKey: bundledClientKey,
	}
}

func (c *Client) StartCacheCleanup(ctx context.Context) { c.requests.StartCacheCleanup(ctx) }

// BundledKeyRejected 报告本进程的内置应用密钥是否被 AcoustID 拒绝过。
func (c *Client) BundledKeyRejected() bool { return bundledKeyRejected.Load() }

func (c *Client) LookupFingerprint(ctx context.Context, fp sources.Fingerprint) ([]sources.FingerprintMatch, error) {
	if fp.Value == "" || fp.DurationSeconds < 1 || fp.DurationSeconds > 32767 {
		return nil, sources.ErrInvalidQuery
	}
	duration := strconv.Itoa(fp.DurationSeconds)
	form := func(ctx context.Context) (url.Values, error) {
		var keys map[string]string
		if c.keys != nil {
			var err error
			keys, err = c.keys(ctx)
			if err != nil {
				return nil, err
			}
		}
		clientKey := c.bundledClientKey
		if clientKey == "" {
			clientKey = keys["application"]
			if clientKey == "" {
				return nil, &sources.InvalidKeyError{Name: "application"}
			}
		}
		if keys["user"] == "" {
			return nil, &sources.InvalidKeyError{Name: "user"}
		}
		return url.Values{
			"client": {clientKey}, "user": {keys["user"]},
			"duration": {duration}, "fingerprint": {fp.Value},
			"meta": {"recordings releases"}, "format": {"json"},
		}, nil
	}
	var result []sources.FingerprintMatch
	err := c.requests.PostForm(ctx, c.address, duration+"\x00"+fp.Value, form, func(status int, body []byte) (bool, bool, error) {
		var data struct {
			Status  string
			Error   struct{ Code int }
			Results []match
		}
		parseErr := json.Unmarshal(body, &data)
		if data.Status == "error" && parseErr == nil {
			switch data.Error.Code {
			case 4, 17:
				if c.bundledClientKey != "" && bundledKeyRejected.CompareAndSwap(false, true) {
					message := "AcoustID 拒绝了 ROOMusic 内置的应用密钥"
					c.logger.ErrorContext(ctx, message, "event", "sources.acoustid.bundled_key_rejected", "module", "sources", "message", message)
				}
				return false, false, &sources.InvalidKeyError{Name: "application"}
			case 6:
				return false, false, &sources.InvalidKeyError{Name: "user"}
			case 3, 8:
				return false, false, sources.ErrInvalidQuery
			case 5, 13, 14:
				return false, true, sources.ErrTryLater
			}
		}
		if status == 429 || status == 503 {
			return false, true, sources.ErrTryLater
		}
		if parseErr != nil || status != 200 || data.Status != "ok" || data.Results == nil {
			return false, false, sources.ErrTryLater
		}
		result = project(data.Results)
		return len(result) == 0, false, nil
	})
	return result, err
}

type artist struct {
	ID, Name, JoinPhrase string
}

func credits(artists []artist) []sources.Credit {
	out := make([]sources.Credit, 0, len(artists))
	for _, item := range artists {
		out = append(out, sources.Credit{ArtistID: item.ID, Name: item.Name, JoinPhrase: item.JoinPhrase})
	}
	return out
}

type date struct {
	Year, Month, Day int
}

func (d date) String() string {
	switch {
	case d.Year == 0:
		return ""
	case d.Month == 0:
		return fmt.Sprintf("%04d", d.Year)
	case d.Day == 0:
		return fmt.Sprintf("%04d-%02d", d.Year, d.Month)
	default:
		return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day)
	}
}

type release struct {
	ID, Title, Country string
	Date               date
	Artists            []artist
	TrackCount         int `json:"track_count"`
	MediumCount        int `json:"medium_count"`
}

type recording struct {
	ID, Title string
	Artists   []artist
	Duration  float64
	Releases  []release
}

func (r recording) project() sources.FingerprintRecording {
	out := sources.FingerprintRecording{ID: r.ID, Title: r.Title, Artists: credits(r.Artists)}
	if r.Duration > 0 {
		ms := int64(math.Round(r.Duration * 1000))
		out.LengthMS = &ms
	}
	for _, rel := range r.Releases {
		if rel.ID != "" {
			out.ReleaseIDs = append(out.ReleaseIDs, rel.ID)
			out.Releases = append(out.Releases, sources.FingerprintRelease{
				ID: rel.ID, Title: rel.Title, Artists: credits(rel.Artists), Date: rel.Date.String(), Country: rel.Country,
				TrackCount: rel.TrackCount, MediumCount: rel.MediumCount,
			})
		}
	}
	return out
}

type match struct {
	ID         string
	Score      float64
	Recordings []recording
}

func project(rows []match) []sources.FingerprintMatch {
	slices.SortStableFunc(rows, func(a, b match) int { return cmp.Compare(b.Score, a.Score) })
	seen := make(map[string]bool)
	out := make([]sources.FingerprintMatch, 0, len(rows))
	for _, row := range rows {
		m := sources.FingerprintMatch{ID: row.ID, Score: row.Score}
		for _, rec := range row.Recordings {
			if rec.ID != "" && !seen[rec.ID] {
				seen[rec.ID] = true
				m.Recordings = append(m.Recordings, rec.project())
			}
		}
		if len(m.Recordings) > 0 {
			out = append(out, m)
		}
	}
	return out
}
