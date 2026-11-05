package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestScaleAcceptanceThresholds(t *testing.T) {
	if scaleTotalBudget != 90*time.Minute || scaleBackfillBudget != 36*time.Minute || scaleRequestBudget != 45*time.Second {
		t.Fatal("运行、回填或单请求预算改变")
	}
	for _, test := range []struct {
		name           string
		values         []float64
		complete, pass bool
		p95, p99, max  float64
	}{
		{"没有证据", nil, true, false, 0, 0, 0},
		{"完整边界", []float64{200, 200}, true, true, 200, 200, 200},
		{"未完成不能通过", []float64{100}, false, false, 100, 100, 100},
		{"p95越界", []float64{201}, true, false, 201, 201, 201},
		{"p99越界", append(make([]float64, 98), 501, 1200), true, false, 0, 501, 1200},
		{"max超过1秒不是独立门槛", append(make([]float64, 99), 1200), true, true, 0, 0, 1200},
		{"p99边界", append(make([]float64, 98), 500, 1200), true, true, 0, 500, 1200},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := append([]float64(nil), test.values...)
			got := scaleCATPHoldStats(test.values, test.complete)
			if got.Pass != test.pass || got.P95MS != test.p95 || got.P99MS != test.p99 || got.MaxMS != test.max || got.Evaluated != test.complete || !reflect.DeepEqual(before, test.values) {
				t.Fatalf("统计错误: %+v input=%v", got, test.values)
			}
		})
	}
}

