// Package library 拥有目录与扫描能力：允许根内路径校验、只读来源发现、扫描运行与取消、把观察交给 catalog 暂存、
// 诊断、中断恢复与仅在完整成功后才执行的 missing 对账。它不含 HTTP，也从不写音乐库。
package library

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/artwork"
	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/library/parser"
)

type registeredRoot struct {
	ID, Path   string
	Exclusions []string
	filter     scanFilter
}

type scanExecutor interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type uncancelableScanExecutor struct{ scanExecutor }

func (executor uncancelableScanExecutor) BeginTx(ctx context.Context, options *sql.TxOptions) (*sql.Tx, error) {
	return executor.scanExecutor.BeginTx(context.WithoutCancel(ctx), options)
}

func (executor uncancelableScanExecutor) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return executor.scanExecutor.ExecContext(context.WithoutCancel(ctx), query, args...)
}

func (executor uncancelableScanExecutor) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return executor.scanExecutor.QueryContext(context.WithoutCancel(ctx), query, args...)
}

func (executor uncancelableScanExecutor) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return executor.scanExecutor.QueryRowContext(context.WithoutCancel(ctx), query, args...)
}

type trackSourceIdentity struct {
	RootID       string
	RelativePath string
}

func createTrackSourceIdentity(rootID, relativePath string) (trackSourceIdentity, error) {
	rawPath := strings.ReplaceAll(strings.TrimSpace(relativePath), "\\", "/")
	cleanedPath := pathpkg.Clean(rawPath)
	if rootID == "" || cleanedPath == "." || strings.IndexByte(rawPath, 0) >= 0 || strings.HasPrefix(rawPath, "/") || strings.HasPrefix(rawPath, "//") || len(rawPath) >= 2 && rawPath[1] == ':' || cleanedPath == ".." || strings.HasPrefix(cleanedPath, "../") {
		return trackSourceIdentity{}, fmt.Errorf("invalid source relative path")
	}
	return trackSourceIdentity{RootID: rootID, RelativePath: cleanedPath}, nil
}

type scanOutcome struct {
	Complete bool
	Canceled bool
	Failed   bool
}

var errArtworkSource = errors.New("artwork source failure")
var errArtworkBinding = errors.New("artwork binding failure")
var errStagingCleanup = errors.New("staging cleanup failure")

func terminalScanStatus(outcome scanOutcome) string {
	if outcome.Canceled {
		return "canceled"
	}
	if outcome.Failed {
		return "failed"
	}
	if !outcome.Complete {
		return "incomplete"
	}
	return "succeeded"
}

func scanStatusAllowsMissingReconciliation(status string) bool {
	return status == "succeeded"
}

func (service *Service) runScan(scanID string, execution *scanExecution) {
	scanContext, cancel := context.WithCancel(service.scanContext)
	pollDone := make(chan struct{})
	stopReason := make(chan error, 1)
	go service.pollScanCancellation(scanContext, scanID, cancel, pollDone, stopReason)
	defer func() {
		cancel()
		<-pollDone
		if err := execution.close(context.Background()); err != nil {
			service.logger.Error("scan coordination release failed", "event", "library.scan.coordination_failed", "scan_run_id", scanID, "error", err)
		}
		service.scanMutex.Lock()
		if service.runningScan == scanID {
			service.runningScan = ""
		}
		service.scanMutex.Unlock()
	}()
	executor := uncancelableScanExecutor{scanExecutor: execution.connection}
	roots, loadError := service.loadRegisteredRoots(scanContext, executor)
	outcome := scanOutcome{Complete: loadError == nil, Failed: loadError != nil}
	if loadError != nil {
		roots = nil
	}
	feedback := newScanFeedback(service, executor, scanID, roots)
	if loadError == nil && !service.countScanRoots(scanContext, executor, scanID, roots, feedback) {
		outcome.Complete = false
	}
	rootIDs := make([]string, 0, len(roots))
	for index, root := range roots {
		if scanContext.Err() != nil {
			outcome.Complete = false
			break
		}
		rootIDs = append(rootIDs, root.ID)
		feedback.selectRoot(index, root)
		feedback.weight = feedback.counts[index].files
		if feedback.progress.FilesTotal != nil && *feedback.progress.FilesTotal == 0 {
			feedback.weight = 1
		}
		feedback.phase(scanContext, "reading_tags", feedback.counts[index].files)
		if rootError := service.scanRoot(scanContext, executor, scanID, root, feedback); rootError != nil {
			outcome.Complete = false
			service.logger.Error("scan root incomplete", "event", "library.scan.root_incomplete", "scan_run_id", scanID, "root_id", root.ID, "error", rootError)
			if errors.Is(rootError, errStagingCleanup) {
				break
			}
		}
		if scanContext.Err() == nil {
			feedback.fraction(1, 1)
			feedback.base += feedback.weight
			feedback.flush(scanContext, true)
		}
	}
	if scanContext.Err() != nil {
		outcome.Complete = false
		outcome.Failed = false
		select {
		case err := <-stopReason:
			if errors.Is(err, errScanCancellationRequested) {
				outcome.Canceled = true
			} else {
				service.logger.Error("scan cancellation polling failed", "event", "library.scan.coordination_failed", "scan_run_id", scanID, "error", err)
			}
		default:
			// 应用停机只表示本次扫描未完成；只有观察到持久化取消请求才写入 canceled。
		}
	}
	if loadError != nil {
		if diagnosticError := service.recordDiagnosticWith(context.Background(), execution.connection, scanID, "", "database_error", "无法读取已注册目录"); diagnosticError != nil {
			outcome.Failed = true
		}
	}
	finalizeContext, finalizeCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer finalizeCancel()
	if scanContext.Err() == nil && outcome.Complete {
		feedback.progress.Phase = "saving"
		feedback.progress.Unit = "scopes"
		feedback.progress.PercentBP = 9900
		feedback.root.Index = nil
		feedback.root.ID = nil
		feedback.root.Name = nil
	}
	feedback.flush(finalizeContext, true)
	status, updateError := service.finalizeScan(finalizeContext, execution.connection, scanID, rootIDs, outcome, feedback)
	if updateError != nil {
		service.logger.Error("scan terminal status persistence failed", "event", "library.scan.terminal_write_failed", "scan_id", scanID, "error", updateError)
		return
	}
	service.logger.Info("scan completed", "event", "library.scan.completed", "scan_run_id", scanID, "outcome", status)
}

