package library

import (
	"context"
	"database/sql"
	"io/fs"
	"math/big"
	"path/filepath"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
)

const maxFeedbackCount int64 = 9007199254740991

// ScanProgress 是持久化的工作量快照；阶段分子属于当前根，文件累计属于整次扫描。
type ScanProgress struct {
	Version        int       `json:"version"`
	Phase          string    `json:"phase"`
	PercentBP      int64     `json:"percent_bp"`
	Completed      int64     `json:"completed"`
	Total          int64     `json:"total"`
	Unit           string    `json:"unit"`
	FilesCounted   int64     `json:"files_counted"`
	FilesTotal     *int64    `json:"files_total"`
	FilesProcessed int64     `json:"files_processed"`
	CountComplete  bool      `json:"count_complete"`
	FilesChanged   bool      `json:"files_changed"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// RootProgress 不直接序列化，HTTP presenter 按角色决定是否包含根身份。
type RootProgress struct {
	Index    *int
	Total    int
	ID, Name *string
}

type rootCount struct {
	files    int64
	complete bool
}
type scanFeedback struct {
	progress                  ScanProgress
	root                      RootProgress
	counts                    []rootCount
	current                   int
	base, weight, denominator int64
	seq, units                int64
	lastAttempt               time.Time
	now                       func() time.Time
	progressUnavailable       bool
	invalid                   bool
	changes                   *ingest.ScanFeedback
	service                   *Service
	executor                  scanExecutor
	scanID                    string
}

func newScanFeedback(service *Service, executor scanExecutor, scanID string, roots []registeredRoot) *scanFeedback {
	return &scanFeedback{service: service, executor: executor, scanID: scanID, changes: ingest.NewScanFeedback(scanID), now: time.Now,
		progress: ScanProgress{Version: 1, Phase: "counting", Unit: "roots", Total: int64(len(roots))}, root: RootProgress{Total: len(roots)}, counts: make([]rootCount, len(roots))}
}

func feedbackArg(values []*scanFeedback) *scanFeedback {
	if len(values) > 0 {
		return values[0]
	}
	return nil
}

func (f *scanFeedback) add(target *int64) {
	if *target >= maxFeedbackCount {
		f.invalid = true
		f.changes.Break()
		return
	}
	*target++
}
func (f *scanFeedback) selectRoot(index int, root registeredRoot) {
	f.current = index
	i := index + 1
	name := filepath.Base(root.Path)
	id := root.ID
	f.root.Index = &i
	f.root.ID = &id
	f.root.Name = &name
}

// weightedProgress 只在最终基点取整；任意安全整数计数的中间乘积也不会溢出。
func weightedProgress(base, weight, denominator, numerator, divisor int64) int64 {
	if denominator <= 0 || divisor <= 0 {
		return 500
	}
	if numerator > divisor {
		numerator = divisor
	}
	n := new(big.Int).Mul(big.NewInt(base), big.NewInt(divisor))
	n.Add(n, new(big.Int).Mul(big.NewInt(weight), big.NewInt(numerator)))
	n.Mul(n, big.NewInt(9400))
	d := new(big.Int).Mul(big.NewInt(denominator), big.NewInt(divisor))
	n.Quo(n, d)
	return min(int64(9900), 500+n.Int64())
}
func (f *scanFeedback) fraction(numerator, divisor int64) {
	f.progress.PercentBP = max(f.progress.PercentBP, weightedProgress(f.base, f.weight, f.denominator, numerator, divisor))
}
func (f *scanFeedback) phase(ctx context.Context, phase string, total int64) {
	if ctx.Err() != nil {
		return
	}
	f.progress.Phase = phase
	f.progress.Completed = 0
	f.progress.Total = total
	switch phase {
	case "reading_tags":
		f.progress.Unit = "files"
		f.fraction(0, 1)
	case "organizing":
		f.progress.Unit = "roots"
		f.fraction(3, 5)
	case "saving":
		f.progress.Unit = "scopes"
		f.fraction(4, 5)
	}
	f.flush(ctx, true)
}
func (f *scanFeedback) processed(ctx context.Context) {
	if f == nil || ctx.Err() != nil {
		return
	}
	f.add(&f.progress.FilesProcessed)
	f.add(&f.progress.Completed)
	if f.progress.Completed > f.counts[f.current].files {
		f.progress.FilesChanged = true
	}
	if f.progress.Total > 0 {
		// 构造有理数而不是先将根文件数乘五，避免接近安全整数上限时丢精度。
		n := min(f.progress.Completed, f.progress.Total)
		f.fraction(3*n, 5*f.progress.Total)
	}
	f.units++
	f.flush(ctx, false)
}
func (f *scanFeedback) scopeDone(ctx context.Context) {
	if f == nil || ctx.Err() != nil {
		return
	}
	f.add(&f.progress.Completed)
	if f.progress.Total > 0 {
		f.fraction(4*f.progress.Total+min(f.progress.Completed, f.progress.Total), 5*f.progress.Total)
	}
	f.units++
	f.flush(ctx, false)
}

func (f *scanFeedback) flush(ctx context.Context, force bool) {
	if f == nil || f.invalid || f.progressUnavailable || ctx.Err() != nil {
		return
	}
	now := time.Now()
	if f.now != nil {
		now = f.now()
	}
	if !force && f.units < 256 && now.Sub(f.lastAttempt) < time.Second {
		return
	}
	f.lastAttempt = now
	f.units = 0
	f.add(&f.seq)
	// 服务端语句预算不会因客户端取消查询而直接丢失持有协调锁的连接。
	tx, err := f.executor.BeginTx(ctx, nil)
	if err == nil {
		defer tx.Rollback()
		_, err = tx.ExecContext(ctx, "SET LOCAL statement_timeout='500ms'")
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE scan_runs SET phase=$2,progress_bp=GREATEST(progress_bp,$3),
 phase_completed=$4,phase_total=$5,progress_updated_at=clock_timestamp(),progress_seq=$6,
 files_counted=GREATEST(files_counted,$7),files_total=$8,files_processed=GREATEST(files_processed,$9),count_complete=$10,files_changed=$11,
 roots_total=$12,current_root_index=$13,current_root_id=$14::uuid,current_root_name=$15
 WHERE id=$1::uuid AND status='running' AND feedback_version=1 AND progress_seq<$6`,
				f.scanID, f.progress.Phase, f.progress.PercentBP, f.progress.Completed, f.progress.Total, f.seq, f.progress.FilesCounted, f.progress.FilesTotal,
				f.progress.FilesProcessed, f.progress.CountComplete, f.progress.FilesChanged, f.root.Total, f.root.Index, f.root.ID, f.root.Name)
		}
		if err == nil {
			err = tx.Commit()
		}
	}
	if err != nil && f.service.logger != nil {
		f.service.logger.Warn("扫描进度刷新失败", "event", "library.scan.progress_write_failed", "scan_run_id", f.scanID)
	}
}

