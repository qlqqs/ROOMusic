package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
)

func feedbackFixtureScan(t *testing.T, fixture *libraryFixture) string {
	t.Helper()
	id := insertRunningScan(t, fixture)
	_, err := fixture.database.Exec(`UPDATE scan_runs SET feedback_version=1,phase='counting',progress_bp=0,phase_completed=0,phase_total=0,progress_updated_at=NOW(),progress_seq=0,files_counted=0,files_processed=0,count_complete=false,files_changed=false,roots_total=0,report_state='pending' WHERE id=$1::uuid`, id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPostgreSQLScanFeedbackCountAndMonotonicSnapshots(t *testing.T) {
	fixture := newLibraryFixture(t)
	service := scannerTestApplication(fixture)
	first := insertScannerRoot(t, fixture, "count-a")
	second := insertScannerRoot(t, fixture, "count-b")
	for _, r := range []registeredRoot{first, second} {
		for _, name := range []string{"A.CUE", "x.ape", "cover.jpg", "readme.txt"} {
			if err := os.WriteFile(filepath.Join(r.Path, name), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.Symlink("missing", filepath.Join(second.Path, "broken.FLAC")); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id := feedbackFixtureScan(t, fixture)
	f := newScanFeedback(service, fixture.database, id, []registeredRoot{first, second})
	if !service.countScanRoots(ctx, fixture.database, id, []registeredRoot{first, second}, f) {
		t.Fatal("只计目录项，不读取损坏文件")
	}
	if f.progress.FilesTotal == nil || *f.progress.FilesTotal != 5 || f.counts[0].files != 2 || f.counts[1].files != 3 {
		t.Fatalf("counts %+v", f)
	}
	f.selectRoot(0, first)
	f.weight = 2
	f.phase(ctx, "reading_tags", 2)
	f.processed(ctx)
	f.flush(ctx, true)
	old, err := service.GetScan(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	f.phase(ctx, "organizing", 1)
	f.fraction(1, 1)
	f.base = 2
	f.selectRoot(1, second)
	f.weight = 3
	f.phase(ctx, "reading_tags", 3)
	current, err := service.GetScan(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if current.Progress.PercentBP < old.Progress.PercentBP || current.RootProgress.Index == nil || *current.RootProgress.Index != 2 {
		t.Fatalf("nonmonotone %+v / %+v", old, current)
	}
	seq := f.seq
	f.seq = 0
	f.progress.Phase = "counting"
	f.progress.Completed = 0
	f.flush(ctx, true)
	var gotSeq int64
	var phase string
	if err := fixture.database.QueryRow(`SELECT progress_seq,phase FROM scan_runs WHERE id=$1::uuid`, id).Scan(&gotSeq, &phase); err != nil {
		t.Fatal(err)
	}
	if gotSeq != seq || phase != "reading_tags" {
		t.Fatalf("迟到快照覆盖 %d %s", gotSeq, phase)
	}
}

func TestPostgreSQLScanFeedbackRealScanReportAndFailureIsolation(t *testing.T) {
	for _, failure := range []string{"none", "changes", "report", "progress", "terminal"} {
		t.Run(failure, func(t *testing.T) {
			fixture := newLibraryFixture(t)
			service := scannerTestApplication(fixture)
			root := insertScannerRoot(t, fixture, "real-feedback")
			if err := os.MkdirAll(filepath.Join(root.Path, "Album"), 0755); err != nil {
				t.Fatal(err)
			}
			for i := 1; i <= 2; i++ {
				if err := os.WriteFile(filepath.Join(root.Path, "Album", fmt.Sprintf("%02d.flac", i)), parsertest.FLACWithComments(t, []string{"ALBUM=Album", "ARTIST=Artist", fmt.Sprintf("TITLE=Track %d", i), fmt.Sprintf("TRACKNUMBER=%d", i)}), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if failure != "none" {
				table, event, condition := "scan_catalog_changes", "INSERT", "true"
				switch failure {
				case "report":
					table = "scan_reports"
				case "progress":
					table = "scan_runs"
					event = "UPDATE"
					condition = "NEW.status='running' AND NEW.progress_seq>OLD.progress_seq"
				case "terminal":
					table = "scan_runs"
					event = "UPDATE"
					condition = "NEW.report_state IS DISTINCT FROM OLD.report_state"
				}
				_, err := fixture.database.Exec(fmt.Sprintf(`CREATE FUNCTION reject_feedback_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF %s THEN RAISE EXCEPTION 'feedback rejected'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_feedback_test BEFORE %s ON %s FOR EACH ROW EXECUTE FUNCTION reject_feedback_test()`, condition, event, table))
				if err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			result, err := service.StartScan(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := service.WaitForScans(ctx); err != nil {
				t.Fatal(err)
			}
			scan, err := service.GetScan(ctx, result.Scan.ID)
			if err != nil {
				t.Fatal(err)
			}
			if scan.Status != "succeeded" || scan.Progress == nil || scan.Progress.PercentBP != 10000 {
				t.Fatalf("扫描被反馈错误改变: %+v", scan)
			}
			assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_status='present'`, 2)
			report, err := service.GetScanReport(ctx, scan.ID, 1, 20)
			if failure == "changes" || failure == "report" || failure == "terminal" {
				if !errors.Is(err, ErrReportUnavailable) || scan.ReportStatus != "unavailable" {
					t.Fatalf("报告未降级: %+v %v", scan, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if report.Albums.Added != 1 || report.Tracks.Added != 2 || report.Files.Processed != 2 || !report.MissingReconciled || len(report.AddedAlbums.Items) != 1 {
				t.Fatalf("报告 %+v", report)
			}
		})
	}
}

func TestPostgreSQLScanFeedbackRecoveryAndTerminalFreeze(t *testing.T) {
	fixture := newLibraryFixture(t)
	service := scannerTestApplication(fixture)
	id := feedbackFixtureScan(t, fixture)
	_, err := fixture.database.Exec(`UPDATE scan_runs SET progress_bp=1234,phase='reading_tags',files_processed=3 WHERE id=$1::uuid`, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := RecoverInterruptedScans(context.Background(), fixture.database); err != nil {
		t.Fatal(err)
	}
	scan, err := service.GetScan(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if scan.Status != "incomplete" || scan.ReportStatus != "unavailable" || scan.Progress.PercentBP != 1234 {
		t.Fatalf("恢复快照 %+v", scan)
	}
	f := newScanFeedback(service, fixture.database, id, nil)
	f.progress.PercentBP = 9900
	f.seq = 10
	f.flush(context.Background(), true)
	scan, err = service.GetScan(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if scan.Progress.PercentBP != 1234 {
		t.Fatal("终态被迟到刷新覆盖")
	}
}

func TestPostgreSQLScanFeedbackUnavailableRootAndCanceledCount(t *testing.T) {
	fixture := newLibraryFixture(t)
	service := scannerTestApplication(fixture)
	root := insertScannerRoot(t, fixture, "unavailable")
	if err := os.Remove(root.Path); err != nil {
		t.Fatal(err)
	}
	id := feedbackFixtureScan(t, fixture)
	f := newScanFeedback(service, fixture.database, id, []registeredRoot{root})
	if service.countScanRoots(context.Background(), fixture.database, id, []registeredRoot{root}, f) || f.progress.CountComplete {
		t.Fatal("不可用根不能被当成空目录成功")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	other := newScanFeedback(service, fixture.database, id, []registeredRoot{root})
	if service.countScanRoots(ctx, fixture.database, id, []registeredRoot{root}, other) || other.progress.FilesTotal != nil {
		t.Fatal("计数取消时分母必须为空")
	}
}
