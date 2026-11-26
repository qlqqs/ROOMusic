package program

import (
	"context"
	"maps"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
)

func TestPostgreSQLScanReadMediaQueue(t *testing.T) {
	f := newPendingRefreshFixture(t)
	ctx := t.Context()
	now := time.Date(2026, 4, 8, 0, 0, 0, 0, time.UTC)
	_, err := f.writer.SetSettings(ctx, f.adminID, true)
	requirePendingRefreshOK(t, err)
	w := NewWorker(f.db, f.writer, curation.NewSources(f.db), func(context.Context) (bool, error) { return false, nil }, nil, Options{
		Now: func() time.Time { return now },
	})
	type entry struct {
		release, track string
	}
	add := func(name string) entry {
		t.Helper()
		r, tr := f.release(t, name, "合成艺人")
		return entry{r, tr}
	}
	music, podcast, audiobook, mixed, missing, cue := add("音乐"), add("播客"), add("有声书"), add("混合发行"), add("仅缺失轨带标记"), add("CUE 播客")
	for i, e := range []entry{music, podcast, audiobook, mixed, missing, cue} {
		// 预先入队，使后续标记测试覆盖已经存在的 queued 和到期 retry。
		f.exec(t, `INSERT INTO curation_work_states
			(subject,target_id,queue_release_id,queue_class,queue_gap,queue_added_at,rule_version,input_digest,status,needs_work)
			VALUES('release',$1,$1,'local_complete',ARRAY[0,0,0,0],$2,$3,'scan-read-fixture','pending',true)`,
			e.release, now.Add(time.Duration(i)*time.Minute), MatchRuleVersion)
	}
	f.exec(t, `UPDATE curation_work_states SET status='try_later',needs_work=false,next_attempt_at=$2 WHERE target_id=$1`, audiobook.release, now.Add(-time.Minute))
	f.exec(t, `UPDATE curation_work_states SET status='try_later',needs_work=false,next_attempt_at=$2 WHERE target_id=$1`, missing.release, now.Add(-time.Minute))

	requireCounts := func(t *testing.T, want int64) {
		t.Helper()
		remaining, ready, stable, err := w.queueCounts(ctx, "release")
		requirePendingRefreshOK(t, err)
		if remaining != want || ready != want || stable != want {
			t.Errorf("节目队列剩余/就绪/稳定 = %d/%d/%d，期望均为 %d", remaining, ready, stable, want)
		}
	}
	t.Run("queued_before_scan", func(t *testing.T) {
		requireCounts(t, 6)
		item, ok, err := w.nextQueueItem(ctx, "release")
		requirePendingRefreshOK(t, err)
		if !ok || item.TargetID != cue.release {
			t.Errorf("补标前应先取时间最新的发行，实际 %+v，找到=%t", item, ok)
		}
	})

	for _, tagged := range []struct {
		entry
		snapshot string
	}{
		{podcast, `{"podcast":true}`}, {audiobook, `{"audiobook":true}`},
		{mixed, `{"podcast":true,"audiobook":false}`}, {missing, `{"audiobook":true}`},
		{cue, `{"podcast":true}`}, {music, `{"podcast":false,"audiobook":false}`},
	} {
		f.exec(t, `UPDATE tracks SET scan_tags=$2::jsonb WHERE id=$1`, tagged.track, tagged.snapshot)
	}
	f.exec(t, `UPDATE tracks SET source_status='missing',source_identity=NULL WHERE id=$1`, missing.track)
	f.exec(t, `UPDATE tracks SET source_kind='cue_virtual',cue_sheet_path='synthetic.cue',cue_parent_relative_path='parent.flac',
		cue_referenced_file='parent.flac',cue_index_frames=0 WHERE id=$1`, cue.track)

	t.Run("curation_pagination_and_program_queue", func(t *testing.T) {
		want := map[string]bool{music.release: true, missing.release: true}
		got := map[string]bool{}
		var after *catalog.CurationCursor
		for range 7 {
			page, err := catalog.PageCurationReleases(ctx, f.db, after, 1)
			requirePendingRefreshOK(t, err)
			if len(page) == 0 {
				break
			}
			if got[page[0].ID] {
				t.Fatalf("整理分页重复返回 %s", page[0].ID)
			}
			got[page[0].ID] = true
			after = &page[0]
		}
		if !maps.Equal(got, want) {
			t.Errorf("整理分页 = %v，期望音乐和仅缺失成员带标记的发行 %v", got, want)
		}
		var visible int
		err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM (`+catalog.VisibleReleaseIDsSQL+`) v`).Scan(&visible)
		requirePendingRefreshOK(t, err)
		if visible != 6 {
			t.Errorf("普通曲库可见发行数 = %d，期望 6", visible)
		}
		requireCounts(t, 2)
		for _, expected := range []entry{missing, music} {
			item, ok, err := w.nextQueueItem(ctx, "release")
			requirePendingRefreshOK(t, err)
			if !ok || item.TargetID != expected.release {
				t.Errorf("程序队列取项 = %+v，找到=%t，期望 %s", item, ok, expected.release)
			}
			// 清掉已取项的测试种子，继续核对剩余队首。
			f.exec(t, `DELETE FROM curation_work_states WHERE subject='release' AND target_id=$1`, expected.release)
		}
		requireCounts(t, 0)
		item, ok, err := w.nextQueueItem(ctx, "release")
		requirePendingRefreshOK(t, err)
		if ok {
			t.Errorf("带标记的程序待办或到期重试仍可领取：%+v", item)
		}
	})

	t.Run("point_refresh_and_reeligibility", func(t *testing.T) {
		f.exec(t, `DELETE FROM curation_work_states WHERE subject='release' AND target_id=$1`, podcast.release)
		requirePendingRefreshOK(t, w.refreshReleaseQueue(ctx, podcast.release))
		var projected int
		err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM curation_work_states WHERE subject='release' AND target_id=$1`, podcast.release).Scan(&projected)
		requirePendingRefreshOK(t, err)
		if projected != 0 {
			t.Errorf("带播客标记的发行通过点刷创建了 %d 条程序队列记录", projected)
		}
		f.exec(t, `UPDATE tracks SET scan_tags=NULL WHERE id=$1`, podcast.track)
		requirePendingRefreshOK(t, w.refreshReleaseQueue(ctx, podcast.release))
		requireCounts(t, 1)
		item, ok, err := w.nextQueueItem(ctx, "release")
		requirePendingRefreshOK(t, err)
		if !ok || item.TargetID != podcast.release {
			t.Errorf("去标后程序队列取项 = %+v，找到=%t，期望 %s", item, ok, podcast.release)
		}
	})
}
