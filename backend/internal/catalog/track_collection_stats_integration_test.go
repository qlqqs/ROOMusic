package catalog

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLTrackCollectionStats(t *testing.T) {
	f := newPlacementFixture(t)
	f.database.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	r := f.release("统计")
	present := f.track(r, "存在", "present", 1.5)
	missing := f.track(r, "缺失", "missing", 2.25)
	ids := []string{present, strings.ToUpper(present), missing, uuid.New(), "invalid"}
	for _, value := range []any{nil, 0, -1, "NaN", "Infinity", "-Infinity"} {
		ids = append(ids, f.track(r, "未知", "missing", value))
	}
	// 统计不生成完整摘要，不让坏封面影响匹配与时长事实。
	f.artwork(r, "../private.jpg")
	query := &personalCountingQueryer{Queryer: f.database}
	got, err := NewStore(query).LoadTrackCollectionStats(ctx, ids)
	want := TrackCollectionStats{MatchedCount: 8, MissingCount: 7, KnownDurationSeconds: 3.75, UnknownDurationCount: 6}
	if err != nil || got != want || query.queries != 1 {
		t.Fatalf("stats=%+v want=%+v queries=%d err=%v", got, want, query.queries, err)
	}
	if err := f.database.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	bounds := append(personalTestIDs(9999), present)
	got, err = f.store.LoadTrackCollectionStats(ctx, bounds)
	if err != nil || got.MatchedCount != 1 || got.KnownDurationSeconds != 1.5 {
		t.Fatalf("10000 boundary: %+v %v", got, err)
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if _, err := f.store.LoadTrackCollectionStats(cancelled, []string{present}); !errors.Is(err, context.Canceled) || !errors.Is(err, ErrQuery) {
		t.Fatalf("canceled query error lost: %v", err)
	}
	f.exec(`UPDATE tracks SET duration_seconds=$2 WHERE id=ANY($1::text[]::uuid[])`, []string{present, missing}, math.MaxFloat64)
	if _, err := f.store.LoadTrackCollectionStats(ctx, []string{present, missing}); err == nil {
		t.Fatal("duration overflow accepted")
	}
}

func TestPostgreSQLTrackCollectionStatsSnapshot(t *testing.T) {
	f := newPlacementFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	r := f.release("快照")
	id := f.track(r, "曲目", "present", 1)
	tx, err := f.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	reader := NewStore(tx)
	before, err := reader.LoadTrackCollectionStats(ctx, []string{id})
	if err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE tracks SET source_status='missing',duration_seconds=2 WHERE id=$1`, id)
	after, err := reader.LoadTrackCollectionStats(ctx, []string{id})
	if err != nil || before != after {
		t.Fatalf("mixed statistics snapshot: before=%+v after=%+v err=%v", before, after, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	current, err := f.store.LoadTrackCollectionStats(ctx, []string{id})
	if err != nil || current.MissingCount != 1 || current.KnownDurationSeconds != 2 {
		t.Fatalf("new snapshot did not see scan: %+v %v", current, err)
	}
}
