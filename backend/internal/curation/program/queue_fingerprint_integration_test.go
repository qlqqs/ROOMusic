package program

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
)

type testQueuePrints func(context.Context, string) (TrackFingerprint, FingerprintStatus, error)

func (f testQueuePrints) Fingerprint(ctx context.Context, id string) (TrackFingerprint, FingerprintStatus, error) {
	return f(ctx, id)
}

func newFingerprintQueueFixture(t *testing.T, scan func(context.Context) (bool, error), prints testQueuePrints, finder b3FingerprintFinder) (pendingRefreshFixture, *Worker, *b3MatchFinder, string) {
	t.Helper()
	f := newPendingRefreshFixture(t)
	_, err := f.writer.SetSettings(t.Context(), f.adminID, true)
	requirePendingRefreshOK(t, err)
	release, _ := f.release(t, "合成专辑", "合成艺人")
	mb := &b3MatchFinder{}
	gate := curation.NewSources(f.db,
		curation.SourceRegistration{Declaration: musicbrainz.Declaration(), Releases: mb, Recordings: mb},
		curation.SourceRegistration{Declaration: sources.Declaration{ID: "acoustid", DefaultEnabled: true,
			Capabilities: []sources.Capability{sources.FingerprintLookup}}, Fingerprints: finder},
	).WithScanCheck(scan)
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	w := NewWorker(f.db, f.writer, gate, scan, nil, Options{Now: func() time.Time { return now }, Fingerprints: prints})
	requirePendingRefreshOK(t, w.refreshQueueProjection(t.Context()))
	return f, w, mb, release
}

func requireQueueItem(t *testing.T, w *Worker, subject string) queueItem {
	t.Helper()
	item, found, err := w.nextQueueItem(t.Context(), subject)
	requirePendingRefreshOK(t, err)
	if !found {
		t.Fatalf("应有可领取的 %s 项", subject)
	}
	return item
}

func testQueueWorkState(t *testing.T, w *Worker, subject, id string) curation.WorkState {
	t.Helper()
	states, err := curation.LoadWorkStates(t.Context(), w.db, subject, []string{id})
	requirePendingRefreshOK(t, err)
	return states[id]
}

func TestPostgreSQLFingerprintProjectionRejectsLateGeneration(t *testing.T) {
	ctx := t.Context()
	started := make(chan struct{})
	unblock := make(chan struct{})
	var computes atomic.Int32
	prints := testQueuePrints(func(ctx context.Context, id string) (TrackFingerprint, FingerprintStatus, error) {
		round := computes.Add(1)
		if round == 2 {
			close(started)
			select {
			case <-unblock:
			case <-ctx.Done():
				return TrackFingerprint{}, FingerprintTransient, ctx.Err()
			}
			return TrackFingerprint{}, FingerprintTransient, nil
		}
		return TrackFingerprint{Value: id, DurationSeconds: 100}, FingerprintOK, nil
	})
	f, w, mb, release := newFingerprintQueueFixture(t, func(context.Context) (bool, error) { return false, nil }, prints,
		func(string) ([]sources.FingerprintMatch, error) { return nil, nil })
	first := requireQueueItem(t, w, "fingerprint")
	requirePendingRefreshOK(t, w.processQueueItem(ctx, first))
	complete := testQueueWorkState(t, w, "fingerprint", first.TargetID)
	for range 2 {
		requirePendingRefreshOK(t, w.refreshQueueProjection(ctx))
		if again := testQueueWorkState(t, w, "fingerprint", first.TargetID); !reflect.DeepEqual(again, complete) || again.Status != "done" {
			t.Fatalf("同代盘点应保留完成记录，之前 %+v，之后 %+v", complete, again)
		}
	}
	old := requireQueueItem(t, w, "fingerprint")
	if old.TargetID == first.TargetID || old.Generation != first.Generation {
		t.Fatalf("同代第二项应是另一首，首项 %+v，第二项 %+v", first, old)
	}
	oldCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.processQueueItem(oldCtx, old) }()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("旧代计算开始前已退出：%v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("旧代计算尚未开始")
	}

	f.exec(t, `UPDATE curation_work_states SET status='try_later',needs_work=false,retry_count=1,
		match_rule_version=$2,next_attempt_at=$3,
		result=jsonb_build_object('retry',jsonb_build_object('step','acoustid','kind','album','source','local_fingerprint',
			'reason','fingerprint_failed','files',$4::text)) WHERE subject='release' AND target_id=$1`,
		release, MatchRuleVersion, w.opts.Now().Add(5*time.Second), old.Files)
	f.exec(t, `UPDATE tracks SET relative_path='new-snapshot.flac',source_size=900,source_modified_at=now(),content_fingerprint='new-content' WHERE id=$1`, old.TargetID)
	requirePendingRefreshOK(t, w.refreshQueueProjection(ctx))
	newState := testQueueWorkState(t, w, "release", release)
	if newState.Generation <= old.Generation || newState.RetryCount != 0 || newState.NextAttemptAt != nil || newState.Attempts != 0 {
		t.Fatalf("等待期间换文件应清理旧代错误，旧项 %+v，新状态 %+v", old, newState)
	}
	close(unblock)
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, errReleaseChanged) {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("旧代计算尚未返回")
	}

	seen := make(map[string]bool)
	for range 2 {
		current := requireQueueItem(t, w, "fingerprint")
		if current.Generation != newState.Generation || current.Files == old.Files || seen[current.TargetID] {
			t.Fatalf("旧代晚到后仍应各领取新快照一次，旧项 %+v，新项 %+v，已取 %v", old, current, seen)
		}
		seen[current.TargetID] = true
		requirePendingRefreshOK(t, w.processQueueItem(ctx, current))
	}
	item := requireQueueItem(t, w, "release")
	requirePendingRefreshOK(t, w.processQueueItem(ctx, item))
	after := testQueueWorkState(t, w, "release", release)
	if after.Status != "unlisted" || after.Attempts != 1 || after.RetryCount != 0 || after.RetrySource != "" || mb.searches == 0 || computes.Load() != 4 {
		t.Fatalf("仅应使用新代结果完成一次识别，状态 %+v，计算 %d，名字查询 %d", after, computes.Load(), mb.searches)
	}
}

