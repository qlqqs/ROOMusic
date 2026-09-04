package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
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
	normalizedPath := pathpkg.Clean(rawPath)
	if rootID == "" || normalizedPath == "." || strings.IndexByte(rawPath, 0) >= 0 || strings.HasPrefix(rawPath, "/") || strings.HasPrefix(rawPath, "//") || len(rawPath) >= 2 && rawPath[1] == ':' || normalizedPath == ".." || strings.HasPrefix(normalizedPath, "../") {
		return trackSourceIdentity{}, fmt.Errorf("invalid source relative path")
	}
	return trackSourceIdentity{RootID: rootID, RelativePath: normalizedPath}, nil
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
		for _, rootID := range rootIDs {
			if _, err := transaction.ExecContext(ctx, "UPDATE tracks SET source_status='missing' WHERE source_root_id=$1::uuid AND observed_at < (SELECT started_at FROM scan_runs WHERE id=$2::uuid)", rootID, scanID); err != nil {
				return "", err
			}
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
		physicalObservation := sourceObservation{RelativePath: relativePath, Directory: directoryOf(relativePath), Title: observation.Title, Album: observation.Album, AlbumArtist: observation.AlbumArtist, Artist: observation.Artist, SourceType: observation.SourceType, MediaType: observation.MediaType, Genre: observation.Genre, CatalogNumber: observation.Catalog, Edition: observation.Edition, Label: observation.Label, Barcode: observation.Barcode, Year: observation.Year, TrackNumber: observation.TrackNumber, DiscNumber: observation.DiscNumber, SourceKind: observation.SourceKind, InferredFields: observation.InferredFields, FieldSources: observationFieldSources(observation), DurationSeconds: observation.DurationSeconds, Codec: observation.Codec, BitDepth: observation.BitDepth, SampleRate: observation.SampleRate, Channels: observation.Channels, Bitrate: observation.Bitrate, Artwork: observation.Artwork, ArtworkMIME: observation.ArtworkMIME, Credits: append([]creditObservation(nil), observation.Credits...)}
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

func buildCueVirtualObservation(parent audioObservation, document cueDocument, cue cueTrack, sheetRelativePath string) sourceObservation {
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

	referencedIdentity := normalizedPath(cue.ReferencedFile)
	parentRelative := normalizedPath(filepath.ToSlash(filepath.Join(filepath.Dir(sheetRelativePath), referencedIdentity)))
	virtualPath := sheetRelativePath + "#track-" + strconv.Itoa(cue.Number) + "@" + parentRelative + "#index-" + strconv.Itoa(cue.IndexFrames)
	return sourceObservation{
		RelativePath:          virtualPath,
		Directory:             directoryOf(sheetRelativePath),
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

func stageObservation(ctx context.Context, executor scanExecutor, scanID, rootID string, observation sourceObservation) error {
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
	scope := observationOrganizationScope(observation)
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

func observationOrganizationScope(observation sourceObservation) string {
	location := locateObservation(observation)
	if location.isDiscDir {
		return "disc-parent:" + location.parent
	}
	if location.dir == "" {
		if album := albumEvidence(observation); album != "" {
			digest := sha256.Sum256([]byte(album))
			return fmt.Sprintf("root-album:%x", digest[:12])
		}
		return "root-source:" + normalizedPath(observation.RelativePath)
	}
	return "directory:" + location.dir
}

func loadStagedObservationScope(ctx context.Context, executor scanExecutor, scanID, rootID, scope string) ([]sourceObservation, error) {
	rows, err := executor.QueryContext(ctx, `SELECT observation FROM scan_observations
		WHERE scan_run_id=$1::uuid AND root_id=$2::uuid AND organization_scope=$3
		ORDER BY relative_path LIMIT $4`, scanID, rootID, scope, maxStagedObservationsPerScope+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	observations := make([]sourceObservation, 0, 64)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var observation sourceObservation
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
			for _, candidate := range organizeObservations(observations) {
				ripLogRefs, diagnostics := discoverCandidateRipLogEvidence(root, candidate)
				applyRipLogEvidence(&candidate, ripLogRefs)
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

func discoverCandidateRipLogEvidence(root registeredRoot, candidate organizedCandidate) ([]string, []ripLogDiagnostic) {
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
			if len(references) == maxGroupingEvidenceRefs {
				break
			}
			if !strings.EqualFold(filepath.Ext(entry.Name()), ".log") || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			relativePath := normalizedPath(filepath.ToSlash(filepath.Join(directory, entry.Name())))
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

func candidateSourceDirectories(candidate organizedCandidate) []string {
	seen := map[string]bool{}
	directories := make([]string, 0)
	add := func(value string) {
		value = normalizedPath(value)
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
				directory = directoryOf(track.Observation.RelativePath)
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

func directoryOf(path string) string {
	d := filepath.ToSlash(filepath.Dir(path))
	if d == "." {
		return ""
	}
	return d
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
func (application *roomusicApplication) persistOrganizedCandidates(ctx context.Context, executor scanExecutor, scanID string, root registeredRoot, candidates []organizedCandidate) error {
	for _, c := range candidates {
		if err := application.persistOneCandidate(ctx, executor, scanID, root, c); err != nil {
			return err
		}
	}
	return nil
}

func sourceIdentityForObservation(rootID string, observation sourceObservation) string {
	if strings.EqualFold(observation.SourceKind, "cue_virtual") || strings.Contains(strings.ToLower(observation.SourceKind), "cue") {
		sheet := normalizedPath(observation.CueSheetPath)
		parent := normalizedPath(observation.CueParentRelativePath)
		if parent == "" {
			parent = normalizedPath(observation.CueReferencedFile)
		}
		return fmt.Sprintf("%s:cue:v1:%s:%s:%d:%d", rootID, sheet, parent, observation.TrackNumber, observation.CueIndexFrames)
	}
	return rootID + ":physical:v1:" + normalizedPath(observation.RelativePath)
}

// legacySourceIdentity 只用于从 0010 原地升级；新行始终使用上述版本化身份。
func legacySourceIdentity(rootID string, observation sourceObservation) string {
	return rootID + ":" + normalizedPath(observation.RelativePath)
}

func legacyCueSourceIdentity(rootID string, observation sourceObservation) string {
	if !isCueObservation(observation) {
		return ""
	}
	sheet := normalizedPath(observation.CueSheetPath)
	parent := cueParentPath(observation)
	if sheet == "" || parent == "" || observation.TrackNumber < 1 {
		return ""
	}
	sheetDirectory := filepath.Dir(filepath.FromSlash(sheet))
	parentFromSheet, err := filepath.Rel(sheetDirectory, filepath.FromSlash(parent))
	if err != nil {
		return ""
	}
	parentFromSheet = normalizedPath(filepath.ToSlash(parentFromSheet))
	if parentFromSheet == "" {
		return ""
	}
	legacyPath := sheet + "#track-" + strconv.Itoa(observation.TrackNumber) + "@" + parentFromSheet
	return rootID + ":" + legacyPath
}

func (application *roomusicApplication) persistOneCandidate(ctx context.Context, executor scanExecutor, scanID string, root registeredRoot, candidate organizedCandidate) (err error) {
	tx, err := executor.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	anchor := candidateIdentity(root.ID, candidate.Anchor)
	var releaseID, groupID string
	err = tx.QueryRowContext(ctx, "SELECT id::text,group_id::text FROM releases WHERE source_root_id=$1::uuid AND candidate_anchor=$2", root.ID, anchor).Scan(&releaseID, &groupID)
	if errors.Is(err, sql.ErrNoRows) {
		groupID, releaseID = createIdentifier(), createIdentifier()
		if _, err = tx.ExecContext(ctx, "INSERT INTO release_groups(id) VALUES($1::uuid)", groupID); err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO releases(id,group_id,title,artist,source_root_id,relative_directory,candidate_anchor,candidate_kind,album_artist,year,source_type,media_type,genre,catalog_number,edition,label,barcode)
				VALUES($1::uuid,$2::uuid,$3,$4,$5::uuid,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`, releaseID, groupID, candidate.Title, candidate.Artist, root.ID, candidate.Anchor.Scope, anchor, string(candidate.Anchor.Kind), candidate.AlbumArtist, releaseYear(candidate), nullableString(candidate.SourceType), nullableString(candidate.MediaType), nullableString(candidate.Genre), nullableString(candidate.CatalogNumber), nullableString(candidate.Edition), nullableString(candidate.Label), nullableString(candidate.Barcode))
		}
	} else if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE releases SET title=$1,artist=$2,relative_directory=$3,candidate_kind=$4,album_artist=$5,year=$6,source_type=$7,media_type=$8,genre=$9,catalog_number=$10,edition=$11,label=$12,barcode=$13 WHERE id=$14::uuid`, candidate.Title, candidate.Artist, candidate.Anchor.Scope, string(candidate.Anchor.Kind), candidate.AlbumArtist, releaseYear(candidate), nullableString(candidate.SourceType), nullableString(candidate.MediaType), nullableString(candidate.Genre), nullableString(candidate.CatalogNumber), nullableString(candidate.Edition), nullableString(candidate.Label), nullableString(candidate.Barcode), releaseID)
	}
	if err != nil {
		return err
	}

	discs := make([]int, 0, len(candidate.Mediums))
	for disc := range candidate.Mediums {
		discs = append(discs, disc)
	}
	sort.Ints(discs)
	for _, disc := range discs {
		var mediumID string
		err = tx.QueryRowContext(ctx, "SELECT id::text FROM media WHERE release_id=$1::uuid AND position=$2", releaseID, disc).Scan(&mediumID)
		if errors.Is(err, sql.ErrNoRows) {
			mediumID = createIdentifier()
			_, err = tx.ExecContext(ctx, "INSERT INTO media(id,release_id,position) VALUES($1::uuid,$2::uuid,$3)", mediumID, releaseID, disc)
		}
		if err != nil {
			return err
		}
		for _, organizedTrack := range candidate.Mediums[disc] {
			observation := organizedTrack.Observation
			observation.RelativePath = normalizedPath(observation.RelativePath)
			trackArtist := canonicalTrackArtist(observation.Artist)
			identity := sourceIdentityForObservation(root.ID, observation)
			var trackID string
			if isCueObservation(observation) {
				err = tx.QueryRowContext(ctx, "SELECT id::text FROM tracks WHERE source_identity IN ($1,$2,$3) ORDER BY id LIMIT 1", identity, legacySourceIdentity(root.ID, observation), legacyCueSourceIdentity(root.ID, observation)).Scan(&trackID)
			} else {
				err = tx.QueryRowContext(ctx, "SELECT id::text FROM tracks WHERE source_identity IN ($1,$2) OR (source_root_id=$3::uuid AND relative_path=$4) ORDER BY id LIMIT 1", identity, legacySourceIdentity(root.ID, observation), root.ID, observation.RelativePath).Scan(&trackID)
			}
			if errors.Is(err, sql.ErrNoRows) {
				trackID = createIdentifier()
				_, err = tx.ExecContext(ctx, `INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,source_identity,duration_seconds,codec,sample_rate,channels,bitrate,bit_depth,cue_sheet_path,cue_parent_relative_path,cue_referenced_file,cue_index_frames,cue_end_frames,cue_isrc)
					VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,$7::uuid,$8,'present',NOW(),$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)`, trackID, mediumID, organizedTrack.Position, observation.Title, trackArtist, disc, root.ID, observation.RelativePath, observation.SourceKind, identity, observation.DurationSeconds, observation.Codec, observation.SampleRate, observation.Channels, observation.Bitrate, nullablePositiveInt(observation.BitDepth), nullableString(observation.CueSheetPath), nullableString(observation.CueParentRelativePath), nullableString(observation.CueReferencedFile), nullableCueFrame(observation.CueIndexFrames, observation.CueIndexPresent), nullableCueFrame(observation.CueEndFrames, observation.CueEndPresent), nullableString(observation.CueISRC))
			} else if err == nil {
				_, err = tx.ExecContext(ctx, `UPDATE tracks SET medium_id=$1::uuid,position=$2,title=$3,artist=$4,disc_number=$5,source_status='present',observed_at=NOW(),source_kind=$6,source_identity=$7,duration_seconds=$8,codec=$9,sample_rate=$10,channels=$11,bitrate=$12,bit_depth=$13,cue_sheet_path=$14,cue_parent_relative_path=$15,cue_referenced_file=$16,cue_index_frames=$17,cue_end_frames=$18,cue_isrc=$19 WHERE id=$20::uuid`, mediumID, organizedTrack.Position, observation.Title, trackArtist, disc, observation.SourceKind, identity, observation.DurationSeconds, observation.Codec, observation.SampleRate, observation.Channels, observation.Bitrate, nullablePositiveInt(observation.BitDepth), nullableString(observation.CueSheetPath), nullableString(observation.CueParentRelativePath), nullableString(observation.CueReferencedFile), nullableCueFrame(observation.CueIndexFrames, observation.CueIndexPresent), nullableCueFrame(observation.CueEndFrames, observation.CueEndPresent), nullableString(observation.CueISRC), trackID)
			}
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "DELETE FROM track_observations WHERE track_id=$1::uuid", trackID); err != nil {
				return err
			}
			for _, field := range observation.fieldObservations() {
				if _, err = tx.ExecContext(ctx, "INSERT INTO track_observations(track_id,scan_run_id,field_name,value,source_kind,inferred,observed_at) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,NOW())", trackID, scanID, field.Name, field.Value, field.SourceKind, field.Inferred); err != nil {
					return err
				}
			}
			if _, err = tx.ExecContext(ctx, "DELETE FROM track_credits WHERE track_id=$1::uuid", trackID); err != nil {
				return err
			}
			for position, credit := range canonicalCreditObservations(observation.Credits) {
				if _, err = tx.ExecContext(ctx, "INSERT INTO track_credits(track_id,role,name,position) VALUES($1::uuid,$2,$3,$4)", trackID, credit.Role, credit.Name, position+1); err != nil {
					return err
				}
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM media WHERE release_id=$1::uuid AND NOT EXISTS (
		SELECT 1 FROM tracks WHERE tracks.medium_id=media.id)`, releaseID); err != nil {
		return err
	}

	if _, err = tx.ExecContext(ctx, "DELETE FROM release_field_decisions WHERE release_id=$1::uuid", releaseID); err != nil {
		return err
	}
	for _, decision := range candidate.Decisions {
		if _, err = tx.ExecContext(ctx, `INSERT INTO release_field_decisions(release_id,field_key,selected_value,selected_source,confidence,action,rule_id,candidates,reason,scan_run_id)
			VALUES($1::uuid,$2,to_jsonb($3::text),$4,$5,$6,$7,to_jsonb($8::text[]),$9,$10::uuid)`, releaseID, decision.Field, decision.Value, decision.Source, decision.Confidence, decision.Action, decision.RuleID, decision.Candidates, decision.Reason, scanID); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM release_credits WHERE release_id=$1::uuid", releaseID); err != nil {
		return err
	}
	if candidate.AlbumArtist != "" {
		if _, err = tx.ExecContext(ctx, "INSERT INTO release_credits(release_id,role,name,position) VALUES($1::uuid,'album_artist',$2,1)", releaseID, candidate.AlbumArtist); err != nil {
			return err
		}
	}
	refs := append([]string(nil), candidate.GroupingRefs...)
	if len(refs) == 0 {
		for _, disc := range discs {
			for _, track := range candidate.Mediums[disc] {
				refs = append(refs, normalizedPath(track.Observation.RelativePath))
			}
		}
		sort.Strings(refs)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO release_grouping_evidence(release_id,candidate_kind,rule_id,source_refs,reason,scan_run_id)
		VALUES($1::uuid,$2,$3,to_jsonb($4::text[]),$5,$6::uuid)
		ON CONFLICT(release_id) DO UPDATE SET candidate_kind=EXCLUDED.candidate_kind,rule_id=EXCLUDED.rule_id,source_refs=EXCLUDED.source_refs,reason=EXCLUDED.reason,scan_run_id=EXCLUDED.scan_run_id,observed_at=NOW()`, releaseID, candidate.Anchor.Kind, "organizer_v2", refs, strings.Join(candidate.Attention, ";"), scanID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	if artworkObservation, ok := candidateArtworkObservation(candidate); ok {
		if artworkErr := application.saveArtworkForRelease(ctx, executor, releaseID, root, artworkObservation); artworkErr != nil {
			return fmt.Errorf("%w: %v", errArtworkBinding, artworkErr)
		}
	}
	return nil
}

func canonicalCreditObservations(values []creditObservation) []creditObservation {
	seen := make(map[string]bool, len(values))
	result := make([]creditObservation, 0, len(values))
	for _, value := range values {
		value.Role = strings.ToLower(normalizeValue(value.Role))
		value.Name = normalizeValue(value.Name)
		if value.Role == "" || value.Name == "" {
			continue
		}
		key := value.Role + "\x00" + value.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Role != result[j].Role {
			return result[i].Role < result[j].Role
		}
		return result[i].Name < result[j].Name
	})
	return result
}

func candidateArtworkObservation(candidate organizedCandidate) (sourceObservation, bool) {
	discs := make([]int, 0, len(candidate.Mediums))
	for disc := range candidate.Mediums {
		discs = append(discs, disc)
	}
	sort.Ints(discs)
	for _, disc := range discs {
		for _, track := range candidate.Mediums[disc] {
			observation := track.Observation
			if len(observation.Artwork) > 0 {
				return observation, true
			}
		}
	}
	for _, disc := range discs {
		if len(candidate.Mediums[disc]) > 0 {
			return candidate.Mediums[disc][0].Observation, true
		}
	}
	return sourceObservation{}, false
}

func releaseYear(candidate organizedCandidate) any {
	// Release 核心列必须与 organizer 已保存的字段决定一致，不能在持久化层把
	// inferred 年份重新计票并覆盖权威 tag/CUE 证据。
	for _, decision := range candidate.Decisions {
		if decision.Field != "year" {
			continue
		}
		year, err := strconv.Atoi(decision.Value)
		if err == nil && year > 0 {
			return year
		}
	}
	return nil
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullablePositiveInt(value int) any {
	if value <= 0 {
		return nil
	}
	return value
}

func nullableCueFrame(value int, present bool) any {
	if !present || value < 0 {
		return nil
	}
	return value
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
	organized := sourceObservation{RelativePath: sourceIdentity.RelativePath, Directory: directoryOf(sourceIdentity.RelativePath), Title: observation.Title, Album: observation.Album, AlbumArtist: observation.AlbumArtist, Artist: observation.Artist, SourceType: observation.SourceType, MediaType: observation.MediaType, Genre: observation.Genre, CatalogNumber: observation.Catalog, Year: observation.Year, TrackNumber: observation.TrackNumber, DiscNumber: observation.DiscNumber, SourceKind: observation.SourceKind, InferredFields: observation.InferredFields, FieldSources: observationFieldSources(observation), DurationSeconds: observation.DurationSeconds, Codec: observation.Codec, BitDepth: observation.BitDepth, SampleRate: observation.SampleRate, Channels: observation.Channels, Bitrate: observation.Bitrate, Artwork: observation.Artwork, ArtworkMIME: observation.ArtworkMIME}
	if organized.Directory == "" && organized.InferredFields["album"] {
		organized.Album = ""
	}
	return application.persistOrganizedCandidates(context, executor, scanID, root, organizeObservations([]sourceObservation{organized}))
}

func (application *roomusicApplication) saveArtwork(ctx context.Context, root registeredRoot, relativePath string, observation audioObservation) error {
	return application.saveArtworkWith(ctx, application.database.connection, root, relativePath, observation)
}

func (application *roomusicApplication) saveArtworkWith(ctx context.Context, executor scanExecutor, root registeredRoot, relativePath string, observation audioObservation) error {
	var releaseID string
	if err := executor.QueryRowContext(ctx, "SELECT id::text FROM releases WHERE source_root_id=$1::uuid AND relative_directory=$2 ORDER BY id LIMIT 1", root.ID, directoryOf(relativePath)).Scan(&releaseID); err != nil {
		return err
	}
	return application.saveArtworkForRelease(ctx, executor, releaseID, root, sourceObservation{RelativePath: relativePath, Artwork: observation.Artwork, ArtworkMIME: observation.ArtworkMIME})
}

func (application *roomusicApplication) saveArtworkForRelease(ctx context.Context, executor scanExecutor, releaseID string, root registeredRoot, observation sourceObservation) error {
	relativePath := normalizedPath(observation.RelativePath)
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
	if len(data) == 0 {
		return application.removeReleaseArtwork(ctx, executor, releaseID)
	}
	if len(data) > 16<<20 {
		return fmt.Errorf("artwork too large")
	}
	detectedMIME := sniffArtworkMIME(data)
	if mimeType == "" {
		mimeType = detectedMIME
	}
	if detectedMIME == "" || mimeType != detectedMIME {
		return fmt.Errorf("unsupported artwork")
	}
	width, height := artworkDimensions(data, mimeType)
	if width <= 0 || height <= 0 {
		return fmt.Errorf("invalid artwork")
	}
	var previousKey sql.NullString
	if err := executor.QueryRowContext(ctx, "SELECT storage_key FROM release_artworks WHERE release_id=$1::uuid", releaseID).Scan(&previousKey); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	key := hash + "." + strings.TrimPrefix(strings.TrimPrefix(mimeType, "image/"), "x-")
	artworkDirectory := filepath.Join(application.config.DataDirectory, "artwork")
	if err := os.MkdirAll(artworkDirectory, 0o750); err != nil {
		return err
	}
	path := filepath.Join(artworkDirectory, key)
	needsWrite := true
	info, statErr := os.Lstat(path)
	if statErr == nil {
		if info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular() && info.Size() == int64(len(data)) {
			existing, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			needsWrite = !bytes.Equal(existing, data)
		}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return statErr
	}
	wroteManagedFile := false
	if needsWrite {
		temporary, createErr := os.CreateTemp(artworkDirectory, ".artwork-*")
		if createErr != nil {
			return createErr
		}
		temporaryPath := temporary.Name()
		defer os.Remove(temporaryPath)
		if chmodErr := temporary.Chmod(0o640); chmodErr != nil {
			temporary.Close()
			return chmodErr
		}
		if _, writeErr := temporary.Write(data); writeErr != nil {
			temporary.Close()
			return writeErr
		}
		if closeErr := temporary.Close(); closeErr != nil {
			return closeErr
		}
		if renameErr := os.Rename(temporaryPath, path); renameErr != nil {
			return renameErr
		}
		wroteManagedFile = true
	}
	_, err := executor.ExecContext(ctx, `INSERT INTO release_artworks(release_id,content_hash,mime_type,width,height,storage_key,source_type) VALUES($1::uuid,$2,$3,$4,$5,$6,$7) ON CONFLICT(release_id) DO UPDATE SET content_hash=EXCLUDED.content_hash,mime_type=EXCLUDED.mime_type,width=EXCLUDED.width,height=EXCLUDED.height,storage_key=EXCLUDED.storage_key,source_type=EXCLUDED.source_type`, releaseID, hash, mimeType, width, height, key, sourceType)
	if err != nil {
		if wroteManagedFile {
			if cleanupErr := removeUnreferencedArtwork(ctx, executor, path, key); cleanupErr != nil {
				return errors.Join(err, fmt.Errorf("remove failed artwork: %w", cleanupErr))
			}
		}
		return err
	}
	if previousKey.Valid && previousKey.String != key && filepath.Base(previousKey.String) == previousKey.String {
		if cleanupErr := removeUnreferencedArtwork(ctx, executor, filepath.Join(artworkDirectory, previousKey.String), previousKey.String); cleanupErr != nil {
			return fmt.Errorf("remove replaced artwork: %w", cleanupErr)
		}
	}
	return nil
}

func (application *roomusicApplication) removeReleaseArtwork(ctx context.Context, executor scanExecutor, releaseID string) error {
	var key sql.NullString
	err := executor.QueryRowContext(ctx, "DELETE FROM release_artworks WHERE release_id=$1::uuid RETURNING storage_key", releaseID).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if key.Valid && filepath.Base(key.String) == key.String {
		if cleanupErr := removeUnreferencedArtwork(ctx, executor, filepath.Join(application.config.DataDirectory, "artwork", key.String), key.String); cleanupErr != nil {
			return fmt.Errorf("remove released artwork: %w", cleanupErr)
		}
	}
	return nil
}

func removeUnreferencedArtwork(ctx context.Context, executor scanExecutor, path, key string) error {
	var references int
	if err := executor.QueryRowContext(ctx, "SELECT COUNT(*) FROM release_artworks WHERE storage_key=$1", key).Scan(&references); err != nil {
		return err
	}
	if references != 0 {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func artworkDimensions(data []byte, mimeType string) (int, int) {
	switch mimeType {
	case "image/png":
		if len(data) >= 24 {
			return int(binary.BigEndian.Uint32(data[16:20])), int(binary.BigEndian.Uint32(data[20:24]))
		}
	case "image/gif":
		if len(data) >= 10 {
			return int(binary.LittleEndian.Uint16(data[6:8])), int(binary.LittleEndian.Uint16(data[8:10]))
		}
	case "image/jpeg":
		for offset := 2; offset+9 < len(data); {
			if data[offset] != 0xff {
				offset++
				continue
			}
			marker := data[offset+1]
			offset += 2
			if marker == 0xd8 || marker == 0xd9 || marker == 0x01 || marker >= 0xd0 && marker <= 0xd7 {
				continue
			}
			if offset+2 > len(data) {
				break
			}
			length := int(binary.BigEndian.Uint16(data[offset : offset+2]))
			if length < 2 || offset+length > len(data) {
				break
			}
			if (marker >= 0xc0 && marker <= 0xc3 || marker >= 0xc5 && marker <= 0xc7 || marker >= 0xc9 && marker <= 0xcb || marker >= 0xcd && marker <= 0xcf) && length >= 7 {
				return int(binary.BigEndian.Uint16(data[offset+5 : offset+7])), int(binary.BigEndian.Uint16(data[offset+3 : offset+5]))
			}
			offset += length
		}
	case "image/webp":
		if len(data) < 30 {
			break
		}
		switch string(data[12:16]) {
		case "VP8X":
			width := 1 + int(data[24]) + int(data[25])<<8 + int(data[26])<<16
			height := 1 + int(data[27]) + int(data[28])<<8 + int(data[29])<<16
			return width, height
		case "VP8 ":
			if data[23] == 0x9d && data[24] == 0x01 && data[25] == 0x2a {
				return int(binary.LittleEndian.Uint16(data[26:28]) & 0x3fff), int(binary.LittleEndian.Uint16(data[28:30]) & 0x3fff)
			}
		case "VP8L":
			if data[20] == 0x2f {
				width := 1 + int(data[21]) + int(data[22]&0x3f)<<8
				height := 1 + int(data[22]>>6) + int(data[23])<<2 + int(data[24]&0x0f)<<10
				return width, height
			}
		}
	}
	return 0, 0
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
		if mime := sniffArtworkMIME(data); mime != "" {
			return data, mime, nil
		}
		if firstErr == nil {
			firstErr = fmt.Errorf("folder artwork %s has an unsupported format", name)
		}
	}
	return nil, "", firstErr
}

func sniffArtworkMIME(data []byte) string {
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return "image/jpeg"
	}
	if len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n" {
		return "image/png"
	}
	if len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a") {
		return "image/gif"
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	return ""
}

func (application *roomusicApplication) markMissing(context context.Context, scanID string, rootIDs []string) error {
	for _, rootID := range rootIDs {
		if _, err := application.database.connection.ExecContext(context, "UPDATE tracks SET source_status='missing' WHERE source_root_id=$1::uuid AND observed_at < (SELECT started_at FROM scan_runs WHERE id=$2::uuid)", rootID, scanID); err != nil {
			return err
		}
	}
	return nil
}
func (application *roomusicApplication) recordDiagnostic(context context.Context, scanID, relativePath, kind, message string) error {
	return application.recordDiagnosticWith(context, application.database.connection, scanID, relativePath, kind, message)
}

func (application *roomusicApplication) recordDiagnosticWith(context context.Context, executor scanExecutor, scanID, relativePath, kind, message string) error {
	_, err := executor.ExecContext(context, "INSERT INTO scan_diagnostics(scan_run_id,relative_path,kind,message) SELECT $1::uuid,$2,$3,$4 WHERE (SELECT COUNT(*) FROM scan_diagnostics WHERE scan_run_id=$1::uuid)<100", scanID, relativePath, kind, message)
	return err
}
