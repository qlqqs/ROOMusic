package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

const searchCountLimit = 1000

var ErrUnsupportedSearchType = errors.New("unsupported search type")

// searchSnapshot 的回调始终使用同一个只读 RR 事务；不能把状态、计数、页和摘要分开读。
func searchSnapshot[T any](ctx context.Context, store *Store, read func(*Store, CatalogState, int64) (T, error)) (T, error) {
	var zero T
	beginner, ok := store.database.(ReadTransactionBeginner)
	if !ok {
		return zero, fmt.Errorf("%w: search requires a snapshot-capable database", ErrQuery)
	}
	return readWithLocalJITOff(ctx, beginner, func(tx *sql.Tx) (T, error) {
		snapshot := NewStore(tx)
		state, generation, err := snapshot.searchState(ctx)
		if err != nil {
			return zero, fmt.Errorf("%w: load search state: %w", ErrQuery, err)
		}
		result, err := read(snapshot, state, generation)
		if err != nil {
			return zero, fmt.Errorf("%w: read search snapshot: %w", ErrQuery, err)
		}
		return result, nil
	})
}

func (s *Store) searchState(ctx context.Context) (CatalogState, int64, error) {
	state, err := searchindex.LoadState(ctx, s.database)
	if err != nil {
		return CatalogState{}, 0, err
	}
	result := CatalogState{Epoch: state.CatalogEpoch, SearchMode: "legacy", ArtistsState: "unavailable"}
	var artistEpoch int64
	if err := s.database.QueryRowContext(ctx, `SELECT catalog_epoch FROM catalog_projection_state WHERE capability='artists'`).Scan(&artistEpoch); err == nil {
		result.Epoch += artistEpoch
		ready, err := artistgraph.Ready(ctx, s.database)
		if err != nil {
			return CatalogState{}, 0, err
		}
		result.ArtistsState = "preparing"
		if ready {
			result.ArtistsState = "ready"
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return CatalogState{}, 0, err
	}
	// 字表损坏不阻断旧目录；成功预检之前绝不调用 Text/CatalogNumber。
	if state.Ready() && textnorm.LoadEmbedded() == nil {
		result.SearchMode = "normalized"
		return result, state.PublishedGeneration, nil
	}
	return result, 0, nil
}

func (s *Store) LoadCatalogState(ctx context.Context) (CatalogState, error) {
	return searchSnapshot(ctx, s, func(_ *Store, state CatalogState, _ int64) (CatalogState, error) { return state, nil })
}

func emptySearchItems(kind string) any {
	if kind == "artists" {
		return []SearchArtistItem{}
	}
	if kind == "albums" {
		return []SearchAlbumItem{}
	}
	return []SearchTrackItem{}
}

// Search 返回一栏的完整安全摘要及有界计数，空页也保留全局计数下界。
func (s *Store) Search(ctx context.Context, params SearchParams) (SearchResult, error) {
	if params.Type != "albums" && params.Type != "tracks" && params.Type != "artists" {
		return SearchResult{}, ErrUnsupportedSearchType
	}
	return searchSnapshot(ctx, s, func(snapshot *Store, state CatalogState, generation int64) (SearchResult, error) {
		result := SearchResult{Query: params.Query, Type: params.Type, CatalogState: state, Items: emptySearchItems(params.Type),
			Pagination: SearchPagination{Page: params.Page, PageSize: params.PageSize, TotalRelation: "eq"}}
		if params.Type == "artists" {
			if state.ArtistsState == "unavailable" {
				return SearchResult{}, ErrUnsupportedSearchType
			}
			if state.ArtistsState != "ready" {
				return SearchResult{}, ErrArtistCatalogPreparing
			}
			items, pagination, err := snapshot.searchArtists(ctx, params.Query, params.Page, params.PageSize, true)
			result.Items = items
			result.Pagination = pagination
			return result, err
		}
		plan := newSearchPlan(params.Query, params.Type, state.SearchMode, generation)
		plan.artistAliases = state.ArtistsState == "ready"
		if plan.empty {
			return result, nil
		}
		plan, err := snapshot.prepareSearchPlan(ctx, plan)
		if err != nil {
			return SearchResult{}, err
		}
		total, err := snapshot.countSearch(ctx, plan, true)
		if err != nil {
			return SearchResult{}, err
		}
		result.Pagination.Total, result.Pagination.TotalRelation = boundedSearchTotal(total)
		hits, hasMore, err := snapshot.searchPage(ctx, plan, params.PageSize, (params.Page-1)*params.PageSize)
		if err != nil {
			return SearchResult{}, err
		}
		result.Items, err = snapshot.completeSearch(ctx, params.Type, hits)
		result.Pagination.HasMore = hasMore
		return result, err
	})
}

func boundedSearchTotal(total int64) (int64, string) {
	if total > searchCountLimit {
		return searchCountLimit, "gte"
	}
	return total, "eq"
}

// SearchSuggestions 每栏仅取 limit+1，不执行实体总数探测；两栏共享状态与目录快照。
func (s *Store) SearchSuggestions(ctx context.Context, query string, limit int) (SearchSuggestions, error) {
	return searchSnapshot(ctx, s, func(snapshot *Store, state CatalogState, generation int64) (SearchSuggestions, error) {
		result := SearchSuggestions{Query: query, CatalogState: state, AvailableTypes: []string{"albums", "tracks"},
			Artists: SearchSuggestionSection[SearchArtistItem]{Items: []SearchArtistItem{}},
			Albums:  SearchSuggestionSection[SearchAlbumItem]{Items: []SearchAlbumItem{}},
			Tracks:  SearchSuggestionSection[SearchTrackItem]{Items: []SearchTrackItem{}}}
		if state.ArtistsState == "ready" {
			result.AvailableTypes = []string{"artists", "albums", "tracks"}
		}
		for _, kind := range result.AvailableTypes {
			if kind == "artists" {
				items, pagination, err := snapshot.searchArtists(ctx, query, 1, limit, false)
				if err != nil {
					return SearchSuggestions{}, err
				}
				result.Artists = SearchSuggestionSection[SearchArtistItem]{Items: items, HasMore: pagination.HasMore}
				continue
			}
			plan := newSearchPlan(query, kind, state.SearchMode, generation)
			plan.artistAliases = state.ArtistsState == "ready"
			if plan.empty {
				continue
			}
			plan, err := snapshot.prepareSearchPlan(ctx, plan)
			if err != nil {
				return SearchSuggestions{}, err
			}
			hits, hasMore, err := snapshot.searchPage(ctx, plan, limit, 0)
			if err != nil {
				return SearchSuggestions{}, err
			}
			items, err := snapshot.completeSearch(ctx, kind, hits)
			if err != nil {
				return SearchSuggestions{}, err
			}
			if kind == "albums" {
				result.Albums = SearchSuggestionSection[SearchAlbumItem]{Items: items.([]SearchAlbumItem), HasMore: hasMore}
			} else {
				result.Tracks = SearchSuggestionSection[SearchTrackItem]{Items: items.([]SearchTrackItem), HasMore: hasMore}
			}
		}
		return result, nil
	})
}
