package library

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
)

// nil 范围表示全库；根条目的 nil directories 表示该根整树。
// revision 绑定发起事件时的登记记录，改址后的根不接收旧事件。
type scanTarget struct {
	removedTrees []string
	revision     int64
	directories  []string
}

type scanTargets map[string]scanTarget
type prepareScan func(context.Context, *sql.Tx) (scanTargets, error)

func albumDirectory(dir string) string {
	if ingest.IsDiscDirectory(filepath.Base(dir)) {
		return ingest.DirectoryOf(dir)
	}
	return dir
}

// 局部遍历从登记根逐段校验，避免队列中的旧目录在执行前被换成链接。
func (root registeredRoot) directoryExists(dir string) (bool, error) {
	if dir == "" {
		return true, nil
	}
	if !filepath.IsLocal(dir) || filepath.Clean(dir) != dir {
		return false, fs.ErrInvalid
	}
	excluded := firstExcludedPath(root.filter.exclusions, dir)
	var current string
	for _, name := range strings.Split(dir, "/") {
		current = filepath.Join(current, name)
		if filepath.ToSlash(current) == excluded {
			return false, nil
		}
		info, err := os.Lstat(filepath.Join(root.Path, current))
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !info.IsDir() {
			return false, nil
		}
	}
	return true, nil
}

func (root registeredRoot) walk(visit fs.WalkDirFunc) error {
	if root.directories == nil {
		return filepath.WalkDir(root.Path, visit)
	}
	for _, dir := range root.directories {
		exists, err := root.directoryExists(dir)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		base := filepath.Join(root.Path, dir)
		entries, err := os.ReadDir(base)
		if errors.Is(err, fs.ErrNotExist) && dir != "" {
			continue
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if err := visit(filepath.Join(base, entry.Name()), entry, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// CUE 所在目录和其引用的父音频目录共用一次规划。按数据库中已有的引用展开，
// 新 sheet 引用的音频仍交给现有解析器读取。
func (root registeredRoot) expandDirectories(ctx context.Context, db scanExecutor, target scanTarget) ([]string, error) {
	// 不可用时交给正式遍历记录 root_unavailable，不展开链接指向的目录。
	if !registeredRootAvailable(root.Path) {
		return slices.Clone(target.directories), nil
	}
	dirs := map[string]bool{}
	queue := slices.Clone(target.directories)
	if len(target.removedTrees) > 0 {
		rows, err := db.QueryContext(ctx, `SELECT DISTINCT regexp_replace(f.path,'(^|/)[^/]*$','')
			FROM curation_scan_files f WHERE root_id=$1::uuid
			AND EXISTS(SELECT FROM unnest($2::text[]) removed(dir) WHERE starts_with(f.path,removed.dir||'/'))`, root.ID, target.removedTrees)
		if err != nil {
			return nil, err
		}
		previous, err := readScanDirectories(rows)
		if err != nil {
			return nil, err
		}
		queue = append(queue, previous...)
	}
	for len(queue) > 0 {
		batch := []string{}
		for _, dir := range queue {
			if !dirs[dir] {
				dirs[dir] = true
				batch = append(batch, dir)
			}
		}
		if len(batch) == 0 {
			break
		}
		queue = nil
		for _, dir := range batch {
			parent := albumDirectory(dir)
			if parent != dir {
				queue = append(queue, parent)
				continue
			}
			exists, err := root.directoryExists(parent)
			if err != nil {
				return nil, err
			}
			if !exists {
				continue
			}
			entries, err := os.ReadDir(filepath.Join(root.Path, parent))
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			for _, entry := range entries {
				child := filepath.Join(parent, entry.Name())
				if entry.IsDir() && ingest.IsDiscDirectory(entry.Name()) && !root.filter.skipDir(child, entry.Name()) {
					queue = append(queue, child)
				}
			}
		}
		rows, err := db.QueryContext(ctx, `WITH refs AS (
			SELECT DISTINCT regexp_replace(cue_sheet_path,'(^|/)[^/]*$','') AS sheet,
			regexp_replace(coalesce(cue_parent_relative_path,relative_path),'(^|/)[^/]*$','') AS parent
			FROM tracks WHERE source_root_id=$1::uuid AND source_status='present' AND cue_sheet_path IS NOT NULL
		) SELECT sheet FROM refs WHERE parent=ANY($2::text[]) UNION SELECT parent FROM refs WHERE sheet=ANY($2::text[]) UNION SELECT unnest(directories) FROM local_model_groupings WHERE root_id=$1 AND directories && $2::text[]`, root.ID, batch)
		if err != nil {
			return nil, err
		}
		dependencies, err := readScanDirectories(rows)
		if err != nil {
			return nil, err
		}
		queue = append(queue, dependencies...)
	}
	return slices.Sorted(maps.Keys(dirs)), nil
}

func readScanDirectories(rows *sql.Rows) ([]string, error) {
	defer rows.Close()
	var result []string
	for rows.Next() {
		var dir string
		if err := rows.Scan(&dir); err != nil {
			return nil, err
		}
		result = append(result, dir)
	}
	return result, rows.Err()
}

func scopedRoots(ctx context.Context, db scanExecutor, roots []registeredRoot, targets scanTargets) ([]registeredRoot, error) {
	if targets == nil {
		return roots, nil
	}
	selected := []registeredRoot{}
	for _, root := range roots {
		target, ok := targets[root.ID]
		if !ok {
			continue
		}
		if root.revision != target.revision {
			return nil, errors.New("扫描目录配置已变更")
		}
		if target.directories != nil {
			dirs, err := root.expandDirectories(ctx, db, target)
			if err != nil {
				return nil, err
			}
			root.directories = dirs
		}
		selected = append(selected, root)
	}
	if len(selected) != len(targets) {
		return nil, errors.New("扫描目录已停用")
	}
	return selected, nil
}
