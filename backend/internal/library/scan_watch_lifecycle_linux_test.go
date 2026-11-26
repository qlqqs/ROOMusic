package library

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"golang.org/x/sys/unix"
)

func latestDirectoryScan(t *testing.T, f *libraryFixture) string {
	t.Helper()
	var id string
	directoryOK(t, f.database.QueryRowContext(t.Context(), "SELECT id FROM scan_runs ORDER BY started_at DESC,id DESC LIMIT 1").Scan(&id))
	return id
}

func requireWatchMode(t *testing.T, s *Service, rootID, mode string) {
	t.Helper()
	waitDirectoryCondition(t, "目录监听模式 "+mode, func() bool {
		roots, err := s.ListRoots(t.Context())
		directoryOK(t, err)
		for _, root := range roots {
			if root.ID == rootID {
				if root.ScanWatchMode == nil {
					return mode == ""
				}
				return *root.ScanWatchMode == mode
			}
		}
		t.Fatalf("目录列表缺少 %s", rootID)
		return false
	})
}

func currentDirectoryWatch(s *Service, id string) *watchedRoot {
	s.watch.mu.Lock()
	defer s.watch.mu.Unlock()
	return s.watch.roots[id]
}

func waitDirectoryEvents(t *testing.T, s *Service, id string, dirs ...string) {
	t.Helper()
	waitDirectoryCondition(t, "目录事件", func() bool {
		s.watch.mu.Lock()
		defer s.watch.mu.Unlock()
		root := s.watch.roots[id]
		if root == nil {
			return false
		}
		for _, dir := range dirs {
			if !slices.ContainsFunc(root.pending, func(b *directoryBatch) bool { return b.directories[dir] }) {
				return false
			}
		}
		return true
	})
}

func registerTestWatchContext(t *testing.T, s *Service) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() {
		cancel()
		done := make(chan struct{})
		go func() { s.watch.workers.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("目录监听读循环未退出")
		}
	})
	return ctx
}

