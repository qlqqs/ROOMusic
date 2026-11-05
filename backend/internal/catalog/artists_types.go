package catalog

import "errors"

var (
	ErrArtistCatalogPreparing = errors.New("artist catalog preparing")
	ErrArtistNotFound         = errors.New("artist not found")
)

// ArtistSummary 是共享的安全身份摘要，不含本人收藏状态或治理证据。
type ArtistSummary struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	InitialGroup string   `json:"initial_group"`
	AlbumCount   int64    `json:"album_count"`
	TrackCount   int64    `json:"track_count"`
	Roles        []string `json:"roles"`
	Available    bool     `json:"available"`
}

type ArtistPagination struct {
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Total    int64 `json:"total"`
}

type ArtistPage[T any] struct {
	ArtistID   string           `json:"artist_id,omitempty"`
	Items      []T              `json:"items"`
	Pagination ArtistPagination `json:"pagination"`
}

type ArtistListParams struct {
	Query, Initial, Role, Sort string
	Page, PageSize             int
}

type ArtistName struct {
	ID            string `json:"id"`
	KeyID         string `json:"key_id"`
	Name          string `json:"name"`
	IsDisplayName bool   `json:"is_display_name"`
	Origin        string `json:"origin"`
}

type ArtistRoleCount struct {
	Role    string `json:"role"`
	Total   int64  `json:"total"`
	Present int64  `json:"present"`
}

type ArtistDetail struct {
	Artist           ArtistSummary     `json:"artist"`
	Revision         int64             `json:"revision"`
	NameSource       string            `json:"name_source"`
	Names            []ArtistName      `json:"names"`
	NamesPagination  ArtistPagination  `json:"names_pagination"`
	RoleCounts       []ArtistRoleCount `json:"role_counts"`
	RedirectArtistID *string           `json:"redirect_artist_id"`
}

// ArtistAlbumOptions 是专辑参与范围和标题过滤；零值保持旧读取行为。
type ArtistAlbumOptions struct {
	Participation string
	Query         string
}

type ArtistAlbum struct {
	Group             ReleaseGroupSummary `json:"group"`
	CreditedReleaseID string              `json:"credited_release_id"`
	Roles             []string            `json:"roles,omitempty"`
}

type ArtistQueue struct {
	ArtistID     string         `json:"artist_id"`
	Order        string         `json:"order"`
	Seed         *string        `json:"seed"`
	Items        []TrackSummary `json:"items"`
	Total        int64          `json:"total"`
	Returned     int            `json:"returned"`
	Limit        int            `json:"limit"`
	Truncated    bool           `json:"truncated"`
	MissingTotal int64          `json:"missing_total"`
}

type SearchArtistItem struct {
	Artist ArtistSummary `json:"artist"`
	Match  SearchMatch   `json:"match"`
}