func isScanCandidate(path string) bool {
	extension := strings.ToLower(filepath.Ext(path))
	return extension == ".cue" || isSupportedAudioExtension(extension) || isAudioCandidateExtension(extension)
}

func (service *Service) countScanRoots(ctx context.Context, executor scanExecutor, scanID string, roots []registeredRoot, f *scanFeedback) bool {
	if ctx.Err() != nil {
		return false
	}
	complete := true
	for index, root := range roots {
		if ctx.Err() != nil {
			return false
		}
		f.selectRoot(index, root)
		f.flush(ctx, true)
		count := rootCount{complete: true}
		if !registeredRootAvailable(root.Path) {
			count.complete = false
			_ = service.recordDiagnosticWith(ctx, executor, scanID, "", "root_unavailable", "注册目录当前不可用")
		} else {
			err := filepath.WalkDir(root.Path, func(path string, entry fs.DirEntry, walkErr error) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if walkErr != nil {
					count.complete = false
					return service.recordDiagnosticWith(ctx, executor, scanID, safeRelativePath(root.Path, path), "permission_error", "无法读取文件")
				}
				if !entry.IsDir() && isScanCandidate(path) {
					f.add(&count.files)
					f.add(&f.progress.FilesCounted)
					f.units++
					f.flush(ctx, false)
				}
				return ctx.Err()
			})
			if err != nil {
				count.complete = false
			}
		}
		f.counts[index] = count
		complete = complete && count.complete
		if ctx.Err() != nil {
			return false
		}
		f.progress.Completed = int64(index + 1)
		f.progress.PercentBP = 500 * int64(index+1) / int64(len(roots))
		f.flush(ctx, true)
	}
	if ctx.Err() != nil {
		return false
	}
	total := f.progress.FilesCounted
	f.progress.FilesTotal = &total
	f.progress.CountComplete = complete
	f.denominator = total
	if total == 0 {
		f.denominator = int64(len(roots))
	}
	f.progress.PercentBP = 500
	f.flush(ctx, true)
	return complete
}

