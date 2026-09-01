package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type registeredRoot struct{ ID, Path string }

type trackSourceIdentity struct {
	RootID       string
	RelativePath string
}

func createTrackSourceIdentity(rootID, relativePath string) (trackSourceIdentity, error) {
	normalizedPath := filepath.ToSlash(filepath.Clean(relativePath))
	if normalizedPath == "." || filepath.IsAbs(relativePath) || normalizedPath == ".." || strings.HasPrefix(normalizedPath, "../") {
		return trackSourceIdentity{}, fmt.Errorf("invalid source relative path")
	}
	return trackSourceIdentity{RootID: rootID, RelativePath: normalizedPath}, nil
}

type scanOutcome struct {
	Complete bool
	Canceled bool
	Failed   bool
}

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

func (application *roomusicApplication) runScan(scanID string) {
	scanContext := context.Background()
	defer func() {
		application.scanMutex.Lock()
		application.runningScan = ""
		application.scanMutex.Unlock()
	}()
	roots, loadError := application.loadRegisteredRoots(scanContext)
	outcome := scanOutcome{Complete: loadError == nil, Failed: loadError != nil}
	rootIDs := make([]string, 0, len(roots))
	for _, root := range roots {
		rootIDs = append(rootIDs, root.ID)
		if application.scanRoot(scanContext, scanID, root) != nil {
			outcome.Complete = false
		}
	}
	status := terminalScanStatus(outcome)
	if scanStatusAllowsMissingReconciliation(status) {
		if reconciliationError := application.markMissing(scanContext, scanID, rootIDs); reconciliationError != nil {
			outcome.Complete = false
			outcome.Failed = true
			if diagnosticError := application.recordDiagnostic(scanContext, scanID, "", "reconciliation_failure", "无法完成缺失来源对账"); diagnosticError != nil {
				outcome.Failed = true
			}
		}
	}
	if loadError != nil {
		if diagnosticError := application.recordDiagnostic(scanContext, scanID, "", "database_error", "无法读取已注册目录"); diagnosticError != nil {
			outcome.Failed = true
		}
	}
	status = terminalScanStatus(outcome)
	if _, updateError := application.database.connection.ExecContext(scanContext, "UPDATE scan_runs SET status=$1,finished_at=$2 WHERE id=$3::uuid", status, time.Now().UTC(), scanID); updateError != nil {
		application.logger.Error("scan terminal status persistence failed", "event", "library.scan.terminal_write_failed", "scan_id", scanID, "error", updateError)
	}
}

