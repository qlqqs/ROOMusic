package personal

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
)

func TestPostgreSQLRatingListArtistAnchors(t *testing.T) {
	f := newPersonalFixture(t)
	user, other := f.user(t), f.user(t)
	a, b := f.artist(t, "艺人甲"), f.artist(t, "艺人乙")
	alias, canonical := min(a, b), max(a, b)
	third := f.artist(t, "第三位艺人")
	private := f.artist(t, "另一用户评分的艺人")
	f.artistsReady(t)
	at := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	for i, tc := range []struct {
		user, artist string
		rating       int
	}{
		{user, canonical, 5},
		{user, alias, 2},
		{user, third, 4},
		{other, private, 5},
	} {
		if _, err := f.service.SetRating(t.Context(), tc.user, "artist", tc.artist, tc.rating); err != nil {
			t.Fatal(err)
		}
		f.exec(t, `UPDATE rating_artists SET rated_at=$3 WHERE user_id=$1 AND artist_id=$2`, tc.user, tc.artist, at.Add(time.Duration(i)*time.Hour))
	}
	type result struct {
		id     string
		rating int
		at     time.Time
	}
	oldHigh := result{canonical, 5, at}
	newLow := result{alias, 2, at.Add(time.Hour)}
	mergedLow := result{canonical, 2, at.Add(time.Hour)}
	four := result{third, 4, at.Add(2 * time.Hour)}
	check := func(t *testing.T, minRating int, sort string, page, size int, total int64, want ...result) {
		t.Helper()
		got, err := f.service.RatedArtists(t.Context(), user, RatedListParams{MinRating: minRating, Sort: sort, Page: Page{Number: page, Size: size}})
		if err != nil {
			t.Fatal(err)
		}
		if got.Items == nil || got.Pagination != (Pagination{Page: page, PageSize: size, Total: total}) || len(got.Items) != len(want) {
			t.Fatalf("艺人列表 %+v，期望第 %d 页、总数 %d、艺人 %v", got, page, total, want)
		}
		for i, item := range got.Items {
			if item.Artist.ID != want[i].id || item.Rating != want[i].rating || !item.RatedAt.Equal(want[i].at) {
				t.Fatalf("第 %d 项 %+v，期望 %+v", i, item, want[i])
			}
		}
	}
	t.Run("合并前", func(t *testing.T) {
		check(t, 1, "rating", 1, 10, 3, oldHigh, four, newLow)
		check(t, 1, "recent", 1, 10, 3, four, newLow, oldHigh)
	})
	f.artistRoute(t, alias, canonical)
	t.Run("合并后旧高分不通过筛选", func(t *testing.T) {
		check(t, 4, "rating", 1, 10, 1, four)
		check(t, 5, "recent", 1, 10, 0)
		check(t, 1, "rating", 1, 1, 2, four)
		check(t, 1, "rating", 2, 1, 2, mergedLow)
		check(t, 1, "rating", 3, 1, 2)
		check(t, 1, "recent", 1, 10, 2, four, mergedLow)
	})
	t.Run("相同评分时间按原锚点", func(t *testing.T) {
		f.exec(t, `UPDATE rating_artists SET rated_at=$3 WHERE user_id=$1 AND artist_id=$2`, user, alias, at)
		check(t, 1, "recent", 1, 10, 2, four, result{canonical, 2, at})
		f.exec(t, `UPDATE rating_artists SET rated_at=$3 WHERE user_id=$1 AND artist_id=$2`, user, alias, newLow.at)
	})
	f.artistRoute(t, alias, alias)
	t.Run("拆分后恢复各自评分", func(t *testing.T) {
		check(t, 1, "rating", 1, 10, 3, oldHigh, four, newLow)
		check(t, 4, "recent", 1, 10, 2, four, oldHigh)
	})
}

