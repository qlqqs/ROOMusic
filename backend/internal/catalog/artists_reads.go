package catalog

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

func artistSnapshot[T any](ctx context.Context, s *Store, read func(*Store) (T, error)) (T, error) {
	var zero T
	beginner, ok := s.database.(ReadTransactionBeginner)
	if !ok {
		return zero, fmt.Errorf("%w: artists require a snapshot-capable database", ErrQuery)
	}
	return readWithLocalJITOff(ctx, beginner, func(tx *sql.Tx) (T, error) {
		snapshot := NewStore(tx)
		if err := snapshot.requireArtistsReady(ctx); err != nil {
			return zero, err
		}
		return read(snapshot)
	})
}

func (s *Store) canonicalArtist(ctx context.Context, id string) (string, error) {
	resolved, err := s.resolveArtists(ctx, []string{id})
	if err != nil {
		return "", err
	}
	if canonical, ok := resolved[id]; ok {
		return canonical, nil
	}
	return "", ErrArtistNotFound
}

// 同一名字键的全部写法一起展示；作用域拆开的同名身份也保留该键的写法。
const artistNamesCTE = `artist_family(id) AS (
 SELECT $1::uuid UNION SELECT o.artist_id FROM artist_overrides o JOIN artist_family f ON o.redirect_artist_id=f.id
), artist_names_selected AS MATERIALIZED (
 SELECT n.id,n.key_id,n.spelling,n.origin FROM artist_names n JOIN artist_name_keys k ON k.id=n.key_id
 LEFT JOIN artist_name_bindings b ON b.key_id=k.id
 WHERE COALESCE(b.artist_id,k.initial_artist_id) IN (SELECT id FROM artist_family)
 OR EXISTS (SELECT 1 FROM artist_effective_credits e JOIN artist_names en ON en.id=e.name_id WHERE e.artist_id=$1::uuid AND en.key_id=n.key_id)
)`

func (s *Store) artistNames(ctx context.Context, id string, page, size int) (ArtistPage[ArtistName], error) {
	result := ArtistPage[ArtistName]{ArtistID: id, Items: []ArtistName{}, Pagination: ArtistPagination{Page: page, PageSize: size}}
	if err := s.database.QueryRowContext(ctx, `WITH RECURSIVE `+artistNamesCTE+` SELECT count(*) FROM artist_names_selected`, id).Scan(&result.Pagination.Total); err != nil {
		return result, err
	}
	rows, err := s.database.QueryContext(ctx, `WITH RECURSIVE `+artistNamesCTE+`
 SELECT n.id::text,n.key_id::text,n.spelling,n.spelling=p.effective_display_name,n.origin
 FROM artist_names_selected n JOIN artist_profiles p ON p.artist_id=$1::uuid
 ORDER BY n.key_id,n.spelling COLLATE "C",n.id LIMIT $2 OFFSET $3`, id, size, (page-1)*size)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var item ArtistName
		if err := rows.Scan(&item.ID, &item.KeyID, &item.Name, &item.IsDisplayName, &item.Origin); err != nil {
			return result, err
		}
		result.Items = append(result.Items, item)
	}
	return result, rows.Err()
}

func (s *Store) ListArtistNames(ctx context.Context, id string, page, size int) (ArtistPage[ArtistName], error) {
	return artistSnapshot(ctx, s, func(snapshot *Store) (ArtistPage[ArtistName], error) {
		canonical, err := snapshot.canonicalArtist(ctx, id)
		if err != nil {
			return ArtistPage[ArtistName]{}, err
		}
		return snapshot.artistNames(ctx, canonical, page, size)
	})
}

func (s *Store) LoadArtist(ctx context.Context, id string) (ArtistDetail, error) {
	return artistSnapshot(ctx, s, func(snapshot *Store) (ArtistDetail, error) {
		canonical, err := snapshot.canonicalArtist(ctx, id)
		if err != nil {
			return ArtistDetail{}, err
		}
		// 旧身份详情保留自身摘要，只有子路由自动解析到合并后的身份。
		summaries, err := snapshot.loadArtistSummaries(ctx, []string{id})
		if err != nil {
			return ArtistDetail{}, err
		}
		summary, ok := summaries[id]
		if !ok {
			return ArtistDetail{}, fmt.Errorf("%w: missing artist profile", ErrQuery)
		}
		result := ArtistDetail{Artist: summary, Names: []ArtistName{}, RoleCounts: []ArtistRoleCount{}}
		if canonical != id {
			result.RedirectArtistID = &canonical
		}
		if err := snapshot.database.QueryRowContext(ctx, `SELECT a.edit_revision,CASE WHEN o.display_name IS NULL THEN 'auto' ELSE 'manual' END FROM artists a LEFT JOIN artist_overrides o ON o.artist_id=a.id WHERE a.id=$1::uuid`, id).Scan(&result.Revision, &result.NameSource); err != nil {
			return result, err
		}
		names, err := snapshot.artistNames(ctx, id, 1, 20)
		if err != nil {
			return result, err
		}
		result.Names = names.Items
		result.NamesPagination = names.Pagination
		rows, err := snapshot.database.QueryContext(ctx, `WITH `+artistParticipationCTE+`
 SELECT role,count(DISTINCT track_id),count(DISTINCT track_id) FILTER (WHERE present)
 FROM artist_participation GROUP BY role ORDER BY `+artistRoleOrder, []string{id})
		if err != nil {
			return result, err
		}
		defer rows.Close()
		for rows.Next() {
			var count ArtistRoleCount
			if err := rows.Scan(&count.Role, &count.Total, &count.Present); err != nil {
				return result, err
			}
			result.RoleCounts = append(result.RoleCounts, count)
		}
		return result, rows.Err()
	})
}

