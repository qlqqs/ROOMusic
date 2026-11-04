package catalog

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestLoadTrackSummariesInputBounds(t *testing.T) {
	store := NewStore(nil) // 无数据库证明空输入与超限请求不访问数据库。
	for _, ids := range [][]string{nil, {}, {"", "invalid"}} {
		got, err := store.LoadTrackSummaries(context.Background(), ids)
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("empty input: %v, %v", got, err)
		}
	}
	ids := make([]string, 1001)
	for i := range ids {
		ids[i] = uuid.New()
	}
	if _, err := store.LoadTrackSummaries(context.Background(), ids); err == nil {
		t.Fatal("accepted 1001 distinct IDs")
	}
}

func TestPostgreSQLLoadTrackSummaries(t *testing.T) {
	f := newEffectiveFixture(t)
	ctx := context.Background()
	visible, hidden := f.release("可见发行"), f.release("缺失发行")
	f.artwork(visible, "summary-cover.webp")
	a := f.track(visible, "有事实", "present", 245.5)
	b := f.track(hidden, "缺失标题", "missing", nil)
	f.exec(`UPDATE tracks SET codec=' flac ',bit_depth=24,sample_rate=96000,channels=2,bitrate=500000 WHERE id=$1::uuid`, a)
	f.exec(`UPDATE tracks SET codec=' ',bit_depth=NULL,sample_rate=NULL,channels=NULL,bitrate=NULL WHERE id=$1::uuid`, b)
	got, err := f.store.LoadTrackSummaries(ctx, []string{a, strings.ToUpper(a), a, b, uuid.New(), "invalid"})
	if err != nil || len(got) != 2 {
		t.Fatalf("summaries: %+v, %v", got, err)
	}
	known, missing := got[a], got[b]
	if !known.Present || known.DurationSeconds == nil || *known.DurationSeconds != 245.5 || known.Source.Codec == nil || *known.Source.Codec != "flac" || *known.Source.BitDepth != 24 || *known.Source.SampleRate != 96000 || *known.Source.Channels != 2 || *known.Source.Bitrate != 500000 || known.Source.SourceKind != "physical" {
		t.Fatalf("facts: %+v", known)
	}
	want, err := f.store.LoadReleaseSummary(ctx, visible.releaseID)
	if err != nil || !reflect.DeepEqual(known.Release, want) {
		t.Fatalf("summary mismatch: %+v, %+v, %v", known.Release, want, err)
	}
	if missing.Present || missing.TrackID != b || missing.Title != "缺失标题" || missing.Artist != "艺术家" || missing.GroupID != hidden.groupID || missing.Release.ID != hidden.releaseID || missing.Release.Title != "缺失发行" || missing.Release.TrackCount != 0 || missing.DurationSeconds != nil || missing.Source.Codec != nil || missing.Source.BitDepth != nil || missing.Source.SampleRate != nil || missing.Source.Channels != nil || missing.Source.Bitrate != nil {
		t.Fatalf("missing metadata: %+v", missing)
	}
	if _, err := f.store.LoadReleaseSummary(ctx, hidden.releaseID); !errors.Is(err, ErrReleaseNotFound) {
		t.Fatalf("visibility changed: %v", err)
	}
	placements, err := f.store.LoadTrackPlacements(ctx, []string{b})
	if err != nil || len(placements) != 0 {
		t.Fatalf("placement semantics changed: %v %v", placements, err)
	}
	f.exec(`UPDATE tracks SET medium_id=$2::uuid,source_status='present' WHERE id=$1::uuid`, b, visible.mediumID)
	got, err = f.store.LoadTrackSummaries(ctx, []string{b})
	if err != nil || !got[b].Present || got[b].Release.ID != visible.releaseID || got[b].GroupID != visible.groupID {
		t.Fatalf("current placement: %+v %v", got, err)
	}
	// 有效修正与列表共用投影，而不是读取旧扫描值。
	f.correct(visible.releaseID, "title", "修正标题")
	got, err = f.store.LoadTrackSummaries(ctx, []string{a})
	if err != nil || got[a].Release.Title != "修正标题" {
		t.Fatalf("effective release: %+v %v", got, err)
	}
}

func TestPostgreSQLLoadTrackSummariesUnknownDurationAndLimit(t *testing.T) {
	f := newPlacementFixture(t)
	release := f.release("未知事实")
	for _, value := range []any{nil, 0, -1, "NaN", "Infinity", "-Infinity"} {
		id := f.track(release, "曲目", "missing", value)
		got, err := f.store.LoadTrackSummaries(context.Background(), []string{id})
		if err != nil || got[id].DurationSeconds != nil {
			t.Fatalf("duration %v: %+v %v", value, got, err)
		}
	}
	id := f.track(release, "边界", "present", 10)
	ids := []string{id}
	for len(ids) < 1000 {
		ids = append(ids, uuid.New())
	}
	ids = append(ids, strings.ToUpper(id), id, "invalid")
	got, err := f.store.LoadTrackSummaries(context.Background(), ids)
	if err != nil || len(got) != 1 {
		t.Fatalf("1000 distinct IDs: %d %v", len(got), err)
	}
	f.artwork(release, "../private.webp")
	if _, err := f.store.LoadTrackSummaries(context.Background(), []string{id}); err == nil || strings.Contains(err.Error(), "private.webp") {
		t.Fatalf("unsafe artwork: %v", err)
	}
}

func TestPostgreSQLLoadTrackSummariesRejectsEmptySourceKind(t *testing.T) {
	f := newPlacementFixture(t)
	r := f.release("来源事实")
	id := f.track(r, "曲目", "present", 100)
	f.exec(`UPDATE tracks SET codec=E'\t \n' WHERE id=$1::uuid`, id)
	got, err := f.store.LoadTrackSummaries(context.Background(), []string{id})
	if err != nil || got[id].Source.Codec != nil {
		t.Fatalf("blank codec: %+v %v", got, err)
	}
	f.exec(`UPDATE tracks SET source_kind=' ' WHERE id=$1::uuid`, id)
	if _, err := f.store.LoadTrackSummaries(context.Background(), []string{id}); err == nil {
		t.Fatal("empty source kind was projected")
	}
}
