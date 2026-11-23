package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLCurationTrackAutoSurvivesRescan(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	app := f.application
	path := filepath.Join(app.allowedRoot, "重扫", "01.flac")
	write := func(title string) {
		writeSyntheticTrack(t, path, 72, "ALBUM=重扫", "ARTIST=本地艺人", "TITLE="+title, "TRACKNUMBER=1")
	}
	write("扫描标题")
	runScan(t, app, &f.admin)
	release := programString(t, app, `SELECT id FROM releases WHERE relative_directory='重扫'`)
	track, _ := externalTrack(t, app, release)
	op := applyTrackAuto(t, f, trackAutoRequest(track, release, "fixture", "", map[string]any{"title": "自动标题", "artist": "自动艺人"}))
	check := func() {
		t.Helper()
		requireTrackAutoOwner(t, f, track, "title", "自动标题", op)
		requireTrackAutoOwner(t, f, track, "artist", "自动艺人", op)
		b3RequireCount(t, app, 1, `SELECT count(*) FROM track_effective WHERE id=$1 AND title='自动标题' AND artist='自动艺人'`, track)
	}
	check()
	write("第二次扫描后的标题")
	runScan(t, app, &f.admin)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM tracks WHERE id=$1 AND title='第二次扫描后的标题' AND source_status='present'`, track)
	check()
}

func TestPostgreSQLCurationUnavailableReviewPreservesWrites(t *testing.T) {
	f := newSchedulerFixture(t)
	f.configure(t, &f.admin)
	item := f.pending(t, f.a)
	check := func() {
		t.Helper()
		for _, table := range []string{"operations", "release_auto_values", "track_auto_values", "external_links"} {
			b3RequireCount(t, f.application, 0, "SELECT count(*) FROM "+table)
		}
	}
	check()
	f.enable(t)
	f.next(t, false).tool(t, "correct_release", schedulerCorrection(f.a, 1, "edition", setChange("合成版本")))
	f.next(t, true).complete(t)
	want := f.clock.options().Now().Add(5 * time.Minute)
	schedulerWait(t, "不可用审查进入退避", func() bool {
		got := f.item(t, item.ID)
		return got.ReviewRetries == 1 && got.NextAttemptAt != nil && got.NextAttemptAt.Equal(want)
	})
	check()
}

func TestPostgreSQLCurationBatchRevertKeepsManualEdit(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	before := f.apply(t, f.a, "fixture", "", map[string]any{"title": "批前标题"})
	batch := f.batch(t, "program")
	op := f.apply(t, f.a, "fixture", batch, map[string]any{"title": "批内标题"})
	requireCurationOK(t, f.writer.CloseBatch(t.Context(), batch))
	manual, rev := f.correct(t, f.a, 1, map[string]any{"title": setChange("人工标题")})
	result := f.scope(t, curation.Scope{Kind: "batch", ID: batch}, "", uuid.New(), uuid.New())
	if result.Reverted != 1 || len(result.Skipped) != 0 || result.NextCursor != nil {
		t.Fatalf("按批撤销结果 %+v", result)
	}
	f.textOwner(t, "title", "批前标题", before)
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM operations WHERE reverts_operation_id=$1`, op)
	b3RequireCount(t, f.application, 0, `SELECT count(*) FROM operations WHERE reverts_operation_id=$1`, manual)
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM release_effective WHERE id=$1 AND title='人工标题' AND edit_revision=$2`, f.a, rev)
}
