package main

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

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/artwork"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
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

func (application *roomusicApplication) runScan(scanID string, execution *scanExecution) {
	scanContext, cancel := context.WithCancel(application.scanContext)
	pollDone := make(chan struct{})
	stopReason := make(chan error, 1)
	go application.pollScanCancellation(scanContext, scanID, cancel, pollDone, stopReason)
	defer func() {
		cancel()
		<-pollDone
		if err := execution.close(context.Background()); err != nil {
			application.logger.Error("scan coordination release failed", "event", "library.scan.coordination_failed", "scan_run_id", scanID, "error", err)
		}
		application.scanMutex.Lock()
		if application.runningScan == scanID {
			application.runningScan = ""
		}
		application.scanMutex.Unlock()
	}()
	roots, loadError := application.loadRegisteredRoots(scanContext, execution.connection)
	outcome := scanOutcome{Complete: loadError == nil, Failed: loadError != nil}
	rootIDs := make([]string, 0, len(roots))
	for _, root := range roots {
		if scanContext.Err() != nil {
			outcome.Complete = false
			break
		}
		rootIDs = append(rootIDs, root.ID)
		if rootError := application.scanRoot(scanContext, execution.connection, scanID, root); rootError != nil {
			outcome.Complete = false
			application.logger.Error("scan root incomplete", "event", "library.scan.root_incomplete", "scan_run_id", scanID, "root_id", root.ID, "error", rootError)
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
				application.logger.Error("scan cancellation polling failed", "event", "library.scan.coordination_failed", "scan_run_id", scanID, "error", err)
			}
		default:
			// 应用停机只表示本次扫描未完成；只有观察到持久化取消请求才写入 canceled。
		}
	}
	if loadError != nil {
		if diagnosticError := application.recordDiagnosticWith(context.Background(), execution.connection, scanID, "", "database_error", "无法读取已注册目录"); diagnosticError != nil {
			outcome.Failed = true
		}
	}
	finalizeContext, finalizeCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer finalizeCancel()
	status, updateError := application.finalizeScan(finalizeContext, execution.connection, scanID, rootIDs, outcome)
	if updateError != nil {
		application.logger.Error("scan terminal status persistence failed", "event", "library.scan.terminal_write_failed", "scan_id", scanID, "error", updateError)
		return
	}
	application.logger.Info("scan completed", "event", "library.scan.completed", "scan_run_id", scanID, "outcome", status)
}

var errScanCancellationRequested = errors.New("scan cancellation requested")

