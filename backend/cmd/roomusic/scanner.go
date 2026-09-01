package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
		if extension != ".flac" && extension != ".mp3" {
			if diagnosticError := application.recordDiagnostic(context, scanID, relativePath, "unsupported_format", "仅支持 FLAC 和 MP3"); diagnosticError != nil {
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
