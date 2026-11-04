package library

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func boundaryAudio(t *testing.T, root registeredRoot, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root.Path, name), parsertest.FLACWithComments(t, []string{"ALBUM=测试专辑", "ARTIST=测试艺人", "TITLE=" + name}), 0600); err != nil {
		t.Fatal(err)
	}
}
func boundaryCompletedScan(t *testing.T, service *Service) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	started, err := service.StartScan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.WaitForScans(ctx); err != nil {
		t.Fatal(err)
	}
	scan, err := service.GetScan(ctx, started.Scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if scan.Status != "succeeded" {
		t.Fatalf("预备扫描失败: %+v", scan)
	}
	return scan.ID
}
func boundaryHolder(t *testing.T, fixture *libraryFixture) *sql.Conn {
	t.Helper()
	execution, ok, err := acquireScanExecution(context.Background(), fixture.database)
	if err != nil || !ok {
		t.Fatalf("获取扫描 holder: %v %v", ok, err)
	}
	t.Cleanup(func() {
		if err := execution.close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return execution.connection
}

func TestPostgreSQLScanFeedbackBoundariesEmptyRoots(t *testing.T) {
	for _, roots := range []int{0, 2} {
		t.Run(string(rune('0'+roots)), func(t *testing.T) {
			fixture := newLibraryFixture(t)
			service := scannerTestApplication(fixture)
			for i := 0; i < roots; i++ {
				insertScannerRoot(t, fixture, string(rune('a'+i)))
			}
			id := boundaryCompletedScan(t, service)
			report, err := service.GetScanReport(context.Background(), id, 1, 20)
			if err != nil {
				t.Fatal(err)
			}
			if report.Files.Total == nil || *report.Files.Total != 0 || !report.Files.CountComplete || report.Files.Processed != 0 || report.Files.Changed || !report.MissingReconciled || report.Tracks != (ScanReportCounts{}) || report.Albums != (ScanReportCounts{}) {
				t.Fatalf("空输入报告错误: %+v", report)
			}
		})
	}
}

func TestPostgreSQLScanFeedbackBoundariesFileDrift(t *testing.T) {
	for _, direction := range []string{"added", "removed"} {
		t.Run(direction, func(t *testing.T) {
			fixture := newLibraryFixture(t)
			service := scannerTestApplication(fixture)
			root := insertScannerRoot(t, fixture, "drift")
			boundaryAudio(t, root, "a.flac")
			boundaryCompletedScan(t, service)
			ctx := context.Background()
			id := feedbackFixtureScan(t, fixture)
			holder := boundaryHolder(t, fixture)
			f := newScanFeedback(service, holder, id, []registeredRoot{root})
			if !service.countScanRoots(ctx, holder, id, []registeredRoot{root}, f) {
				t.Fatal("计数失败")
			}
			wantProcessed := int64(2)
			if direction == "added" {
				boundaryAudio(t, root, "b.flac")
			} else {
				wantProcessed = 0
				if err := os.Remove(filepath.Join(root.Path, "a.flac")); err != nil {
					t.Fatal(err)
				}
			}
			f.selectRoot(0, root)
			f.weight = 1
			f.phase(ctx, "reading_tags", 1)
			if f.progress.FilesChanged {
				t.Fatal("尚未读取不能推断文件减少")
			}
			if err := service.scanRoot(ctx, holder, id, root, f); err != nil {
				t.Fatal(err)
			}
			if f.progress.FilesTotal == nil || *f.progress.FilesTotal != 1 || f.progress.FilesProcessed != wantProcessed || !f.progress.FilesChanged {
				t.Fatalf("文件漂移快照错误: %+v", f.progress)
			}
			status, err := service.finalizeScan(ctx, holder, id, []string{root.ID}, scanOutcome{Complete: true}, f)
			if err != nil || status != "succeeded" {
				t.Fatalf("漂移不应阻止完整成功: %s %v", status, err)
			}
			report, err := service.GetScanReport(ctx, id, 1, 20)
			if err != nil {
				t.Fatal(err)
			}
			if report.Files.Total == nil || *report.Files.Total != 1 || report.Files.Processed != wantProcessed || !report.Files.Changed {
				t.Fatalf("漂移报告错误: %+v", report)
			}
			if direction == "removed" && (report.Tracks.Missing != 1 || report.Albums.Missing != 1) {
				t.Fatalf("真实删除未对账: %+v", report)
			}
		})
	}
}

func TestPostgreSQLScanFeedbackBoundariesIncompleteCountRemainsIncomplete(t *testing.T) {
	fixture := newLibraryFixture(t)
	service := scannerTestApplication(fixture)
	bad := insertScannerRoot(t, fixture, "a-unavailable")
	good := insertScannerRoot(t, fixture, "b-good")
	if err := os.Remove(bad.Path); err != nil {
		t.Fatal(err)
	}
	boundaryAudio(t, good, "good.flac")
	ctx := context.Background()
	id := feedbackFixtureScan(t, fixture)
	holder := boundaryHolder(t, fixture)
	roots := []registeredRoot{bad, good}
	f := newScanFeedback(service, holder, id, roots)
	if service.countScanRoots(ctx, holder, id, roots, f) || f.progress.CountComplete || f.counts[0].complete || !f.counts[1].complete {
		t.Fatalf("全根完整性错误: %+v", f.counts)
	}
	if err := os.Mkdir(bad.Path, 0755); err != nil {
		t.Fatal(err)
	}
	boundaryAudio(t, bad, "restored.flac")
	for i, root := range roots {
		f.selectRoot(i, root)
		f.weight = f.counts[i].files
		f.phase(ctx, "reading_tags", f.counts[i].files)
		err := service.scanRoot(ctx, holder, id, root, f)
		if (i == 0 && err == nil) || (i == 1 && err != nil) {
			t.Fatalf("各根必须使用自己的完整性: root=%d err=%v", i, err)
		}
		f.fraction(1, 1)
		f.base += f.weight
	}
	f.flush(ctx, true)
	status, err := service.finalizeScan(ctx, holder, id, []string{bad.ID, good.ID}, scanOutcome{}, f)
	if err != nil || status != "incomplete" {
		t.Fatalf("低估计数不可成为成功: %s %v", status, err)
	}
	report, err := service.GetScanReport(ctx, id, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "partial" || report.MissingReconciled || report.Files.CountComplete || report.Files.Processed != 2 || !report.Files.Changed {
		t.Fatalf("不完整报告错误: %+v", report)
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_status='present'`, 2)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM scan_observations`, 0)
}

func TestPostgreSQLScanFeedbackBoundariesCanceledStagesFreeze(t *testing.T) {
	for _, phase := range []string{"counting", "reading_tags", "organizing", "saving"} {
		t.Run(phase, func(t *testing.T) {
			fixture := newLibraryFixture(t)
			service := scannerTestApplication(fixture)
			root := insertScannerRoot(t, fixture, "cancel")
			ctx := context.Background()
			id := feedbackFixtureScan(t, fixture)
			holder := boundaryHolder(t, fixture)
			f := newScanFeedback(service, holder, id, []registeredRoot{root})
			f.selectRoot(0, root)
			f.counts[0] = rootCount{files: 10, complete: true}
			f.denominator = 10
			f.weight = 10
			if phase != "counting" {
				total := int64(10)
				f.progress.FilesTotal = &total
				f.progress.CountComplete = true
			}
			f.phase(ctx, phase, 10)
			f.progress.FilesCounted = 10
			f.progress.FilesProcessed = 10
			switch phase {
			case "counting":
				f.progress.Total = 1
				f.progress.FilesCounted = 3
				f.progress.FilesProcessed = 0
			case "reading_tags":
				f.progress.FilesProcessed = 3
				f.progress.Completed = 3
				f.fraction(9, 50)
			case "organizing":
				f.progress.Total = 1
			case "saving":
				f.progress.Total = 3
				f.progress.Completed = 1
				f.fraction(13, 15)
			}
			f.flush(ctx, true)
			before, err := service.GetScan(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			f.processed(canceled)
			f.scopeDone(canceled)
			f.phase(canceled, "saving", 20)
			if _, err := fixture.database.Exec(`UPDATE scan_runs SET cancel_requested_at=NOW() WHERE id=$1::uuid`, id); err != nil {
				t.Fatal(err)
			}
			f.flush(ctx, true)
			frozen, err := service.GetScan(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			status, err := service.finalizeScan(ctx, holder, id, []string{root.ID}, scanOutcome{Complete: true}, f)
			if err != nil || status != "canceled" {
				t.Fatalf("取消未优先: %s %v", status, err)
			}
			f.progress.PercentBP = 9900
			f.progress.Phase = "saving"
			f.flush(ctx, true)
			after, err := service.GetScan(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			a, b := *before.Progress, *after.Progress
			a.UpdatedAt = frozen.Progress.UpdatedAt
			if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(before.RootProgress, after.RootProgress) {
				t.Fatalf("取消快照未冻结: before=%+v after=%+v", a, b)
			}
			report, err := service.GetScanReport(ctx, id, 1, 20)
			if err != nil {
				t.Fatal(err)
			}
			if report.Status != "partial" || report.MissingReconciled || report.Files.Processed != before.Progress.FilesProcessed || report.Files.Changed {
				t.Fatalf("取消报告错误: %+v", report)
			}
			if phase == "counting" && report.Files.Total != nil {
				t.Fatal("计数取消不应发布固定分母")
			}
		})
	}
}

func TestPostgreSQLScanFeedbackBoundariesReportTimeoutSavepoint(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "timeout")
	holder := boundaryHolder(t, fixture)
	ctx := context.Background()
	tx, err := holder.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SET LOCAL statement_timeout='2s'`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE library_roots SET revision=revision+1 WHERE id=$1::uuid`, root.ID); err != nil {
		t.Fatal(err)
	}
	err = reportAttempt(ctx, tx, func() error {
		if _, err := tx.ExecContext(ctx, `UPDATE library_roots SET revision=revision+100 WHERE id=$1::uuid`, root.ID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `SELECT pg_sleep(5)`)
		return err
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "57014" {
		t.Fatalf("未触发服务端 SQL 超时: %v", err)
	}
	var timeout string
	if err = tx.QueryRowContext(ctx, `SHOW statement_timeout`).Scan(&timeout); err != nil || timeout != "2s" {
		t.Fatalf("超时未恢复或事务已损坏: %q %v", timeout, err)
	}
	if err = reportAttempt(ctx, tx, func() error { _, err := tx.ExecContext(ctx, `SELECT 1`); return err }); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRowContext(ctx, `SHOW statement_timeout`).Scan(&timeout); err != nil || timeout != "2s" {
		t.Fatalf("成功保存点也必须恢复预算: %q %v", timeout, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var revision int
	if err = fixture.database.QueryRow(`SELECT revision FROM library_roots WHERE id=$1::uuid`, root.ID).Scan(&revision); err != nil || revision != 2 {
		t.Fatalf("反馈回滚误伤核心写入: revision=%d err=%v", revision, err)
	}
}

func TestPostgreSQLScanFeedbackBoundariesReadFailures(t *testing.T) {
	fixture := newLibraryFixture(t)
	service := scannerTestApplication(fixture)
	ctx := context.Background()
	if _, err := service.GetScanReport(ctx, uuid.New(), 1, 20); !errors.Is(err, ErrScanNotFound) {
		t.Fatalf("未知扫描: %v", err)
	}
	legacy := insertRunningScan(t, fixture)
	if _, err := service.GetScanReport(ctx, legacy, 1, 20); !errors.Is(err, ErrReportLegacy) {
		t.Fatalf("历史扫描: %v", err)
	}
	if _, err := fixture.database.Exec(`UPDATE scan_runs SET status='incomplete',finished_at=NOW() WHERE id=$1::uuid`, legacy); err != nil {
		t.Fatal(err)
	}
	id := feedbackFixtureScan(t, fixture)
	if _, err := service.GetScanReport(ctx, id, 1, 20); !errors.Is(err, ErrReportPending) {
		t.Fatalf("运行中扫描: %v", err)
	}
	if _, err := fixture.database.Exec(`UPDATE scan_runs SET status='succeeded',finished_at=NOW(),report_state='ready' WHERE id=$1::uuid`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetScanReport(ctx, id, 1, 20); !errors.Is(err, ErrReportUnavailable) {
		t.Fatalf("ready 无报告行: %v", err)
	}
	if _, err := fixture.database.Exec(`ALTER TABLE scan_reports RENAME TO scan_reports_unavailable`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetScanReport(ctx, id, 1, 20); !errors.Is(err, ErrReportReadFailed) || errors.Is(err, ErrReportUnavailable) {
		t.Fatalf("数据库错误被混同为报告不可用: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := service.GetScanReport(canceled, id, 1, 20); !errors.Is(err, ErrReportReadFailed) || !errors.Is(err, context.Canceled) {
		t.Fatalf("读取取消须保留错误链: %v", err)
	}
}

func TestPostgreSQLScanFeedbackBoundariesProgressTimeoutKeepsHolder(t *testing.T) {
	fixture := newLibraryFixture(t)
	service := scannerTestApplication(fixture)
	ctx := context.Background()
	id := feedbackFixtureScan(t, fixture)
	holder := boundaryHolder(t, fixture)
	var beforePID int
	if err := holder.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&beforePID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.Exec(`CREATE FUNCTION boundary_slow_progress() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.progress_seq>OLD.progress_seq THEN PERFORM pg_sleep(5); END IF; RETURN NEW; END $$;
 CREATE TRIGGER boundary_slow_progress BEFORE UPDATE ON scan_runs FOR EACH ROW EXECUTE FUNCTION boundary_slow_progress()`); err != nil {
		t.Fatal(err)
	}
	f := newScanFeedback(service, holder, id, nil)
	f.flush(ctx, true)
	var afterPID int
	if err := holder.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&afterPID); err != nil || afterPID != beforePID {
		t.Fatalf("进度超时丢失 holder: %d/%d %v", beforePID, afterPID, err)
	}
	other, acquired, err := acquireScanExecution(ctx, fixture.database)
	if acquired {
		_ = other.close(ctx)
	}
	if err != nil || acquired {
		t.Fatalf("进度超时释放了扫描资格: %v %v", acquired, err)
	}
	if f.changes.Broken() || f.invalid {
		t.Fatal("普通进度失败污染报告健康")
	}
	if _, err := fixture.database.Exec(`DROP TRIGGER boundary_slow_progress ON scan_runs`); err != nil {
		t.Fatal(err)
	}
	f.progressUnavailable = true
	f.progress.FilesCounted = 5
	f.flush(ctx, true)
	var persistedCount int64
	if err := holder.QueryRowContext(ctx, `SELECT files_counted FROM scan_runs WHERE id=$1::uuid`, id).Scan(&persistedCount); err != nil || persistedCount != 0 {
		t.Fatalf("不可用的进度仍写入: %d %v", persistedCount, err)
	}
	status, err := service.finalizeScan(ctx, holder, id, nil, scanOutcome{Complete: true}, f)
	if err != nil || status != "succeeded" {
		t.Fatalf("进度降级改变业务结果: %s %v", status, err)
	}
	scan, err := service.GetScan(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if scan.Progress.PercentBP != 10000 || scan.ReportStatus != "ready" || scan.RootProgress.Index != nil || scan.FinishedAt == nil || !scan.Progress.UpdatedAt.Equal(*scan.FinishedAt) {
		t.Fatalf("成功补偿/报告错误: %+v", scan)
	}
	report, err := service.GetScanReport(ctx, id, 1, 20)
	if err != nil || report.Files.Counted != 5 {
		t.Fatalf("进度不可用不应阻止报告: %+v %v", report, err)
	}
}

func TestPostgreSQLScanFeedbackBoundariesReportRepeatableRead(t *testing.T) {
	fixture := newLibraryFixture(t)
	service := scannerTestApplication(fixture)
	root := insertScannerRoot(t, fixture, "snapshot")
	boundaryAudio(t, root, "a.flac")
	id := boundaryCompletedScan(t, service)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	blocker, err := fixture.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	if _, err = blocker.ExecContext(ctx, `LOCK TABLE scan_reports IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	type response struct {
		report ScanReport
		err    error
	}
	done := make(chan response, 1)
	go func() { r, e := service.GetScanReport(ctx, id, 1, 20); done <- response{r, e} }()
	// 报告已读过 scan_runs 并建立快照，再在第二条查询的表锁处暂停；不用睡眠猜测交错顺序。
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err = fixture.database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE relation='scan_reports'::regclass AND NOT granted)`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("报告未进入受控等待点")
		case r := <-done:
			t.Fatalf("报告越过表锁: %v", r.err)
		case <-ticker.C:
		}
	}
	// 此时提交的新可见性不可混入已经开始的报告；下一次读取才看到它。
	if _, err = fixture.database.ExecContext(ctx, `UPDATE tracks SET source_status='missing'`); err != nil {
		t.Fatal(err)
	}
	if err = blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	var got response
	select {
	case got = <-done:
	case <-ctx.Done():
		t.Fatal("报告读取未结束")
	}
	if got.err != nil {
		t.Fatal(got.err)
	}
	if len(got.report.AddedAlbums.Items) != 1 || got.report.AddedAlbums.Items[0].Album == nil || got.report.AddedAlbums.Items[0].CurrentGroupID == nil {
		t.Fatalf("同次报告混入较新的可见性: %+v", got.report.AddedAlbums)
	}
	next, err := service.GetScanReport(ctx, id, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if next.Albums != got.report.Albums || next.Tracks != got.report.Tracks || len(next.AddedAlbums.Items) != 1 || next.AddedAlbums.Items[0].Album != nil || next.AddedAlbums.Items[0].CurrentGroupID != nil {
		t.Fatalf("历史计数必须冻结、当前摘要必须更新: %+v", next)
	}
}

func TestPostgreSQLScanFeedbackBoundariesHintReadFailure(t *testing.T) {
	fixture := newLibraryFixture(t)
	service := scannerTestApplication(fixture)
	root := insertScannerRoot(t, fixture, "hints")
	boundaryAudio(t, root, "a.flac")
	id := boundaryCompletedScan(t, service)
	// 只破坏治理提示依赖；报告、历史页和当前专辑摘要仍可读。
	if _, err := fixture.database.Exec(`ALTER TABLE release_issue_reviews RENAME TO release_issue_reviews_unavailable`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetScanReport(context.Background(), id, 1, 20); !errors.Is(err, ErrReportReadFailed) || errors.Is(err, ErrReportUnavailable) {
		t.Fatalf("认领提示读取错误未正确分类: %v", err)
	}
}

type boundaryScopeCountExecutor struct {
	scanExecutor
	failures int
}

func (e *boundaryScopeCountExecutor) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if strings.Contains(query, "COUNT(DISTINCT organization_scope)") {
		e.failures++
		return e.scanExecutor.QueryRowContext(ctx, `SELECT 1/0`)
	}
	return e.scanExecutor.QueryRowContext(ctx, query, args...)
}

func TestPostgreSQLScanFeedbackBoundariesScopeCountFailure(t *testing.T) {
	fixture := newLibraryFixture(t)
	service := scannerTestApplication(fixture)
	root := insertScannerRoot(t, fixture, "scope-count")
	boundaryAudio(t, root, "a.flac")
	ctx := context.Background()
	id := feedbackFixtureScan(t, fixture)
	holder := boundaryHolder(t, fixture)
	executor := &boundaryScopeCountExecutor{scanExecutor: holder}
	f := newScanFeedback(service, executor, id, []registeredRoot{root})
	if !service.countScanRoots(ctx, executor, id, []registeredRoot{root}, f) {
		t.Fatal("计数失败")
	}
	f.selectRoot(0, root)
	f.weight = 1
	f.phase(ctx, "reading_tags", 1)
	if err := service.scanRoot(ctx, executor, id, root, f); err != nil {
		t.Fatalf("进度分母查询失败不应中断核心保存: %v", err)
	}
	if executor.failures != 1 || !f.progressUnavailable || f.changes.Broken() {
		t.Fatalf("分母降级应独立于报告健康: failures=%d unavailable=%v reportBroken=%v", executor.failures, f.progressUnavailable, f.changes.Broken())
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_status='present'`, 1)
	status, err := service.finalizeScan(ctx, holder, id, []string{root.ID}, scanOutcome{Complete: true}, f)
	if err != nil || status != "succeeded" {
		t.Fatalf("分母错误改变终态: %s %v", status, err)
	}
	report, err := service.GetScanReport(ctx, id, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "ready" || report.Tracks.Added != 1 || report.Albums.Added != 1 || report.Files.Processed != 1 {
		t.Fatalf("核心保存或报告记账丢失: %+v", report)
	}
}

func TestPostgreSQLScanFeedbackBoundariesFinalizationTimeout(t *testing.T) {
	for _, stage := range []string{"report", "terminal"} {
		t.Run(stage, func(t *testing.T) {
			fixture := newLibraryFixture(t)
			service := scannerTestApplication(fixture)
			root := insertScannerRoot(t, fixture, "final-timeout")
			boundaryAudio(t, root, "a.flac")
			trigger := `CREATE FUNCTION boundary_slow_report() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(5); RETURN NEW; END $$;
  CREATE TRIGGER boundary_slow_report BEFORE INSERT ON scan_reports FOR EACH ROW EXECUTE FUNCTION boundary_slow_report()`
			if stage == "terminal" {
				trigger = `CREATE FUNCTION boundary_slow_terminal() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.report_state IS DISTINCT FROM OLD.report_state THEN PERFORM pg_sleep(5); END IF; RETURN NEW; END $$;
  CREATE TRIGGER boundary_slow_terminal BEFORE UPDATE ON scan_runs FOR EACH ROW EXECUTE FUNCTION boundary_slow_terminal()`
			}
			if _, err := fixture.database.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			id := boundaryCompletedScan(t, service)
			scan, err := service.GetScan(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if scan.ReportStatus != "unavailable" || scan.Progress.PercentBP != 10000 || scan.Progress.Phase != "saving" || scan.RootProgress.Index != nil || scan.FinishedAt == nil || !scan.Progress.UpdatedAt.Equal(*scan.FinishedAt) {
				t.Fatalf("终态反馈超时未正确补偿: %+v", scan)
			}
			if _, err := service.GetScanReport(context.Background(), id, 1, 20); !errors.Is(err, ErrReportUnavailable) {
				t.Fatalf("超时报告未降级: %v", err)
			}
			assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_status='present'`, 1)
			if stage == "terminal" {
				var bp int
				var state string
				if err := fixture.database.QueryRow(`SELECT progress_bp,report_state FROM scan_runs WHERE id=$1::uuid`, id).Scan(&bp, &state); err != nil {
					t.Fatal(err)
				}
				if bp != 9900 || state != "pending" {
					t.Fatalf("应验证原字段 fallback 而非反馈正常提交: %d %s", bp, state)
				}
			}
		})
	}
}

func TestPostgreSQLScanFeedbackBoundariesRootLoadFailure(t *testing.T) {
	fixture := newLibraryFixture(t)
	service := scannerTestApplication(fixture)
	insertScannerRoot(t, fixture, "unknown-roots")
	if _, err := fixture.database.Exec(`ALTER TABLE library_roots RENAME TO library_roots_unavailable`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	started, err := service.StartScan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.WaitForScans(ctx); err != nil {
		t.Fatal(err)
	}
	scan, err := service.GetScan(ctx, started.Scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if scan.Status != "failed" || scan.Progress == nil {
		t.Fatalf("根列表故障应失败: %+v", scan)
	}
	if scan.Progress.CountComplete || scan.Progress.FilesTotal != nil {
		t.Fatalf("根集合未知不得发布完整零计数: %+v", scan.Progress)
	}
	report, err := service.GetScanReport(ctx, scan.ID, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if report.Files.CountComplete || report.Files.Total != nil || report.MissingReconciled {
		t.Fatalf("报告将未知根集合误作空库: %+v", report)
	}
}

func TestPostgreSQLScanFeedbackBoundariesRestoreTimeoutFailure(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "restore-timeout")
	holder := boundaryHolder(t, fixture)
	ctx := context.Background()
	// 仅在隔离 schema 内遮蔽恢复调用；work 自身成功，故障精确发生在恢复预算时。
	if _, err := fixture.database.Exec(`CREATE FUNCTION set_config(text,text,boolean) RETURNS text LANGUAGE plpgsql AS $$
 BEGIN RAISE EXCEPTION '受控恢复预算故障' USING ERRCODE='22023'; END $$`); err != nil {
		t.Fatal(err)
	}
	tx, err := holder.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_catalog.set_config('search_path',quote_ident(current_schema())||',pg_catalog',true)`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `SET LOCAL statement_timeout='2s'`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE library_roots SET revision=revision+1 WHERE id=$1::uuid`, root.ID); err != nil {
		t.Fatal(err)
	}
	err = reportAttempt(ctx, tx, func() error {
		_, err := tx.ExecContext(ctx, `UPDATE library_roots SET revision=revision+100 WHERE id=$1::uuid`, root.ID)
		return err
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "22023" {
		t.Fatalf("未命中恢复预算故障: %v", err)
	}
	var timeout string
	if err = tx.QueryRowContext(ctx, `SHOW statement_timeout`).Scan(&timeout); err != nil || timeout != "2s" {
		t.Fatalf("恢复预算失败后事务未恢复: timeout=%q err=%v", timeout, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var revision int
	if err = fixture.database.QueryRow(`SELECT revision FROM library_roots WHERE id=$1::uuid`, root.ID).Scan(&revision); err != nil || revision != 2 {
		t.Fatalf("必须仅回滚反馈部分并保留核心写入: revision=%d err=%v", revision, err)
	}
}