var errScanCancellationRequested = errors.New("scan cancellation requested")

func (service *Service) pollScanCancellation(ctx context.Context, scanID string, cancel context.CancelFunc, done chan<- struct{}, stopReason chan<- error) {
	defer close(done)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var requested bool
			err := service.database.QueryRowContext(ctx, "SELECT cancel_requested_at IS NOT NULL FROM scan_runs WHERE id=$1::uuid AND status='running'", scanID).Scan(&requested)
			if errors.Is(err, sql.ErrNoRows) {
				stopReason <- errors.New("active scan row disappeared")
				cancel()
				return
			}
			if err != nil {
				stopReason <- err
				cancel()
				return
			}
			if requested {
				stopReason <- errScanCancellationRequested
				cancel()
				return
			}
		}
	}
}

func (service *Service) finalizeScan(ctx context.Context, connection *sql.Conn, scanID string, rootIDs []string, outcome scanOutcome, feedbacks ...*scanFeedback) (string, error) {
	transaction, err := connection.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer transaction.Rollback()
	if err := ingest.LockProjectionWrites(ctx, transaction); err != nil {
		return "", err
	}
	var existingStatus string
	var cancelRequestedAt sql.NullTime
	if err := transaction.QueryRowContext(ctx, "SELECT status,cancel_requested_at FROM scan_runs WHERE id=$1::uuid FOR UPDATE", scanID).Scan(&existingStatus, &cancelRequestedAt); err != nil {
		return "", err
	}
	if existingStatus != "running" {
		return existingStatus, transaction.Commit()
	}
	status := terminalScanStatus(outcome)
	if cancelRequestedAt.Valid {
		status = "canceled"
	}
	feedback := feedbackArg(feedbacks)
	if scanStatusAllowsMissingReconciliation(status) {
		var changes *ingest.ScanFeedback
		if feedback != nil {
			changes = feedback.changes
		}
		if err := ingest.MarkMissingSourcesWithFeedback(ctx, transaction, scanID, rootIDs, changes); err != nil {
			return "", err
		}
	}
	var errorMessage any
	if status == "failed" {
		errorMessage = "scan_failed"
	} else if status == "incomplete" {
		errorMessage = "scan_incomplete"
	}
	if err := finalizeScanFeedback(ctx, transaction, scanID, status, errorMessage, feedback); err != nil {
		return "", err
	}
	if err := transaction.Commit(); err != nil {
		return "", err
	}
	return status, nil
}

