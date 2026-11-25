package listening

import (
	"context"
	"database/sql"
	"sort"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

type albumPlays struct {
	id    string
	count int64
	last  time.Time
}

// AlbumIDs 聚合本人的全部播放记录；使用目录当前的在场曲目归属，再按组分页。
func (s *Service) AlbumIDs(ctx context.Context, userID string, frequent bool, limit, offset int) ([]string, error) {
	if limit == 0 {
		return []string{}, nil
	}
	return catalog.ReadWithLocalJITOff(ctx, s.database, func(tx *sql.Tx) ([]string, error) {
		groups := map[string]*albumPlays{}
		store := catalog.NewStore(tx)
		var after *string
		for {
			rows, err := tx.QueryContext(ctx, `SELECT track_id::text,count(*),max(played_at)
 FROM play_history WHERE user_id=$1::uuid AND ($2::uuid IS NULL OR track_id>$2)
 GROUP BY track_id ORDER BY track_id LIMIT 1000`, userID, after)
			if err != nil {
				return nil, err
			}
			ids := []string{}
			plays := []albumPlays{}
			for rows.Next() {
				var v albumPlays
				if err := rows.Scan(&v.id, &v.count, &v.last); err != nil {
					rows.Close()
					return nil, err
				}
				ids = append(ids, v.id)
				plays = append(plays, v)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
			facts, err := store.LoadTrackFacts(ctx, ids)
			if err != nil {
				return nil, err
			}
			for _, v := range plays {
				f := facts[v.id]
				if !f.Present {
					continue
				}
				a := groups[f.GroupID]
				if a == nil {
					a = &albumPlays{id: f.GroupID}
					groups[f.GroupID] = a
				}
				a.count += v.count
				if a.last.Before(v.last) {
					a.last = v.last
				}
			}
			if len(ids) < 1000 {
				break
			}
			after = &ids[len(ids)-1]
		}
		albums := make([]*albumPlays, 0, len(groups))
		for _, a := range groups {
			albums = append(albums, a)
		}
		sort.Slice(albums, func(i, j int) bool {
			a, b := albums[i], albums[j]
			if frequent && a.count != b.count {
				return a.count > b.count
			}
			if !a.last.Equal(b.last) {
				return a.last.After(b.last)
			}
			return a.id < b.id
		})
		ids := []string{}
		for _, a := range albums[min(offset, len(albums)):] {
			if len(ids) == limit {
				break
			}
			ids = append(ids, a.id)
		}
		return ids, nil
	})
}