func TestPostgreSQLRatingQueueBoundaries(t *testing.T) {
	f := newPersonalFixture(t)
	user, other := f.user(t), f.user(t)
	ids := readFixtureTracks(t, f, f.release(t, "评分队列边界"), 12006)
	// 首批边界落在同分同时刻内。高分队列下一批还要跨时间和星级，
	// 最近队列先取低分与更新的曲目，然后跨越同一时间的批边界。
	f.exec(t, `INSERT INTO rating_tracks(user_id,track_id,rating,rated_at)
		SELECT $1,id,CASE WHEN i<=10004 THEN 5 WHEN i<=11000 THEN 4 ELSE 3 END,
		'2026-11-20T00:00:00Z'::timestamptz + CASE WHEN i<=10002 THEN interval '1 day'
			WHEN i<=10004 THEN interval '0 day' WHEN i<=11000 THEN interval '2 day' ELSE interval '3 day' END
		FROM unnest($2::text[]::uuid[]) WITH ORDINALITY AS x(id,i)`, user, ids)
	foreign := f.track(t, f.release(t, "他人的评分"), "仅另一用户评分的曲目", 60)
	f.exec(t, `INSERT INTO rating_tracks(user_id,track_id,rating,rated_at) VALUES($1,$2,5,'2026-11-25T00:00:00Z')`, other, foreign)

	var high, earlyHigh, medium, low []string
	for i, id := range ids {
		index := i + 1
		if index%6 == 0 {
			continue
		}
		switch {
		case index <= 10002:
			high = append(high, id)
		case index <= 10004:
			earlyHigh = append(earlyHigh, id)
		case index <= 11000:
			medium = append(medium, id)
		default:
			low = append(low, id)
		}
	}
	for _, tc := range []struct {
		name, sort     string
		minRating      int
		total, missing int64
		want           []string
	}{
		{"跨万首高分排序", "rating", 3, 12006, 2001, slices.Concat(high, earlyHigh, medium, low)},
		{"跨万首时间排序", "recent", 3, 12006, 2001, slices.Concat(low, medium, high, earlyHigh)},
		{"筛除低分高分排序", "rating", 4, 11000, 1833, slices.Concat(high, earlyHigh, medium)},
		{"筛除低分时间排序", "recent", 4, 11000, 1833, slices.Concat(medium, high, earlyHigh)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queue, err := f.service.RatedTrackQueue(t.Context(), user, tc.minRating, tc.sort)
			if err != nil {
				t.Fatal(err)
			}
			assertRatingQueue(t, queue, tc.total, tc.missing, tc.want)
		})
	}
	t.Run("恰好万首不截断", func(t *testing.T) {
		f.exec(t, `DELETE FROM rating_tracks WHERE user_id=$1 AND track_id>$2`, user, ids[11999])
		queue, err := f.service.RatedTrackQueue(t.Context(), user, 3, "rating")
		if err != nil {
			t.Fatal(err)
		}
		assertRatingQueue(t, queue, 12000, 2000, slices.Concat(high, earlyHigh, medium, low[:len(low)-5]))
	})
}

func assertRatingQueue(t *testing.T, queue QueueTracks, total, missing int64, ids []string) {
	t.Helper()
	const limit = 10000
	eligible := total - missing
	returned := min(eligible, limit)
	if queue.Total != total || queue.MissingCount != missing || queue.EligibleCount != eligible || queue.ReturnedCount != returned ||
		queue.OmittedCount != eligible-returned || queue.Truncated != (eligible > limit) || queue.Limit != limit ||
		len(queue.Items) != int(returned) || int(eligible) != len(ids) {
		t.Fatalf("队列计数错误：总数 %d，缺失 %d，可播放 %d，返回 %d，省略 %d，上限 %d，截断 %t，实际条目数 %d",
			queue.Total, queue.MissingCount, queue.EligibleCount, queue.ReturnedCount, queue.OmittedCount, queue.Limit, queue.Truncated, len(queue.Items))
	}
	for i, item := range queue.Items {
		if item.TrackID != ids[i] || !item.Present {
			t.Fatalf("第 %d 首为 %s（可用 %t），期望可用的 %s", i+1, item.TrackID, item.Present, ids[i])
		}
	}
}

func seedRatingPlanRows(t *testing.T, f *personalFixture, users []string) {
	t.Helper()
	const perKind = 12006
	tracks := readFixtureTracks(t, f, f.release(t, "评分查询计划"), perKind)
	f.exec(t, `INSERT INTO release_groups(id)
		SELECT ('20000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid FROM generate_series(1,$1::integer) i`, perKind)
	f.exec(t, `INSERT INTO artists(id,initial_name)
		SELECT ('30000000-0000-4000-8000-'||lpad(i::text,12,'0'))::uuid,'合成艺人 '||i FROM generate_series(1,$1::integer) i`, perKind)
	f.exec(t, `INSERT INTO artist_profiles(artist_id,auto_display_name,effective_display_name,sort_key,initial_group,version)
		SELECT id,initial_name,initial_name,initial_name,'#',1 FROM artists`)
	f.artistsReady(t)
	for _, tc := range []struct {
		table, columns, values, targets string
		args                            []any
	}{
		{"rating_tracks", "track_id", "x.id", "unnest($2::text[]::uuid[]) WITH ORDINALITY AS x(id,i)", []any{users, tracks}},
		{"rating_albums", "group_id,saved_title", "x.id,'合成专辑'", "(SELECT id,row_number() OVER (ORDER BY id) AS i FROM release_groups) x", []any{users}},
		{"rating_artists", "artist_id,resolved_artist_id", "x.id,COALESCE(x.previous,x.id)", `(
			SELECT id,row_number() OVER (ORDER BY id) AS i,
				CASE WHEN row_number() OVER (ORDER BY id)%3=0 THEN lag(id) OVER (ORDER BY id) END AS previous
			FROM artists) x`, []any{users}},
	} {
		f.exec(t, fmt.Sprintf(`INSERT INTO %s(user_id,%s,rating,rated_at)
			SELECT u.id,%s,1+((x.i+u.ord)%%5), '2026-11-20T00:00:00Z'::timestamptz + (x.i%%300) * interval '1 second'
			FROM %s CROSS JOIN unnest($1::text[]::uuid[]) WITH ORDINALITY AS u(id,ord)
			WHERE (x.i/5+u.ord)%%3=0 ORDER BY u.ord,x.i`, tc.table, tc.columns, tc.values, tc.targets), tc.args...)
	}
}