const artistVisiblePredicate = `EXISTS (
 SELECT 1 FROM artist_effective_credits e WHERE e.artist_id=a.id AND (
 EXISTS (SELECT 1 FROM tracks t WHERE t.id=e.track_id AND t.source_status='present') OR
 EXISTS (SELECT 1 FROM media m JOIN tracks t ON t.medium_id=m.id WHERE m.release_id=e.release_id AND t.source_status='present')))`

func selectedArtistsCTE() string {
	return `WITH selected AS (
 SELECT a.id,p.sort_key,p.initial_group FROM artists a JOIN artist_profiles p ON p.artist_id=a.id
 LEFT JOIN artist_overrides o ON o.artist_id=a.id WHERE o.redirect_artist_id IS NULL AND ` + artistVisiblePredicate + `
 AND ($1='' OR p.sort_key LIKE '%'||$1||'%' ESCAPE '~' OR EXISTS (SELECT 1 FROM artist_search_names n WHERE n.artist_id=a.id AND n.normalized_value LIKE '%'||$1||'%' ESCAPE '~'))
 AND ($2='' OR p.initial_group=$2) AND ($3='' OR EXISTS (SELECT 1 FROM artist_effective_credits e WHERE e.artist_id=a.id AND (e.role=$3 OR ($3='album_artist' AND e.role='release_artist' AND e.release_id IS NOT NULL)) AND (
 EXISTS(SELECT 1 FROM tracks t WHERE t.id=e.track_id AND t.source_status='present') OR EXISTS(SELECT 1 FROM media m JOIN tracks t ON t.medium_id=m.id WHERE m.release_id=e.release_id AND t.source_status='present')))))`
}

func (s *Store) artistListCount(ctx context.Context, query, initial, role string) (int64, error) {
	if err := textnorm.LoadEmbedded(); err != nil {
		return 0, err
	}
	args := []any{searchindex.EscapeLike(textnorm.Text(query)), initial, role}
	var count int64
	err := s.database.QueryRowContext(ctx, selectedArtistsCTE()+` SELECT count(*) FROM selected`, args...).Scan(&count)
	return count, err
}

func (s *Store) ListArtists(ctx context.Context, params ArtistListParams) (ArtistPage[ArtistSummary], error) {
	return artistSnapshot(ctx, s, func(snapshot *Store) (ArtistPage[ArtistSummary], error) {
		result := ArtistPage[ArtistSummary]{Items: []ArtistSummary{}, Pagination: ArtistPagination{Page: params.Page, PageSize: params.PageSize}}
		prefix := selectedArtistsCTE()
		total, err := snapshot.artistListCount(ctx, params.Query, params.Initial, params.Role)
		if err != nil {
			return result, err
		}
		result.Pagination.Total = total
		key := textnorm.Text(params.Query)
		args := []any{searchindex.EscapeLike(key), params.Initial, params.Role}
		order := `CASE WHEN initial_group='kana' THEN 'ZZ' WHEN initial_group='#' THEN 'ZZZ' ELSE initial_group END COLLATE "C",sort_key COLLATE "C",id`
		if params.Sort == "album_count" {
			prefix += `, album_counts AS (SELECT s.id,count(DISTINCT r.group_id) AS album_count FROM selected s
 JOIN artist_effective_credits e ON e.artist_id=s.id
 JOIN media m ON m.release_id=e.release_id OR m.id=(SELECT t.medium_id FROM tracks t WHERE t.id=e.track_id)
 JOIN releases r ON r.id=m.release_id WHERE EXISTS(SELECT 1 FROM tracks t WHERE t.medium_id=m.id AND t.source_status='present' AND (e.track_id IS NULL OR t.id=e.track_id)) GROUP BY s.id)`
			order = `COALESCE((SELECT album_count FROM album_counts c WHERE c.id=selected.id),0) DESC,sort_key COLLATE "C",id`
		}
		ids, err := snapshot.artistIDs(ctx, prefix+` SELECT id::text FROM selected ORDER BY `+order+` LIMIT $4 OFFSET $5`, append(args, params.PageSize, (params.Page-1)*params.PageSize)...)
		if err != nil {
			return result, err
		}
		summaries, err := snapshot.loadArtistSummaries(ctx, ids)
		if err != nil {
			return result, err
		}
		for _, id := range ids {
			item, ok := summaries[id]
			if !ok {
				return result, fmt.Errorf("%w: missing artist summary", ErrQuery)
			}
			result.Items = append(result.Items, item)
		}
		return result, nil
	})
}

func (s *Store) artistIDs(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.database.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}
