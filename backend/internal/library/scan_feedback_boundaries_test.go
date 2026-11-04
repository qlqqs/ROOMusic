package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type feedbackBoundaryExecutor struct {
	scanExecutor
	begin func(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

func (e feedbackBoundaryExecutor) BeginTx(ctx context.Context, options *sql.TxOptions) (*sql.Tx, error) {
	return e.begin(ctx, options)
}

func TestScanFeedbackBoundariesCanceledPhaseAndEmptyCount(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := newScanFeedback(&Service{}, nil, "", nil)
	f.progress = ScanProgress{Version: 1, Phase: "reading_tags", Unit: "files", Completed: 3, Total: 10, PercentBP: 2192, FilesProcessed: 3}
	before := f.progress
	f.phase(ctx, "organizing", 1)
	if !reflect.DeepEqual(f.progress, before) {
		t.Errorf("取消后阶段被推进: before=%+v after=%+v", before, f.progress)
	}
	f = newScanFeedback(&Service{}, nil, "", nil)
	if (&Service{}).countScanRoots(ctx, nil, "", nil, f) || f.progress.FilesTotal != nil || f.progress.CountComplete {
		t.Fatalf("零根计数取消被发布为已完成: %+v", f.progress)
	}
}

func TestScanFeedbackBoundariesSynchronousFlush(t *testing.T) {
	entered, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	f := newScanFeedback(&Service{}, feedbackBoundaryExecutor{begin: func(context.Context, *sql.TxOptions) (*sql.Tx, error) {
		close(entered)
		<-release
		return nil, errors.New("受控写入失败")
	}}, "", nil)
	go func() { f.flush(context.Background(), true); close(returned) }()
	<-entered
	select {
	case <-returned:
		t.Error("flush 把 holder 写入放到了后台")
	default:
	}
	close(release)
	<-returned
}

func TestScanFeedbackBoundariesBatchAndRetryClock(t *testing.T) {
	now := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	attempts := 0
	f := newScanFeedback(&Service{}, feedbackBoundaryExecutor{begin: func(context.Context, *sql.TxOptions) (*sql.Tx, error) {
		attempts++
		return nil, errors.New("受控写入失败")
	}}, "", []registeredRoot{{}})
	f.now = func() time.Time { return now }
	f.lastAttempt = now
	f.counts[0].files = 1000
	f.progress.Total = 1000
	ctx := context.Background()
	for i := 0; i < 255; i++ {
		f.processed(ctx)
	}
	if attempts != 0 {
		t.Fatal("256 单位前发生写入")
	}
	f.processed(ctx)
	if attempts != 1 || f.units != 0 {
		t.Fatalf("256 单位未触发单次刷新: attempts=%d units=%d", attempts, f.units)
	}
	f.processed(ctx)
	if attempts != 1 {
		t.Fatal("失败后逐文件重试")
	}
	now = now.Add(time.Second - time.Nanosecond)
	f.flush(ctx, false)
	if attempts != 1 {
		t.Fatal("不足 1 秒不应触发")
	}
	now = now.Add(time.Nanosecond)
	f.flush(ctx, false)
	if attempts != 2 {
		t.Fatal("1 秒边界未触发")
	}
	f.flush(ctx, false)
	if attempts != 2 {
		t.Fatal("失败后同一边界重复尝试")
	}
	f.phase(ctx, "saving", 2)
	if attempts != 3 {
		t.Fatal("切阶段未强制刷新")
	}
	now = now.Add(time.Second)
	f.scopeDone(ctx)
	if attempts != 4 || f.progress.Completed != 1 {
		t.Fatal("保存单位未走相同时间节流")
	}
	if f.changes.Broken() {
		t.Fatal("进度错误不应破坏报告健康")
	}
	f.progressUnavailable = true
	f.flush(ctx, true)
	if attempts != 4 || f.changes.Broken() || f.invalid {
		t.Fatal("分母不可用应独立停止进度写入")
	}
}

func TestScanFeedbackBoundariesTerminalPresentation(t *testing.T) {
	timestamp := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	finished := timestamp.Add(time.Minute)
	for _, phase := range []string{"counting", "reading_tags", "organizing", "saving"} {
		for _, status := range []string{"succeeded", "canceled", "incomplete", "failed"} {
			t.Run(phase+"/"+status, func(t *testing.T) {
				r := feedbackRow{version: sql.NullInt64{Int64: 1, Valid: true}, phase: sql.NullString{String: phase, Valid: true}, updated: sql.NullTime{Time: timestamp, Valid: true}, bp: sql.NullInt64{Int64: 1234, Valid: true}, state: sql.NullString{String: "pending", Valid: true}, index: sql.NullInt64{Int64: 1, Valid: true}, roots: sql.NullInt64{Int64: 2, Valid: true}, id: sql.NullString{String: "root", Valid: true}, name: sql.NullString{String: "音乐", Valid: true}}
				scan := Scan{Status: status, FinishedAt: &finished}
				r.apply(&scan)
				if scan.ReportStatus != "unavailable" || scan.Progress == nil {
					t.Fatalf("终态 pending 未降级: %+v", scan)
				}
				p := scan.Progress
				if status == "succeeded" {
					if p.PercentBP != 10000 || p.Phase != "saving" || !p.UpdatedAt.Equal(finished) || scan.RootProgress.Index != nil || scan.RootProgress.ID != nil || scan.RootProgress.Name != nil {
						t.Fatalf("成功反馈补偿错误: %+v %+v", p, scan.RootProgress)
					}
				} else if p.PercentBP != 1234 || p.Phase != phase || !p.UpdatedAt.Equal(timestamp) || scan.RootProgress.Index == nil {
					t.Fatalf("非成功快照被改变: %+v", scan)
				}
			})
		}
	}
}

func TestScanFeedbackBoundariesWeightedStageTransitions(t *testing.T) {
	now := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	f := newScanFeedback(&Service{}, feedbackBoundaryExecutor{begin: func(context.Context, *sql.TxOptions) (*sql.Tx, error) {
		return nil, errors.New("仅检查内存进度")
	}}, "", []registeredRoot{{}, {}})
	f.now = func() time.Time { return now }
	f.lastAttempt = now
	f.counts = []rootCount{{files: 100, complete: true}, {files: 300, complete: true}}
	f.denominator = 400
	f.weight = 100
	ctx := context.Background()
	f.phase(ctx, "reading_tags", 100)
	for i := 0; i < 100; i++ {
		f.processed(ctx)
	}
	if f.progress.PercentBP != 1910 {
		t.Fatalf("第一根读取: %+v", f.progress)
	}
	f.phase(ctx, "organizing", 1)
	f.phase(ctx, "saving", 1)
	f.scopeDone(ctx)
	if f.progress.PercentBP != 2850 {
		t.Fatalf("第一根保存: %+v", f.progress)
	}
	f.base = 100
	f.weight = 300
	f.current = 1
	f.phase(ctx, "reading_tags", 300)
	if f.progress.PercentBP != 2850 || f.progress.Completed != 0 {
		t.Fatalf("换根边界: %+v", f.progress)
	}
	for i := 0; i < 150; i++ {
		f.processed(ctx)
	}
	if f.progress.PercentBP != 4965 || f.progress.FilesProcessed != 250 {
		t.Fatalf("根权重未作用于全局累计: %+v", f.progress)
	}
	// 读取尚未结束，少于计数不等于文件减少。
	if f.progress.FilesChanged {
		t.Fatal("部分进度不应判作文件漂移")
	}
	f.phase(ctx, "organizing", 1)
	f.phase(ctx, "saving", 0)
	if f.progress.PercentBP != 8490 {
		t.Fatalf("零 scope 入口不能直接结算: %+v", f.progress)
	}
	f.fraction(1, 1)
	if f.progress.PercentBP != 9900 {
		t.Fatal("工作结束应保留终态提交的 1%")
	}
}

func TestScanFeedbackBoundariesCountingBatch(t *testing.T) {
	root := registeredRoot{Path: t.TempDir()}
	for i := 0; i < 257; i++ {
		if err := os.WriteFile(filepath.Join(root.Path, fmt.Sprintf("%03d.ape", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	service := &Service{}
	var snapshots []int64
	var f *scanFeedback
	executor := feedbackBoundaryExecutor{begin: func(context.Context, *sql.TxOptions) (*sql.Tx, error) {
		snapshots = append(snapshots, f.progress.FilesCounted)
		return nil, errors.New("受控进度错误")
	}}
	f = newScanFeedback(service, executor, "", []registeredRoot{root})
	now := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	f.now = func() time.Time { return now }
	if !service.countScanRoots(context.Background(), executor, "", []registeredRoot{root}, f) {
		t.Fatal("纯计数不可因进度写失败而失败")
	}
	if !reflect.DeepEqual(snapshots, []int64{0, 256, 257, 257}) {
		t.Fatalf("计数阶段未按 256 候选分批及边界强制写入: %v", snapshots)
	}
	if f.changes.Broken() || *f.progress.FilesTotal != 257 {
		t.Fatalf("计数结果被反馈失败改变: %+v", f.progress)
	}
}