func TestPostgreSQLDirectoryWatchLifecycle(t *testing.T) {
	t.Run("实际事件与根生命周期", func(t *testing.T) {
		f := newLibraryFixture(t)
		clock := newDirectoryTestClock()
		s := scannerTestApplication(f).WithDirectoryWatchClock(clock)
		ctx := registerTestWatchContext(t, s)
		var watchedFD atomic.Int64
		s.watch.system.add = func(fd int, path string, mask uint32) (int, error) {
			watchedFD.Store(int64(fd))
			return unix.InotifyAddWatch(fd, path, mask)
		}
		actor := uuid.New()
		directoryExec(t, f, `INSERT INTO users(id,username,password_hash,role) VALUES($1,'watch-admin','synthetic','admin')`, actor)
		op := func(key string) RootOperation {
			return RootOperation{ActorID: actor, IdempotencyKey: key, RequestID: key}
		}
		root := registeredRoot{Path: filepath.Join(f.allowedRoot, "Local")}
		directoryOK(t, os.MkdirAll(filepath.Join(root.Path, "Continuous"), 0o755))
		directoryOK(t, os.Mkdir(filepath.Join(root.Path, "Interrupted"), 0o755))
		allowed := []string{f.allowedRoot}
		added, err := s.AddRoot(ctx, op("add"), root.Path, allowed)
		directoryOK(t, err)
		var created RootInfo
		directoryOK(t, json.Unmarshal(added.Response, &created))
		root.ID = created.ID
		directoryOK(t, s.refreshWatches(ctx))
		requireWatchMode(t, s, root.ID, "")

		gate := blockDirectoryProjection(t, f)
		baseline := startDirectoryScan(t, s, StartOptions{Mode: ScanModeFull})
		gate.wait(t)
		directoryOK(t, s.refreshWatches(ctx))
		if currentDirectoryWatch(s, root.ID) != nil || watchedFD.Load() != 0 {
			t.Error("首次整根扫描提交前已运行监听")
		}
		assertDatabaseCount(t, f, `SELECT count(*) FROM library_roots WHERE id=$1 AND watch_baseline_at IS NULL`, 1, root.ID)
		gate.release(t)
		waitDirectoryScan(t, s, baseline, "succeeded")
		directoryOK(t, s.refreshWatches(ctx))
		requireWatchMode(t, s, root.ID, "realtime")

		// 在监听根外写完文件，逐首原子放入，确保每次推进时钟前只有一个文件到达事件。
		staging := registeredRoot{Path: t.TempDir()}
		copyTrack := func(dir, name string, seed byte) {
			t.Helper()
			writeDirectoryTrack(t, staging, name, dir, seed)
			directoryOK(t, os.Rename(filepath.Join(staging.Path, name), filepath.Join(root.Path, dir, name)))
			waitDirectoryEvents(t, s, root.ID, dir)
		}
		seenCount := func(want int) {
			t.Helper()
			assertDatabaseCount(t, f, `SELECT count(*) FROM tracks WHERE source_root_id=$1 AND source_status='present'`, want, root.ID)
		}
		cycle := func() { s.handleWatchedRoot(ctx, root.ID, clock.Now()) }
		copyTrack("Continuous", "01.flac", 31)
		clock.advance(time.Second)
		cycle()
		seenCount(0)
		copyTrack("Continuous", "02.flac", 32)
		// 观察第二首到达后更新的截止时刻，防止把第一首仍在排队误认为第二首已处理。
		waitDirectoryCondition(t, "第二首延后安静窗口", func() bool {
			s.watch.mu.Lock()
			defer s.watch.mu.Unlock()
			pending := s.watch.roots[root.ID].pending
			return len(pending) == 1 && pending[0].due.Equal(clock.Now().Add(watchQuiet))
		})
		clock.advance(1999 * time.Millisecond)
		cycle()
		seenCount(0)
		assertDatabaseCount(t, f, `SELECT count(*) FROM scan_runs`, 1)
		directoryExec(t, f, `CREATE FUNCTION test_watch_second_track() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN PERFORM pg_advisory_xact_lock(hashtextextended(current_schema(),42)); RETURN NEW; END $$;
			CREATE TRIGGER test_watch_second_track BEFORE INSERT ON tracks FOR EACH ROW
			WHEN (NEW.relative_path='Continuous/02.flac') EXECUTE FUNCTION test_watch_second_track()`)
		secondTrack := beginDirectoryGate(t, f)
		_, err = secondTrack.tx.ExecContext(t.Context(), `SELECT pg_advisory_xact_lock(hashtextextended(current_schema(),42))`)
		directoryOK(t, err)
		clock.advance(time.Millisecond)
		cycle()
		secondTrack.wait(t)
		seenCount(0)
		secondTrack.release(t)
		waitDirectoryScan(t, s, latestDirectoryScan(t, f), "succeeded")
		seenCount(2)
		assertDatabaseCount(t, f, `SELECT count(*) FROM scan_runs`, 2)

		copyTrack("Interrupted", "01.flac", 33)
		clock.advance(2001 * time.Millisecond)
		cycle()
		waitDirectoryScan(t, s, latestDirectoryScan(t, f), "succeeded")
		seenCount(3)
		copyTrack("Interrupted", "02.flac", 34)
		clock.advance(2 * time.Second)
		cycle()
		waitDirectoryScan(t, s, latestDirectoryScan(t, f), "succeeded")
		seenCount(4)

		// 改名和移出走真实 move cookie；整棵移入后已有的子目录也要入库。
		oldTrack := presentTrackIDs(t, f, root.ID)["Continuous/01.flac"]
		moveFixture(t, root, "Continuous", "Renamed")
		waitDirectoryEvents(t, s, root.ID, "Continuous", "Renamed")
		clock.advance(2 * time.Second)
		cycle()
		waitDirectoryScan(t, s, latestDirectoryScan(t, f), "succeeded")
		requireTrackAt(t, presentTrackIDs(t, f, root.ID), "Renamed/01.flac", oldTrack)
		directoryOK(t, os.Remove(filepath.Join(root.Path, "Renamed", "02.flac")))
		waitDirectoryEvents(t, s, root.ID, "Renamed")
		clock.advance(2 * time.Second)
		cycle()
		waitDirectoryScan(t, s, latestDirectoryScan(t, f), "succeeded")
		seenCount(3)
		directoryOK(t, os.Rename(filepath.Join(root.Path, "Renamed"), filepath.Join(staging.Path, "Tree")))
		waitDirectoryEvents(t, s, root.ID, "Renamed")
		clock.advance(2 * time.Second)
		cycle()
		waitDirectoryScan(t, s, latestDirectoryScan(t, f), "succeeded")
		seenCount(2)
		directoryOK(t, os.Rename(filepath.Join(staging.Path, "Tree"), filepath.Join(root.Path, "Imported")))
		waitDirectoryEvents(t, s, root.ID, "Imported")
		clock.advance(2 * time.Second)
		cycle()
		waitDirectoryScan(t, s, latestDirectoryScan(t, f), "succeeded")
		seenCount(3)

		exclusion := filepath.Join(root.Path, "Interrupted")
		// IN_MODIFY/CLOSE_WRITE 必须使已入库的文件重新观察。
		modified := filepath.Join(root.Path, "Imported", "01.flac")
		var oldSize int64
		directoryOK(t, f.database.QueryRowContext(t.Context(), `SELECT source_size FROM tracks WHERE source_root_id=$1
			AND relative_path='Imported/01.flac' AND source_status='present'`, root.ID).Scan(&oldSize))
		file, err := os.OpenFile(modified, os.O_WRONLY|os.O_APPEND, 0)
		directoryOK(t, err)
		_, err = file.Write([]byte{0, 0, 0})
		directoryOK(t, err)
		directoryOK(t, file.Close())
		// 另一个目录的单个属性事件排在同一 fd 的改动、关闭之后。
		now := time.Now()
		directoryOK(t, os.Chtimes(filepath.Join(exclusion, "01.flac"), now, now))
		waitDirectoryEvents(t, s, root.ID, "Imported", "Interrupted")
		clock.advance(2 * time.Second)
		cycle()
		waitDirectoryScan(t, s, latestDirectoryScan(t, f), "succeeded")
		assertDatabaseCount(t, f, `SELECT count(*) FROM tracks WHERE source_root_id=$1 AND relative_path='Imported/01.flac'
			AND source_status='present' AND source_size=$2`, 1, root.ID, oldSize+3)

		beforeExclusion := currentDirectoryWatch(s, root.ID)
		_, err = s.AddExclusion(ctx, op("exclude"), root.ID, exclusion, allowed)
		directoryOK(t, err)
		directoryOK(t, s.refreshWatches(ctx))
		requireWatchMode(t, s, root.ID, "realtime")
		if currentDirectoryWatch(s, root.ID) == beforeExclusion {
			t.Error("新增排除后没有重新注册监听")
		}
		writeDirectoryTrack(t, root, "Interrupted/03.flac", "Interrupted", 35)
		now = time.Now()
		directoryOK(t, os.Chtimes(modified, now, now))
		waitDirectoryEvents(t, s, root.ID, "Imported")
		clock.advance(2 * time.Second)
		cycle()
		waitDirectoryScan(t, s, latestDirectoryScan(t, f), "succeeded")
		seenCount(3)
		_, err = s.RemoveExclusion(ctx, op("unexclude"), root.ID, "Interrupted")
		directoryOK(t, err)
		directoryOK(t, s.refreshWatches(ctx))
		requireWatchMode(t, s, root.ID, "realtime")
		waitDirectoryEvents(t, s, root.ID, "Interrupted")
		clock.advance(2 * time.Second)
		cycle()
		waitDirectoryScan(t, s, latestDirectoryScan(t, f), "succeeded")
		seenCount(4)

		_, err = s.DisableRoot(ctx, op("disable"), root.ID, 1)
		directoryOK(t, err)
		directoryOK(t, s.refreshWatches(ctx))
		requireWatchMode(t, s, root.ID, "")
		if currentDirectoryWatch(s, root.ID) != nil {
			t.Error("停用后仍保留旧监听")
		}
		replacement := filepath.Join(f.allowedRoot, "Replacement")
		directoryOK(t, os.Mkdir(replacement, 0o755))
		_, err = s.RelocateRoot(ctx, op("relocate"), root.ID, 2, replacement, allowed)
		directoryOK(t, err)
		_, err = s.RestoreRoot(ctx, op("restore"), root.ID, 3)
		directoryOK(t, err)
		directoryOK(t, s.refreshWatches(ctx))
		requireWatchMode(t, s, root.ID, "")
		root.Path = replacement
		writeDirectoryTrack(t, root, "Replacement/01.flac", "Replacement", 36)
		boundaryCompletedScan(t, s)

		// 新服务从数据库重新建立监听，停机等待其读循环与 fd 一起退出。
		restarted := scannerTestApplication(f).WithDirectoryWatchClock(clock)
		watchCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		var fd atomic.Int64
		restarted.watch.system.add = func(descriptor int, path string, mask uint32) (int, error) {
			fd.Store(int64(descriptor))
			return unix.InotifyAddWatch(descriptor, path, mask)
		}
		done := make(chan struct{})
		go func() { restarted.RunDirectoryWatcher(watchCtx); close(done) }()
		requireWatchMode(t, restarted, root.ID, "realtime")
		waitDirectoryCondition(t, "重启后的调度计时器", func() bool {
			clock.mu.Lock()
			defer clock.mu.Unlock()
			return clock.timer != nil && !clock.timer.stopped
		})
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("停机后监听调度器未退出")
		}
		requireClosedWatchFD(t, int(fd.Load()))
		clock.mu.Lock()
		timerStopped := clock.timer.stopped
		clock.mu.Unlock()
		if !timerStopped {
			t.Error("停机后监听调度计时器仍在运行")
		}
	})

	t.Run("忙碌、运行中事件和溢出", func(t *testing.T) {
		f := newLibraryFixture(t)
		clock := newDirectoryTestClock()
		s := scannerTestApplication(f).WithDirectoryWatchClock(clock)
		root := insertScannerRoot(t, f, "watch-batches")
		root.revision = 1
		writeDirectoryTrack(t, root, "A/01.flac", "A", 38)
		writeDirectoryTrack(t, root, "B/01.flac", "B", 39)
		boundaryCompletedScan(t, s)
		watched := &watchedRoot{root: root, mode: "realtime"}
		s.watch.roots[root.ID] = watched
		record := func(event watchEvent) { s.watch.record(watched, event) }
		cycle := func() { s.handleWatchedRoot(t.Context(), root.ID, clock.Now()) }

		gate := blockDirectoryProjection(t, f)
		blocker := scannerTestApplication(f)
		busy := startDirectoryScan(t, blocker, scopedDirectoryOptions(root, []string{"B"}, nil))
		gate.wait(t)
		record(watchEvent{dirs: []string{"A"}})
		clock.advance(watchQuiet)
		cycle()
		assertDatabaseCount(t, f, `SELECT count(*) FROM scan_runs`, 2)
		if len(watched.pending) != 1 {
			t.Error("另一进程忙碌时事件丢失")
		}
		gate.release(t)
		waitDirectoryScan(t, blocker, busy, "succeeded")

		gate = blockDirectoryProjection(t, f)
		cycle()
		gate.wait(t)
		first := latestDirectoryScan(t, f)
		writeDirectoryTrack(t, root, "A/02.flac", "A", 40)
		record(watchEvent{dirs: []string{"A"}})
		clock.advance(watchQuiet)
		cycle()
		assertDatabaseCount(t, f, `SELECT count(*) FROM scan_runs`, 3)
		gate.release(t)
		waitDirectoryScan(t, s, first, "succeeded")
		cycle()
		waitDirectoryScan(t, s, latestDirectoryScan(t, f), "succeeded")
		assertDatabaseCount(t, f, `SELECT count(*) FROM scan_runs`, 4)
		assertDatabaseCount(t, f, `SELECT count(*) FROM tracks WHERE source_root_id=$1 AND relative_path='A/02.flac' AND source_status='present'`, 1, root.ID)

		writeDirectoryTrack(t, root, "B/02.flac", "B", 41)
		record(watchEvent{dirs: []string{"A"}})
		record(watchEvent{overflow: true})
		record(watchEvent{overflow: true})
		clock.advance(1999 * time.Millisecond)
		cycle()
		assertDatabaseCount(t, f, `SELECT count(*) FROM scan_runs`, 4)
		clock.advance(time.Millisecond)
		cycle()
		id := latestDirectoryScan(t, f)
		waitDirectoryScan(t, s, id, "succeeded")
		report, err := s.GetScanReport(t.Context(), id, 1, 20)
		directoryOK(t, err)
		if report.Files.Total == nil || *report.Files.Total != 4 {
			t.Errorf("溢出后应整根补扫四首：%+v", report)
		}
		cycle()
		requireWatchMode(t, s, root.ID, "realtime")
		assertDatabaseCount(t, f, `SELECT count(*) FROM scan_runs WHERE NOT whole_library`, 4)
	})

	t.Run("降级轮询、重探和每日计划互不消费", testDirectoryRootPolling)
}

