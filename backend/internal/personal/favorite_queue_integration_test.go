package personal

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// readFixtureTracks 批量构造纯合成行：每六首一首 missing，每七首一首未知时长。
func readFixtureTracks(t *testing.T, f *personalFixture, release fixtureRelease, count int) []string {
	t.Helper()
	f.exec(t, `INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,duration_seconds)
 SELECT ('10000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,$1::uuid,i,'合成曲目','艺人',1,$2::uuid,'read-'||i||'.flac',CASE WHEN i%6=0 THEN 'missing' ELSE 'present' END,NOW(),'physical',CASE WHEN i%7=0 THEN NULL ELSE 2 END FROM generate_series(1,$3::integer) i`, release.mediumID, f.rootID, count)
	ids := make([]string, count)
	for i := range ids {
		ids[i] = fmt.Sprintf("10000000-0000-4000-8000-%012d", i+1)
	}
	return ids
}

func readFixturePlaylist(t *testing.T, f *personalFixture, user string, ids []string) string {
	t.Helper()
	id := uuid.New()
	f.exec(t, `INSERT INTO personal_playlists(id,user_id,name,track_count,created_at,updated_at) VALUES($1::uuid,$2::uuid,'读取歌单',$3,'2026-11-04T00:00:00Z','2026-11-04T00:00:00Z')`, id, user, len(ids))
	if len(ids) > 0 {
		f.exec(t, `INSERT INTO personal_playlist_items(id,user_id,playlist_id,track_id,order_key,added_at) SELECT gen_random_uuid(),$1::uuid,$2::uuid,x.track_id,x.ord*4294967296,'2026-11-04T00:00:00Z' FROM unnest($3::text[]::uuid[]) WITH ORDINALITY AS x(track_id,ord)`, user, id, ids)
	}
	return id
}

