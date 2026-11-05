package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	scaleTotalBudget       = 90 * time.Minute
	scaleBackfillBudget    = 36 * time.Minute
	scaleRequestBudget     = 45 * time.Second
	scaleProbeInterval     = 30 * time.Second
	scaleReadProbeInterval = time.Second
	scaleCATPP95TargetMS   = 200.0
	scaleCATPP99TargetMS   = 500.0
	scaleM3P95TargetMS     = 1000.0
)

type scaleCATPHoldReport struct {
	Samples                       int
	MedianMS, P95MS, P99MS, MaxMS float64
	P95TargetMS, P99TargetMS      float64
	Evaluated, Pass               bool
	P95Pass, P99Pass              bool
	UnconfirmedBatches            int
	EvidenceError                 string
	MaxBatch                      *scaleCommittedGate
}

func scaleCATPHoldStats(values []float64, complete bool) scaleCATPHoldReport {
	result := scaleCATPHoldReport{Samples: len(values), P95TargetMS: scaleCATPP95TargetMS, P99TargetMS: scaleCATPP99TargetMS, Evaluated: complete}
	if len(values) == 0 {
		return result
	}
	copied := append([]float64(nil), values...)
	stats := scaleStats(0, copied, scaleCATPP95TargetMS)
	result.MedianMS, result.P95MS, result.MaxMS = stats.MedianMS, stats.P95MS, copied[len(copied)-1]
	result.P99MS = copied[(99*len(copied)+99)/100-1]
	result.P95Pass = complete && stats.Pass
	result.P99Pass = complete && result.P99MS <= scaleCATPP99TargetMS
	result.Pass = result.P95Pass && result.P99Pass
	return result
}

type scaleCATPPhaseReport struct {
	Samples                       int
	MedianMS, P95MS, P99MS, MaxMS float64
}

type scaleCommittedGate struct {
	TransactionID            uint64
	HoldMS                   float64
	Phase                    string
	PID                      uint32
	AcquiredAt, CommittedAt  time.Time
	CommitMS, BeforeCommitMS float64
}

// 从本事务实际成功的完成标记写入识别应用批；清单发现是INSERT，不在此列。
// 仅取内部phase参数，不记录subject UUID或标签值。
func scaleApplicationPhase(query string, args []any) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(query), " "))
	work := strings.HasPrefix(normalized, "update catalog_projection_work ") && strings.Contains(normalized, "set completed=true")
	progress := strings.HasPrefix(normalized, "update catalog_projection_phases set completed=")
	if !work && !progress {
		return ""
	}
	var bound []any
	for _, arg := range args {
		switch arg.(type) {
		case pgx.QueryResultFormatsByOID, pgx.QueryResultFormats, pgx.QueryExecMode:
			continue
		}
		bound = append(bound, arg)
	}
	if len(bound) < 2 {
		return ""
	}
	phase, _ := bound[1].(string)
	return phase
}

// 在COMMIT结束时先入统计，日志只是确认。即使提交后loadState报错，慢批也不会消失。
func (d *scaleDiagnostics) recordApplicationCommitLocked(batch scaleCommittedGate) error {
	previous := len(d.pendingBatches)
	d.pendingBatches[batch.TransactionID] = batch
	d.batchHolds = append(d.batchHolds, batch.HoldMS)
	d.batchHoldsByPhase[batch.Phase] = append(d.batchHoldsByPhase[batch.Phase], batch.HoldMS)
	d.emitLocked("transactions", map[string]any{"event": "application_batch_committed", "batch": batch})
	if d.maxBatch == nil || batch.HoldMS > d.maxBatch.HoldMS {
		copy := batch
		d.maxBatch = &copy
	}
	if previous > 0 {
		return fmt.Errorf("发现新应用提交transaction=%d时仍有%d个未确认应用提交，禁止覆盖或继续重试", batch.TransactionID, previous)
	}
	return nil
}
func (d *scaleDiagnostics) committedBatch(phase string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.pendingBatches) != 1 {
		return fmt.Errorf("已提交批phase=%s对应未确认CATP提交数量=%d，必须唯一", phase, len(d.pendingBatches))
	}
	for id, batch := range d.pendingBatches {
		if phase != batch.Phase {
			return fmt.Errorf("应用提交transaction=%d的phase=%s与日志phase=%s不一致", id, batch.Phase, phase)
		}
		delete(d.pendingBatches, id)
		d.emitLocked("transactions", map[string]any{"event": "committed_application_batch", "phase": phase, "transaction_id": id, "catp_hold_ms": batch.HoldMS})
	}
	return nil
}
func (d *scaleDiagnostics) unconfirmedLocked() []scaleCommittedGate {
	result := make([]scaleCommittedGate, 0, len(d.pendingBatches))
	for _, batch := range d.pendingBatches {
		result = append(result, batch)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].TransactionID < result[j].TransactionID })
	return result
}
func (d *scaleDiagnostics) unconfirmedBatches() []scaleCommittedGate {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.unconfirmedLocked()
}
func (d *scaleDiagnostics) batchReports(complete bool) (scaleCATPHoldReport, map[string]scaleCATPPhaseReport) {
	d.mu.Lock()
	defer d.mu.Unlock()
	phases := make(map[string]scaleCATPPhaseReport, len(d.batchHoldsByPhase))
	for phase, values := range d.batchHoldsByPhase {
		stats := scaleCATPHoldStats(values, false)
		phases[phase] = scaleCATPPhaseReport{Samples: stats.Samples, MedianMS: stats.MedianMS, P95MS: stats.P95MS, P99MS: stats.P99MS, MaxMS: stats.MaxMS}
	}
	total := scaleCATPHoldStats(d.batchHolds, complete)
	total.UnconfirmedBatches = len(d.pendingBatches)
	total.EvidenceError = d.acceptanceFailure
	if d.maxBatch != nil {
		copy := *d.maxBatch
		total.MaxBatch = &copy
	}
	total.Pass = total.Pass && len(d.pendingBatches) == 0 && d.acceptanceFailure == ""
	return total, phases
}