func TestScaleAcceptanceCommittedApplicationBatchesOnly(t *testing.T) {
	d, err := newScaleDiagnostics(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := scaleDiagnosticContext(context.Background(), "maintenance", "")
	run := func(actor context.Context, pid uint32, status byte, query, tag string, args ...any) {
		d.endQuery(d.beginQuery(actor, pid, status, query, args), 'T', tag, nil)
	}
	commit := func(actor context.Context, pid uint32, command, tag string, err error) {
		d.endQuery(d.beginQuery(actor, pid, 'T', command, nil), 'I', tag, err)
	}
	run(ctx, 1, 'I', "begin", "BEGIN")
	// 长OPER等待/事务寿命不应污染CATP持有；重复gate也不重置首次取得时间。
	d.transactions[1].Started = time.Now().Add(-time.Minute)
	run(ctx, 1, 'T', "SELECT pg_advisory_xact_lock($1)", "SELECT 1", int64(0x4f504552))
	run(ctx, 1, 'T', "SELECT pg_advisory_xact_lock($1)", "SELECT 1", int64(0x43415450))
	acquired := time.Now().Add(-100 * time.Millisecond)
	d.transactions[1].CATPAcquired = &acquired
	run(ctx, 1, 'T', "SELECT pg_advisory_xact_lock($1)", "SELECT 1", int64(0x43415450))
	if !d.transactions[1].CATPAcquired.Equal(acquired) {
		t.Fatal("重复CATP覆盖首次acquire")
	}
	run(ctx, 1, 'T', "UPDATE catalog_projection_work w SET completed=TRUE WHERE build_id=$1 AND phase=$2", "UPDATE 1", "build", "tracks")
	commit(context.Background(), 1, "commit", "COMMIT", nil)
	pending := d.unconfirmedBatches()
	if len(pending) != 1 || pending[0].HoldMS < 100 || pending[0].HoldMS > 900 {
		t.Fatalf("CATP计入OPER等待或丢失: %+v", pending)
	}
	if len(d.batchHolds) != 1 {
		t.Fatal("真实COMMIT不能等日志才入统计")
	}
	total, _ := d.batchReports(true)
	if total.Pass || total.UnconfirmedBatches != 1 {
		t.Fatal("未确认提交允许通过")
	}
	if err := d.committedBatch("tracks"); err != nil {
		t.Fatal(err)
	}
	if err := d.committedBatch("tracks"); err == nil {
		t.Fatal("重复日志错误复用同一事务")
	}
	for _, item := range []struct {
		name, actor, command, tag string
		gate                      bool
		err                       error
	}{
		{"清单发现", "maintenance", "commit", "COMMIT", false, nil},
		{"回滚", "maintenance", "rollback", "ROLLBACK", true, nil},
		{"失败COMMIT", "maintenance", "commit", "", true, errors.New("失败")},
		{"COMMIT退为ROLLBACK", "maintenance", "commit", "ROLLBACK", true, nil},
		{"扫描", "scan_background", "commit", "COMMIT", true, nil},
		{"M3", "http:correction", "commit", "COMMIT", true, nil},
	} {
		t.Run(item.name, func(t *testing.T) {
			actor := scaleDiagnosticContext(context.Background(), item.actor, "")
			run(actor, 2, 'I', "begin", "BEGIN")
			if item.gate {
				run(actor, 2, 'T', "SELECT pg_advisory_xact_lock($1)", "SELECT 1", int64(0x43415450))
			}
			if item.gate {
				run(actor, 2, 'T', "UPDATE catalog_projection_phases SET completed=$3 WHERE build_id=$1 AND phase=$2", "UPDATE 1", "build", "tracks", 1)
			}
			commit(context.Background(), 2, item.command, item.tag, item.err)
			if len(d.pendingBatches) != 0 {
				t.Fatalf("非应用提交误进入候选: %+v", d.pendingBatches)
			}
		})
	}
	totals, phases := d.batchReports(true)
	if totals.Samples != 1 || phases["tracks"].Samples != 1 || !totals.Pass {
		t.Fatalf("批统计错误: %+v %+v", totals, phases)
	}
}

// 只驱动tracer，不连接数据库；模拟成功完成标记与真实COMMIT回调顺序。
func scaleSimulateApplicationCommit(d *scaleDiagnostics, pid uint32, phase string, hold time.Duration) {
	ctx := scaleDiagnosticContext(context.Background(), "maintenance", "")
	query := func(status byte, sql, tag string, args ...any) {
		d.endQuery(d.beginQuery(ctx, pid, status, sql, args), 'T', tag, nil)
	}
	query('I', "begin", "BEGIN")
	query('T', "SELECT pg_advisory_xact_lock($1)", "SELECT 1", int64(0x43415450))
	query('T', "UPDATE catalog_projection_work SET completed=TRUE WHERE build_id=$1 AND phase=$2", "UPDATE 1", "build", phase)
	acquired := time.Now().Add(-hold)
	d.transactions[pid].CATPAcquired = &acquired
	d.endQuery(d.beginQuery(context.Background(), pid, 'T', "commit", nil), 'I', "COMMIT", nil)
}
func TestScaleAcceptanceBatchLogAndFirstFailure(t *testing.T) {
	d, err := newScaleDiagnostics(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	guard := &scaleBackfillGuard{diagnostics: d, stop: cancel}
	d.onAcceptanceFailure = guard.fail
	logger := slog.New(&scaleBatchHandler{base: slog.NewJSONHandler(io.Discard, nil), diagnostics: d, guard: guard}).With("phase", "artist_releases")
	scaleSimulateApplicationCommit(d, 1, "artist_releases", 100*time.Millisecond)
	logger.Info("非批次", "event", "catalog_projection_backfill_started")
	if len(d.batchHolds) != 1 || len(d.pendingBatches) != 1 {
		t.Fatal("非批次事件消耗候选")
	}
	logger.Info("已提交", "event", "catalog_projection_backfill_batch")
	if len(d.batchHolds) != 1 || len(d.pendingBatches) != 0 || ctx.Err() != nil {
		t.Fatal("合法批次关联失败")
	}
	// >1秒提交仍立即入统计，但不能取消；日志确认后允许继续收集。
	scaleSimulateApplicationCommit(d, 1, "artist_releases", 1001*time.Millisecond)
	if ctx.Err() != nil || guard.reason() != "" {
		t.Fatal("单批max错误中止整轮采集")
	}
	logger.Info("慢批已提交", "event", "catalog_projection_backfill_batch")
	totals, phases := d.batchReports(true)
	if totals.Pass || totals.P99MS <= 500 || totals.MaxBatch == nil || totals.MaxBatch.Phase != "artist_releases" || totals.MaxBatch.PID != 1 {
		t.Fatalf("完整p99失败或max归因丢失: %+v", totals)
	}
	guard.fail("真实操作失败")
	if ctx.Err() == nil {
		t.Fatal("真实错误没有取消清理")
	}
	first := guard.reason()
	guard.fail("后续context取消不能覆盖首次失败")
	if guard.reason() != first {
		t.Fatal("首次失败被覆盖")
	}
	totals, phases = d.batchReports(false)
	if totals.Samples != 2 || totals.MaxMS <= 1000 || totals.UnconfirmedBatches != 0 || phases["artist_releases"].Samples != 2 {
		t.Fatalf("失败批没有进报告: %+v %+v", totals, phases)
	}
}
func TestScaleAcceptancePostCommitFailureCannotHideRetry(t *testing.T) {
	d, err := newScaleDiagnostics(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	guard := &scaleBackfillGuard{diagnostics: d, stop: cancel}
	d.onAcceptanceFailure = guard.fail
	logger := slog.New(&scaleBatchHandler{base: slog.NewJSONHandler(io.Discard, nil), diagnostics: d, guard: guard})
	scaleSimulateApplicationCommit(d, 1, "tracks", 100*time.Millisecond)
	original := d.unconfirmedBatches()[0]
	worker := scaleDiagnosticContext(context.Background(), "maintenance", "")
	d.endQuery(d.beginQuery(worker, 1, 'I', "SELECT * FROM catalog_projection_state", nil), 'I', "", errors.New("提交后loadState失败"))
	// markFailed也是CATP COMMIT，但不得覆盖前一个未消费的应用提交。
	d.endQuery(d.beginQuery(worker, 1, 'I', "begin", nil), 'T', "BEGIN", nil)
	d.endQuery(d.beginQuery(worker, 1, 'T', "SELECT pg_advisory_xact_lock($1)", []any{int64(0x43415450)}), 'T', "SELECT 1", nil)
	d.endQuery(d.beginQuery(worker, 1, 'T', "UPDATE catalog_projection_state SET status='failed'", nil), 'T', "UPDATE 1", nil)
	d.endQuery(d.beginQuery(context.Background(), 1, 'T', "commit", nil), 'I', "COMMIT", nil)
	pending := d.unconfirmedBatches()
	if len(pending) != 1 || pending[0] != original {
		t.Fatal("markFailed覆盖了应用提交")
	}
	logger.Info("后台失败", "event", "catalog_projection_backfill_failed")
	if ctx.Err() == nil || !strings.Contains(guard.reason(), "catalog_projection_backfill_failed") {
		t.Fatal("允许生产重试掩盖失败")
	}
	// 即便错误地继续调度，collector也保留两个提交并拒绝确认，而不是覆盖。
	scaleSimulateApplicationCommit(d, 1, "tracks", 100*time.Millisecond)
	if len(d.pendingBatches) != 2 || d.acceptanceFailure == "" {
		t.Fatal("后续提交覆盖未确认应用批")
	}
	if err := d.committedBatch("tracks"); err == nil {
		t.Fatal("存在多个未确认提交仍接受日志")
	}
	totals, _ := d.batchReports(true)
	if totals.Pass || totals.Samples != 2 || totals.UnconfirmedBatches != 2 {
		t.Fatalf("漏提交错误通过: %+v", totals)
	}
}

func TestScaleAcceptanceProbeCadenceAndVerification(t *testing.T) {
	start := time.Unix(0, 0)
	for _, test := range []struct{ elapsed, next time.Duration }{{0, 30 * time.Second}, {29 * time.Second, 30 * time.Second}, {30 * time.Second, 60 * time.Second}, {76 * time.Second, 90 * time.Second}} {
		if got := scaleNextProbe(start, start.Add(test.elapsed)).Sub(start); got != test.next {
			t.Fatalf("调度=%s want=%s", got, test.next)
		}
	}
	if scaleProbeLabel(0) == scaleProbeLabel(1) || scaleProbeLabel(0) != scaleProbeLabel(2) {
		t.Fatal("label没有交替")
	}
	if !scaleProbeDirectoryMatches("在线探针", "在线探针专辑", "探针艺人", true) || scaleProbeDirectoryMatches("错误探针", "在线探针专辑", "探针艺人", true) || scaleProbeDirectoryMatches("在线探针", "在线探针专辑", "探针艺人", false) {
		t.Fatal("目录校验不能排除旧标签/旧观察")
	}
	if !scaleProbeCorrectionMatches(7, 8, "新厂牌", "新厂牌") || scaleProbeCorrectionMatches(7, 7, "新厂牌", "新厂牌") || scaleProbeCorrectionMatches(7, 8, "新厂牌", "旧厂牌") {
		t.Fatal("修正未排除no_changes/错误值")
	}
	cycle := scaleProbeCycle{SearchStatus: "ready", ArtistStatus: "building", CorrectionSearchStatus: "ready", CorrectionArtistStatus: "building", AfterCorrectionSearchStatus: "ready", AfterCorrectionArtistStatus: "building", DirectoryVerified: true, CorrectionVerified: true}
	if !scaleArtistPhaseCycle(cycle) {
		t.Fatal("艺人阶段成功负载未识别")
	}
	cycle.CorrectionVerified = false
	if scaleArtistPhaseCycle(cycle) {
		t.Fatal("只有202被当作艺人阶段成功负载")
	}
	cycle.CorrectionVerified = true
	cycle.AfterCorrectionArtistStatus = "ready"
	if scaleArtistPhaseCycle(cycle) {
		t.Fatal("revision读取与PATCH之间发布被算作回填期覆盖")
	}
	cycle.AfterCorrectionArtistStatus = "building"
	cycle.CorrectionArtistStatus = "ready"
	if scaleArtistPhaseCycle(cycle) {
		t.Fatal("发布后写入被当作回填期负载")
	}
}

func TestScaleAcceptanceMinimalDiagnosticsStopsBeforeMeasurement(t *testing.T) {
	d, err := newScaleDiagnostics(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.detailed = false
	ctx := scaleDiagnosticContext(context.Background(), "maintenance", "")
	for _, q := range []struct {
		sql, tag string
		status   byte
		args     []any
	}{
		{"begin", "BEGIN", 'T', nil},
		{"SELECT pg_advisory_xact_lock($1)", "SELECT 1", 'T', []any{int64(0x43415450)}},
		{"UPDATE catalog_projection_phases SET completed=$3 WHERE build_id=$1 AND phase=$2", "UPDATE 1", 'T', []any{"build", "releases", 1}},
		{"commit", "COMMIT", 'I', nil},
	} {
		d.endQuery(d.beginQuery(ctx, 7, 'T', q.sql, q.args), q.status, q.tag, nil)
	}
	if err := d.committedBatch("releases"); err != nil {
		t.Fatal(err)
	}
	if err := d.stopOnline(); err != nil {
		t.Fatal(err)
	}
	fresh := context.Background()
	if d.beginQuery(fresh, 7, 'I', "SELECT 1", nil) != fresh {
		t.Fatal("正式计时仍开CATP trace")
	}
	total, _ := d.batchReports(true)
	if total.Samples != 1 || !total.Pass {
		t.Fatalf("非详细模式遗漏应用批: %+v", total)
	}
}

func TestScaleAcceptanceApplicationPhaseIdentification(t *testing.T) {
	for _, item := range []struct{ query, want string }{
		{"UPDATE catalog_projection_work w SET completed=TRUE,completed_input_revision=s.input_revision FROM catalog_projection_subject_state s WHERE w.build_id=$1 AND w.phase=$2", "tracks"},
		{"UPDATE catalog_projection_work SET completed=TRUE WHERE build_id=$1 AND phase=$2", "tracks"},
		{"UPDATE catalog_projection_phases SET completed=$3 WHERE build_id=$1 AND phase=$2", "tracks"},
		{"INSERT INTO catalog_projection_phases(build_id,phase,completed) VALUES($1,$2,0)", ""},
		{"UPDATE catalog_projection_work SET completed=FALSE WHERE build_id=$1 AND phase=$2", ""},
		{"UPDATE catalog_projection_state SET status='failed' WHERE build_id=$1", ""},
	} {
		if got := scaleApplicationPhase(item.query, []any{"build", "tracks", 10}); got != item.want {
			t.Fatalf("phase=%q want=%q query=%s", got, item.want, item.query)
		}
	}
	if got := scaleApplicationPhase("UPDATE catalog_projection_work SET completed=TRUE", nil); got != "" {
		t.Fatal("缺参数伪造phase")
	}
}

func TestScaleAcceptanceCollectsAllAfterPerformanceFailures(t *testing.T) {
	var report scaleReport
	report.recordCheck("catp", false, "完整p99=501ms")
	first := report.FirstFailure
	report.recordCheck("m3", false, "整轮M3 p95=1001ms")
	report.recordCheck("backfill", false, "联合回填36分钟+1ms")
	// 模拟完整后续搜索/EXPLAIN和两种10000队列；不能被前面的性能失败截断。
	for _, name := range scaleRequiredChecks(26) {
		if _, ok := report.Checks[name]; !ok {
			report.recordCheck(name, true, "已收集")
		}
	}
	if report.Complete || report.Pass {
		t.Fatal("采集结束前错误宣称通过")
	}
	report.finishCollection(26)
	if !report.Complete || report.Pass || report.FirstError != "" || report.FirstFailure != first || len(report.AcceptanceFailures) != 3 {
		t.Fatalf("统一判定丢失失败: %+v", report)
	}
	for _, name := range []string{"search_26_suggestions", "search_26_results", "search_26_explain", "queue_album", "queue_random"} {
		check, ok := report.Checks[name]
		if !ok || !check.Pass {
			t.Fatalf("性能失败后漏测%s", name)
		}
	}
	// 后续通过不得覆盖已记录的失败，完整采集不等于通过。
	report.recordCheck("catp", true, "后来的局部样本不能覆盖整轮失败")
	if report.Checks["catp"].Pass || report.Pass {
		t.Fatal("后续PASS掩盖失败")
	}
	var missing scaleReport
	missing.finishCollection(26)
	if missing.Complete || missing.Pass || len(missing.AcceptanceFailures) != len(scaleRequiredChecks(26)) {
		t.Fatal("未执行项被当作PASS")
	}
	var success scaleReport
	for _, name := range scaleRequiredChecks(26) {
		success.recordCheck(name, true, "通过")
	}
	success.finishCollection(26)
	if !success.Pass {
		t.Fatal("完整通过却未通过")
	}
}

func TestScaleAcceptanceOnlineWholeRound(t *testing.T) {
	requests := []scaleDiagnosticHTTP{
		{Kind: "correction", Status: 200, ElapsedMS: 2000}, // 原9900用例也进入同一分布。
		{Kind: "scan_start", Status: 202},
		{Kind: "audio", Status: 206},
		{Kind: "suggestions", Status: 200},
	}
	for range 19 {
		requests = append(requests, scaleDiagnosticHTTP{Kind: "correction", Status: 200, ElapsedMS: 1000})
	}
	cycles := []scaleProbeCycle{{ScanStatus: "succeeded", DirectoryVerified: true}}
	got := scaleEvaluateOnline(requests, cycles, true)
	if !got.M3.Pass || got.M3.Requests != 20 || got.M3.Successful != 20 || got.M3.Latency.P95MS != 1000 || !got.Scans.Pass || !got.Audio.Pass || !got.Search.Pass {
		t.Fatalf("整轮聚合错误: %+v", got)
	}
	got = scaleEvaluateOnline(requests[:len(requests)-1], cycles, true)
	if got.M3.Pass || got.M3.Latency.P95MS != 2000 {
		t.Fatal("原9900慢请求被漏掉或用子集p95均值代替")
	}
	failed := append([]scaleDiagnosticHTTP(nil), requests...)
	failed[0].Status = 503
	failed[2].Status = 503
	failed[3].Status = 503
	got = scaleEvaluateOnline(failed, cycles, true)
	if got.M3.Pass || got.M3.AllSucceeded || got.Audio.Pass || got.Search.Pass || got.Audio.ServiceUnavailable != 1 || got.Search.ServiceUnavailable != 1 {
		t.Fatalf("503被掩盖: %+v", got)
	}
	got = scaleEvaluateOnline(requests, []scaleProbeCycle{{ScanStatus: "running"}}, true)
	if got.Scans.Pass {
		t.Fatal("202代替真实succeeded")
	}
	got = scaleEvaluateOnline(requests, cycles, false)
	if got.M3.Pass || got.Scans.Pass || got.Audio.Pass || got.Search.Pass || got.Evaluated {
		t.Fatal("未完成轮次被判PASS")
	}
	got = scaleEvaluateOnline(nil, nil, true)
	if got.M3.Pass || got.Scans.Pass || got.Audio.Pass || got.Search.Pass {
		t.Fatal("零样本被判PASS")
	}
}

func TestScaleAcceptanceExecutionBudgetIndependent(t *testing.T) {
	for _, item := range []struct {
		input string
		want  time.Duration
		valid bool
	}{
		{"", 90 * time.Minute, true}, {"120m", 2 * time.Hour, true}, {"0", 0, false}, {"-1m", 0, false}, {"invalid", 0, false},
	} {
		got, err := scaleExecutionBudget(item.input)
		if (err == nil) != item.valid || got != item.want {
			t.Fatalf("budget %q=%s err=%v", item.input, got, err)
		}
	}
	if scaleBackfillBudget != 36*time.Minute || scaleRequestBudget != 45*time.Second {
		t.Fatal("总预算配置污染验收线或单请求预算")
	}
}

// 只模拟testing.Cleanup的LIFO，不把故意注入的Error变成当前单测失败。
type scaleCleanupTestTB struct {
	testing.TB
	callbacks []func()
	messages  []string
}

func (tb *scaleCleanupTestTB) Cleanup(fn func()) { tb.callbacks = append(tb.callbacks, fn) }
func (tb *scaleCleanupTestTB) Error(args ...any) {
	tb.messages = append(tb.messages, fmt.Sprint(args...))
}
func (tb *scaleCleanupTestTB) Errorf(format string, args ...any) {
	tb.messages = append(tb.messages, fmt.Sprintf(format, args...))
}
func (tb *scaleCleanupTestTB) runCleanups() {
	for i := len(tb.callbacks) - 1; i >= 0; i-- {
		tb.callbacks[i]()
	}
}

func TestScaleAcceptanceFinalReportAfterAllCleanup(t *testing.T) {
	for _, performanceFailed := range []bool{false, true} {
		t.Run(fmt.Sprintf("performance_failed_%v", performanceFailed), func(t *testing.T) {
			report := scaleReport{Complete: true, Pass: true}
			tb := &scaleCleanupTestTB{}
			var order []string
			var saved scaleReport
			if performanceFailed {
				report.recordCheck("catp", false, "p99超过500ms")
			}
			firstPerformance := report.FirstFailure
			// 与benchmark相同：先注册最终报告，再取得资源和注册资源清理。
			scaleRegisterFinalReport(tb.Cleanup, &report, func() bool { return performanceFailed || len(tb.messages) > 0 }, func() error {
				order = append(order, "report")
				raw, err := json.Marshal(report)
				if err != nil {
					return err
				}
				return json.Unmarshal(raw, &saved)
			}, func(err error) { t.Error(err) })
			schemaTB := &scaleReportingTB{TB: tb, onError: func(stage string, err error) { report.recordRunError(stage, err) }}
			func() {
				defer func() {
					order = append(order, "diagnostics")
					report.recordRunError("cleanup.diagnostics.close", errors.New("诊断关闭失败"))
					tb.Error("诊断关闭失败")
				}()
				defer func() {
					order = append(order, "worker")
					report.recordRunError("cleanup.maintenance.join", errors.New("worker退出失败"))
					tb.Error("worker退出失败")
				}()
				schemaTB.Cleanup(func() { order = append(order, "schema"); schemaTB.Errorf("drop schema: %s", "清理失败") })
				tb.Cleanup(func() {
					order = append(order, "directory")
					report.recordRunError("cleanup.temp_directory.remove", errors.New("目录删除失败"))
					tb.Error("目录删除失败")
				})
			}()
			if report.Finalized {
				t.Fatal("defer尚未配合全部Cleanup完成就封口")
			}
			tb.runCleanups()
			wantOrder := []string{"worker", "diagnostics", "directory", "schema", "report"}
			if !reflect.DeepEqual(order, wantOrder) {
				t.Fatalf("最终报告顺序错误: %v", order)
			}
			if saved.Pass || !saved.Complete || !saved.Finalized || len(saved.Errors) != 4 || !strings.HasPrefix(saved.FirstError, "cleanup.maintenance.join:") {
				t.Fatalf("清理失败被吞或封口太早: %+v", saved)
			}
			if saved.Errors[3].Stage != "cleanup.schema" {
				t.Fatal("testing.Cleanup中的schema错误未显式记录")
			}
			if performanceFailed {
				if saved.FirstFailure != firstPerformance || len(saved.AcceptanceFailures) != 1 {
					t.Fatal("性能失败与清理失败没有同时保留")
				}
			} else if saved.FirstFailure != saved.FirstError {
				t.Fatal("清理首因丢失")
			}
		})
	}
}

func TestScaleAcceptanceCleanupPreservesOriginalError(t *testing.T) {
	report := scaleReport{Complete: true}
	report.recordRunError("operation", errors.New("最初SQL失败"))
	first := report.FirstError
	report.recordCheck("m3", false, "已有性能失败")
	report.recordRunError("cleanup.wait_for_scans", errors.New("扫描未退出"))
	report.recordRunError("cleanup.database.close", errors.New("连接池关闭失败"))
	// 多次刷新报告时同一个guard首因不重复追加，也不能掩盖后来的不同错误。
	report.recordRunError("operation", errors.New("最初SQL失败"))
	if report.FirstError != first || report.FirstFailure != first || len(report.Errors) != 3 || len(report.AcceptanceFailures) != 1 || report.Pass {
		t.Fatalf("首因/全部错误没有保留: %+v", report)
	}
}

func TestScaleAcceptanceFinalReportWriteFailure(t *testing.T) {
	tb := &scaleCleanupTestTB{}
	report := scaleReport{Complete: true, Pass: true}
	scaleRegisterFinalReport(tb.Cleanup, &report, func() bool { return false }, func() error { return errors.New("写最终报告失败") }, func(err error) { tb.Error(err) })
	tb.runCleanups()
	if report.Pass || len(report.Errors) != 1 || report.Errors[0].Stage != "report.final_write" || len(tb.messages) != 1 {
		t.Fatalf("最终落盘失败静默: %+v", report)
	}
}