func TestPostgreSQLFingerprintControlsDoNotCountAlbumFailure(t *testing.T) {
	for _, tc := range []string{"取消", "扫描开始", "数据库写失败", "计算途中停用来源"} {
		t.Run(tc, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var computes, lookups int
			var scanning bool
			var duringPrint func()
			f, w, mb, release := newFingerprintQueueFixture(t, func(context.Context) (bool, error) { return scanning, nil },
				func(context.Context, string) (TrackFingerprint, FingerprintStatus, error) {
					computes++
					duringPrint()
					if tc == "计算途中停用来源" {
						return TrackFingerprint{Value: "old-in-flight", DurationSeconds: 100}, FingerprintOK, nil
					}
					return TrackFingerprint{}, FingerprintTransient, nil
				}, func(string) ([]sources.FingerprintMatch, error) {
					lookups++
					return nil, nil
				})
			item := requireQueueItem(t, w, "fingerprint")
			before := testQueueWorkState(t, w, "release", release)
			switch tc {
			case "取消":
				duringPrint = cancel
			case "扫描开始":
				duringPrint = func() { scanning = true }
			case "数据库写失败":
				duringPrint = func() {}
				f.exec(t, `CREATE FUNCTION reject_queue_failure() RETURNS trigger LANGUAGE plpgsql AS $$
					BEGIN RAISE EXCEPTION 'fixture rejected query failure'; END $$`)
				f.exec(t, `CREATE TRIGGER reject_queue_failure BEFORE UPDATE ON curation_work_states FOR EACH ROW
					WHEN (NEW.subject='release' AND NEW.status='try_later') EXECUTE FUNCTION reject_queue_failure()`)
			case "计算途中停用来源":
				duringPrint = func() {
					_, err := w.sources.SetEnabled(t.Context(), "acoustid", f.adminID, false)
					requirePendingRefreshOK(t, err)
					requirePendingRefreshOK(t, w.refreshQueueProjection(t.Context()))
				}
			}
			err := w.processQueueItem(ctx, item)
			switch tc {
			case "取消":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("期望取消，实际 %v", err)
				}
			case "扫描开始":
				if failureCode(err) != "scan_running" {
					t.Fatalf("期望扫描闸门拒绝，实际 %v", err)
				}
			case "数据库写失败":
				requirePendingRefreshOK(t, err)
				err = w.processQueueItem(ctx, requireQueueItem(t, w, "release"))
				var dbError interface{ SQLState() string }
				if !errors.As(err, &dbError) || dbError.SQLState() != "P0001" {
					t.Fatalf("期望保留数据库异常，实际 %v", err)
				}
			case "计算途中停用来源":
				if err != nil && !errors.Is(err, errReleaseChanged) {
					t.Fatal(err)
				}
				requirePendingRefreshOK(t, w.processQueueItem(ctx, requireQueueItem(t, w, "release")))
				after := testQueueWorkState(t, w, "release", release)
				if after.Status != "unlisted" || after.Attempts != 1 || after.RetryCount != 0 || after.RetrySource != "" || mb.searches == 0 {
					t.Fatalf("停用后应释放依赖，执行名字查询，状态 %+v，名字查询 %d", after, mb.searches)
				}
			}
			if after := testQueueWorkState(t, w, "release", release); tc != "计算途中停用来源" && !reflect.DeepEqual(after, before) {
				t.Errorf("发行计次或状态被改写，之前 %+v，之后 %+v", before, after)
			}
			if computes != 1 || lookups != 0 {
				t.Errorf("本地调用 %d 次，AcoustID 调用 %d 次，期望各 1 和 0", computes, lookups)
			}
		})
	}
}