// 第一处失败不可被取消后续报错覆盖；先留现场，再停止worker和在线请求。
type scaleBackfillGuard struct {
	mu                    sync.Mutex
	failure               string
	stop                  context.CancelFunc
	diagnostics           *scaleDiagnostics
	cleanupCheckpointOnce sync.Once
	cleanupCheckpointErr  error
}

func (g *scaleBackfillGuard) fail(message string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.failure != "" {
		return
	}
	g.failure = message
	if err := g.diagnostics.checkpoint("diagnostics-first-failure"); err != nil {
		g.failure += "; 保存现场失败: " + err.Error()
	}
	if err := g.checkpointBeforeCleanup(); err != nil {
		g.failure += "; 保存清理前现场失败: " + err.Error()
	}
	g.stop()
}
func (g *scaleBackfillGuard) checkpointBeforeCleanup() error {
	g.cleanupCheckpointOnce.Do(func() { g.cleanupCheckpointErr = g.diagnostics.checkpoint("diagnostics-before-cleanup") })
	return g.cleanupCheckpointErr
}
func (g *scaleBackfillGuard) reason() string { g.mu.Lock(); defer g.mu.Unlock(); return g.failure }

type scaleBatchHandler struct {
	base        slog.Handler
	diagnostics *scaleDiagnostics
	guard       *scaleBackfillGuard
	attrs       []slog.Attr
}

func (h *scaleBatchHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.base.Enabled(ctx, level)
}
func (h *scaleBatchHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	copy := *h
	copy.base = h.base.WithAttrs(attrs)
	copy.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &copy
}
func (h *scaleBatchHandler) WithGroup(name string) slog.Handler {
	copy := *h
	copy.base = h.base.WithGroup(name)
	return &copy
}
func (h *scaleBatchHandler) Handle(ctx context.Context, record slog.Record) error {
	if err := h.base.Handle(ctx, record); err != nil {
		h.guard.fail("写回填日志失败: " + err.Error())
		return err
	}
	var event, phase string
	read := func(attr slog.Attr) bool {
		switch attr.Key {
		case "event":
			event = attr.Value.String()
		case "phase":
			phase = attr.Value.String()
		}
		return true
	}
	for _, attr := range h.attrs {
		read(attr)
	}
	record.Attrs(read)
	if event == "catalog_projection_backfill_failed" {
		h.guard.fail("生产maintenance报告catalog_projection_backfill_failed；停止而不是重试掩盖已提交批证据")
		return nil
	}
	if event == "catalog_projection_backfill_batch" {
		if err := h.diagnostics.committedBatch(phase); err != nil {
			h.guard.fail(err.Error())
		}
	}
	return nil
}

type scaleOnlineLoadReport struct {
	ReadIntervalMS, ScanM3IntervalMS, RequestBudgetMS float64
	Cycles                                            []scaleProbeCycle
	ArtistPhaseCycles                                 int
}
type scaleProbeCycle struct {
	Number                                                                               int
	StartedMS, CompletedMS, ScanStartMS, ScanCompleteMS, CorrectionMS                    float64
	SearchStatus, ArtistStatus, ArtistPhase, ScanID, ScanStatus                          string
	CorrectionSearchStatus, CorrectionArtistStatus, CorrectionArtistPhase                string
	AfterCorrectionSearchStatus, AfterCorrectionArtistStatus, AfterCorrectionArtistPhase string
	ReleaseID, TrackID, Label                                                            string
	ExpectedRevision, AppliedRevision                                                    int64
	DirectoryVerified, CorrectionVerified                                                bool
}

func scaleProbeLabel(cycle int) string { return fmt.Sprintf("在线探针厂牌-%d", cycle%2) }

// 调度锚定worker启动时间；慢请求不堆积补发，实际开始/完成时间另记，不伪造固定吞吐。
func scaleNextProbe(start, now time.Time) time.Time {
	return start.Add((now.Sub(start)/scaleProbeInterval + 1) * scaleProbeInterval)
}

