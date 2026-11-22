// Package sources 定义外部资料源的能力、查询结果与请求策略。
package sources

import (
	"context"
	"errors"
	"strings"
	"time"
)

type Capability string

const (
	ReleaseLookup   Capability = "release_lookup"
	RecordingLookup Capability = "recording_lookup"
	ArtistLookup    Capability = "artist_lookup"
	CoverArt        Capability = "cover_art"
	LyricsLookup    Capability = "lyrics"
)

type Declaration struct {
	ID, Name       string
	Capabilities   []Capability
	DefaultEnabled bool
	MinInterval    time.Duration
}

type ReleaseFinder interface {
	ReleaseByID(context.Context, string) (Release, error)
	ReleasesByBarcode(context.Context, string) ([]Release, error)
	ReleasesByCatalog(context.Context, string, string) ([]Release, error)
	ReleasesByDisc(context.Context, DiscTOC) (DiscMatch, error)
	SearchReleases(context.Context, ReleaseQuery) ([]Release, error)
}

type RecordingFinder interface {
	RecordingByID(context.Context, string) (Recording, error)
	RecordingsByISRC(context.Context, string) ([]Recording, error)
	SearchRecordings(context.Context, RecordingQuery) ([]Recording, error)
}

type ArtistFinder interface {
	ArtistByID(context.Context, string) (Artist, error)
	SearchArtists(context.Context, string) ([]Artist, error)
}

type CoverFinder interface {
	Covers(context.Context, CoverTarget) ([]CoverImage, error)
	FrontCover(context.Context, CoverTarget) (CoverFile, error)
}

type CoverTarget struct {
	Kind string // release 或 release_group
	ID   string
}

type CoverImage struct {
	ID, ReleaseID string
	Front, Back   bool
	Types         []string
	URL           string
	Thumbnails    map[string]string
}

// FrontImage 优先选择明确标为主封面的图片，其次使用类型为 Front 的第一张。
func FrontImage(images []CoverImage) CoverImage {
	var fallback CoverImage
	for _, image := range images {
		if image.ID == "" {
			continue
		}
		if image.Front {
			return image
		}
		if fallback.ID == "" {
			for _, kind := range image.Types {
				if strings.EqualFold(kind, "Front") {
					fallback = image
					break
				}
			}
		}
	}
	return fallback
}

type CoverFile struct {
	Image CoverImage
	Size  string
	Data  []byte
}

type LyricsFinder interface {
	LyricsFor(context.Context, LyricsQuery) (Lyrics, error)
	SearchLyrics(context.Context, LyricsQuery) ([]Lyrics, error)
}

type LyricsQuery struct {
	Title, Artist, Album string
	DurationSeconds      int
}

type Lyrics struct {
	ID                   string
	Title, Artist, Album string
	DurationSeconds      float64
	Instrumental         bool
	Synced, Plain        string
}

type ReleaseQuery struct {
	Title, Artist string
	TrackCount    int
}
type RecordingQuery struct {
	Title, Artist string
	LengthMS      *int64
}
type DiscTOC struct {
	Offsets []int
	Leadout int
}
type DiscMatch struct {
	Exact    bool
	Releases []Release
}
type Release struct {
	ID, Title, Disambiguation      string
	Artists                        []Credit
	Date, Country, Barcode, Status string
	Labels                         []Label
	Group                          Group
	Media                          []Medium
	Complete                       bool
}
type Credit struct{ ArtistID, Name, JoinPhrase string }
type Label struct{ Name, CatalogNumber string }
type Group struct{ ID, Title, PrimaryType, FirstReleaseDate string }
type Medium struct {
	Position      int
	Format, Title string
	TrackCount    int
	DiscIDs       []string
	Tracks        []Track
}
type Track struct {
	ID                          string
	Position                    int
	Number, Title               string
	LengthMS                    *int64
	Artists                     []Credit
	RecordingID, RecordingTitle string
	ISRCs                       []string
}
type Recording struct {
	ID, Title string
	LengthMS  *int64
	Artists   []Credit
	ISRCs     []string
	Releases  []Release
}
type Artist struct {
	ID, Name, SortName, Disambiguation, Type, Country string
	Aliases                                           []Alias
}
type Alias struct {
	Name, Locale string
	Primary      bool
}

var (
	ErrNotFound     = errors.New("not_found")
	ErrRateLimited  = errors.New("rate_limited")
	ErrUnavailable  = errors.New("source_unavailable")
	ErrTryLater     = errors.New("try_later")
	ErrInvalidQuery = errors.New("invalid_query")
)

type backgroundKey struct{}
type caller struct{ permit func(context.Context) error }

// WithBackground 标记后台调用；传入的回调在读取缓存和每次出站前重新核对权限。
func WithBackground(ctx context.Context, permit ...func(context.Context) error) context.Context {
	c := caller{}
	if len(permit) > 0 {
		c.permit = permit[0]
	}
	return context.WithValue(ctx, backgroundKey{}, c)
}