func (application *roomusicApplication) pollScanCancellation(ctx context.Context, scanID string, cancel context.CancelFunc, done chan<- struct{}, stopReason chan<- error) {
	defer close(done)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var requested bool
			err := application.database.connection.QueryRowContext(ctx, "SELECT cancel_requested_at IS NOT NULL FROM scan_runs WHERE id=$1::uuid AND status='running'", scanID).Scan(&requested)
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

func (application *roomusicApplication) finalizeScan(ctx context.Context, connection *sql.Conn, scanID string, rootIDs []string, outcome scanOutcome) (string, error) {
	transaction, err := connection.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer transaction.Rollback()
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
	if scanStatusAllowsMissingReconciliation(status) {
		if err := catalog.MarkMissingSources(ctx, transaction, scanID, rootIDs); err != nil {
			return "", err
		}
	}
	var errorMessage any
	if status == "failed" {
		errorMessage = "scan_failed"
	} else if status == "incomplete" {
		errorMessage = "scan_incomplete"
	}
	if _, err := transaction.ExecContext(ctx, "UPDATE scan_runs SET status=$1,finished_at=NOW(),error_message=$2 WHERE id=$3::uuid AND status='running'", status, errorMessage, scanID); err != nil {
		return "", err
	}
	if err := transaction.Commit(); err != nil {
		return "", err
	}
	return status, nil
}

func (application *roomusicApplication) loadRegisteredRoots(context context.Context, executor scanExecutor) ([]registeredRoot, error) {
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

func (application *roomusicApplication) scanRoot(ctx context.Context, executor scanExecutor, scanID string, root registeredRoot) (returnError error) {
	// 遍历前必须清理同一扫描重试留下的旧行，否则旧 observation 会混入本次候选。
	if err := clearStagedObservations(ctx, executor, scanID, root.ID); err != nil {
		cleanupError := fmt.Errorf("clear stale staged observations: %w", err)
		if diagnosticError := application.recordDiagnosticWith(ctx, executor, scanID, "", "staging_write_failure", "无法清理扫描暂存观察"); diagnosticError != nil {
			cleanupError = errors.Join(cleanupError, fmt.Errorf("record staging cleanup diagnostic: %w", diagnosticError))
		}
		return cleanupError
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cleanupError := clearStagedObservations(cleanupCtx, executor, scanID, root.ID)
		if cleanupError == nil {
			return
		}
		cleanupError = fmt.Errorf("clear staged observations: %w", cleanupError)
		if diagnosticError := application.recordDiagnosticWith(cleanupCtx, executor, scanID, "", "staging_write_failure", "无法清理扫描暂存观察"); diagnosticError != nil {
			cleanupError = errors.Join(cleanupError, fmt.Errorf("record staging cleanup diagnostic: %w", diagnosticError))
		}
		returnError = errors.Join(returnError, cleanupError)
	}()
	if !registeredRootAvailable(root.Path) {
		if diagnosticError := application.recordDiagnosticWith(ctx, executor, scanID, "", "root_unavailable", "注册目录当前不可用"); diagnosticError != nil {
			return fmt.Errorf("record unavailable root diagnostic: %w", diagnosticError)
		}
		return errors.New("root unavailable")
	}
	complete := true
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
			if diagnosticError := application.recordDiagnosticWith(ctx, executor, scanID, safeRelativePath(root.Path, path), "permission_error", "无法读取文件"); diagnosticError != nil {
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
		if entry.Type()&os.ModeSymlink != 0 {
			target, targetError := filepath.EvalSymlinks(path)
			if targetError != nil || !isWithin(root.Path, target) {
				complete = false
				if diagnosticError := application.recordDiagnosticWith(ctx, executor, scanID, safeRelativePath(root.Path, path), "broken_or_escaped_link", "文件链接目标不可读取"); diagnosticError != nil {
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
			if diagnosticError := application.recordDiagnosticWith(ctx, executor, scanID, relativePath, "permission_error", "无法读取文件"); diagnosticError != nil {
				return diagnosticError
			}
			return nil
		}
		if !fileInfo.Mode().IsRegular() {
			complete = false
			if diagnosticError := application.recordDiagnosticWith(ctx, executor, scanID, relativePath, "non_regular_file", "音频来源不是普通文件"); diagnosticError != nil {
				return diagnosticError
			}
			return nil
		}
		if extension == ".cue" {
			if contextErr := ctx.Err(); contextErr != nil {
				return contextErr
			}
			document, cueErr := parseCueDocument(path, root.Path)
			if cueErr != nil {
				complete = false
				if diagnosticError := application.recordDiagnosticWith(ctx, executor, scanID, relativePath, "unsupported_cue", "CUE 文件无法解析"); diagnosticError != nil {
					return diagnosticError
				}
				return nil
			}
			for _, diagnostic := range document.Diagnostics {
				if diagnosticError := application.recordDiagnosticWith(ctx, executor, scanID, relativePath, "cue_reference", diagnostic); diagnosticError != nil {
					return diagnosticError
				}
			}
			parentFacts := make(map[string]audioObservation, len(document.Files))
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
				facts, parseErr := parseAudioFile(reference.ResolvedPath)
				if parseErr != nil {
					complete = false
					if diagnosticError := application.recordDiagnosticWith(ctx, executor, scanID, relativePath, "parse_failure", "CUE 引用音频解析失败"); diagnosticError != nil {
						return diagnosticError
					}
					continue
				}
				parentFacts[reference.Path] = facts
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
				if stageErr := stageObservation(ctx, executor, scanID, root.ID, virtualObservation); stageErr != nil {
					complete = false
					captureProcessingError(stageErr)
					if diagnosticError := application.recordDiagnosticWith(ctx, executor, scanID, relativePath, "staging_write_failure", "无法暂存 CUE 观察"); diagnosticError != nil {
						return diagnosticError
					}
				}
			}
			return nil
		}
		if !isSupportedAudioExtension(extension) {
			complete = false
			if diagnosticError := application.recordDiagnosticWith(ctx, executor, scanID, relativePath, "unsupported_format", "不支持的音频格式"); diagnosticError != nil {
				return diagnosticError
			}
			return nil
		}
		observation, parseError := parseAudioFile(path)
		if parseError != nil {
			complete = false
			if diagnosticError := application.recordDiagnosticWith(ctx, executor, scanID, relativePath, "parse_failure", "音频文件解析失败"); diagnosticError != nil {
				return diagnosticError
			}
			return nil
		}
		physicalObservation := catalog.SourceObservation{RelativePath: relativePath, Directory: catalog.DirectoryOf(relativePath), Title: observation.Title, Album: observation.Album, AlbumArtist: observation.AlbumArtist, Artist: observation.Artist, SourceType: observation.SourceType, MediaType: observation.MediaType, Genre: observation.Genre, CatalogNumber: observation.Catalog, Edition: observation.Edition, Label: observation.Label, Barcode: observation.Barcode, Year: observation.Year, TrackNumber: observation.TrackNumber, DiscNumber: observation.DiscNumber, SourceKind: observation.SourceKind, InferredFields: observation.InferredFields, FieldSources: observationFieldSources(observation), DurationSeconds: observation.DurationSeconds, Codec: observation.Codec, BitDepth: observation.BitDepth, SampleRate: observation.SampleRate, Channels: observation.Channels, Bitrate: observation.Bitrate, Artwork: observation.Artwork, ArtworkMIME: observation.ArtworkMIME, Credits: append([]metadata.Credit(nil), observation.Credits...)}
		// 注册根本身不是 album 证据。目录 fallback 只在根目录以下有效；根级文件缺少
		// album tag 时必须保持为彼此独立的 loose-unknown candidates。
		if physicalObservation.Directory == "" && physicalObservation.InferredFields["album"] {
			physicalObservation.Album = ""
		}
		if stageErr := stageObservation(ctx, executor, scanID, root.ID, physicalObservation); stageErr != nil {
			complete = false
			captureProcessingError(stageErr)
			if diagnosticError := application.recordDiagnosticWith(ctx, executor, scanID, relativePath, "staging_write_failure", "无法暂存音频观察"); diagnosticError != nil {
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
		if alignErr := alignCueStagingScopes(ctx, executor, scanID, root.ID); alignErr != nil {
			complete = false
			captureProcessingError(alignErr)
			if diagnosticError := application.recordDiagnosticWith(ctx, executor, scanID, "", "staging_write_failure", "无法对齐 CUE 父来源"); diagnosticError != nil {
				captureProcessingError(diagnosticError)
			}
		}
		// 即使其它文件解析失败，也持久化有效 observations；扫描会进入 incomplete，
		// 因而不会执行负向 missing 对账。
		if evidenceErr := application.persistStagedCandidates(ctx, executor, scanID, root); evidenceErr != nil {
			complete = false
			captureProcessingError(evidenceErr)
			kind, message := candidatePersistenceDiagnostic(evidenceErr)
			if diagnosticError := application.recordDiagnosticWith(ctx, executor, scanID, "", kind, message); diagnosticError != nil {
				captureProcessingError(diagnosticError)
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

func cueTrackCanBeStaged(track cueTrack) bool {
	return track.ReferenceStatus == "present" && track.IndexPresent
}

func buildCueVirtualObservation(parent audioObservation, document cueDocument, cue cueTrack, sheetRelativePath string) catalog.SourceObservation {
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
	if parsedYear := parseYear(firstNonEmpty(cue.Date, document.Date)); parsedYear > 0 {
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

	referencedIdentity := catalog.NormalizedPath(cue.ReferencedFile)
	parentRelative := catalog.NormalizedPath(filepath.ToSlash(filepath.Join(filepath.Dir(sheetRelativePath), referencedIdentity)))
	virtualPath := sheetRelativePath + "#track-" + strconv.Itoa(cue.Number) + "@" + parentRelative + "#index-" + strconv.Itoa(cue.IndexFrames)
	return catalog.SourceObservation{
		RelativePath:          virtualPath,
		Directory:             catalog.DirectoryOf(sheetRelativePath),
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

const maxStagedObservationBytes = 24 << 20

const (
	stagedScopePageSize           = 256
	maxStagedObservationsPerScope = 10000
	maxRipLogHeaderBytes          = 64 << 10
	maxRipLogDiagnostics          = 20
)

func stageObservation(ctx context.Context, executor scanExecutor, scanID, rootID string, observation catalog.SourceObservation) error {
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
	scope := catalog.OrganizationScope(observation)
	// 每个逻辑 scope 只暂存一份确定性的 embedded artwork；folder artwork 要等到
	// Release id 确定后再发现和绑定。
	if len(observation.Artwork) > 0 {
		var alreadyStaged bool
		if err := executor.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM scan_observations WHERE scan_run_id=$1::uuid AND root_id=$2::uuid
			AND organization_scope=$3 AND observation ? 'artwork')`, scanID, rootID, scope).Scan(&alreadyStaged); err != nil {
			return err
		}
		if alreadyStaged {
			observation.Artwork = nil
			observation.ArtworkMIME = ""
		}
	}
	payload, err := json.Marshal(observation)
	if err != nil {
		return fmt.Errorf("marshal staged observation: %w", err)
	}
	if len(payload) > maxStagedObservationBytes {
		return errors.New("staged observation exceeds bound")
	}
	_, err = executor.ExecContext(ctx, `INSERT INTO scan_observations(scan_run_id,root_id,organization_scope,relative_path,observation)
		VALUES($1::uuid,$2::uuid,$3,$4,$5::jsonb)
		ON CONFLICT(scan_run_id,root_id,relative_path) DO UPDATE SET organization_scope=EXCLUDED.organization_scope,observation=EXCLUDED.observation,observed_at=NOW()`, scanID, rootID, scope, observation.RelativePath, payload)
	return err
}

func loadStagedObservationScope(ctx context.Context, executor scanExecutor, scanID, rootID, scope string) ([]catalog.SourceObservation, error) {
	rows, err := executor.QueryContext(ctx, `SELECT observation FROM scan_observations
		WHERE scan_run_id=$1::uuid AND root_id=$2::uuid AND organization_scope=$3
		ORDER BY relative_path LIMIT $4`, scanID, rootID, scope, maxStagedObservationsPerScope+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	observations := make([]catalog.SourceObservation, 0, 64)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var observation catalog.SourceObservation
		if err := json.Unmarshal(raw, &observation); err != nil {
			return nil, fmt.Errorf("decode staged observation: %w", err)
		}
		observations = append(observations, observation)
		if len(observations) > maxStagedObservationsPerScope {
			return nil, errors.New("staged organization scope exceeds bound")
		}
	}
	return observations, rows.Err()
}

func (application *roomusicApplication) persistStagedCandidates(ctx context.Context, executor scanExecutor, scanID string, root registeredRoot) error {
	var firstErr error
	lastScope := ""
	for {
		rows, err := executor.QueryContext(ctx, `SELECT DISTINCT organization_scope FROM scan_observations
			WHERE scan_run_id=$1::uuid AND root_id=$2::uuid AND organization_scope>$3
			ORDER BY organization_scope LIMIT $4`, scanID, root.ID, lastScope, stagedScopePageSize)
		if err != nil {
			return err
		}
		scopes := make([]string, 0, stagedScopePageSize)
		for rows.Next() {
			var scope string
			if err := rows.Scan(&scope); err != nil {
				rows.Close()
				return err
			}
			scopes = append(scopes, scope)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(scopes) == 0 {
			break
		}
		for _, scope := range scopes {
			observations, loadErr := loadStagedObservationScope(ctx, executor, scanID, root.ID, scope)
			if loadErr != nil {
				if firstErr == nil {
					firstErr = loadErr
				}
				continue
			}
			for _, candidate := range catalog.OrganizeObservations(observations) {
				ripLogRefs, diagnostics := discoverCandidateRipLogEvidence(root, candidate)
				catalog.ApplyRipLogEvidence(&candidate, ripLogRefs)
				for _, diagnostic := range diagnostics {
					if diagnosticErr := application.recordDiagnosticWith(ctx, executor, scanID, diagnostic.RelativePath, diagnostic.Kind, diagnostic.Message); diagnosticErr != nil && firstErr == nil {
						firstErr = diagnosticErr
					}
				}
				if persistErr := application.persistOneCandidate(ctx, executor, scanID, root, candidate); persistErr != nil && firstErr == nil {
					firstErr = persistErr
				}
			}
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

func discoverCandidateRipLogEvidence(root registeredRoot, candidate catalog.OrganizedCandidate) ([]string, []ripLogDiagnostic) {
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
			if len(references) == catalog.MaxGroupingEvidenceRefs {
				break
			}
			if !strings.EqualFold(filepath.Ext(entry.Name()), ".log") || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			relativePath := catalog.NormalizedPath(filepath.ToSlash(filepath.Join(directory, entry.Name())))
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

func candidateSourceDirectories(candidate catalog.OrganizedCandidate) []string {
	seen := map[string]bool{}
	directories := make([]string, 0)
	add := func(value string) {
		value = catalog.NormalizedPath(value)
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
				directory = catalog.DirectoryOf(track.Observation.RelativePath)
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

func clearStagedObservations(ctx context.Context, executor scanExecutor, scanID, rootID string) error {
	_, err := executor.ExecContext(ctx, "DELETE FROM scan_observations WHERE scan_run_id=$1::uuid AND root_id=$2::uuid", scanID, rootID)
	return err
}

func alignCueStagingScopes(ctx context.Context, executor scanExecutor, scanID, rootID string) error {
	_, err := executor.ExecContext(ctx, `UPDATE scan_observations parent
		SET organization_scope=cue.organization_scope
		FROM (
			SELECT observation->>'cue_parent_relative_path' AS parent_path, MIN(organization_scope) AS organization_scope
			FROM scan_observations
			WHERE scan_run_id=$1::uuid AND root_id=$2::uuid AND observation->>'source_kind'='cue_virtual'
			GROUP BY observation->>'cue_parent_relative_path'
		) cue
		WHERE parent.scan_run_id=$1::uuid AND parent.root_id=$2::uuid
		  AND parent.relative_path=cue.parent_path`, scanID, rootID)
	return err
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func observationFieldSources(observation audioObservation) map[string]string {
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

// persistOrganizedCandidates 为每个 candidate 使用独立短事务。Track 身份基于
// root + 相对来源路径，因此重扫更新既有行，同时允许 candidate 拓扑变化。
// persistOneCandidate 在 catalog 事务提交后为该 Release 绑定封面；封面失败以 errArtworkBinding 包装返回。
func (application *roomusicApplication) persistOneCandidate(ctx context.Context, executor scanExecutor, scanID string, root registeredRoot, candidate catalog.OrganizedCandidate) error {
	releaseID, err := catalog.PersistCandidate(ctx, executor, scanID, root.ID, candidate)
	if err != nil {
		return err
	}
	if artworkObservation, ok := candidate.ArtworkObservation(); ok {
		if artworkErr := application.saveArtworkForRelease(ctx, executor, releaseID, root, artworkObservation); artworkErr != nil {
			return fmt.Errorf("%w: %v", errArtworkBinding, artworkErr)
		}
	}
	return nil
}

func (application *roomusicApplication) persistOrganizedCandidates(ctx context.Context, executor scanExecutor, scanID string, root registeredRoot, candidates []catalog.OrganizedCandidate) error {
	for _, c := range candidates {
		if err := application.persistOneCandidate(ctx, executor, scanID, root, c); err != nil {
			return err
		}
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

func (application *roomusicApplication) saveObservation(context context.Context, scanID string, root registeredRoot, relativePath string, observation audioObservation) error {
	return application.saveObservationWith(context, application.database.connection, scanID, root, relativePath, observation)
}

func (application *roomusicApplication) saveObservationWith(context context.Context, executor scanExecutor, scanID string, root registeredRoot, relativePath string, observation audioObservation) error {
	sourceIdentity, err := createTrackSourceIdentity(root.ID, relativePath)
	if err != nil {
		return err
	}
	organized := catalog.SourceObservation{RelativePath: sourceIdentity.RelativePath, Directory: catalog.DirectoryOf(sourceIdentity.RelativePath), Title: observation.Title, Album: observation.Album, AlbumArtist: observation.AlbumArtist, Artist: observation.Artist, SourceType: observation.SourceType, MediaType: observation.MediaType, Genre: observation.Genre, CatalogNumber: observation.Catalog, Year: observation.Year, TrackNumber: observation.TrackNumber, DiscNumber: observation.DiscNumber, SourceKind: observation.SourceKind, InferredFields: observation.InferredFields, FieldSources: observationFieldSources(observation), DurationSeconds: observation.DurationSeconds, Codec: observation.Codec, BitDepth: observation.BitDepth, SampleRate: observation.SampleRate, Channels: observation.Channels, Bitrate: observation.Bitrate, Artwork: observation.Artwork, ArtworkMIME: observation.ArtworkMIME}
	if organized.Directory == "" && organized.InferredFields["album"] {
		organized.Album = ""
	}
	return application.persistOrganizedCandidates(context, executor, scanID, root, catalog.OrganizeObservations([]catalog.SourceObservation{organized}))
}

// saveArtworkForRelease 为 Release 取得封面并交给受管存储：优先用观察里的内嵌封面，否则在曲目所在
// 文件夹发现封面图片；两者都没有时移除既有关联。文件夹封面发现失败以原错误返回，不当作没有封面。
func (application *roomusicApplication) saveArtworkForRelease(ctx context.Context, executor scanExecutor, releaseID string, root registeredRoot, observation catalog.SourceObservation) error {
	relativePath := catalog.NormalizedPath(observation.RelativePath)
	directory := filepath.Dir(filepath.Join(root.Path, filepath.FromSlash(relativePath)))
	data, mimeType := observation.Artwork, observation.ArtworkMIME
	sourceType := "embedded"
	if len(data) == 0 {
		sourceType = "folder"
		var discoveryErr error
		data, mimeType, discoveryErr = discoverFolderArtwork(directory)
		if discoveryErr != nil {
			return fmt.Errorf("discover folder artwork: %w", discoveryErr)
		}
	}
	store := artwork.NewStore(application.config.DataDirectory)
	if len(data) == 0 {
		return store.Remove(ctx, executor, releaseID)
	}
	return store.Save(ctx, executor, releaseID, data, mimeType, sourceType)
}

func discoverFolderArtwork(directory string) ([]byte, string, error) {
	return discoverFolderArtworkWithReader(directory, os.ReadFile)
}

func discoverFolderArtworkWithReader(directory string, readFile func(string) ([]byte, error)) ([]byte, string, error) {
	var firstErr error
	for _, name := range []string{"cover.jpg", "cover.jpeg", "cover.png", "cover.webp", "folder.jpg", "folder.jpeg", "folder.png", "front.jpg", "front.jpeg", "front.png"} {
		path := filepath.Join(directory, name)
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 16<<20 {
			if firstErr == nil {
				firstErr = fmt.Errorf("folder artwork %s is not a safe bounded regular file", name)
			}
			continue
		}
		data, err := readFile(path)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if mime := artwork.SniffMIME(data); mime != "" {
			return data, mime, nil
		}
		if firstErr == nil {
			firstErr = fmt.Errorf("folder artwork %s has an unsupported format", name)
		}
	}
	return nil, "", firstErr
}

func (application *roomusicApplication) recordDiagnostic(context context.Context, scanID, relativePath, kind, message string) error {
	return application.recordDiagnosticWith(context, application.database.connection, scanID, relativePath, kind, message)
}

func (application *roomusicApplication) recordDiagnosticWith(context context.Context, executor scanExecutor, scanID, relativePath, kind, message string) error {
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