// 仅调整整轮执行预算；36分钟验收线和45秒单请求预算不随之改变。
func scaleExecutionBudget(value string) (time.Duration, error) {
	if value == "" {
		return scaleTotalBudget, nil
	}
	budget, err := time.ParseDuration(value)
	if err != nil || budget <= 0 {
		return 0, fmt.Errorf("ROOMUSIC_SEARCH_SCALE_TIMEOUT必须是正时长，例如90m")
	}
	return budget, nil
}

type scaleAcceptanceCheck struct {
	Pass   bool
	Detail string
}
type scaleAcceptanceFailure struct{ Name, Detail string }

// 性能失败只累积证据，不取消负载/后续测量；真正操作错误仍交给guard。
func (r *scaleReport) recordCheck(name string, pass bool, detail string) {
	if r.Checks == nil {
		r.Checks = map[string]scaleAcceptanceCheck{}
	}
	if previous, ok := r.Checks[name]; ok && !previous.Pass {
		return
	}
	r.Checks[name] = scaleAcceptanceCheck{Pass: pass, Detail: detail}
	if !pass {
		r.AcceptanceFailures = append(r.AcceptanceFailures, scaleAcceptanceFailure{Name: name, Detail: detail})
		if r.FirstFailure == "" {
			r.FirstFailure = name + ": " + detail
		}
	}
	r.Pass = r.Complete && r.FirstError == "" && len(r.AcceptanceFailures) == 0
}
func scaleRequiredChecks(queryCount int) []string {
	checks := []string{"backfill", "catp", "m3", "scans", "audio", "online_search", "fixture", "page_100", "queue_album", "queue_random"}
	for i := 0; i < queryCount; i++ {
		for _, kind := range []string{"suggestions", "results", "explain"} {
			checks = append(checks, fmt.Sprintf("search_%02d_%s", i+1, kind))
		}
	}
	return checks
}
func (r *scaleReport) finishCollection(queryCount int) {
	complete := true
	for _, name := range scaleRequiredChecks(queryCount) {
		if _, ok := r.Checks[name]; !ok {
			complete = false
			r.recordCheck(name, false, "验收项未收集，不能宣称通过")
		}
	}
	r.Complete = complete
	r.Pass = complete && r.FirstError == "" && len(r.AcceptanceFailures) == 0
}

type scaleRunError struct{ Stage, Message string }

// 首因保持不变，每个不同的操作/清理错误都保留；性能失败不抑制清理错误。
func (r *scaleReport) recordRunError(stage string, err error) {
	if err == nil {
		return
	}
	item := scaleRunError{Stage: stage, Message: scaleDiagnosticError(err)}
	for _, previous := range r.Errors {
		if previous == item {
			return
		}
	}
	r.Errors = append(r.Errors, item)
	if r.FirstError == "" {
		r.FirstError = stage + ": " + item.Message
	}
	if r.FirstFailure == "" {
		r.FirstFailure = r.FirstError
	}
	r.Pass = false
}

// 最先注册，借助testing.Cleanup的LIFO在所有defer/schema/临时目录清理之后落盘。
// write必须返回错误而不是Fatal，避免最终清理被Goexit截断。
func scaleRegisterFinalReport(register func(func()), report *scaleReport, failed func() bool, write func() error, onError func(error)) {
	register(func() {
		if failed() && !report.Complete && report.FirstError == "" {
			report.recordRunError("benchmark", fmt.Errorf("基准未完成，详见测试日志"))
		}
		report.Finalized = true
		report.Pass = report.Complete && report.FirstError == "" && len(report.AcceptanceFailures) == 0
		if err := write(); err != nil {
			report.recordRunError("report.final_write", err)
			onError(err)
		}
	})
}

// NewSchema在自己的testing.Cleanup里报错，必须同步写入报告，不能只看b.Failed位。
type scaleReportingTB struct {
	testing.TB
	onError  func(string, error)
	cleaning bool
}

func (t *scaleReportingTB) Cleanup(cleanup func()) {
	t.TB.Cleanup(func() { t.cleaning = true; defer func() { t.cleaning = false }(); cleanup() })
}
func (t *scaleReportingTB) record(message string) {
	stage := "schema"
	if t.cleaning {
		stage = "cleanup.schema"
	}
	t.onError(stage, fmt.Errorf("%s", message))
}
func (t *scaleReportingTB) Error(args ...any) { t.record(fmt.Sprint(args...)); t.TB.Error(args...) }
func (t *scaleReportingTB) Errorf(format string, args ...any) {
	t.record(fmt.Sprintf(format, args...))
	t.TB.Errorf(format, args...)
}
func (t *scaleReportingTB) Fatal(args ...any) { t.record(fmt.Sprint(args...)); t.TB.Fatal(args...) }
func (t *scaleReportingTB) Fatalf(format string, args ...any) {
	t.record(fmt.Sprintf(format, args...))
	t.TB.Fatalf(format, args...)
}

// 自有合成目录显式记录删除错误，避免testing内部TempDir清理仅置失败位而丢失原因。
func scaleTempDir(t testing.TB, onError func(string, error)) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "roomusic-scale-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { onError("temp_directory.remove", os.RemoveAll(dir)) })
	return dir
}
