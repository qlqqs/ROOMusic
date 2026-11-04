// Package library 拥有目录与扫描能力：允许根内路径校验、只读来源发现、扫描运行与取消、把观察交给 catalog 暂存、
// 诊断、中断恢复与仅在完整成功后才执行的 missing 对账。它不含 HTTP，也从不写音乐库。
package library

import (
	"bytes"
	"context"
	"database/sql"
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

type registeredRoot struct{ ID, Path string }

type scanExecutor interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
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
	roots, loadError := service.loadRegisteredRoots(scanContext, execution.connection)
	outcome := scanOutcome{Complete: loadError == nil, Failed: loadError != nil}
	if loadError != nil {
		roots = nil
	}
	feedback := newScanFeedback(service, execution.connection, scanID, roots)
	if loadError == nil && !service.countScanRoots(scanContext, execution.connection, scanID, roots, feedback) {
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
		if rootError := service.scanRoot(scanContext, execution.connection, scanID, root, feedback); rootError != nil {
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
	rows, err := executor.QueryContext(context, "SELECT id::text,path FROM library_roots WHERE status='active' ORDER BY path,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	roots := []registeredRoot{}
	for rows.Next() {
		var root registeredRoot
		if err := rows.Scan(&root.ID, &root.Path); err != nil {
			return nil, err
		}
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
	err := filepath.WalkDir(root.Path, func(path string, entry fs.DirEntry, walkError error) error {
		if contextErr := ctx.Err(); contextErr != nil {
			complete = false
			return contextErr
		}
		if walkError != nil {
			complete = false
			if diagnosticError := service.recordDiagnosticWith(ctx, executor, scanID, safeRelativePath(root.Path, path), "permission_error", "无法读取文件"); diagnosticError != nil {
				return diagnosticError
			}
			return nil
		}
		if entry.IsDir() && entry.Type()&os.ModeSymlink != 0 {
			return fs.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		if isScanCandidate(path) {
			defer feedback.processed(ctx)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, targetError := filepath.EvalSymlinks(path)
			if targetError != nil || !isWithin(root.Path, target) {
				complete = false
				if diagnosticError := service.recordDiagnosticWith(ctx, executor, scanID, safeRelativePath(root.Path, path), "broken_or_escaped_link", "文件链接目标不可读取"); diagnosticError != nil {
					return diagnosticError
				}
				return nil
			}
		}
		relativePath := safeRelativePath(root.Path, path)
		extension := strings.ToLower(filepath.Ext(path))
		if extension != ".cue" && !isSupportedAudioExtension(extension) && !isAudioCandidateExtension(extension) {
			return nil
		}
		fileInfo, statError := os.Stat(path)
		if statError != nil {
			complete = false
			if diagnosticError := service.recordDiagnosticWith(ctx, executor, scanID, relativePath, "permission_error", "无法读取文件"); diagnosticError != nil {
				return diagnosticError
			}
			return nil
		}
		if !fileInfo.Mode().IsRegular() {
			complete = false
			if diagnosticError := service.recordDiagnosticWith(ctx, executor, scanID, relativePath, "non_regular_file", "音频来源不是普通文件"); diagnosticError != nil {
				return diagnosticError
			}
			return nil
		}
		if extension == ".cue" {
			if contextErr := ctx.Err(); contextErr != nil {
				return contextErr
			}
			document, cueErr := parser.ParseCueDocument(path, root.Path)
			if cueErr != nil {
				complete = false
				if diagnosticError := service.recordDiagnosticWith(ctx, executor, scanID, relativePath, "unsupported_cue", "CUE 文件无法解析"); diagnosticError != nil {
					return diagnosticError
				}
				return nil
			}
			for _, diagnostic := range document.Diagnostics {
				if diagnosticError := service.recordDiagnosticWith(ctx, executor, scanID, relativePath, "cue_reference", diagnostic); diagnosticError != nil {
					return diagnosticError
				}
			}
			parentFacts := make(map[string]parser.AudioObservation, len(document.Files))
			parentInfos := make(map[string]fs.FileInfo, len(document.Files))
			for _, reference := range document.Files {
				if cueReferenceMakesScanIncomplete(reference.Status) {
					complete = false
				}
				if reference.Status != "present" {
					continue
				}
				if reference.ResolvedPath == "" || !isWithin(root.Path, reference.ResolvedPath) {
					complete = false
					continue
				}
				facts, parseErr := parser.ParseAudioFile(reference.ResolvedPath)
				if parseErr != nil {
					complete = false
					if diagnosticError := service.recordDiagnosticWith(ctx, executor, scanID, relativePath, "parse_failure", "CUE 引用音频解析失败"); diagnosticError != nil {
						return diagnosticError
					}
					continue
				}
				parentInfo, parentStatError := os.Stat(reference.ResolvedPath)
				if parentStatError != nil {
					complete = false
					if diagnosticError := service.recordDiagnosticWith(ctx, executor, scanID, relativePath, "permission_error", "无法读取文件"); diagnosticError != nil {
						return diagnosticError
					}
					continue
				}
				parentFacts[reference.Path] = facts
				parentInfos[reference.Path] = parentInfo
			}
			for _, cue := range document.Tracks {
				if contextErr := ctx.Err(); contextErr != nil {
					return contextErr
				}
				if cueReferenceMakesScanIncomplete(cue.ReferenceStatus) {
					complete = false
				}
				if !cueTrackCanBeStaged(cue) {
					continue
				}
				o, ok := parentFacts[cue.ReferencedFile]
				if !ok {
					complete = false
					continue
				}
				virtualObservation := buildCueVirtualObservation(o, document, cue, relativePath)
				// 虚拟轨的指纹依据是父文件：大小与修改时间取自父文件。
				virtualObservation.SourceSize = parentInfos[cue.ReferencedFile].Size()
				virtualObservation.SourceModifiedAt = parentInfos[cue.ReferencedFile].ModTime()
				if stageErr := stageObservation(ctx, executor, scanID, root.ID, virtualObservation); stageErr != nil {
					complete = false
					captureProcessingError(stageErr)
					if diagnosticError := service.recordDiagnosticWith(ctx, executor, scanID, relativePath, "staging_write_failure", "无法暂存 CUE 观察"); diagnosticError != nil {
						return diagnosticError
					}
				}
			}
			return nil
		}
		if !isSupportedAudioExtension(extension) {
			complete = false
			if diagnosticError := service.recordDiagnosticWith(ctx, executor, scanID, relativePath, "unsupported_format", "不支持的音频格式"); diagnosticError != nil {
				return diagnosticError
			}
			return nil
		}
		observation, parseError := parser.ParseAudioFile(path)
		if parseError != nil {
			complete = false
			if diagnosticError := service.recordDiagnosticWith(ctx, executor, scanID, relativePath, "parse_failure", "音频文件解析失败"); diagnosticError != nil {
				return diagnosticError
			}
			return nil
		}
		physicalObservation := ingest.SourceObservation{RelativePath: relativePath, Directory: ingest.DirectoryOf(relativePath), Title: observation.Title, Album: observation.Album, AlbumArtist: observation.AlbumArtist, Artist: observation.Artist, SourceType: observation.SourceType, MediaType: observation.MediaType, Genre: observation.Genre, CatalogNumber: observation.Catalog, Edition: observation.Edition, Label: observation.Label, Barcode: observation.Barcode, Year: observation.Year, TrackNumber: observation.TrackNumber, DiscNumber: observation.DiscNumber, SourceKind: observation.SourceKind, InferredFields: observation.InferredFields, FieldSources: observationFieldSources(observation), DurationSeconds: observation.DurationSeconds, Codec: observation.Codec, BitDepth: observation.BitDepth, SampleRate: observation.SampleRate, Channels: observation.Channels, Bitrate: observation.Bitrate, Artwork: observation.Artwork, ArtworkMIME: observation.ArtworkMIME, Credits: append([]metadata.Credit(nil), observation.Credits...), Lyrics: observation.Lyrics, LyricsSource: observation.LyricsSource, SourceSize: fileInfo.Size(), SourceModifiedAt: fileInfo.ModTime(), ContentFingerprint: observation.ContentFingerprint}
		// 注册根本身不是 album 证据。目录 fallback 只在根目录以下有效；根级文件缺少
		// album tag 时必须保持为彼此独立的 loose-unknown candidates。
		if physicalObservation.Directory == "" && physicalObservation.InferredFields["album"] {
			physicalObservation.Album = ""
		}
		if stageErr := stageObservation(ctx, executor, scanID, root.ID, physicalObservation); stageErr != nil {
			complete = false
			captureProcessingError(stageErr)
			if diagnosticError := service.recordDiagnosticWith(ctx, executor, scanID, relativePath, "staging_write_failure", "无法暂存音频观察"); diagnosticError != nil {
				return diagnosticError
			}
		}
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		return nil
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
		if alignErr := ingest.AlignCueStagingScopes(ctx, executor, scanID, root.ID); alignErr != nil {
			complete = false
			captureProcessingError(alignErr)
			if diagnosticError := service.recordDiagnosticWith(ctx, executor, scanID, "", "staging_write_failure", "无法对齐 CUE 父来源"); diagnosticError != nil {
				captureProcessingError(diagnosticError)
			}
		}
		// 先规划曲目身份（遍历已经结束，complete 即遍历证据是否完整）；规划失败时暂存行没有完整的规划，不能写入。
		planned, planProblem := service.planRootIdentities(ctx, executor, scanID, root, complete)
		if planProblem != nil {
			complete = false
			captureProcessingError(planProblem)
		}
		// 即使其它文件解析失败，也持久化有效 observations（被规划暂缓的 scope 除外）；扫描会进入 incomplete，
		// 因而不会执行负向 missing 对账。
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
		}
	}
	if !complete {
		return errors.Join(errors.New("incomplete root"), processingError)
	}
	return nil
}

// CUE 父文件缺失或没有 INDEX 是确定且不阻断其它来源的观察：跳过不可用虚拟轨，
// 同时保留有界诊断。unsafe/unchecked 仍令扫描不完整，避免路径逃逸参与负向对账。
func cueReferenceMakesScanIncomplete(status string) bool {
	return status != "present" && status != "missing"
}

func cueTrackCanBeStaged(track parser.CueTrack) bool {
	return track.ReferenceStatus == "present" && track.IndexPresent
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
	observation.Genre = firstNonEmpty(cue.Genre, document.Genre, observation.Genre)
	observation.Catalog = firstNonEmpty(cue.Catalog, document.Catalog, observation.Catalog)
	observation.ISRC = cue.ISRC
	if firstNonEmpty(cue.Genre, document.Genre) != "" {
		fieldSources["genre"] = "cue_sheet"
	}
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
		Genre:                 observation.Genre,
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
	}
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
			for _, candidate := range ingest.OrganizeObservations(observations) {
				ripLogRefs, diagnostics := discoverCandidateRipLogEvidence(root, candidate)
				ingest.ApplyRipLogEvidence(&candidate, ripLogRefs)
				for _, diagnostic := range diagnostics {
					if diagnosticErr := service.recordDiagnosticWith(ctx, executor, scanID, diagnostic.RelativePath, diagnostic.Kind, diagnostic.Message); diagnosticErr != nil && firstErr == nil {
						firstErr = diagnosticErr
					}
				}
				if persistErr := service.persistOneCandidate(ctx, executor, scanID, root, candidate, feedback); persistErr != nil && firstErr == nil {
					firstErr = persistErr
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
func (service *Service) persistOneCandidate(ctx context.Context, executor scanExecutor, scanID string, root registeredRoot, candidate ingest.OrganizedCandidate, feedbacks ...*scanFeedback) error {
	feedback := feedbackArg(feedbacks)
	var changes *ingest.ScanFeedback
	if feedback != nil {
		changes = feedback.changes
	}
	releaseID, err := ingest.PersistCandidateWithFeedback(ctx, executor, scanID, root.ID, candidate, changes)
	if err != nil {
		return err
	}
	if artworkErr := service.saveArtworkForRelease(ctx, executor, releaseID, root, candidate, changes); artworkErr != nil {
		return fmt.Errorf("%w: %v", errArtworkBinding, artworkErr)
	}
	return nil
}

func isSupportedAudioExtension(extension string) bool {
	switch extension {
	case ".flac", ".mp3", ".ogg", ".opus", ".wav", ".m4a":
		return true
	default:
		return false
	}
}

func isAudioCandidateExtension(extension string) bool {
	switch extension {
	case ".aac", ".aif", ".aiff", ".ape", ".dff", ".dsf", ".m4a", ".mka", ".wma":
		return true
	default:
		return false
	}
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
// 文件夹封面发现失败以原错误返回，不当作没有封面。
func (service *Service) saveArtworkForRelease(ctx context.Context, executor scanExecutor, releaseID string, root registeredRoot, candidate ingest.OrganizedCandidate, feedbacks ...*ingest.ScanFeedback) error {
	var data []byte
	var mimeType string
	sourceType := "embedded"
	if observation, ok := candidate.EmbeddedArtwork(); ok {
		data, mimeType = observation.Artwork, observation.ArtworkMIME
	} else {
		sourceType = "folder"
		var discoveryErr error
		data, mimeType, discoveryErr = discoverFolderArtwork(root.Path, candidate.ArtworkDirectories())
		if discoveryErr != nil {
			return fmt.Errorf("discover folder artwork: %w", discoveryErr)
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

func discoverFolderArtwork(rootPath string, directories []string) ([]byte, string, error) {
	return discoverFolderArtworkWithReader(rootPath, directories, os.ReadFile)
}

// discoverFolderArtworkWithReader 按顺序在 directories（相对 rootPath，空串表示根）里找封面，第一个通过检查的文件
// 即为结果。无效的命名封面（符号链接、非普通文件、超限、读取失败、格式不支持）记下第一个错误后继续找；
// 全部找完仍没有结果时才返回该错误。目录不存在不算错误，其他读目录错误按同样方式记录。
func discoverFolderArtworkWithReader(rootPath string, directories []string, readFile func(string) ([]byte, error)) ([]byte, string, error) {
	var firstErr error
	remember := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}
	for _, directory := range directories {
		directoryPath := filepath.Join(rootPath, filepath.FromSlash(directory))
		entries, err := os.ReadDir(directoryPath)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			remember(err)
			continue
		}
		for _, name := range folderArtworkNames {
			for _, entryName := range folderArtworkCandidates(entries, name) {
				data, mimeType, err := readFolderArtworkFile(filepath.Join(directoryPath, entryName), name, readFile)
				if err != nil {
					remember(err)
					continue
				}
				return data, mimeType, nil
			}
		}
	}
	return nil, "", firstErr
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
	mimeType := artwork.SniffMIME(data)
	if mimeType == "" {
		return nil, "", fmt.Errorf("folder artwork %s has an unsupported format", name)
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