func testDirectoryRootPolling(t *testing.T) {
	f := newLibraryFixture(t)
	clock := newDirectoryTestClock()
	s := scannerTestApplication(f).WithDirectoryWatchClock(clock)
	root := insertScannerRoot(t, f, "downgraded")
	network := insertScannerRoot(t, f, "nas")
	local := insertScannerRoot(t, f, "realtime")
	for i, r := range []registeredRoot{root, network, local} {
		writeDirectoryTrack(t, r, "Album/01.flac", filepath.Base(r.Path), byte(51+i))
	}
	boundaryCompletedScan(t, s)
	ctx := registerTestWatchContext(t, s)
	var fail atomic.Bool
	fail.Store(true)
	var networkAttempts atomic.Int64
	s.watch.system = watchSystem{
		network: func(path string) (bool, error) {
			if path == network.Path {
				networkAttempts.Add(1)
			}
			return path == network.Path, nil
		},
		add: func(_ int, path string, _ uint32) (int, error) {
			if path == root.Path && fail.Load() {
				return 0, unix.ENOSPC
			}
			return len(path), nil
		},
	}
	directoryOK(t, s.refreshWatches(ctx))
	requireWatchMode(t, s, root.ID, "scheduled")
	requireWatchMode(t, s, network.ID, "scheduled")
	requireWatchMode(t, s, local.ID, "realtime")
	_, err := s.SaveScanSchedule(ctx, ScanSchedule{Enabled: false, Hour: 4})
	directoryOK(t, err)
	directoryOK(t, s.SavePollInterval(ctx, root.ID, 15))
	directoryOK(t, s.SavePollInterval(ctx, network.ID, 15))
	// 首次进入定时模式落计时锚点，每日计划已关闭。
	directoryOK(t, s.refreshWatches(ctx))
	cycle := func() { s.handleWatchedRoot(ctx, root.ID, clock.Now()) }
	cycle()
	anchor := clock.Now()
	var lastPoll time.Time
	directoryOK(t, f.database.QueryRowContext(ctx, "SELECT last_poll_started_at FROM library_roots WHERE id=$1", root.ID).Scan(&lastPoll))
	if !lastPoll.Equal(anchor) {
		t.Fatalf("首次降级计时锚点 %v，期望 %v", lastPoll, anchor)
	}
	clock.advance(15*time.Minute - time.Millisecond)
	cycle()
	assertDatabaseCount(t, f, `SELECT count(*) FROM scan_runs`, 1)

	active, ok, err := acquireScanExecution(ctx, f.database)
	directoryOK(t, err)
	if !ok {
		t.Fatal("未能独占协调锁")
	}
	t.Cleanup(func() { _ = active.close(context.Background()) })
	clock.advance(time.Millisecond)
	cycle()
	assertDatabaseCount(t, f, `SELECT count(*) FROM scan_runs`, 1)
	directoryOK(t, active.close(ctx))

	// 只有到期根扫描，即使其他根也有尚未观察到的文件。
	writeDirectoryTrack(t, root, "Album/02.flac", "downgraded", 54)
	writeDirectoryTrack(t, local, "Album/02.flac", "realtime", 55)
	fail.Store(false)
	cycle()
	id := latestDirectoryScan(t, f)
	waitDirectoryScan(t, s, id, "succeeded")
	assertDatabaseCount(t, f, `SELECT count(*) FROM scan_runs WHERE id=$1 AND NOT whole_library AND scan_mode='incremental' AND trigger_kind='scheduled'`, 1, id)
	assertDatabaseCount(t, f, `SELECT count(*) FROM tracks WHERE relative_path='Album/02.flac' AND source_root_id=$1`, 1, root.ID)
	assertDatabaseCount(t, f, `SELECT count(*) FROM tracks WHERE relative_path='Album/02.flac' AND source_root_id=$1`, 0, local.ID)
	directoryOK(t, s.refreshWatches(ctx))
	requireWatchMode(t, s, root.ID, "realtime")
	requireWatchMode(t, s, network.ID, "scheduled")

	// 两个实例用同一到期快照尝试领取，后者即使在前一轮结束后拿到锁也不能重复启动。
	roots, err := s.loadRegisteredRoots(ctx, f.database)
	directoryOK(t, err)
	nas := roots[slices.IndexFunc(roots, func(r registeredRoot) bool { return r.ID == network.ID })]
	clock.advance(time.Minute)
	s.handleWatchedRoot(ctx, network.ID, clock.Now())
	clock.advance(15 * time.Minute)
	s.handleWatchedRoot(ctx, network.ID, clock.Now())
	waitDirectoryScan(t, s, latestDirectoryScan(t, f), "succeeded")
	second := scannerTestApplication(f)
	result, err := second.startRootPoll(ctx, nas, clock.Now())
	directoryOK(t, err)
	if result.Started {
		t.Error("同一根轮询时段被第二个实例重复领取")
	}
	directoryOK(t, s.refreshWatches(ctx))
	requireWatchMode(t, s, network.ID, "scheduled")
	if got := networkAttempts.Load(); got != 1 {
		t.Errorf("网络根成功整根扫描后不应重探，探测次数 %d", got)
	}

	// 把每日时段放在已完成的手动扫描和局部轮询之间。
	slot := anchor.Add(time.Hour).Truncate(time.Hour)
	directoryExec(t, f, `UPDATE scan_runs SET started_at=CASE WHEN whole_library THEN $1::timestamptz-interval '1 minute' ELSE $1::timestamptz+interval '1 minute' END`, slot)
	directoryExec(t, f, `UPDATE scan_schedule SET enabled=true,hour=$1,updated_at=$2 WHERE id=1`, slot.Hour(), slot.Add(-time.Hour))
	settings, lastFull, err := s.loadScanScheduleState(ctx)
	directoryOK(t, err)
	if !PlanScheduledScan(ScheduledScanPlanInput{Now: slot, Location: time.UTC, Settings: settings, LastStartedAt: lastFull}).Due {
		t.Fatal("局部轮询误消费了每日计划")
	}
	s.startScheduledScan(ctx, slot)
	id = latestDirectoryScan(t, f)
	waitDirectoryScan(t, s, id, "succeeded")
	assertDatabaseCount(t, f, `SELECT count(*) FROM scan_runs WHERE whole_library AND trigger_kind='scheduled'`, 1)
}
