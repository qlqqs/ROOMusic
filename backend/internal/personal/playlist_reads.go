package personal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

type PlaylistsPage struct {
	Items      []PlaylistSummary `json:"items"`
	Pagination Pagination        `json:"pagination"`
}
type PlaylistDetail struct {
	Playlist PlaylistSummary `json:"playlist"`
}
type PlaylistItem struct {
	ID       string               `json:"id"`
	Position int64                `json:"position"`
	AddedAt  time.Time            `json:"added_at"`
	Track    catalog.TrackSummary `json:"track"`
}
type PlaylistItemsPage struct {
	Playlist        PlaylistState  `json:"playlist"`
	Items           []PlaylistItem `json:"items"`
	Pagination      Pagination     `json:"pagination"`
	PageAfterItemID *string        `json:"page_after_item_id"`
}
type ContainingPlaylist struct {
	PlaylistID string `json:"playlist_id"`
	Revision   int64  `json:"revision"`
}
type ContainingTrackResult struct {
	TrackID string               `json:"track_id"`
	Items   []ContainingPlaylist `json:"items"`
}

func (s *Service) Playlists(ctx context.Context, userID string, page Page) (PlaylistsPage, error) {
	offset, err := readPageOffset(page, 20, 1)
	if err != nil {
		return PlaylistsPage{}, err
	}
	tx, err := s.readTx(ctx)
	if err != nil {
		return PlaylistsPage{}, fmt.Errorf("begin playlists: %w", err)
	}
	defer tx.Rollback()
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM personal_playlists WHERE user_id=$1::uuid`, userID).Scan(&total); err != nil {
		return PlaylistsPage{}, fmt.Errorf("count playlists: %w", err)
	}
	states, err := readPlaylistPage(ctx, tx, userID, page.Size, offset)
	if err != nil {
		return PlaylistsPage{}, err
	}
	summaries, err := projectPlaylists(ctx, tx, s.catalogFor(tx), userID, states)
	if err != nil {
		return PlaylistsPage{}, err
	}
	if err := tx.Commit(); err != nil {
		return PlaylistsPage{}, fmt.Errorf("finish playlists: %w", err)
	}
	return PlaylistsPage{Items: summaries, Pagination: page.pagination(total)}, nil
}

func readPlaylistPage(ctx context.Context, tx *sql.Tx, userID string, size int, offset int64) ([]PlaylistState, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+playlistColumns+` FROM personal_playlists WHERE user_id=$1::uuid ORDER BY updated_at DESC,personal_playlists.id ASC LIMIT $2 OFFSET $3`, userID, size, offset)
	if err != nil {
		return nil, fmt.Errorf("read playlist page: %w", err)
	}
	defer rows.Close()
	states := make([]PlaylistState, 0, size)
	for rows.Next() {
		state, err := scanPlaylist(rows)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return states, rows.Close()
}

func ownedPlaylistState(ctx context.Context, tx *sql.Tx, userID, playlistID string) (PlaylistState, error) {
	state, err := scanPlaylist(tx.QueryRowContext(ctx, `SELECT `+playlistColumns+` FROM personal_playlists WHERE user_id=$1::uuid AND id=$2::uuid`, userID, playlistID))
	if errors.Is(err, sql.ErrNoRows) {
		return PlaylistState{}, ErrNotFound
	}
	if err != nil {
		return PlaylistState{}, fmt.Errorf("read owned playlist: %w", err)
	}
	return state, nil
}

func (s *Service) Playlist(ctx context.Context, userID, playlistID string) (PlaylistDetail, error) {
	ids, err := normalizeIDs([]string{playlistID}, 1)
	if err != nil {
		return PlaylistDetail{}, err
	}
	tx, err := s.readTx(ctx)
	if err != nil {
		return PlaylistDetail{}, fmt.Errorf("begin playlist: %w", err)
	}
	defer tx.Rollback()
	state, err := ownedPlaylistState(ctx, tx, userID, ids[0])
	if err != nil {
		return PlaylistDetail{}, err
	}
	summaries, err := projectPlaylists(ctx, tx, s.catalogFor(tx), userID, []PlaylistState{state})
	if err != nil {
		return PlaylistDetail{}, err
	}
	if err := tx.Commit(); err != nil {
		return PlaylistDetail{}, fmt.Errorf("finish playlist: %w", err)
	}
	return PlaylistDetail{Playlist: summaries[0]}, nil
}

func (s *Service) PlaylistItems(ctx context.Context, userID, playlistID string, page Page) (PlaylistItemsPage, error) {
	ids, err := normalizeIDs([]string{playlistID}, 1)
	if err != nil {
		return PlaylistItemsPage{}, err
	}
	offset, err := readPageOffset(page, 100, 1)
	if err != nil {
		return PlaylistItemsPage{}, err
	}
	tx, err := s.readTx(ctx)
	if err != nil {
		return PlaylistItemsPage{}, fmt.Errorf("begin playlist items: %w", err)
	}
	defer tx.Rollback()
	state, err := ownedPlaylistState(ctx, tx, userID, ids[0])
	if err != nil {
		return PlaylistItemsPage{}, err
	}
	if page.Revision != nil && *page.Revision != state.Revision {
		return PlaylistItemsPage{}, ErrRevisionConflict
	}
	refs, err := readPlaylistItemPage(ctx, tx, userID, ids[0], page.Size+1, offset)
	if err != nil {
		return PlaylistItemsPage{}, err
	}
	result := PlaylistItemsPage{Playlist: state, Items: []PlaylistItem{}, Pagination: page.pagination(state.TrackCount)}
	if len(refs) > page.Size {
		next := refs[page.Size].ID
		result.PageAfterItemID = &next
		refs = refs[:page.Size]
	}
	trackIDs := make([]string, len(refs))
	for i, ref := range refs {
		trackIDs[i] = ref.TrackID
	}
	summaries, err := orderedTrackSummaries(ctx, s.catalogFor(tx), trackIDs, false)
	if err != nil {
		return PlaylistItemsPage{}, err
	}
	for i, ref := range refs {
		result.Items = append(result.Items, PlaylistItem{ID: ref.ID, Position: offset + int64(i) + 1, AddedAt: ref.AddedAt.UTC(), Track: summaries[i]})
	}
	if err := tx.Commit(); err != nil {
		return PlaylistItemsPage{}, fmt.Errorf("finish playlist items: %w", err)
	}
	return result, nil
}

func readPlaylistItemPage(ctx context.Context, tx *sql.Tx, userID, playlistID string, size int, offset int64) ([]ItemRef, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id::text,track_id::text,added_at FROM personal_playlist_items WHERE user_id=$1::uuid AND playlist_id=$2::uuid ORDER BY order_key ASC LIMIT $3 OFFSET $4`, userID, playlistID, size, offset)
	if err != nil {
		return nil, fmt.Errorf("read playlist item page: %w", err)
	}
	defer rows.Close()
	refs := make([]ItemRef, 0, size)
	for rows.Next() {
		var ref ItemRef
		if err := rows.Scan(&ref.ID, &ref.TrackID, &ref.AddedAt); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return refs, rows.Close()
}