func TestPostgreSQLQueueProgressMixesReadyWaitingAndQueryError(t *testing.T) {
	ctx := t.Context()
	f := newPendingRefreshFixture(t)
	_, err := f.writer.SetSettings(ctx, f.adminID, true)
	requirePendingRefreshOK(t, err)
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	retryAt := now.Add(245 * time.Second)
	var ids []string
	for i, status := range []string{"pending", "try_later", "query_error"} {
		id, _ := f.release(t, fmt.Sprintf("合成专辑 %d", i), "合成艺人")
		ids = append(ids, id)
		var when *time.Time
		source, reason, kind, retries := "", "", "", 0
		switch status {
		case "try_later":
			when, source, reason, kind = &retryAt, "musicbrainz", "rate_limited", "source"
		case "query_error":
			source, reason, kind, retries = "acoustid", "invalid_response", "album", 3
		}
		f.exec(t, `INSERT INTO curation_work_states(subject,target_id,queue_release_id,rule_version,match_rule_version,input_digest,
			status,needs_work,next_attempt_at,retry_count,result)
			VALUES ('release',$1,$1,$2,$2,'fixture',$3,$3='pending',$4,$5,
			CASE WHEN $3='pending' THEN '{}'::jsonb ELSE
				jsonb_build_object('retry',jsonb_build_object('source',$6::text,'reason',$7::text,'kind',$8::text)) END)`,
			id, MatchRuleVersion, status, when, retries, source, reason, kind)
	}
	gate := curation.NewSources(f.db)
	w := NewWorker(f.db, f.writer, gate, func(context.Context) (bool, error) { return false, nil }, nil, Options{
		Now: func() time.Time { return now },
	})
	pause := sources.QueuePause{Source: "musicbrainz", Reason: "rate_limited", RetryAt: retryAt}
	w.policy.RestorePause(pause)
	w.setQueueState("release", "paused", pause.Reason, &pause)
	p, err := f.writer.Progress(ctx, MatchRuleVersion, w)
	requirePendingRefreshOK(t, err)
	q := p.Queues[0]
	if p.Releases.Total != 3 || p.Releases.Processed != 1 || p.Releases.TryLater != 1 || p.Releases.QueryError != 1 ||
		q.Remaining != 2 || q.Ready != 1 || q.Source == nil || *q.Source != "musicbrainz" ||
		q.RetryAt == nil || !q.RetryAt.Equal(retryAt) {
		t.Fatalf("队列应保留一个待领和一个等待，发行进度 %+v，队列 %+v", p.Releases, q)
	}
	wait := testQueueWorkState(t, w, "release", ids[1])
	if wait.RetrySource != "musicbrainz" || wait.NextAttemptAt == nil || !wait.NextAttemptAt.Equal(retryAt) {
		t.Fatalf("单张等待来源和期限错误：%+v", wait)
	}
	if got := requireQueueItem(t, w, "release"); got.TargetID != ids[0] {
		t.Fatalf("应领取就绪发行 %s，实际 %+v", ids[0], got)
	}

	// 不可见的终态行不应计入界面。
	f.exec(t, `UPDATE tracks SET source_status='missing' WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1)`, ids[2])
	p, err = f.writer.Progress(ctx, MatchRuleVersion, w)
	requirePendingRefreshOK(t, err)
	if p.Releases.Total != 2 || p.Releases.QueryError != 0 || p.Releases.Processed != 0 || p.Releases.TryLater != 1 {
		t.Fatalf("隐藏查询出错项后的发行进度 %+v", p.Releases)
	}

	f.exec(t, `UPDATE curation_work_states SET needs_work=false WHERE subject='release' AND target_id=$1`, ids[0])
	w.setQueueState("release", "idle", "", nil)
	p, err = f.writer.Progress(ctx, MatchRuleVersion, w)
	requirePendingRefreshOK(t, err)
	q = p.Queues[0]
	if q.State != "paused" || q.Remaining != 1 || q.Ready != 0 || q.Source == nil || *q.Source != "musicbrainz" ||
		q.RetryAt == nil || !q.RetryAt.Equal(retryAt) {
		t.Fatalf("仅剩等待项时应从持久状态显示来源及期限：%+v", q)
	}
}