// scanFeedbackColumns 与 scanRow 同步；旧记录的空值保持 legacy。
const scanFeedbackColumns = `,feedback_version,phase,progress_bp,phase_completed,phase_total,progress_updated_at,
 files_counted,files_total,files_processed,count_complete,files_changed,roots_total,current_root_index,current_root_id::text,current_root_name,report_state`

type feedbackRow struct {
	version, bp, completed, total, counted, filesTotal, processed, roots, index sql.NullInt64
	phase, id, name, state                                                      sql.NullString
	updated                                                                     sql.NullTime
	complete, changed                                                           sql.NullBool
}

func (r *feedbackRow) destinations() []any {
	return []any{&r.version, &r.phase, &r.bp, &r.completed, &r.total, &r.updated, &r.counted, &r.filesTotal, &r.processed, &r.complete, &r.changed, &r.roots, &r.index, &r.id, &r.name, &r.state}
}
func (r feedbackRow) apply(scan *Scan) {
	scan.ReportStatus = "legacy"
	if !r.version.Valid {
		return
	}
	scan.ReportStatus = r.state.String
	if scan.ReportStatus == "" || (scan.Status != "running" && scan.ReportStatus == "pending") {
		scan.ReportStatus = "unavailable"
	}
	if !r.phase.Valid || !r.updated.Valid {
		return
	}
	p := &ScanProgress{Version: 1, Phase: r.phase.String, PercentBP: r.bp.Int64, Completed: r.completed.Int64, Total: r.total.Int64,
		FilesCounted: r.counted.Int64, FilesProcessed: r.processed.Int64, CountComplete: r.complete.Bool, FilesChanged: r.changed.Bool, UpdatedAt: r.updated.Time}
	if r.filesTotal.Valid {
		p.FilesTotal = &r.filesTotal.Int64
	}
	switch p.Phase {
	case "counting", "organizing":
		p.Unit = "roots"
	case "reading_tags":
		p.Unit = "files"
	case "saving":
		p.Unit = "scopes"
	}
	root := &RootProgress{Total: int(r.roots.Int64)}
	if r.index.Valid {
		v := int(r.index.Int64)
		root.Index = &v
	}
	if r.id.Valid {
		root.ID = &r.id.String
	}
	if r.name.Valid {
		root.Name = &r.name.String
	}
	if scan.Status == "succeeded" {
		p.Phase = "saving"
		p.Unit = "scopes"
		p.PercentBP = 10000
		root.Index = nil
		root.ID = nil
		root.Name = nil
		if scan.FinishedAt != nil {
			p.UpdatedAt = *scan.FinishedAt
		}
	}
	scan.Progress = p
	scan.RootProgress = root
}
