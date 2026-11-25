package curation

import (
	"context"

	"github.com/qlqq/roomusic/backend/internal/sources"
)

// 整理依赖带分页依据的来源能力，具体来源通过应用装配接入。
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

type ReleasePageFinder interface {
	SearchReleasePage(context.Context, ReleaseSearch) (ReleasePage, error)
}

type RecordingPageFinder interface {
	SearchRecordingPage(context.Context, RecordingSearch) (RecordingPage, error)
}

type ArtistPageFinder interface {
	SearchArtistPage(context.Context, string) (ArtistPage, error)
}