func TestPostgreSQLFavoriteQueueFiltersBeforeLimitAndKeepsSnapshot(t *testing.T) {
	f := newPersonalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	user := f.user(t)
	ids := readFixtureTracks(t, f, f.release(t, "队列边界"), 12006)
	f.exec(t, `INSERT INTO favorite_tracks(user_id,track_id,created_at) SELECT $1::uuid,x,'2026-11-04T00:00:00Z' FROM unnest($2::text[]::uuid[]) x`, user, ids)
	// 首批边界落在同一时间内；下一批同时取剩余同时间两条和更早时间条目。
	f.exec(t, `UPDATE favorite_tracks SET created_at='2026-11-03T00:00:00Z' WHERE user_id=$1::uuid AND track_id>$2::uuid`, user, ids[10001])
	f.exec(t, `INSERT INTO personal_favorite_state(user_id,revision) VALUES($1::uuid,9)`, user)
	var factsBatches, summariesBatches []int
	mutated := false
	service := &Service{database: f.database, catalogFor: func(tx *sql.Tx) Catalog {
		source := catalog.NewStore(tx)
		return projectionCatalog{Catalog: source,
			facts: func(ctx context.Context, ids []string) (map[string]catalog.TrackFact, error) {
				factsBatches = append(factsBatches, len(ids))
				result, err := source.LoadTrackFacts(ctx, ids)
				if err == nil && !mutated {
					mutated = true
					// 第一个事实批次之后，从另一个连接改变成员和可用性；本次快照必须保持原版。
					f.exec(t, `DELETE FROM favorite_tracks WHERE user_id=$1::uuid AND track_id=$2::uuid`, user, ids[0])
					f.exec(t, `UPDATE personal_favorite_state SET revision=10 WHERE user_id=$1::uuid`, user)
					f.setStatus(t, ids[1], "missing")
				}
				return result, err
			},
			summaries: func(ctx context.Context, ids []string) (map[string]catalog.TrackSummary, error) {
				summariesBatches = append(summariesBatches, len(ids))
				return source.LoadTrackSummaries(ctx, ids)
			},
		}
	}}
	queue, err := service.FavoriteQueue(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if queue.Total != 12006 || queue.MissingCount != 2001 || queue.EligibleCount != 10005 || queue.ReturnedCount != 10000 || queue.OmittedCount != 5 || !queue.Truncated || queue.Limit != 10000 || queue.Revision != 9 {
		t.Fatalf("queue counts: total=%d missing=%d eligible=%d returned=%d omitted=%d revision=%d", queue.Total, queue.MissingCount, queue.EligibleCount, queue.ReturnedCount, queue.OmittedCount, queue.Revision)
	}
	if len(queue.Items) != 10000 || queue.Items[0].TrackID != ids[0] || queue.Items[1].TrackID != ids[1] || queue.Items[9999].TrackID != ids[11998] {
		t.Fatal("queue order or missing window incorrect")
	}
	if !reflect.DeepEqual(factsBatches, []int{10000, 2006}) || len(summariesBatches) != 10 {
		t.Fatalf("batch boundaries: facts=%v summaries=%v", factsBatches, summariesBatches)
	}
	for _, size := range summariesBatches {
		if size != 1000 {
			t.Fatalf("summary batch=%d", size)
		}
	}
	for _, item := range queue.Items {
		if !item.Present {
			t.Fatal("mixed catalog snapshots")
		}
	}
	current, err := f.service.FavoriteQueue(ctx, user)
	if err != nil || current.Total != 12005 || current.MissingCount != 2002 || current.EligibleCount != 10003 || current.OmittedCount != 3 || current.Revision != 10 {
		t.Fatalf("current queue total=%d missing=%d omitted=%d rev=%d err=%v", current.Total, current.MissingCount, current.OmittedCount, current.Revision, err)
	}
}

func TestPostgreSQLFavoriteLookupLargeBatchDoesNotReadCatalog(t *testing.T) {
	f := newPersonalFixture(t)
	user := f.user(t)
	release := f.release(t, "批量状态")
	ids := readFixtureTracks(t, f, release, 10000)
	f.exec(t, `INSERT INTO favorite_tracks(user_id,track_id) SELECT $1::uuid,x FROM unnest($2::text[]::uuid[]) x`, user, ids[:5000])
	f.exec(t, `INSERT INTO favorite_albums(user_id,group_id) VALUES($1::uuid,$2::uuid)`, user, release.groupID)
	f.exec(t, `INSERT INTO personal_favorite_state(user_id,revision) VALUES($1::uuid,1)`, user)
	f.service.catalogFor = func(*sql.Tx) Catalog { return projectionCatalog{} }
	groups := make([]string, 1000)
	groups[0] = release.groupID
	for i := 1; i < len(groups); i++ {
		groups[i] = uuid.New()
	}
	got, err := f.service.LookupFavorites(context.Background(), user, ids, groups)
	if err != nil || len(got.Tracks) != 10000 || len(got.Albums) != 1000 || got.Revision != 1 {
		t.Fatalf("lookup shape tracks=%d albums=%d err=%v", len(got.Tracks), len(got.Albums), err)
	}
	for i, item := range got.Tracks {
		if item.TrackID != ids[i] || item.Favorited != (i < 5000) {
			t.Fatalf("lookup position %d: %+v", i, item)
		}
	}
	for i, item := range got.Albums {
		if item.GroupID != groups[i] || item.Favorited != (i == 0) {
			t.Fatalf("album lookup position %d: %+v", i, item)
		}
	}
	if _, err := f.service.LookupFavorites(context.Background(), user, append(ids, ids[0]), nil); err != ErrInvalidInput {
		t.Fatalf("raw lookup boundary: %v", err)
	}
	if _, err := f.service.LookupFavorites(context.Background(), user, nil, append(groups, groups[0])); err != ErrInvalidInput {
		t.Fatalf("raw group boundary: %v", err)
	}
}
