package backup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const FileNamePattern = `^roomusic-[0-9]{8}-[0-9]{6}(-manual|-pre-migration-[1-9][0-9]{0,9}|-pre-restore)?\.dump$`

var fileNamePattern = regexp.MustCompile(FileNamePattern)
var partialPattern = regexp.MustCompile(`^\.roomusic-.*\.partial\..+$`)

func parseName(name string) (Source, int64, bool) {
	if !fileNamePattern.MatchString(name) {
		return "", 0, false
	}
	switch {
	case strings.HasSuffix(name, "-manual.dump"):
		return SourceManual, 0, true
	case strings.HasSuffix(name, "-pre-restore.dump"):
		return SourcePreRestore, 0, true
	case strings.Contains(name, "-pre-migration-"):
		version, _ := strconv.ParseInt(strings.TrimSuffix(strings.SplitN(name, "-pre-migration-", 2)[1], ".dump"), 10, 64)
		return SourcePreMigration, version, true
	default:
		return SourceScheduled, 0, true
	}
}
func fileName(at time.Time, source Source, version int64) string {
	suffix := ""
	switch source {
	case SourceManual:
		suffix = "-manual"
	case SourcePreRestore:
		suffix = "-pre-restore"
	case SourcePreMigration:
		suffix = fmt.Sprintf("-pre-migration-%d", version)
	}
	return "roomusic-" + at.Format("20060102-150405") + suffix + ".dump"
}
func (s *Service) listFiles() ([]File, error) {
	entries, err := os.ReadDir(s.settings.Directory)
	if errors.Is(err, os.ErrNotExist) {
		return []File{}, nil
	}
	if err != nil {
		return nil, failure("directory", "not_writable")
	}
	files := make([]File, 0, len(entries))
	for _, entry := range entries {
		source, _, valid := parseName(entry.Name())
		if !valid {
			continue
		}
		info, err := os.Lstat(filepath.Join(s.settings.Directory, entry.Name()))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, File{Name: entry.Name(), Source: source, CreatedAt: info.ModTime().UTC(), SizeBytes: info.Size()})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].CreatedAt.Equal(files[j].CreatedAt) {
			return files[i].Name > files[j].Name
		}
		return files[i].CreatedAt.After(files[j].CreatedAt)
	})
	return files, nil
}
func (s *Service) directoryStatus() string {
	info, err := os.Stat(s.settings.Directory)
	if errors.Is(err, os.ErrNotExist) {
		return "missing"
	}
	if err != nil || !info.IsDir() || unix.Access(s.settings.Directory, unix.W_OK|unix.X_OK) != nil {
		return "not_writable"
	}
	return "ready"
}
func (s *Service) OpenBackup(name string) (*os.File, fs.FileInfo, error) {
	if !fileNamePattern.MatchString(name) {
		return nil, nil, ErrNotFound
	}
	root, err := os.OpenRoot(s.settings.Directory)
	if err != nil {
		return nil, nil, ErrNotFound
	}
	defer root.Close()
	before, err := root.Lstat(name)
	if err != nil || !before.Mode().IsRegular() {
		return nil, nil, ErrNotFound
	}
	file, err := root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, ErrNotFound
	}
	info, err := file.Stat()
	// O_NOFOLLOW 拒绝符号链接，O_NONBLOCK 防止竞争替换为 FIFO 后阻塞；再次核对同一普通文件。
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(before, info) {
		file.Close()
		return nil, nil, ErrNotFound
	}
	return file, info, nil
}

// DeleteBackup 只删除已发布的普通备份文件；actorID 只用于日志。
func (s *Service) DeleteBackup(ctx context.Context, name, actorID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !fileNamePattern.MatchString(name) {
		return ErrNotFound
	}
	root, err := os.OpenRoot(s.settings.Directory)
	if err != nil {
		err = deletionError(err)
		s.logDeletion(name, actorID, err)
		return err
	}
	defer root.Close()
	err = deleteIn(root, name)
	if err == nil {
		_ = syncDirectory(s.settings.Directory)
	}
	s.logDeletion(name, actorID, err)
	return err
}

