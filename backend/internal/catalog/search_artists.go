package catalog

import (
	"context"
	"fmt"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

const artistSearchPredicate = `n.projection_generation=(SELECT published_generation FROM catalog_projection_state WHERE capability='artists')
 AND n.short_grams @> ARRAY[$3]::text[] AND n.normalized_value LIKE '%'||$2||'%' ESCAPE '~'`
const artistSearchRank = `CASE WHEN n.normalized_value=$1 THEN 0 WHEN n.normalized_value LIKE $2||'%' ESCAPE '~' THEN 1 ELSE 2 END`

func (s *Store) searchArtists(ctx context.Context, query string, page, size int, count bool) ([]SearchArtistItem, SearchPagination, error) {
	items := []SearchArtistItem{}
	pagination := SearchPagination{Page: page, PageSize: size, TotalRelation: "eq"}
	if err := textnorm.LoadEmbedded(); err != nil {
		return nil, pagination, err
	}
	key := textnorm.Text(query)
	if key == "" {
		return items, pagination, nil
	}
	args := []any{key, searchindex.EscapeLike(key), searchindex.QueryGram(key)}
	// 每个艺人先按自身名字判断，再有界计数；不把命中的名字数量当成艺人数。
	if count {
		var total int64
		err := s.database.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT a.id FROM artists a
 LEFT JOIN artist_overrides o ON o.artist_id=a.id WHERE o.redirect_artist_id IS NULL AND `+artistVisiblePredicate+`
 AND $1::text<>'' AND EXISTS (SELECT 1 FROM artist_search_names n WHERE n.artist_id=a.id AND `+artistSearchPredicate+`) LIMIT 1001) counted`, args...).Scan(&total)
		if err != nil {
			return nil, pagination, err
		}
		pagination.Total, pagination.TotalRelation = boundedSearchTotal(total)
	}
	rows, err := s.database.QueryContext(ctx, `WITH matches AS (
 SELECT DISTINCT ON(n.artist_id) n.artist_id,n.original_value,n.name_id,`+artistSearchRank+` AS rank
 FROM artist_search_names n JOIN artists a ON a.id=n.artist_id LEFT JOIN artist_overrides o ON o.artist_id=a.id
 WHERE o.redirect_artist_id IS NULL AND `+artistSearchPredicate+` AND `+artistVisiblePredicate+`
 ORDER BY n.artist_id,rank,(n.name_id IS NULL) DESC,n.original_value COLLATE "C",n.id
 ) SELECT m.artist_id::text,m.original_value,m.rank,m.original_value=p.effective_display_name
 FROM matches m JOIN artist_profiles p ON p.artist_id=m.artist_id
 ORDER BY m.rank,p.sort_key COLLATE "C",m.artist_id LIMIT $4 OFFSET $5`, append(args, size+1, (page-1)*size)...)
	if err != nil {
		return nil, pagination, err
	}
	ids := []string{}
	matches := []SearchMatch{}
	for rows.Next() {
		if len(ids) == size {
			pagination.HasMore = true
			break
		}
		var id, original string
		var rank int
		var primary bool
		if err := rows.Scan(&id, &original, &rank, &primary); err != nil {
			rows.Close()
			return nil, pagination, err
		}
		if rank < 0 || rank > 2 || len(original) > 4096 {
			rows.Close()
			return nil, pagination, fmt.Errorf("%w: invalid artist search match", ErrQuery)
		}
		match := SearchMatch{Kind: [...]string{"exact", "prefix", "contains"}[rank], Field: "artist_name", Original: original, ArtistID: &id}
		if !primary {
			match.Field = "artist_alias"
			match.Alias = &original
		}
		ids = append(ids, id)
		matches = append(matches, match)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, pagination, err
	}
	summaries, err := s.loadArtistSummaries(ctx, ids)
	if err != nil {
		return nil, pagination, err
	}
	for i, id := range ids {
		artist, ok := summaries[id]
		if !ok {
			return nil, pagination, fmt.Errorf("%w: artist search summary unavailable", ErrQuery)
		}
		items = append(items, SearchArtistItem{Artist: artist, Match: matches[i]})
	}
	return items, pagination, nil
}
