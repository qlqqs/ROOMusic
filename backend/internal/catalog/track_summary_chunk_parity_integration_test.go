package catalog

import (
	"context"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// TestPostgreSQLTrackSummaryChunkParity 用同一发行跨越摘要的 1,000 条输入边界，
// 固定每批上限下的逐批结果合同。摘要 SQL 的重排必须继续匹配发行 owner 的单发行投影。
func TestPostgreSQLTrackSummaryChunkParity(t *testing.T) {
	f := newEffectiveFixture(t)
	ctx := context.Background()
	r := f.release("跨批摘要")
	f.artwork(r, "chunk-parity.jpg")
	secondMedium := uuid.New()
	f.exec(`INSERT INTO media(id,release_id,position,title) VALUES($1::uuid,$2::uuid,2,'第二碟')`, secondMedium, r.releaseID)

	// 一个发行跨两个介质，共 1,001 首；首轨 present，其余 missing。两个缺失轨
	// 共用已解绑的旧来源路径（source_identity 必须为 NULL），attention 按来源去重。
	f.exec(`INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_identity,source_status,observed_at,source_kind,duration_seconds)
		SELECT md5('summary-chunk-track-'||i)::uuid,
		       CASE WHEN i <= 500 THEN $1::uuid ELSE $2::uuid END,
		       i, '跨批曲目'||i, '跨批艺人', 1, $3::uuid,
		       CASE WHEN i IN (1000,1001) THEN 'shared-old-source.flac' ELSE 'chunk-'||i||'.flac' END,
		       CASE WHEN i=1 THEN 'chunk-bound-present' ELSE NULL END,
		       CASE WHEN i=1 THEN 'present' ELSE 'missing' END,
		       NOW(), 'physical', CASE WHEN i=1 THEN 245.5 ELSE NULL END
		FROM generate_series(1,1001) i`, r.mediumID, secondMedium, f.rootID)

	// 打开三类 attention，再通过与当前依据匹配的核对压下它们；核对缺失后改动
	// 一首曲目的 observed_at，使核对失效。该状态同时覆盖逐曲依据与来源去重。
	f.uncertainField(r.releaseID, "source_type", "CD", []string{"CD", "LP"})
	f.groupingConflict(r.releaseID, ` ["synthetic/a.flac","synthetic/b.flac"] `)
	f.correct(r.releaseID, "title", "有效标题")
	f.reviewIssue(r.releaseID, "source_type")
	f.reviewIssue(r.releaseID, "grouping")
	f.reviewIssue(r.releaseID, "missing_tracks")
	f.exec(`UPDATE tracks SET observed_at=observed_at+INTERVAL '1 microsecond' WHERE id=md5('summary-chunk-track-1001')::uuid`)

	var ids []string
	rows, err := f.database.QueryContext(ctx, `SELECT id::text FROM tracks WHERE medium_id IN($1::uuid,$2::uuid) ORDER BY id`, r.mediumID, secondMedium)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1001 {
		t.Fatalf("fixture tracks=%d, want 1001", len(ids))
	}

	got := make(map[string]TrackSummary, len(ids))
	for start := 0; start < len(ids); start += maxSummaryTrackIDs {
		batch, err := f.store.LoadTrackSummaries(ctx, ids[start:min(start+maxSummaryTrackIDs, len(ids))])
		if err != nil {
			t.Fatalf("load batch at %d: %v", start, err)
		}
		for id, summary := range batch {
			got[id] = summary
		}
	}
	if len(got) != len(ids) {
		t.Fatalf("summaries=%d, want %d", len(got), len(ids))
	}

	want, err := scanReleaseSummary(f.database.QueryRowContext(ctx,
		"SELECT "+releasesql.ReleaseSummaryProjection+" FROM "+releasesql.ReleaseSource+releasesql.ReleaseSummaryArtworkJoin+" WHERE releases.id=$1::uuid", r.releaseID))
	if err != nil {
		t.Fatal(err)
	}
	if want.Title != "有效标题" || want.MediumCount != 1 || want.TrackCount != 1 || want.AttentionCount != 1 {
		t.Fatalf("unexpected canonical release summary: %+v", want)
	}
	for id, summary := range got {
		if !reflect.DeepEqual(summary.Release, want) {
			t.Fatalf("track %s batch release summary differs from canonical projection:\n got %+v\nwant %+v", id, summary.Release, want)
		}
	}
	presentCount, missingCount := 0, 0
	for _, summary := range got {
		if summary.Present {
			presentCount++
			if summary.DurationSeconds == nil || *summary.DurationSeconds != 245.5 {
				t.Errorf("present duration changed: %+v", summary)
			}
		} else {
			missingCount++
		}
	}
	if presentCount != 1 || missingCount != 1000 {
		t.Fatalf("present=%d missing=%d, want 1/1000", presentCount, missingCount)
	}
}