// DeleteBackups 按请求顺序去重，只打开一次目录，分别返回删除、缺失和失败名单。
func (s *Service) DeleteBackups(ctx context.Context, names []string, actorID string) (DeleteResult, error) {
	result := DeleteResult{Deleted: []string{}, Missing: []string{}, Failed: []string{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	root, openErr := os.OpenRoot(s.settings.Directory)
	if openErr == nil {
		defer root.Close()
	}
	// 即使请求中途取消，也刷盘已经成功删除的目录项。
	defer func() {
		if len(result.Deleted) > 0 {
			_ = syncDirectory(s.settings.Directory)
		}
	}()
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		var err error
		if openErr != nil {
			err = deletionError(openErr)
		} else {
			err = deleteIn(root, name)
		}
		switch {
		case err == nil:
			result.Deleted = append(result.Deleted, name)
		case errors.Is(err, ErrNotFound):
			result.Missing = append(result.Missing, name)
		default:
			result.Failed = append(result.Failed, name)
		}
		s.logDeletion(name, actorID, err)
	}
	return result, nil
}

func deleteIn(root *os.Root, name string) error {
	if !fileNamePattern.MatchString(name) {
		return ErrNotFound
	}
	info, err := root.Lstat(name)
	if err != nil {
		return deletionError(err)
	}
	if !info.Mode().IsRegular() {
		return ErrNotFound
	}
	return deletionError(root.Remove(name))
}

func deletionError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return ErrNotWritable
}

func (s *Service) logDeletion(name, actorID string, err error) {
	if err == nil {
		s.logger.Info("已删除备份", "event", "backup.deleted", "file", name, "actor_id", actorID)
	} else if errors.Is(err, ErrNotWritable) {
		// 目录打不开时批量请求全部归入失败；非法输入不能作为路径写入日志。
		if !fileNamePattern.MatchString(name) {
			name = ""
		}
		s.logger.Warn("无法删除备份", "event", "backup.delete_failed", "file", name, "reason", "not_writable")
	}
}

func (s *Service) publish(ctx context.Context, partial *os.File, source Source, version int64) (File, error) {
	if err := partial.Sync(); err != nil {
		return File{}, failure("publish", "other")
	}
	for {
		if ctx.Err() != nil {
			return File{}, failure("publish", "killed")
		}
		now := s.now()
		name := fileName(now.In(s.settings.Location), source, version)
		if !fileNamePattern.MatchString(name) {
			return File{}, failure("publish", "invalid_name")
		}
		target := filepath.Join(s.settings.Directory, name)
		if err := os.Link(partial.Name(), target); err != nil {
			if !errors.Is(err, os.ErrExist) {
				return File{}, failure("publish", "other")
			}
			timer := time.NewTimer(time.Second - time.Duration(now.Nanosecond()))
			select {
			case <-ctx.Done():
				timer.Stop()
				return File{}, failure("publish", "killed")
			case <-timer.C:
				continue
			}
		}
		if err := os.Remove(partial.Name()); err != nil {
			_ = os.Remove(target)
			return File{}, failure("publish", "other")
		}
		if err := syncDirectory(s.settings.Directory); err != nil {
			_ = os.Remove(target)
			return File{}, failure("publish", "other")
		}
		info, err := partial.Stat()
		if err != nil {
			_ = os.Remove(target)
			return File{}, failure("publish", "other")
		}
		return File{Name: name, Source: source, CreatedAt: info.ModTime().UTC(), SizeBytes: info.Size()}, nil
	}
}
func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
func (s *Service) cleanPartials() {
	entries, err := os.ReadDir(s.settings.Directory)
	if err != nil {
		return
	}
	cutoff := s.now().Add(-24 * time.Hour)
	for _, entry := range entries {
		if partialPattern.MatchString(entry.Name()) {
			info, err := entry.Info()
			if err == nil && info.Mode().IsRegular() && info.ModTime().Before(cutoff) {
				_ = os.Remove(filepath.Join(s.settings.Directory, entry.Name()))
			}
		}
	}
}
func (s *Service) LatestPreMigrationBackup(version int64) (string, bool) {
	files, err := s.listFiles()
	if err != nil {
		return "", false
	}
	for _, file := range files {
		source, v, _ := parseName(file.Name)
		if source == SourcePreMigration && v == version {
			return file.Name, true
		}
	}
	return "", false
}
