package personal

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type AddResult struct {
	Playlist       PlaylistState `json:"playlist"`
	AddedCount     int           `json:"added_count"`
	ExistingCount  int           `json:"existing_count"`
	DuplicateCount int           `json:"duplicate_count"`
}
type RenameResult struct {
	Playlist PlaylistState `json:"playlist"`
	Changed  bool          `json:"changed"`
}

func (s *Service) createPlaylist(ctx context.Context, tx *sql.Tx, user string, c Command) (AddResult, PlaylistState, error) {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM personal_playlists WHERE user_id=$1::uuid`, user).Scan(&count); err != nil {
		return AddResult{}, PlaylistState{}, err
	}
	if count >= 1000 {
		return AddResult{}, PlaylistState{}, ErrPlaylistLimit
	}
	ids, err := normalizeIDs(c.TrackIDs, TrackLimit)
	if err != nil {
		return AddResult{}, PlaylistState{}, err
	}
	if err = s.requireTracks(ctx, tx, ids); err != nil {
		return AddResult{}, PlaylistState{}, err
	}
	id := uuid.New()
	now := time.Now().UTC()
	p, err := scanPlaylist(tx.QueryRowContext(ctx, `INSERT INTO personal_playlists(id,user_id,name,track_count,created_at,updated_at)
 VALUES($1::uuid,$2::uuid,$3,$4,$5,$5) RETURNING `+playlistColumns, id, user, c.Name, len(ids), now))
	if err != nil {
		return AddResult{}, p, err
	}
	if err = appendItems(ctx, tx, user, p, ids); err != nil {
		return AddResult{}, p, err
	}
	if err = checkTrackCount(ctx, tx, user, p); err != nil {
		return AddResult{}, p, err
	}
	return AddResult{p, len(ids), 0, len(c.TrackIDs) - len(ids)}, p, nil
}
func (s *Service) requireTracks(ctx context.Context, tx *sql.Tx, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	facts, err := s.catalogFor(tx).LoadTrackFacts(ctx, ids)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, ok := facts[id]; !ok {
			return ErrNotFound
		}
	}
	return nil
}
func renamePlaylist(ctx context.Context, tx *sql.Tx, user string, p PlaylistState, name string) (RenameResult, error) {
	if p.Name == name {
		return RenameResult{p, false}, nil
	}
	if p.Revision >= MaxRevision {
		return RenameResult{}, fmt.Errorf("personal revision exhausted")
	}
	p, err := scanPlaylist(tx.QueryRowContext(ctx, `UPDATE personal_playlists SET name=$3,revision=revision+1,updated_at=clock_timestamp() WHERE user_id=$1::uuid AND id=$2::uuid RETURNING `+playlistColumns, user, p.ID, name))
	return RenameResult{p, true}, err
}
func (s *Service) addItems(ctx context.Context, tx *sql.Tx, user string, p PlaylistState, input []string) (AddResult, error) {
	ids, err := normalizeIDs(input, TrackLimit)
	if err != nil {
		return AddResult{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT track_id::text FROM personal_playlist_items WHERE user_id=$1::uuid AND playlist_id=$2::uuid AND track_id=ANY($3::text[]::uuid[])`, user, p.ID, ids)
	if err != nil {
		return AddResult{}, err
	}
	existing := map[string]bool{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return AddResult{}, err
		}
		existing[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return AddResult{}, err
	}
	added := make([]string, 0, len(ids))
	for _, id := range ids {
		if !existing[id] {
			added = append(added, id)
		}
	}
	result := AddResult{p, len(added), len(existing), len(input) - len(ids)}
	if len(added) == 0 {
		return result, nil
	}
	if err = s.requireTracks(ctx, tx, added); err != nil {
		return AddResult{}, err
	}
	if p.TrackCount+int64(len(added)) > TrackLimit {
		return AddResult{}, ErrTrackLimit
	}
	if err = appendItems(ctx, tx, user, p, added); err != nil {
		return AddResult{}, err
	}
	result.Playlist, err = bumpPlaylist(ctx, tx, user, p, int64(len(added)))
	return result, err
}
func appendItems(ctx context.Context, tx *sql.Tx, user string, p PlaylistState, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	var last sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT max(order_key) FROM personal_playlist_items WHERE user_id=$1::uuid AND playlist_id=$2::uuid`, user, p.ID).Scan(&last); err != nil {
		return err
	}
	key := last.Int64
	if key > math.MaxInt64-int64(len(ids))*orderStep {
		if err := rebalanceOrder(ctx, tx, user, p.ID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT max(order_key) FROM personal_playlist_items WHERE user_id=$1::uuid AND playlist_id=$2::uuid`, user, p.ID).Scan(&last); err != nil {
			return err
		}
		key = last.Int64
	}
	itemIDs := make([]string, len(ids))
	keys := make([]int64, len(ids))
	for i := range ids {
		itemIDs[i] = uuid.New()
		key += orderStep
		keys[i] = key
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO personal_playlist_items(id,user_id,playlist_id,track_id,order_key,added_at)
 SELECT x.id,$1::uuid,$2::uuid,x.track_id,x.order_key,$6 FROM unnest($3::text[]::uuid[],$4::text[]::uuid[],$5::bigint[]) AS x(id,track_id,order_key)`, user, p.ID, itemIDs, ids, keys, time.Now().UTC())
	return err
}
func bumpPlaylist(ctx context.Context, tx *sql.Tx, user string, p PlaylistState, delta int64) (PlaylistState, error) {
	if p.Revision >= MaxRevision || p.TrackCount+delta > MaxRevision || p.TrackCount+delta < 0 {
		return PlaylistState{}, fmt.Errorf("personal state exhausted")
	}
	next, err := scanPlaylist(tx.QueryRowContext(ctx, `UPDATE personal_playlists SET revision=revision+1,track_count=track_count+$3,updated_at=clock_timestamp() WHERE user_id=$1::uuid AND id=$2::uuid RETURNING `+playlistColumns, user, p.ID, delta))
	if err == nil {
		err = checkTrackCount(ctx, tx, user, next)
	}
	return next, err
}
func checkTrackCount(ctx context.Context, tx *sql.Tx, user string, p PlaylistState) error {
	var actual int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM personal_playlist_items WHERE user_id=$1::uuid AND playlist_id=$2::uuid`, user, p.ID).Scan(&actual); err != nil {
		return err
	}
	if actual != p.TrackCount {
		return fmt.Errorf("personal item count mismatch")
	}
	return nil
}
