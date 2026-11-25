package musicbrainz

import (
	"context"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

const matchPageSize = 100

// PageInfo 的总数和偏移由上游返回。缺少任一项时，第一页无法证明候选唯一。
type PageInfo struct {
	Query    string `json:"query"`
	Count    *int   `json:"count"`
	Offset   *int   `json:"offset"`
	Limit    int    `json:"limit"`
	Returned int    `json:"returned"`
	Complete bool   `json:"complete"`
}

type ReleaseCounts struct {
	Total *int   `json:"total"`
	Media []*int `json:"media"`
}

type ReleasePage struct {
	PageInfo
	Items  []sources.Release
	Counts map[string]ReleaseCounts
}

type RecordingPage struct {
	PageInfo
	Items         []sources.Recording
	ReleaseCounts map[string]int
}

type ArtistPage struct {
	PageInfo
	Items []sources.Artist
}

type ReleaseSearch struct {
	Title, Artist, ArtistID string
	CatalogNumber, Label    string
	TrackCount              int
	Barcodes                []string
}

type RecordingSearch struct {
	Title, Artist, ArtistID string
	LengthMS                *int64
	ISRCs                   []string
}

func titleArtistTerms(titleField, title, artist, artistID string) ([]string, error) {
	terms := field(nil, titleField, title)
	if len(terms) == 0 {
		return nil, nil
	}
	if artistID != "" {
		if !uuid.Valid(artistID) {
			return nil, sources.ErrInvalidQuery
		}
		return append(terms, "arid:"+strings.ToLower(artistID)), nil
	}
	terms = field(terms, "artist", artist)
	if len(terms) < 2 {
		return nil, nil
	}
	return terms, nil
}

func (q ReleaseSearch) expression() (string, error) {
	var clauses []string
	var barcodes []string
	for _, b := range q.Barcodes {
		term := barcodeTerm(b)
		if term != "" && !slices.Contains(barcodes, term) {
			barcodes = append(barcodes, term)
		}
	}
	if len(barcodes) > 0 {
		slices.Sort(barcodes)
		clauses = append(clauses, strings.Join(barcodes, " OR "))
	}
	if terms := field(nil, "catno", q.CatalogNumber); len(terms) != 0 {
		clauses = append(clauses, strings.Join(field(terms, "label", q.Label), " AND "))
	}
	terms, err := titleArtistTerms("release", q.Title, q.Artist, q.ArtistID)
	if err != nil {
		return "", err
	}
	if len(terms) != 0 {
		if q.TrackCount > 0 {
			terms = append(terms, "tracks:"+strconv.Itoa(q.TrackCount))
		}
		clauses = append(clauses, strings.Join(terms, " AND "))
	}
	switch len(clauses) {
	case 0:
		return "", sources.ErrInvalidQuery
	case 1:
		return clauses[0], nil
	}
	return "(" + strings.Join(clauses, ") OR (") + ")", nil
}

func (c *Client) matchPage(ctx context.Context, kind, query string) ([]Entity, PageInfo, error) {
	params := url.Values{"query": {query}, "limit": {strconv.Itoa(matchPageSize)}}
	return c.entityPage(ctx, kind, params, kind+"s", matchPageSize)
}

func (c *Client) SearchReleasePage(ctx context.Context, q ReleaseSearch) (ReleasePage, error) {
	expression, err := q.expression()
	if err != nil {
		return ReleasePage{}, err
	}
	items, info, err := c.matchPage(ctx, "release", expression)
	if err != nil {
		return ReleasePage{}, err
	}
	page := ReleasePage{PageInfo: info, Items: releases(items), Counts: make(map[string]ReleaseCounts, len(items))}
	for i, e := range items {
		counts := ReleaseCounts{Total: e.TrackCount}
		for j, m := range e.Media {
			counts.Media = append(counts.Media, m.DeclaredTrackCount)
			if m.Position == 0 {
				page.Items[i].Media[j].Position = j + 1
			}
		}
		page.Counts[e.ID] = counts
	}
	return page, nil
}

func (q RecordingSearch) expression() (string, error) {
	if len(q.ISRCs) != 0 {
		var terms []string
		for _, raw := range q.ISRCs {
			if id := normalizedISRC(raw); isrcPattern.MatchString(id) && !slices.Contains(terms, "isrc:"+id) {
				terms = append(terms, "isrc:"+id)
			}
		}
		if len(terms) == 0 {
			return "", sources.ErrInvalidQuery
		}
		slices.Sort(terms)
		return strings.Join(terms, " OR "), nil
	}
	terms, err := titleArtistTerms("recording", q.Title, q.Artist, q.ArtistID)
	if err != nil || len(terms) == 0 {
		return "", sources.ErrInvalidQuery
	}
	if q.LengthMS != nil && *q.LengthMS > 0 {
		ms := *q.LengthMS
		// 本地以远端时长的 1% 核对；查询取逆区间并向外取整。
		lower := max(int64(0), min(ms-3000, int64(math.Floor(float64(ms)/1.01))))
		upper := max(ms+3000, int64(math.Ceil(float64(ms)/0.99)))
		terms = append(terms, "dur:["+strconv.FormatInt(lower, 10)+" TO "+strconv.FormatInt(upper, 10)+"]")
	}
	return strings.Join(terms, " AND "), nil
}

func (c *Client) SearchRecordingPage(ctx context.Context, q RecordingSearch) (RecordingPage, error) {
	expression, err := q.expression()
	if err != nil {
		return RecordingPage{}, err
	}
	items, info, err := c.matchPage(ctx, "recording", expression)
	if err != nil {
		return RecordingPage{}, err
	}
	page := RecordingPage{PageInfo: info, Items: recordings(items), ReleaseCounts: make(map[string]int, len(items))}
	for _, e := range items {
		page.ReleaseCounts[e.ID] = len(e.Releases)
	}
	return page, nil
}

func (c *Client) SearchArtistPage(ctx context.Context, name string) (ArtistPage, error) {
	query, err := artistExpression(name)
	if err != nil {
		return ArtistPage{}, err
	}
	items, info, err := c.matchPage(ctx, "artist", query)
	if err != nil {
		return ArtistPage{}, err
	}
	return ArtistPage{PageInfo: info, Items: artists(items)}, nil
}