func (s *Service) ContainingTrack(ctx context.Context, userID, trackID string) (ContainingTrackResult, error) {
	ids, err := normalizeIDs([]string{trackID}, 1)
	if err != nil {
		return ContainingTrackResult{}, err
	}
	result := ContainingTrackResult{TrackID: ids[0], Items: []ContainingPlaylist{}}
	rows, err := s.database.QueryContext(ctx, `SELECT p.id::text,p.revision FROM personal_playlist_items i JOIN personal_playlists p ON p.user_id=i.user_id AND p.id=i.playlist_id WHERE i.user_id=$1::uuid AND p.user_id=$1::uuid AND i.track_id=$2::uuid ORDER BY p.id ASC LIMIT 1000`, userID, ids[0])
	if err != nil {
		return ContainingTrackResult{}, fmt.Errorf("read containing playlists: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item ContainingPlaylist
		if err := rows.Scan(&item.PlaylistID, &item.Revision); err != nil {
			return ContainingTrackResult{}, err
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return ContainingTrackResult{}, err
	}
	if err := rows.Close(); err != nil {
		return ContainingTrackResult{}, err
	}
	return result, nil
}

// projectPlaylists 只遍历本页至多二十张歌单，保留一批引用与有界统计，不截断超限歌单。
func projectPlaylists(ctx context.Context, tx *sql.Tx, source Catalog, userID string, states []PlaylistState) ([]PlaylistSummary, error) {
	result := make([]PlaylistSummary, len(states))
	ids := make([]string, len(states))
	indexes := make(map[string]int, len(states))
	counts := make([]int64, len(states))
	for i, state := range states {
		ids[i], indexes[state.ID] = state.ID, i
		result[i] = PlaylistSummary{PlaylistState: state, CoverGroups: []CoverGroup{}}
	}
	if len(states) == 0 {
		return result, nil
	}
	var cursor *playlistTrackRef
	for {
		refs, err := readPlaylistTrackBatch(ctx, tx, userID, ids, cursor)
		if err != nil {
			return nil, err
		}
		for start := 0; start < len(refs); {
			end := start + 1
			for end < len(refs) && refs[end].playlistID == refs[start].playlistID {
				end++
			}
			index, ok := indexes[refs[start].playlistID]
			if !ok {
				return nil, fmt.Errorf("playlist projection scope mismatch")
			}
			trackIDs := make([]string, end-start)
			for i, ref := range refs[start:end] {
				trackIDs[i] = ref.trackID
			}
			stats, err := source.LoadTrackCollectionStats(ctx, trackIDs)
			if err != nil {
				return nil, fmt.Errorf("load playlist statistics: %w", err)
			}
			if err := mergePlaylistStatistics(&result[index], stats, int64(len(trackIDs))); err != nil {
				return nil, err
			}
			counts[index] += stats.MatchedCount
			if err := collectPlaylistCoverGroups(ctx, source, &result[index], trackIDs); err != nil {
				return nil, err
			}
			start = end
		}
		if len(refs) < TrackLimit {
			break
		}
		last := refs[len(refs)-1]
		cursor = &last
	}
	groupIDs := make([]string, 0, len(states)*4)
	seenGroups := make(map[string]bool, len(states)*4)
	for i, summary := range result {
		if counts[i] != summary.TrackCount {
			return nil, fmt.Errorf("playlist track count inconsistent")
		}
		for _, group := range summary.CoverGroups {
			if !seenGroups[group.GroupID] {
				seenGroups[group.GroupID] = true
				groupIDs = append(groupIDs, group.GroupID)
			}
		}
	}
	if len(groupIDs) == 0 {
		return result, nil
	}
	groups, err := source.LoadReleaseGroupSummaries(ctx, groupIDs)
	if err != nil {
		return nil, fmt.Errorf("load playlist covers: %w", err)
	}
	for i := range result {
		for j, cover := range result[i].CoverGroups {
			group, ok := groups[cover.GroupID]
			if !ok {
				return nil, fmt.Errorf("playlist cover group missing")
			}
			if group.Available {
				if group.Representative == nil {
					return nil, fmt.Errorf("playlist cover representative missing")
				}
				result[i].CoverGroups[j].Artwork = group.Representative.Artwork
			}
		}
	}
	return result, nil
}