func (service *Service) loadRegisteredRoots(context context.Context, executor scanExecutor) ([]registeredRoot, error) {
	rows, err := executor.QueryContext(context, `SELECT r.id::text,r.path,to_json(COALESCE(array_agg(e.relative_path ORDER BY e.relative_path COLLATE "C") FILTER (WHERE e.relative_path IS NOT NULL),ARRAY[]::text[]))
 FROM library_roots r LEFT JOIN library_root_exclusions e ON e.root_id=r.id
 WHERE r.status='active' GROUP BY r.id ORDER BY r.path,r.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	roots := []registeredRoot{}
	for rows.Next() {
		var root registeredRoot
		var exclusions []byte
		if err := rows.Scan(&root.ID, &root.Path, &exclusions); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(exclusions, &root.Exclusions); err != nil {
			return nil, err
		}
		root.filter = newScanFilter(root.Exclusions)
		roots = append(roots, root)
	}
	return roots, rows.Err()
}

func (service *Service) scanRoot(ctx context.Context, executor scanExecutor, scanID string, root registeredRoot, feedbacks ...*scanFeedback) (returnError error) {
	feedback := feedbackArg(feedbacks)
	// 遍历前必须清理同一扫描重试留下的旧行，否则旧 observation 会混入本次候选。
	if err := ingest.ClearStagedObservations(ctx, executor, scanID, root.ID); err != nil {
		cleanupError := fmt.Errorf("%w: %w", errStagingCleanup, err)
		if diagnosticError := service.recordDiagnosticWith(ctx, executor, scanID, "", "staging_write_failure", "无法清理扫描暂存观察"); diagnosticError != nil {
			cleanupError = errors.Join(cleanupError, fmt.Errorf("record staging cleanup diagnostic: %w", diagnosticError))
		}
		return cleanupError
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cleanupError := ingest.ClearStagedObservations(cleanupCtx, executor, scanID, root.ID)
		if cleanupError == nil {
			return
		}
		cleanupError = fmt.Errorf("%w: %w", errStagingCleanup, cleanupError)
		if diagnosticError := service.recordDiagnosticWith(cleanupCtx, executor, scanID, "", "staging_write_failure", "无法清理扫描暂存观察"); diagnosticError != nil {
			cleanupError = errors.Join(cleanupError, fmt.Errorf("record staging cleanup diagnostic: %w", diagnosticError))
		}
		returnError = errors.Join(returnError, cleanupError)
	}()
	if !registeredRootAvailable(root.Path) {
		if diagnosticError := service.recordDiagnosticWith(ctx, executor, scanID, "", "root_unavailable", "注册目录当前不可用"); diagnosticError != nil {
			return fmt.Errorf("record unavailable root diagnostic: %w", diagnosticError)
		}
		return errors.New("root unavailable")
	}
	complete := feedback == nil || feedback.counts[feedback.current].complete
	var processingError error
	processingErrorCount := 0
	captureProcessingError := func(err error) {
		if err != nil && processingErrorCount < 8 {
			processingError = errors.Join(processingError, err)
			processingErrorCount++
		}
	}
	filter := root.filter
	if filter.exclusions == nil {
		filter = newScanFilter(root.Exclusions)
	}
	hold := func(kind, relative string) error {
		// 物理曲目入库使用规范化相对路径；sheet 路径仍按原始值匹配。
		if kind == ingest.HeldFile {
			relative = ingest.NormalizedPath(relative)
		}
		if err := ingest.HoldSource(ctx, executor, scanID, root.ID, kind, relative); err != nil {
			return errors.Join(err, service.recordDiagnosticWith(ctx, executor, scanID, relative, "staging_write_failure", "无法登记保住的来源"))
		}
		if feedback != nil {
			feedback.skip(ctx)
		}
		return nil
	}
	diagnoseHeld := func(relative, kind, message, heldKind string) error {
		if err := service.recordDiagnosticWith(ctx, executor, scanID, relative, kind, message); err != nil {
			return err
		}
		return hold(heldKind, relative)
	}
	var seen int64
	var walkErrorSeen, excludedHit bool
	err := filepath.WalkDir(root.Path, func(path string, entry fs.DirEntry, walkError error) error {
		if err := ctx.Err(); err != nil {
			complete = false
			return err
		}
		relative := safeRelativePath(root.Path, path)
		if walkError != nil {
			if path == root.Path {
				complete = false
				return errors.Join(errors.New("root unavailable during traversal"), service.recordDiagnosticWith(ctx, executor, scanID, "", "root_unavailable", "注册目录当前不可用"))
			}
			walkErrorSeen = true
			if entry != nil && entry.IsDir() {
				if err := diagnoseHeld(relative, "walk_error", "文件夹读不了", ingest.HeldDirectory); err != nil {
					return err
				}
				return nil
			}
			if err := diagnoseHeld(relative, "permission_error", "无法读取文件", ingest.HeldFile); err != nil {
				return err
			}
			return nil
		}
		if entry.IsDir() {
			// 注册根自身不参与过滤。
			if relative == "" {
				return nil
			}
			if filter.skipDir(relative, entry.Name()) {
				if _, excluded := filter.exclusions[relative]; excluded {
					excludedHit = true
				}
				return fs.SkipDir
			}
			return nil
		}
		if filter.skipFile(entry.Name()) {
			return nil
		}
		extension := strings.ToLower(filepath.Ext(path))
		if extension != ".cue" && !isSupportedAudioExtension(extension) && !isAudioCandidateExtension(extension) {
			return nil
		}
		seen++
		defer feedback.processed(ctx)
		heldKind := ingest.HeldFile
		if extension == ".cue" {
			heldKind = ingest.HeldCueSheet
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, targetError := filepath.EvalSymlinks(path)
			if targetError != nil || !isWithin(root.Path, target) {
				return diagnoseHeld(relative, "broken_or_escaped_link", "文件链接目标不可安全读取", heldKind)
			}
		}
		fileInfo, statError := os.Stat(path)
		if statError != nil {
			return diagnoseHeld(relative, "permission_error", "无法读取文件", heldKind)
		}
		if !fileInfo.Mode().IsRegular() {
			return diagnoseHeld(relative, "non_regular_file", "音频来源不是普通文件", heldKind)
		}
		if extension == ".cue" {
			document, cueErr := parser.ParseCueDocument(path, root.Path)
			if cueErr != nil {
				return diagnoseHeld(relative, "unsupported_cue", "CUE 文件无法解析", ingest.HeldCueSheet)
			}
			for _, diagnostic := range document.Diagnostics {
				if err := service.recordDiagnosticWith(ctx, executor, scanID, relative, "cue_reference", diagnostic); err != nil {
					return err
				}
			}
			parentFacts := make(map[string]parser.AudioObservation, len(document.Files))
			parentInfos := make(map[string]fs.FileInfo, len(document.Files))
			cueFailed := false
			for _, reference := range document.Files {
				if cueReferenceNeedsHold(reference.Status) {
					cueFailed = true
				}
				if reference.Status != "present" {
					continue
				}
				if reference.ResolvedPath == "" || !isWithin(root.Path, reference.ResolvedPath) {
					cueFailed = true
					if err := service.recordDiagnosticWith(ctx, executor, scanID, relative, "cue_reference", "CUE 引用无法安全读取"); err != nil {
						return err
					}
					continue
				}
				parentInfo, parentStatError := os.Stat(reference.ResolvedPath)
				if parentStatError != nil {
					cueFailed = true
					if err := service.recordDiagnosticWith(ctx, executor, scanID, relative, "permission_error", "无法读取 CUE 引用音频"); err != nil {
						return err
					}
					continue
				}
				if !parentInfo.Mode().IsRegular() {
					cueFailed = true
					if err := service.recordDiagnosticWith(ctx, executor, scanID, relative, "cue_reference", "CUE 引用不是普通文件"); err != nil {
						return err
					}
					continue
				}
				facts, parseErr := parser.ParseAudioFile(reference.ResolvedPath)
				if parseErr != nil {
					cueFailed = true
					if err := service.recordDiagnosticWith(ctx, executor, scanID, relative, "parse_failure", "CUE 引用音频解析失败"); err != nil {
						return err
					}
					continue
				}
				parentFacts[reference.Path], parentInfos[reference.Path] = facts, parentInfo
			}
			if cueFailed {
				return hold(ingest.HeldCueSheet, relative)
			}
			for _, cue := range document.Tracks {
				if err := ctx.Err(); err != nil {
					return err
				}
				if !cueTrackCanBeStaged(cue) {
					continue
				}
				o, ok := parentFacts[cue.ReferencedFile]
				if !ok {
					continue
				}
				virtualObservation := buildCueVirtualObservation(o, document, cue, relative)
				virtualObservation.SourceSize, virtualObservation.SourceModifiedAt = parentInfos[cue.ReferencedFile].Size(), parentInfos[cue.ReferencedFile].ModTime()
				if err := stageObservation(ctx, executor, scanID, root.ID, virtualObservation); err != nil {
					complete = false
					captureProcessingError(err)
					if diagnosticError := service.recordDiagnosticWith(ctx, executor, scanID, relative, "staging_write_failure", "无法暂存 CUE 观察"); diagnosticError != nil {
						return diagnosticError
					}
				}
			}
			return nil
		}
		if !isSupportedAudioExtension(extension) {
			return diagnoseHeld(relative, "unsupported_format", "不支持的音频格式", ingest.HeldFile)
		}
		observation, parseError := parser.ParseAudioFile(path)
		if parseError != nil {
			return diagnoseHeld(relative, "parse_failure", "音频文件解析失败", ingest.HeldFile)
		}
		physicalObservation := buildPhysicalSourceObservation(observation, relative)
		physicalObservation.SourceSize, physicalObservation.SourceModifiedAt = fileInfo.Size(), fileInfo.ModTime()
		if physicalObservation.Directory == "" && physicalObservation.InferredFields["album"] {
			physicalObservation.Album = ""
		}
		if err := stageObservation(ctx, executor, scanID, root.ID, physicalObservation); err != nil {
			complete = false
			captureProcessingError(err)
			if diagnosticError := service.recordDiagnosticWith(ctx, executor, scanID, relative, "staging_write_failure", "无法暂存音频观察"); diagnosticError != nil {
				return diagnosticError
			}
		}
		return ctx.Err()
	})
	if err != nil {
		complete = false
		captureProcessingError(err)
	}
	if ctx.Err() == nil && err == nil {
		if feedback != nil {
			if feedback.progress.Completed != feedback.counts[feedback.current].files {
				feedback.progress.FilesChanged = true
			}
			feedback.phase(ctx, "organizing", 1)
		}
		if !registeredRootAvailable(root.Path) {
			return errors.Join(errors.New("root unavailable before organizing"), service.recordDiagnosticWith(ctx, executor, scanID, "", "root_unavailable", "注册目录当前不可用"))
		}
		if seen == 0 && !walkErrorSeen && !excludedHit {
			present, err := ingest.RootHasPresentTracks(ctx, executor, root.ID)
			if err != nil {
				return errors.Join(err, service.recordDiagnosticWith(ctx, executor, scanID, "", "database_error", "无法读取目录来源"))
			}
			if present {
				return errors.Join(errors.New("empty root"), service.recordDiagnosticWith(ctx, executor, scanID, "", "root_empty", "目录是空的，可能还没挂载"))
			}
		}
		if alignErr := ingest.AlignCueStagingScopes(ctx, executor, scanID, root.ID); alignErr != nil {
			complete = false
			captureProcessingError(alignErr)
			if diagnosticError := service.recordDiagnosticWith(ctx, executor, scanID, "", "staging_write_failure", "无法对齐 CUE 父来源"); diagnosticError != nil {
				captureProcessingError(diagnosticError)
			}
		}
		if markErr := ingest.MarkDiscParentScopes(ctx, executor, scanID, root.ID); markErr != nil {
			complete = false
			captureProcessingError(markErr)
			if diagnosticError := service.recordDiagnosticWith(ctx, executor, scanID, "", "staging_write_failure", "无法标记碟父目录独占范围"); diagnosticError != nil {
				captureProcessingError(diagnosticError)
			}
		}
		// 先规划曲目身份（遍历已经结束，complete 即遍历证据是否完整）；规划失败时暂存行没有完整的规划，不能写入。
		planned, planProblem := service.planRootIdentities(ctx, executor, scanID, root, complete)
		if planProblem != nil {
			complete = false
			captureProcessingError(planProblem)
		}
		// 读取失败的来源已登记保住；有效候选仍按原规划写入，基础设施失败仍禁止负向对账。
		if feedback != nil && ctx.Err() == nil {
			feedback.progress.Completed = 1
			feedback.fraction(4, 5)
			feedback.flush(ctx, true)
		}
		if planned {
			if !registeredRootAvailable(root.Path) {
				return errors.Join(errors.New("root unavailable before saving"), service.recordDiagnosticWith(ctx, executor, scanID, "", "root_unavailable", "注册目录当前不可用"))
			}
			if feedback != nil {
				total, countErr := ingest.CountPlannedScopes(ctx, executor, scanID, root.ID)
				if countErr != nil {
					// scope 总数仅服务进度；未知时冻结快照，不能跳过目录保存或污染报告健康。
					feedback.progressUnavailable = true
					if service.logger != nil {
						service.logger.Warn("扫描保存进度总数读取失败", "event", "library.scan.progress_count_failed", "scan_run_id", scanID)
					}
				} else {
					feedback.phase(ctx, "saving", total)
				}
			}
			if evidenceErr := service.persistStagedCandidates(ctx, executor, scanID, root, feedback); evidenceErr != nil {
				complete = false
				captureProcessingError(evidenceErr)
				kind, message := candidatePersistenceDiagnostic(evidenceErr)
				if diagnosticError := service.recordDiagnosticWith(ctx, executor, scanID, "", kind, message); diagnosticError != nil {
					captureProcessingError(diagnosticError)
				}
			}
			if ctx.Err() == nil {
				if _, err := ingest.TouchHeldSources(ctx, executor, scanID, root.ID); err != nil {
					complete = false
					captureProcessingError(err)
					captureProcessingError(service.recordDiagnosticWith(ctx, executor, scanID, "", "catalog_write_failure", "无法更新保住来源的观察时间"))
				}
			}
			if ctx.Err() == nil {
				if transferErr := service.transferEmptiedAlbumFavorites(ctx, executor, scanID, root.ID); transferErr != nil && service.logger != nil {
					service.logger.Warn("扫描专辑收藏转移失败", "event", "library.scan.favorite_transfer_failed", "module", "library", "scan_run_id", scanID)
				}
			}
		}
	}
	if ctx.Err() != nil {
		complete = false
		captureProcessingError(ctx.Err())
	}
	if !complete {
		return errors.Join(errors.New("incomplete root"), processingError)
	}
	return nil
}

// CUE 父文件缺失或没有 INDEX 是确定且不阻断其它来源的观察：跳过不可用虚拟轨，
// unsafe/unchecked 则保住该 sheet 的已有虚拟轨，但不阻断其它来源对账。
func cueReferenceNeedsHold(status string) bool {
	return status != "present" && status != "missing"
}

func cueTrackCanBeStaged(track parser.CueTrack) bool {
	return track.ReferenceStatus == "present" && track.IndexPresent
}

func buildPhysicalSourceObservation(observation parser.AudioObservation, relativePath string) ingest.SourceObservation {
	return ingest.SourceObservation{RelativePath: relativePath, Directory: ingest.DirectoryOf(relativePath), Title: observation.Title, Album: observation.Album, AlbumArtist: observation.AlbumArtist, Artist: observation.Artist, SourceType: observation.SourceType, MediaType: observation.MediaType, Genres: append([]string(nil), observation.Genres...), CatalogNumber: observation.Catalog, Edition: observation.Edition, Label: observation.Label, Barcode: observation.Barcode, Year: observation.Year, TrackNumber: observation.TrackNumber, DiscNumber: observation.DiscNumber, SourceKind: observation.SourceKind, InferredFields: observation.InferredFields, FieldSources: observationFieldSources(observation), DurationSeconds: observation.DurationSeconds, Codec: observation.Codec, BitDepth: observation.BitDepth, SampleRate: observation.SampleRate, Channels: observation.Channels, Bitrate: observation.Bitrate, Artwork: observation.Artwork, ArtworkMIME: observation.ArtworkMIME, Credits: append([]metadata.Credit(nil), observation.Credits...), ArtistTagEvidence: append([]metadata.ArtistTagEvidence(nil), observation.ArtistTagEvidence...), Lyrics: observation.Lyrics, LyricsSource: observation.LyricsSource, ContentFingerprint: observation.ContentFingerprint}
}

func buildCueVirtualObservation(parent parser.AudioObservation, document parser.CueDocument, cue parser.CueTrack, sheetRelativePath string) ingest.SourceObservation {
	observation := parent
	observation.InferredFields = cloneBoolMap(parent.InferredFields)
	observation.SourceKind = "cue_virtual"
	observation.TrackNumber = cue.Number
	fieldSources := observationFieldSources(observation)
	if sheetTitle := firstNonEmpty(cue.SheetTitle, document.Title); sheetTitle != "" {
		observation.Album = sheetTitle
		delete(observation.InferredFields, "album")
		fieldSources["album"] = "cue_sheet"
	}
	if sheetArtist := firstNonEmpty(cue.SheetArtist, document.Artist); sheetArtist != "" {
		observation.AlbumArtist = sheetArtist
		fieldSources["album_artist"] = "cue_sheet"
	}
	genres := metadata.SplitGenres(cue.Genre)
	if len(genres) == 0 {
		genres = metadata.SplitGenres(document.Genre)
	}
	if len(genres) > 0 {
		observation.Genres = genres
		fieldSources["genre"] = "cue_sheet"
	}
	observation.Catalog = firstNonEmpty(cue.Catalog, document.Catalog, observation.Catalog)
	observation.ISRC = cue.ISRC
	if firstNonEmpty(cue.Catalog, document.Catalog) != "" {
		fieldSources["catalog_number"] = "cue_sheet"
	}
	if cue.ISRC != "" {
		fieldSources["isrc"] = "cue_track"
	}
	if parsedYear := parser.ParseYear(firstNonEmpty(cue.Date, document.Date)); parsedYear > 0 {
		observation.Year = parsedYear
		fieldSources["year"] = "cue_sheet"
	}
	if cue.DurationSeconds > 0 {
		observation.DurationSeconds = cue.DurationSeconds
	}
	if cue.Title != "" {
		observation.Title = cue.Title
		delete(observation.InferredFields, "title")
		fieldSources["title"] = "cue_track"
	}
	if cueArtist := firstNonEmpty(cue.Artist, cue.SheetArtist, document.Artist); cueArtist != "" {
		observation.Artist = cueArtist
		delete(observation.InferredFields, "artist")
		fieldSources["artist"] = "cue_sheet"
		if cue.PerformerPresent {
			fieldSources["artist"] = "cue_track"
		}
	}
	delete(observation.InferredFields, "track_number")
	fieldSources["track_number"] = "cue_track"

	referencedIdentity := ingest.NormalizedPath(cue.ReferencedFile)
	parentRelative := ingest.NormalizedPath(filepath.ToSlash(filepath.Join(filepath.Dir(sheetRelativePath), referencedIdentity)))
	virtualPath := sheetRelativePath + "#track-" + strconv.Itoa(cue.Number) + "@" + parentRelative + "#index-" + strconv.Itoa(cue.IndexFrames)
	return ingest.SourceObservation{
		RelativePath:          virtualPath,
		Directory:             ingest.DirectoryOf(sheetRelativePath),
		Title:                 observation.Title,
		Album:                 observation.Album,
		AlbumArtist:           observation.AlbumArtist,
		Artist:                observation.Artist,
		SourceType:            observation.SourceType,
		MediaType:             observation.MediaType,
		Genres:                append([]string(nil), observation.Genres...),
		CatalogNumber:         observation.Catalog,
		Edition:               observation.Edition,
		Label:                 observation.Label,
		Barcode:               observation.Barcode,
		Year:                  observation.Year,
		TrackNumber:           observation.TrackNumber,
		DiscNumber:            observation.DiscNumber,
		SourceKind:            observation.SourceKind,
		InferredFields:        observation.InferredFields,
		FieldSources:          fieldSources,
		DurationSeconds:       observation.DurationSeconds,
		Codec:                 observation.Codec,
		BitDepth:              observation.BitDepth,
		SampleRate:            observation.SampleRate,
		Channels:              observation.Channels,
		Bitrate:               observation.Bitrate,
		CueSheetPath:          sheetRelativePath,
		CueParentRelativePath: parentRelative,
		CueReferencedFile:     parentRelative,
		CueIndexFrames:        cue.IndexFrames,
		CueIndexPresent:       cue.IndexPresent,
		CueEndFrames:          cue.EndFrames,
		CueEndPresent:         cue.EndPresent,
		CueISRC:               cue.ISRC,
		ContentFingerprint:    parent.ContentFingerprint,
		ArtistTagEvidence:     cueArtistTagEvidence(parent, document, cue, fieldSources),
	}
}

// cueArtistTagEvidence 只把实际用于虚拟轨署名的来源带入；被 CUE 覆盖的父标签不冒充本轨证据。
func cueArtistTagEvidence(parent parser.AudioObservation, document parser.CueDocument, cue parser.CueTrack, sources map[string]string) []metadata.ArtistTagEvidence {
	var evidence []metadata.ArtistTagEvidence
	for _, entry := range parent.ArtistTagEvidence {
		if entry.Role == "track_artist" && sources["artist"] != "cue_track" && sources["artist"] != "cue_sheet" ||
			entry.Role == "album_artist" && sources["album_artist"] != "cue_sheet" {
			evidence = append(evidence, entry)
		}
	}
	evidence = append(evidence, document.ArtistTagEvidence...)
	evidence = append(evidence, cue.ArtistTagEvidence...)
	if sources["artist"] == "cue_sheet" {
		for _, entry := range document.ArtistTagEvidence {
			entry.Role = "track_artist"
			evidence = append(evidence, entry)
		}
	}
	return evidence
}

func registeredRootAvailable(root string) bool {
	absolute, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return false
	}
	info, err := os.Lstat(absolute)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false
	}
	real, err := filepath.EvalSymlinks(absolute)
	return err == nil && filepath.Clean(real) == absolute
}

func candidatePersistenceDiagnostic(err error) (string, string) {
	if errors.Is(err, errArtworkBinding) {
		return "artwork_failure", "封面处理失败"
	}
	return "catalog_write_failure", "无法保存整理证据"
}

const (
	maxRipLogHeaderBytes = 64 << 10
	maxRipLogDiagnostics = 20
)

// stageObservation 先校验并规范化观察里的相对路径与 CUE 引用（路径安全属于 library），再交给 catalog 写入暂存表。
func stageObservation(ctx context.Context, executor scanExecutor, scanID, rootID string, observation ingest.SourceObservation) error {
	if err := metadata.ValidateArtistTagEvidence(observation.ArtistTagEvidence); err != nil {
		return err
	}
	identity, err := createTrackSourceIdentity(rootID, observation.RelativePath)
	if err != nil {
		return err
	}
	observation.RelativePath = identity.RelativePath
	for _, reference := range []string{observation.CueSheetPath, observation.CueParentRelativePath} {
		if reference == "" {
			continue
		}
		if _, err := createTrackSourceIdentity(rootID, reference); err != nil {
			return fmt.Errorf("invalid staged CUE reference: %w", err)
		}
	}
	return ingest.StageObservation(ctx, executor, scanID, rootID, observation)
}

func (service *Service) persistStagedCandidates(ctx context.Context, executor scanExecutor, scanID string, root registeredRoot, feedbacks ...*scanFeedback) error {
	feedback := feedbackArg(feedbacks)
	var firstErr error
	lastScope := ""
	for {
		scopePage := ingest.StagedScopes
		if feedback != nil {
			scopePage = ingest.PlannedStagedScopes
		}
		scopes, err := scopePage(ctx, executor, scanID, root.ID, lastScope)
		if err != nil {
			return err
		}
		if len(scopes) == 0 {
			break
		}
		for _, scope := range scopes {
			if err := ctx.Err(); err != nil {
				return err
			}
			observations, loadErr := ingest.LoadStagedScope(ctx, executor, scanID, root.ID, scope)
			if loadErr != nil {
				if firstErr == nil {
					firstErr = loadErr
				}
				feedback.scopeDone(ctx)
				continue
			}
			candidates := ingest.OrganizeObservations(observations)
			versionBatches := map[string][]ingest.OrganizedCandidate{}
			for _, candidate := range candidates {
				if candidate.VersionGroup != "" {
					versionBatches[candidate.VersionGroup] = append(versionBatches[candidate.VersionGroup], candidate)
				}
			}
			groupIDs := map[string]string{}
			groupErrors := map[string]error{}
			for _, candidate := range candidates {
				versionGroup := candidate.VersionGroup
				if versionGroup != "" {
					if _, planned := groupIDs[versionGroup]; !planned && groupErrors[versionGroup] == nil {
						groupIDs[versionGroup], groupErrors[versionGroup] = ingest.PlannedVersionGroup(ctx, executor, scanID, root.ID, versionBatches[versionGroup])
					}
					if groupErr := groupErrors[versionGroup]; groupErr != nil {
						if firstErr == nil {
							firstErr = groupErr
						}
						continue
					}
					candidate.JoinGroupID = groupIDs[versionGroup]
				}
				ripLogRefs, diagnostics := discoverCandidateRipLogEvidence(root, candidate)
				ingest.ApplyRipLogEvidence(&candidate, ripLogRefs)
				for _, diagnostic := range diagnostics {
					if diagnosticErr := service.recordDiagnosticWith(ctx, executor, scanID, diagnostic.RelativePath, diagnostic.Kind, diagnostic.Message); diagnosticErr != nil && firstErr == nil {
						firstErr = diagnosticErr
					}
				}
				releaseID, persistErr := service.persistOneCandidate(ctx, executor, scanID, root, candidate, feedback)
				if persistErr != nil && firstErr == nil {
					firstErr = persistErr
				}
				// 封面失败不撤销已提交的发行，后续版本仍须加入它的组。
				if versionGroup != "" && groupIDs[versionGroup] == "" && releaseID != "" {
					groupIDs[versionGroup], groupErrors[versionGroup] = ingest.ReleaseGroupOf(ctx, executor, releaseID)
					if groupErr := groupErrors[versionGroup]; groupErr != nil && firstErr == nil {
						firstErr = groupErr
					}
				}
			}
			feedback.scopeDone(ctx)
		}
		lastScope = scopes[len(scopes)-1]
	}
	return firstErr
}

type ripLogDiagnostic struct {
	RelativePath string
	Kind         string
	Message      string
}

func discoverCandidateRipLogEvidence(root registeredRoot, candidate ingest.OrganizedCandidate) ([]string, []ripLogDiagnostic) {
	directories := candidateSourceDirectories(candidate)
	references := make([]string, 0)
	diagnostics := make([]ripLogDiagnostic, 0)
	appendDiagnostic := func(relativePath, kind, message string) {
		if len(diagnostics) < maxRipLogDiagnostics {
			diagnostics = append(diagnostics, ripLogDiagnostic{RelativePath: relativePath, Kind: kind, Message: message})
		}
	}
	for _, directory := range directories {
		fullDirectory := root.Path
		if directory != "" {
			identity, err := createTrackSourceIdentity(root.ID, directory+"/.roomusic-directory")
			if err != nil {
				continue
			}
			fullDirectory = filepath.Join(root.Path, filepath.Dir(filepath.FromSlash(identity.RelativePath)))
		}
		info, err := os.Lstat(fullDirectory)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !isWithin(root.Path, fullDirectory) {
			continue
		}
		entries, err := os.ReadDir(fullDirectory)
		if err != nil {
			appendDiagnostic(directory, "rip_log_unreadable", "无法读取抓轨日志所在目录")
			continue
		}
		for _, entry := range entries {
			if len(references) == ingest.MaxGroupingEvidenceRefs {
				break
			}
			if !strings.EqualFold(filepath.Ext(entry.Name()), ".log") || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			relativePath := ingest.NormalizedPath(filepath.ToSlash(filepath.Join(directory, entry.Name())))
			if _, err := createTrackSourceIdentity(root.ID, relativePath); err != nil {
				continue
			}
			logPath := filepath.Join(fullDirectory, entry.Name())
			logInfo, err := os.Lstat(logPath)
			if err != nil {
				appendDiagnostic(relativePath, "rip_log_unreadable", "无法读取抓轨日志")
				continue
			}
			if logInfo.Mode()&os.ModeSymlink != 0 || !logInfo.Mode().IsRegular() || !isWithin(root.Path, logPath) {
				continue
			}
			matched, err := hasExplicitRipLogSignature(logPath)
			if err != nil {
				appendDiagnostic(relativePath, "rip_log_unreadable", "无法读取抓轨日志")
				continue
			}
			if matched {
				references = append(references, relativePath)
			}
		}
	}
	return references, diagnostics
}

func candidateSourceDirectories(candidate ingest.OrganizedCandidate) []string {
	seen := map[string]bool{}
	directories := make([]string, 0)
	add := func(value string) {
		value = ingest.NormalizedPath(value)
		if seen[value] {
			return
		}
		seen[value] = true
		directories = append(directories, value)
	}
	add(candidate.Anchor.Scope)
	for _, tracks := range candidate.Mediums {
		for _, track := range tracks {
			directory := track.Observation.Directory
			if directory == "" {
				directory = ingest.DirectoryOf(track.Observation.RelativePath)
			}
			add(directory)
		}
	}
	sort.Strings(directories)
	return directories
}

func hasExplicitRipLogSignature(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return false, err
	}
	header, err := io.ReadAll(io.LimitReader(file, maxRipLogHeaderBytes))
	if err != nil {
		return false, err
	}
	header = bytes.ToLower(header)
	return bytes.Contains(header, []byte("exact audio copy")) || bytes.Contains(header, []byte("x lossless decoder")), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func observationFieldSources(observation parser.AudioObservation) map[string]string {
	sources := map[string]string{}
	for field, inferred := range observation.InferredFields {
		if !inferred {
			continue
		}
		switch field {
		case "album":
			sources[field] = "folder_fallback"
		case "title":
			sources[field] = "filename_fallback"
		case "artist":
			sources[field] = "display_fallback"
		default:
			sources[field] = "default_fallback"
		}
	}
	return sources
}

// persistOneCandidate 用 catalog 的独立短事务写入一个候选；封面在事务提交后为该 Release 绑定，
// 封面失败以 errArtworkBinding 包装返回。曲目沿用还是新建由规划决定，见 planRootIdentities。
func (service *Service) persistOneCandidate(ctx context.Context, executor scanExecutor, scanID string, root registeredRoot, candidate ingest.OrganizedCandidate, feedbacks ...*scanFeedback) (string, error) {
	feedback := feedbackArg(feedbacks)
	var changes *ingest.ScanFeedback
	if feedback != nil {
		changes = feedback.changes
	}
	releaseID, err := ingest.PersistCandidateWithFeedback(ctx, executor, scanID, root.ID, candidate, changes)
	if err != nil {
		return "", err
	}
	if artworkErr := service.saveArtworkForRelease(ctx, executor, scanID, releaseID, root, candidate, changes); artworkErr != nil {
		return releaseID, fmt.Errorf("%w: %v", errArtworkBinding, artworkErr)
	}
	return releaseID, nil
}

func safeRelativePath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." {
		return ""
	}
	return filepath.ToSlash(relative)
}
func isWithin(root, path string) bool {
	rootReal, rootError := filepath.EvalSymlinks(root)
	pathReal, pathError := filepath.EvalSymlinks(path)
	return rootError == nil && pathError == nil && (pathReal == rootReal || strings.HasPrefix(pathReal, rootReal+string(filepath.Separator)))
}

// saveArtworkForRelease 为 Release 取得封面并交给受管存储：优先用候选曲目里的内嵌封面，否则按
// catalog 给出的目录顺序（发行目录、再各曲目目录）发现文件夹封面；两者都没有时移除既有关联。
// 文件夹封面发现失败只记诊断并保留原关系；受管存储失败仍向上返回。
func (service *Service) saveArtworkForRelease(ctx context.Context, executor scanExecutor, scanID, releaseID string, root registeredRoot, candidate ingest.OrganizedCandidate, feedbacks ...*ingest.ScanFeedback) error {
	var data []byte
	var mimeType string
	sourceType := "embedded"
	if observation, ok := candidate.EmbeddedArtwork(); ok {
		data, mimeType = observation.Artwork, observation.ArtworkMIME
	} else {
		sourceType = "folder"
		var discoveryErr error
		data, mimeType, discoveryErr = discoverFolderArtwork(root.Path, candidate.ArtworkDirectories(), root.filter)
		if errors.Is(discoveryErr, errArtworkSource) {
			return service.recordDiagnosticWith(ctx, executor, scanID, candidate.Anchor.Scope, "artwork_unreadable", "封面文件读不了")
		}
		if discoveryErr != nil {
			return discoveryErr
		}
	}
	store := artwork.NewStore(service.dataDirectory)
	if len(feedbacks) > 0 && feedbacks[0] != nil {
		if len(data) == 0 {
			return store.RemoveWithFeedback(ctx, executor, releaseID, feedbacks[0])
		}
		return store.SaveWithFeedback(ctx, executor, releaseID, data, mimeType, sourceType, feedbacks[0])
	}
	if len(data) == 0 {
		return store.Remove(ctx, executor, releaseID)
	}
	return store.Save(ctx, executor, releaseID, data, mimeType, sourceType)
}

// folderArtworkNames 是文件夹封面的文件名及优先顺序（小写）；实际文件名按小写后匹配。
var folderArtworkNames = []string{"cover.jpg", "cover.jpeg", "cover.png", "cover.webp", "folder.jpg", "folder.jpeg", "folder.png", "front.jpg", "front.jpeg", "front.png"}

// folderArtworkSubdirectoryNames 只匹配直接附属子目录，不递归、不跟随目录符号链接。
var folderArtworkSubdirectoryNames = map[string]bool{"scans": true, "artwork": true, "art": true, "covers": true}

func discoverFolderArtwork(rootPath string, directories []string, filter scanFilter) ([]byte, string, error) {
	return discoverFolderArtworkWithReader(rootPath, directories, os.ReadFile, filter)
}

// discoverFolderArtworkWithReader 依次查原目录精确名、原目录 front、附属子目录精确名、附属子目录 front。
// 每轮命中即返回；无效封面记下第一个错误，四轮全部找完仍没有可用封面才报错。
func discoverFolderArtworkWithReader(rootPath string, directories []string, readFile func(string) ([]byte, error), filter scanFilter) ([]byte, string, error) {
	type artworkDirectory struct {
		path         string
		relativePath string
		entries      []os.DirEntry
	}
	var firstErr error
	remember := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}
	readDirectory := func(relativePath string) (artworkDirectory, bool) {
		directoryPath := filepath.Join(rootPath, filepath.FromSlash(relativePath))
		entries, err := os.ReadDir(directoryPath)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				remember(err)
			}
			return artworkDirectory{}, false
		}
		return artworkDirectory{path: directoryPath, relativePath: relativePath, entries: entries}, true
	}
	search := func(directory artworkDirectory, exact bool) ([]byte, string) {
		var names []string
		if exact {
			for _, name := range folderArtworkNames {
				names = append(names, folderArtworkCandidates(directory.entries, name)...)
			}
		} else {
			for _, entry := range directory.entries {
				name := strings.ToLower(entry.Name())
				ext := filepath.Ext(name)
				if (ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp") && strings.Contains(strings.TrimSuffix(name, ext), "front") {
					names = append(names, entry.Name())
				}
			}
		}
		for _, name := range names {
			if filter.skipFile(name) {
				continue
			}
			data, mimeType, err := readFolderArtworkFile(filepath.Join(directory.path, name), name, readFile)
			if err != nil {
				remember(err)
				continue
			}
			return data, mimeType
		}
		return nil, ""
	}
	// 第一轮保持原目录与命名优先级；缓存目录项供第二轮和附属目录发现复用。
	var originals []artworkDirectory
	for _, directory := range directories {
		loaded, ok := readDirectory(directory)
		if !ok {
			continue
		}
		originals = append(originals, loaded)
		if data, mimeType := search(loaded, true); len(data) > 0 {
			return data, mimeType, nil
		}
	}
	for _, directory := range originals {
		if data, mimeType := search(directory, false); len(data) > 0 {
			return data, mimeType, nil
		}
	}
	var subsidiaries []artworkDirectory
	for _, directory := range originals {
		for _, entry := range directory.entries {
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !folderArtworkSubdirectoryNames[strings.ToLower(entry.Name())] {
				continue
			}
			relativePath := pathpkg.Join(directory.relativePath, entry.Name())
			if filter.skipDir(relativePath, entry.Name()) {
				continue
			}
			loaded, ok := readDirectory(relativePath)
			if !ok {
				continue
			}
			subsidiaries = append(subsidiaries, loaded)
			if data, mimeType := search(loaded, true); len(data) > 0 {
				return data, mimeType, nil
			}
		}
	}
	for _, directory := range subsidiaries {
		if data, mimeType := search(directory, false); len(data) > 0 {
			return data, mimeType, nil
		}
	}
	if firstErr != nil {
		return nil, "", fmt.Errorf("%w: %w", errArtworkSource, firstErr)
	}
	return nil, "", nil
}

// folderArtworkCandidates 返回目录中小写后等于 name 的文件名：全小写的在前，其余按字节序（ReadDir 已按名字排序）。
func folderArtworkCandidates(entries []os.DirEntry, name string) []string {
	var exact, variants []string
	for _, entry := range entries {
		switch entryName := entry.Name(); {
		case entryName == name:
			exact = append(exact, entryName)
		case strings.ToLower(entryName) == name:
			variants = append(variants, entryName)
		}
	}
	return append(exact, variants...)
}

func readFolderArtworkFile(path, name string, readFile func(string) ([]byte, error)) ([]byte, string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 16<<20 {
		return nil, "", fmt.Errorf("folder artwork %s is not a safe bounded regular file", name)
	}
	data, err := readFile(path)
	if err != nil {
		return nil, "", err
	}
	mimeType, err := artwork.ValidateImage(data)
	if err != nil {
		return nil, "", err
	}
	return data, mimeType, nil
}

func (service *Service) recordDiagnosticWith(context context.Context, executor scanExecutor, scanID, relativePath, kind, message string) error {
	_, err := executor.ExecContext(context, "INSERT INTO scan_diagnostics(scan_run_id,relative_path,kind,message) SELECT $1::uuid,$2,$3,$4 WHERE (SELECT COUNT(*) FROM scan_diagnostics WHERE scan_run_id=$1::uuid)<100", scanID, relativePath, kind, message)
	return err
}

// cloneBoolMap 复制推断字段表，且总返回非 nil 的映射（CUE 虚拟轨随后会删除其中的键）。
func cloneBoolMap(input map[string]bool) map[string]bool {
	result := make(map[string]bool, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