func (application *roomusicApplication) loadRegisteredRoots(context context.Context) ([]registeredRoot, error) {
	rows, err := application.database.connection.QueryContext(context, "SELECT id::text,path FROM library_roots ORDER BY path,id")
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

func (application *roomusicApplication) scanRoot(context context.Context, scanID string, root registeredRoot) error {
	rootInfo, err := os.Stat(root.Path)
	if err != nil || !rootInfo.IsDir() {
		if diagnosticError := application.recordDiagnostic(context, scanID, "", "root_unavailable", "注册目录当前不可用"); diagnosticError != nil {
			return fmt.Errorf("record unavailable root diagnostic: %w", diagnosticError)
		}
		return errors.New("root unavailable")
	}
	complete := true
	err = filepath.WalkDir(root.Path, func(path string, entry fs.DirEntry, walkError error) error {
		if contextErr := context.Err(); contextErr != nil {
			complete = false
			return contextErr
		}
		if walkError != nil {
			complete = false
			if diagnosticError := application.recordDiagnostic(context, scanID, safeRelativePath(root.Path, path), "permission_error", "无法读取文件"); diagnosticError != nil {
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
				if diagnosticError := application.recordDiagnostic(context, scanID, safeRelativePath(root.Path, path), "broken_or_escaped_link", "文件链接目标不可读取"); diagnosticError != nil {
					return diagnosticError
				}
				return nil
			}
		}
		relativePath := safeRelativePath(root.Path, path)
		extension := strings.ToLower(filepath.Ext(path))
		if extension == ".cue" {
			tracks, referenced, cueErr := parseCue(path)
			if cueErr != nil {
				complete = false
				application.recordDiagnostic(context, scanID, relativePath, "unsupported_cue", cueErr.Error())
				return nil
			}
			audioPath := filepath.Join(filepath.Dir(path), referenced)
			if !isWithin(root.Path, audioPath) {
				complete = false
				application.recordDiagnostic(context, scanID, relativePath, "unsupported_cue", "CUE 引用越界")
				return nil
			}
			base, parseErr := parseAudioFile(audioPath)
			if parseErr != nil {
				complete = false
				application.recordDiagnostic(context, scanID, relativePath, "parse_failure", "CUE 引用音频解析失败")
				return nil
			}
			for _, cue := range tracks {
				o := base
				o.SourceKind = "cue_virtual"
				o.TrackNumber = cue.Number
				if cue.Title != "" {
					o.Title = cue.Title
				}
				if cue.Artist != "" {
					o.Artist = cue.Artist
				}
				// Include the normalized referenced file so changing FILE cannot reuse an old identity.
				referencedIdentity := filepath.ToSlash(filepath.Clean(referenced))
				virtualPath := relativePath + "#track-" + strconv.Itoa(cue.Number) + "@" + referencedIdentity
				if saveErr := application.saveObservation(context, scanID, root, virtualPath, o); saveErr != nil {
					complete = false
					application.recordDiagnostic(context, scanID, virtualPath, "catalog_write_failure", "无法保存 CUE 虚拟音轨")
				}
			}
			return nil
		}
		if extension != ".flac" && extension != ".mp3" && extension != ".ogg" && extension != ".opus" && extension != ".wav" {
			if diagnosticError := application.recordDiagnostic(context, scanID, relativePath, "unsupported_format", "不支持的音频格式"); diagnosticError != nil {
				return diagnosticError
			}
			return nil
		}
		observation, parseError := parseAudioFile(path)
		if parseError != nil {
			complete = false
			if diagnosticError := application.recordDiagnostic(context, scanID, relativePath, "parse_failure", "音频文件解析失败"); diagnosticError != nil {
				return diagnosticError
			}
			return nil
		}
		if saveError := application.saveObservation(context, scanID, root, relativePath, observation); saveError != nil {
			complete = false
			if diagnosticError := application.recordDiagnostic(context, scanID, relativePath, "catalog_write_failure", "无法保存音频观察"); diagnosticError != nil {
				return diagnosticError
			}
		}
		if artworkError := application.saveArtwork(context, root, relativePath, observation); artworkError != nil {
			application.recordDiagnostic(context, scanID, relativePath, "artwork_failure", "封面处理失败")
		}
		return nil
	})
	if err != nil {
		complete = false
	}
	if !complete {
		return errors.New("incomplete root")
	}
	return nil
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
	sourceIdentity, err := createTrackSourceIdentity(root.ID, relativePath)
	if err != nil {
		return err
	}
	directory := filepath.ToSlash(filepath.Dir(sourceIdentity.RelativePath))
	if directory == "." {
		directory = ""
	}
	transaction, err := application.database.connection.BeginTx(context, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	var releaseID, mediumID, trackID string
	err = transaction.QueryRowContext(context, "SELECT id::text FROM releases WHERE source_root_id=$1::uuid AND relative_directory=$2", root.ID, directory).Scan(&releaseID)
	if err == sql.ErrNoRows {
		groupID := createIdentifier()
		releaseID, mediumID = createIdentifier(), createIdentifier()
		if _, err = transaction.ExecContext(context, "INSERT INTO release_groups(id) VALUES($1::uuid)", groupID); err == nil {
			_, err = transaction.ExecContext(context, "INSERT INTO releases(id,group_id,title,artist,source_root_id,relative_directory) VALUES($1::uuid,$2::uuid,$3,$4,$5::uuid,$6)", releaseID, groupID, observation.Album, observation.Artist, root.ID, directory)
		}
		if err == nil {
			_, err = transaction.ExecContext(context, "INSERT INTO media(id,release_id,position) VALUES($1::uuid,$2::uuid,1)", mediumID, releaseID)
		}
	} else if err == nil {
		err = transaction.QueryRowContext(context, "SELECT id::text FROM media WHERE release_id=$1::uuid ORDER BY position LIMIT 1", releaseID).Scan(&mediumID)
	}
	if err != nil {
		return err
	}
	err = transaction.QueryRowContext(context, "SELECT id::text FROM tracks WHERE source_root_id=$1::uuid AND relative_path=$2", sourceIdentity.RootID, sourceIdentity.RelativePath).Scan(&trackID)
	if err == sql.ErrNoRows {
		trackID = createIdentifier()
		_, err = transaction.ExecContext(context, "INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,$7::uuid,$8,'present',NOW())", trackID, mediumID, observation.TrackNumber, observation.Title, observation.Artist, observation.DiscNumber, sourceIdentity.RootID, sourceIdentity.RelativePath)
	} else if err == nil {
		_, err = transaction.ExecContext(context, "UPDATE tracks SET medium_id=$1::uuid,position=$2,title=$3,artist=$4,disc_number=$5,source_status='present',observed_at=NOW() WHERE id=$6::uuid", mediumID, observation.TrackNumber, observation.Title, observation.Artist, observation.DiscNumber, trackID)
	}
	if err == nil {
		for _, field := range observation.fieldObservations() {
			_, err = transaction.ExecContext(context, "INSERT INTO track_observations(track_id,scan_run_id,field_name,value,source_kind,inferred,observed_at) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,NOW())", trackID, scanID, field.Name, field.Value, field.SourceKind, field.Inferred)
			if err != nil {
				break
			}
		}
	}
	if err != nil {
		return fmt.Errorf("save observation: %w", err)
	}
	return transaction.Commit()
}

func (application *roomusicApplication) saveArtwork(ctx context.Context, root registeredRoot, relativePath string, observation audioObservation) error {
	directory := filepath.Dir(filepath.Join(root.Path, filepath.FromSlash(relativePath)))
	data, mimeType := observation.Artwork, observation.ArtworkMIME
	sourceType := "embedded"
	if len(data) == 0 {
		sourceType = "folder"
		data, mimeType = discoverFolderArtwork(directory)
	}
	if len(data) == 0 {
		return nil
	}
	if len(data) > 16<<20 {
		return fmt.Errorf("artwork too large")
	}
	if mimeType == "" {
		mimeType = sniffArtworkMIME(data)
	}
	if mimeType == "" {
		return fmt.Errorf("unsupported artwork")
	}
	width, height := artworkDimensions(data, mimeType)
	if width <= 0 || height <= 0 {
		return fmt.Errorf("invalid artwork")
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	key := hash + "." + strings.TrimPrefix(strings.TrimPrefix(mimeType, "image/"), "x-")
	if err := os.MkdirAll(filepath.Join(application.config.DataDirectory, "artwork"), 0o750); err != nil {
		return err
	}
	path := filepath.Join(application.config.DataDirectory, "artwork", key)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.WriteFile(path, data, 0o640); err != nil {
			return err
		}
	}
	var releaseID string
	if err := application.database.connection.QueryRowContext(ctx, "SELECT id::text FROM releases WHERE source_root_id=$1::uuid AND relative_directory=$2", root.ID, filepath.ToSlash(filepath.Dir(relativePath))).Scan(&releaseID); err != nil {
		return err
	}
	_, err := application.database.connection.ExecContext(ctx, `INSERT INTO release_artworks(release_id,content_hash,mime_type,width,height,storage_key,source_type) VALUES($1::uuid,$2,$3,$4,$5,$6,$7) ON CONFLICT(release_id) DO UPDATE SET content_hash=EXCLUDED.content_hash,mime_type=EXCLUDED.mime_type,width=EXCLUDED.width,height=EXCLUDED.height,storage_key=EXCLUDED.storage_key,source_type=EXCLUDED.source_type`, releaseID, hash, mimeType, width, height, key, sourceType)
	return err
}

func artworkDimensions(data []byte, mimeType string) (int, int) {
	if mimeType == "image/png" && len(data) >= 24 {
		return int(binary.BigEndian.Uint32(data[16:20])), int(binary.BigEndian.Uint32(data[20:24]))
	}
	if mimeType == "image/gif" && len(data) >= 10 {
		return int(binary.LittleEndian.Uint16(data[6:8])), int(binary.LittleEndian.Uint16(data[8:10]))
	}
	return 1, 1
}

func discoverFolderArtwork(directory string) ([]byte, string) {
	for _, name := range []string{"cover.jpg", "cover.jpeg", "cover.png", "cover.webp", "folder.jpg", "folder.jpeg", "folder.png", "front.jpg", "front.jpeg", "front.png"} {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			continue
		}
		if mime := sniffArtworkMIME(data); mime != "" {
			return data, mime
		}
	}
	return nil, ""
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
	_, err := application.database.connection.ExecContext(context, "INSERT INTO scan_diagnostics(scan_run_id,relative_path,kind,message) SELECT $1::uuid,$2,$3,$4 WHERE (SELECT COUNT(*) FROM scan_diagnostics WHERE scan_run_id=$1::uuid)<100", scanID, relativePath, kind, message)
	return err
}
