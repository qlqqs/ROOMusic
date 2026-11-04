package listening

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

func TestPostgreSQLHistoryAggregationPaginationAndDelete(t *testing.T) {
	f := newListeningFixture(t)
	// 目录补全前必须释放统计结果的连接。
	f.database.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	a, b := f.user(t), f.user(t)
	r := f.release(t, "第一页")
	ids := []string{f.track(t, r, "甲", 200), f.track(t, r, "乙", 200), f.track(t, r, "丙", 200)}
	sort.Strings(ids)
	for _, id := range ids {
		f.playAt(t, a, id, baseTime)
	}
	f.playAt(t, a, ids[2], baseTime.Add(time.Hour))
	f.playAt(t, b, ids[2], baseTime)
	f.setStatus(t, ids[0], "missing")
	wantIDs := []string{ids[2], ids[0], ids[1]}
	var seen []string
	for page := 1; page <= 2; page++ {
		got, err := f.service.Tracks(ctx, a, page, 2)
		if err != nil || got.Pagination.Total != 3 || got.Pagination.Page != page || got.Pagination.PageSize != 2 || !got.HasHistory {
			t.Fatalf("page %d: %+v %v", page, got, err)
		}
		for _, item := range got.Items {
			seen = append(seen, item.TrackID)
		}
		if page == 1 && (got.Items[0].PlayCount != 2 || !got.Items[0].LastPlayedAt.Equal(baseTime.Add(time.Hour)) || got.Items[1].Present || got.Items[1].Title == "") {
			t.Fatalf("aggregation: %+v", got)
		}
	}
	if !reflect.DeepEqual(seen, wantIDs) {
		t.Fatalf("pagination: %v want %v", seen, wantIDs)
	}
	for _, page := range []int{3, 9999999} {
		got, err := f.service.Tracks(ctx, a, page, 2)
		if err != nil || got.Items == nil || len(got.Items) != 0 || got.Pagination.Total != 3 || got.Pagination.Page != page || !got.HasHistory {
			t.Fatalf("out of range: %+v %v", got, err)
		}
	}
	for _, want := range []int64{2, 0} {
		n, err := f.service.DeleteTrack(ctx, a, ids[2])
		if err != nil || n != want {
			t.Fatalf("delete: %d %v want %d", n, err, want)
		}
	}
	got, err := f.service.Queue(ctx, a)
	if err != nil || got.Total != 2 || len(got.Items) != 2 || got.Items[0].TrackID != ids[0] {
		t.Fatalf("remaining: %+v %v", got, err)
	}
	other, err := f.service.Queue(ctx, b)
	if err != nil || other.Total != 1 || other.Items[0].PlayCount != 1 {
		t.Fatalf("other user: %+v %v", other, err)
	}
	// 删除缺失曲目同样允许。
	if n, err := f.service.DeleteTrack(ctx, a, ids[0]); err != nil || n != 1 {
		t.Fatalf("delete missing: %d %v", n, err)
	}
}

func TestPostgreSQLHistoryQueueCandidateBoundary(t *testing.T) {
	f := newListeningFixture(t)
	ctx := context.Background()
	user := f.user(t)
	r := f.release(t, "边界发行")
	// 用合成行批量准备 1,001 首，避免逐首请求；统计包含缺失候选。
	f.exec(t, `INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind)
 SELECT ('10000000-0000-4000-8000-' || lpad(i::text,12,'0'))::uuid,$1::uuid,i,'边界曲目','艺人',1,$2::uuid,'queue-'||i||'.flac',CASE WHEN i=1000 THEN 'missing' ELSE 'present' END,NOW(),'physical'
 FROM generate_series(1,1001) i`, r.mediumID, f.rootID)
	f.exec(t, `INSERT INTO play_history(user_id,track_id,idempotency_key,played_seconds,played_at)
 SELECT $1::uuid,id,gen_random_uuid(),120,$2::timestamptz + position * interval '1 second' FROM tracks WHERE position<=1000`, user, baseTime)
	q, err := f.service.Queue(ctx, user)
	if err != nil || q.Total != 1000 || len(q.Items) != 1000 || q.Limit != 1000 || q.Truncated || !q.HasHistory || q.Items[0].Present {
		t.Fatalf("1000: total=%d items=%d truncated=%v err=%v", q.Total, len(q.Items), q.Truncated, err)
	}
	f.exec(t, `INSERT INTO play_history(user_id,track_id,idempotency_key,played_seconds,played_at)
 SELECT $1::uuid,id,gen_random_uuid(),120,$2::timestamptz + position * interval '1 second' FROM tracks WHERE position=1001`, user, baseTime)
	q, err = f.service.Queue(ctx, user)
	if err != nil || q.Total != 1001 || len(q.Items) != 1000 || !q.Truncated || q.Items[1].Present || q.Items[999].TrackID != fmt.Sprintf("10000000-0000-4000-8000-%012d", 2) {
		t.Fatalf("1001: total=%d items=%d truncated=%v err=%v", q.Total, len(q.Items), q.Truncated, err)
	}
	// 每页 100 项跨越 11 页，与队列前 1,000 项逐项一致，尾页只有最旧一首。
	seen := map[string]bool{}
	for page := 1; page <= 11; page++ {
		got, err := f.service.Tracks(ctx, user, page, 100)
		want := 100
		if page == 11 {
			want = 1
		}
		if err != nil || len(got.Items) != want || got.Pagination.Total != 1001 {
			t.Fatalf("page %d: %d items, total %d, %v", page, len(got.Items), got.Pagination.Total, err)
		}
		for index, item := range got.Items {
			offset := (page-1)*100 + index
			wantID := fmt.Sprintf("10000000-0000-4000-8000-%012d", 1001-offset)
			if seen[item.TrackID] || item.TrackID != wantID {
				t.Fatalf("duplicate or misplaced track at %d: %s", offset, item.TrackID)
			}
			if offset < 1000 && item.TrackID != q.Items[offset].TrackID {
				t.Fatal("queue and page order differ")
			}
			seen[item.TrackID] = true
		}
	}
	if len(seen) != 1001 {
		t.Fatalf("pagination lost tracks: %d", len(seen))
	}
	empty, err := f.service.Queue(ctx, f.user(t))
	if err != nil || empty.Items == nil || len(empty.Items) != 0 || empty.Total != 0 || empty.HasHistory || empty.Truncated || empty.Limit != 1000 {
		t.Fatalf("empty: %+v %v", empty, err)
	}
}

type missingHistorySummary struct{ Catalog }

func (missingHistorySummary) LoadTrackSummaries(context.Context, []string) (map[string]catalog.TrackSummary, error) {
	return map[string]catalog.TrackSummary{}, nil
}

func TestPostgreSQLHistoryFailsClosedOnMissingSummary(t *testing.T) {
	f := newListeningFixture(t)
	u := f.user(t)
	f.playAt(t, u, f.track(t, f.release(t, "专辑"), "曲目", 200), baseTime)
	s := newService(f.database, missingHistorySummary{})
	if _, err := s.Queue(context.Background(), u); err == nil {
		t.Fatal("missing referenced summary was silently discarded")
	}
}
