package musicbrainz

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/qlqq/roomusic/backend/internal/sources"
)

var luceneEscapes = strings.NewReplacer(
	`\`, `\\`, "+", `\+`, "-", `\-`, "&&", `\&\&`, "||", `\|\|`, "!", `\!`, "(", `\(`, ")", `\)`,
	"{", `\{`, "}", `\}`, "[", `\[`, "]", `\]`, "^", `\^`, `"`, `\"`, "~", `\~`, "*", `\*`, "?", `\?`, ":", `\:`, "/", `\/`,
)
var isrcPattern = regexp.MustCompile(`^[A-Z]{2}[A-Z0-9]{3}[0-9]{7}$`)

func field(terms []string, name, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return terms
	}
	return append(terms, name+`:"`+luceneEscapes.Replace(value)+`"`)
}

func (c *Client) ReleaseByID(ctx context.Context, id string) (sources.Release, error) {
	e, err := c.Lookup(ctx, "release", id, []string{"recordings", "isrcs", "artist-credits", "labels", "release-groups", "media", "discids"})
	if err != nil {
		return sources.Release{}, err
	}
	return release(e, true), nil
}

func (c *Client) searchReleases(ctx context.Context, q string, limit int) ([]sources.Release, error) {
	if q == "" {
		return nil, sources.ErrInvalidQuery
	}
	items, err := c.Search(ctx, "release", q, limit)
	if err != nil {
		return nil, err
	}
	return releases(items), nil
}

func (c *Client) ReleasesByBarcode(ctx context.Context, barcode string) ([]sources.Release, error) {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, barcode)
	if digits == "" {
		return nil, sources.ErrInvalidQuery
	}
	if len(digits) == 13 && digits[0] == '0' {
		digits = digits[1:]
	}
	q := "barcode:" + digits
	if len(digits) == 12 {
		q += " OR barcode:0" + digits
	}
	return c.searchReleases(ctx, q, 25)
}

func (c *Client) ReleasesByCatalog(ctx context.Context, catalogNumber, label string) ([]sources.Release, error) {
	terms := field(nil, "catno", catalogNumber)
	if len(terms) == 0 {
		return nil, sources.ErrInvalidQuery
	}
	terms = field(terms, "label", label)
	return c.searchReleases(ctx, strings.Join(terms, " AND "), 25)
}

func (c *Client) SearchReleases(ctx context.Context, q sources.ReleaseQuery) ([]sources.Release, error) {
	terms := field(nil, "release", q.Title)
	terms = field(terms, "artist", q.Artist)
	if q.TrackCount > 0 {
		terms = append(terms, "tracks:"+strconv.Itoa(q.TrackCount))
	}
	return c.searchReleases(ctx, strings.Join(terms, " AND "), 10)
}

func (c *Client) RecordingByID(ctx context.Context, id string) (sources.Recording, error) {
	e, err := c.Lookup(ctx, "recording", id, []string{"artist-credits", "isrcs", "releases"})
	if err != nil {
		return sources.Recording{}, err
	}
	return recording(e), nil
}

func (c *Client) RecordingsByISRC(ctx context.Context, isrc string) ([]sources.Recording, error) {
	id := strings.ToUpper(strings.Map(func(r rune) rune {
		if r == '-' || unicode.IsSpace(r) {
			return -1
		}
		return r
	}, isrc))
	if !isrcPattern.MatchString(id) {
		return nil, sources.ErrInvalidQuery
	}
	items, err := c.entities(ctx, "isrc/"+id, url.Values{"inc": {"artist-credits releases"}}, "recordings", 0)
	if errors.Is(err, sources.ErrNotFound) {
		return []sources.Recording{}, nil
	}
	if err != nil {
		return nil, err
	}
	return recordings(items), nil
}

func (c *Client) SearchRecordings(ctx context.Context, q sources.RecordingQuery) ([]sources.Recording, error) {
	terms := field(nil, "recording", q.Title)
	terms = field(terms, "artist", q.Artist)
	if q.LengthMS != nil {
		ms := *q.LengthMS
		terms = append(terms, fmt.Sprintf("dur:[%d TO %d]", ms-3000, ms+3000))
	}
	if len(terms) == 0 {
		return nil, sources.ErrInvalidQuery
	}
	items, err := c.Search(ctx, "recording", strings.Join(terms, " AND "), 10)
	if err != nil {
		return nil, err
	}
	return recordings(items), nil
}

func (c *Client) ArtistByID(ctx context.Context, id string) (sources.Artist, error) {
	e, err := c.Lookup(ctx, "artist", id, []string{"aliases"})
	if err != nil {
		return sources.Artist{}, err
	}
	return artist(e), nil
}

func (c *Client) SearchArtists(ctx context.Context, name string) ([]sources.Artist, error) {
	terms := field(nil, "artist", name)
	if len(terms) == 0 {
		return nil, sources.ErrInvalidQuery
	}
	items, err := c.Search(ctx, "artist", terms[0], 10)
	if err != nil {
		return nil, err
	}
	out := make([]sources.Artist, 0, len(items))
	for _, e := range items {
		out = append(out, artist(e))
	}
	return out, nil
}