type ratingPageTrace struct {
	queries []pgx.TraceQueryStartData
}

func (trace *ratingPageTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(data.SQL, "SELECT target_id,rating,rated_at") && strings.Contains(data.SQL, "LIMIT") {
		data.Args = slices.Clone(data.Args)
		trace.queries = append(trace.queries, data)
	}
	return ctx
}

func (*ratingPageTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestPostgreSQLRatingListPaginationExplain(t *testing.T) {
	f := newPersonalFixture(t)
	users := make([]string, 9)
	for i := range users {
		users[i] = f.user(t)
	}
	seedRatingPlanRows(t, f, users)
	postgrestest.AnalyzeSchema(t, t.Context(), f.database, "rating_tracks", "rating_albums", "rating_artists")

	config, err := pgx.ParseConfig(f.databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	trace := &ratingPageTrace{}
	config.Tracer = trace
	tracedDB := stdlib.OpenDB(*config)
	defer tracedDB.Close()
	service := NewService(tracedDB)

	for _, tc := range []struct {
		kind, sort, index string
	}{
		{"tracks", "rating", "rating_tracks_highest_idx"},
		{"tracks", "recent", "rating_tracks_recent_idx"},
		{"albums", "rating", "rating_albums_highest_idx"},
		{"albums", "recent", "rating_albums_recent_idx"},
		{"artists", "rating", ""},
		{"artists", "recent", ""},
	} {
		t.Run(tc.kind+"/"+tc.sort, func(t *testing.T) {
			trace.queries = nil
			p := RatedListParams{MinRating: 4, Sort: tc.sort, Page: Page{Number: 2, Size: 50}}
			var count int
			var err error
			switch tc.kind {
			case "tracks":
				page, readErr := service.RatedTracks(t.Context(), users[0], p)
				count, err = len(page.Items), readErr
			case "albums":
				page, readErr := service.RatedAlbums(t.Context(), users[0], p)
				count, err = len(page.Items), readErr
			case "artists":
				page, readErr := service.RatedArtists(t.Context(), users[0], p)
				count, err = len(page.Items), readErr
			}
			if err != nil || count != 50 || len(trace.queries) != 1 {
				t.Fatalf("分页返回 %d 项，抓到 %d 条分页 SQL，错误 %v", count, len(trace.queries), err)
			}
			query := trace.queries[0]
			tx, err := f.service.readTx(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			rows, err := tx.QueryContext(t.Context(), "EXPLAIN (ANALYZE, BUFFERS) "+query.SQL, query.Args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var lines []string
			for rows.Next() {
				var line string
				if err := rows.Scan(&line); err != nil {
					t.Fatal(err)
				}
				lines = append(lines, line)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			plan := strings.Join(lines, "\n")
			t.Logf("第 2 页，每页 50 项，最低 4 星\n%s", plan)
			if tc.kind != "artists" {
				scan := strings.Index(plan, " using "+tc.index+" ")
				if scan < 0 {
					t.Fatalf("真实分页查询应使用 %s", tc.index)
				}
				return
			}
			if strings.Contains(plan, "Seq Scan on rating_artists") {
				t.Fatal("艺人评分不应全表扫描")
			}
			scopedScan := false
			artistBitmapHeapIndent := -1
			artistIndexScan := false
			for _, line := range lines {
				content := strings.TrimLeft(line, " ")
				if node, ok := strings.CutPrefix(content, "->  "); ok {
					indent := len(line) - len(content)
					if indent <= artistBitmapHeapIndent {
						artistBitmapHeapIndent = -1
					}
					if strings.HasPrefix(node, "Bitmap Heap Scan on rating_artists ") {
						artistBitmapHeapIndent = indent
					}
					direct := strings.HasPrefix(node, "Index Scan ") || strings.HasPrefix(node, "Index Only Scan ")
					artistIndexScan = (direct && strings.Contains(node, " on rating_artists ")) ||
						(artistBitmapHeapIndent >= 0 && strings.HasPrefix(node, "Bitmap Index Scan "))
				}
				if artistIndexScan && strings.HasPrefix(content, "Index Cond:") && strings.Contains(content, "user_id = ") {
					scopedScan = true
					break
				}
			}
			if !scopedScan {
				t.Fatal("艺人评分应使用包含 user_id 条件的索引扫描")
			}
			sort := strings.Index(plan, "->  Sort ")
			filter := strings.Index(plan, "Filter: (rated.rating")
			unique := strings.Index(plan, "->  Unique ")
			if sort < 0 || filter <= sort || unique <= filter {
				t.Fatal("艺人评分应先选出各艺人的最新评分，再按星级筛选并排序")
			}
		})
	}
}
