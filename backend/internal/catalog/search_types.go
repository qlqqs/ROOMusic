package catalog

// CatalogState 只公开目录能力及不透明代次，不包含构建进度或内部错误。
type CatalogState struct {
	Epoch        int64  `json:"epoch"`
	SearchMode   string `json:"search_mode"`
	ArtistsState string `json:"artists_state"`
}

// SearchMatch 的 nullable 字段仍必传；原文来自实际命中的有效字段。
type SearchMatch struct {
	Kind      string  `json:"kind"`
	Field     string  `json:"field"`
	Role      *string `json:"role"`
	Original  string  `json:"original"`
	ArtistID  *string `json:"artist_id"`
	Alias     *string `json:"alias"`
	ReleaseID *string `json:"release_id"`
	TrackID   *string `json:"track_id"`
}

type SearchAlbumItem struct {
	Group            ReleaseGroupSummary `json:"group"`
	MatchedReleaseID string              `json:"matched_release_id"`
	Match            SearchMatch         `json:"match"`
}

type SearchTrackItem struct {
	TrackSummary
	Match SearchMatch `json:"match"`
}

// SearchPagination 与旧目录的精确计数分页分开，1,000 是计数预算而非可翻页上限。
type SearchPagination struct {
	Page          int    `json:"page"`
	PageSize      int    `json:"page_size"`
	Total         int64  `json:"total"`
	TotalRelation string `json:"total_relation"`
	HasMore       bool   `json:"has_more"`
}

// SearchParams 已由 HTTP 边界验证；这里只支持④的专辑和曲目。
type SearchParams struct {
	Query          string
	Type           string
	Page, PageSize int
}

type SearchResult struct {
	Query        string           `json:"query"`
	Type         string           `json:"type"`
	CatalogState CatalogState     `json:"catalog_state"`
	Items        any              `json:"items"`
	Pagination   SearchPagination `json:"pagination"`
}

type SearchSuggestionSection[T any] struct {
	Items   []T  `json:"items"`
	HasMore bool `json:"has_more"`
}

type SearchSuggestions struct {
	Query          string                                   `json:"query"`
	CatalogState   CatalogState                             `json:"catalog_state"`
	AvailableTypes []string                                 `json:"available_types"`
	Artists        SearchSuggestionSection[struct{}]        `json:"artists"`
	Albums         SearchSuggestionSection[SearchAlbumItem] `json:"albums"`
	Tracks         SearchSuggestionSection[SearchTrackItem] `json:"tracks"`
}
