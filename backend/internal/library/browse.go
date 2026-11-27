package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"strings"
	"syscall"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

var (
	ErrLibraryPathNotFound = errors.New("library path not found")
	ErrInvalidLibraryPath  = errors.New("invalid library path")
)

// LibraryDirectoryEntry 是登记根下一个可见的目录项。链接只报告名字和类型。
type LibraryDirectoryEntry struct {
	Name      string                     `json:"name"`
	Type      string                     `json:"type"`
	SizeBytes *int64                     `json:"size_bytes,omitempty"`
	Owners    []catalog.LibraryFileOwner `json:"owners,omitempty"`
}

// ListLibraryDirectory 返回按名称排序的一页直接子项及过滤后的总数。
func (s *Service) ListLibraryDirectory(ctx context.Context, rootID, relative string, page, pageSize int) ([]LibraryDirectoryEntry, int, error) {
	root, filter, err := s.openBrowseRoot(ctx, rootID, relative)
	if err != nil {
		return nil, 0, err
	}
	dir, err := openBrowseDirectory(root, relative)
	if err != nil {
		return nil, 0, err
	}
	defer dir.Close()
	entries, err := fs.ReadDir(dir.FS(), ".")
	if err != nil {
		return nil, 0, ErrLibraryPathNotFound
	}
	visible := entries[:0]
	for _, entry := range entries {
		if !filter.skipDir(path.Join(relative, entry.Name()), entry.Name()) {
			visible = append(visible, entry)
		}
	}
	start := min((page-1)*pageSize, len(visible))
	selected := visible[start:min(start+pageSize, len(visible))]
	items := make([]LibraryDirectoryEntry, 0, len(selected))
	var trackPaths, attachmentPaths []string
	for _, entry := range selected {
		item := LibraryDirectoryEntry{Name: entry.Name()}
		if entry.Type()&os.ModeSymlink != 0 {
			item.Type = "link"
			items = append(items, item)
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, 0, ErrLibraryPathNotFound
		}
		size := info.Size()
		item.SizeBytes = &size
		item.Type = "other"
		if info.IsDir() {
			item.Type = "directory"
		}
		if info.Mode().IsRegular() {
			key := path.Join(relative, item.Name)
			ext := strings.ToLower(path.Ext(item.Name))
			item.Type = libraryFileType(ext)
			if isScannableAudio(ext) || ext == ".cue" {
				trackPaths = append(trackPaths, key)
			}
			if item.Type != "audio" {
				attachmentPaths = append(attachmentPaths, key)
			}
		}
		items = append(items, item)
	}
	owners, err := catalog.NewStore(s.database).LoadLibraryFileOwners(ctx, rootID, trackPaths, attachmentPaths)
	if err != nil {
		return nil, 0, err
	}
	for i := range items {
		item := &items[i]
		item.Owners = owners[path.Join(relative, item.Name)]
		if item.Type == "other" && len(item.Owners) > 0 && item.Owners[0].TrackNumber > 0 {
			item.Type = "audio"
		}
	}
	return items, len(visible), nil
}

func (s *Service) openBrowseRoot(ctx context.Context, rootID, relative string) (*os.Root, scanFilter, error) {
	if relative != "" && (relative == "." || !fs.ValidPath(relative) || strings.ContainsRune(relative, '\x00') || len(relative) > 4096) {
		return nil, scanFilter{}, ErrInvalidLibraryPath
	}
	var location string
	var encoded []byte
	err := s.database.QueryRowContext(ctx, `
SELECT r.path,to_json(ARRAY(SELECT e.relative_path FROM library_root_exclusions e WHERE e.root_id=r.id))
FROM library_roots r WHERE r.id=$1::uuid AND r.status='active'`, rootID).Scan(&location, &encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, scanFilter{}, ErrLibraryPathNotFound
	}
	if err != nil {
		return nil, scanFilter{}, err
	}
	var exclusions []string
	if err := json.Unmarshal(encoded, &exclusions); err != nil {
		return nil, scanFilter{}, err
	}
	filter := newScanFilter(exclusions)
	if PathExcluded(filter.exclusions, relative) {
		return nil, scanFilter{}, ErrLibraryPathNotFound
	}
	before, err := os.Lstat(location)
	if err != nil || !before.IsDir() || !registeredRootAvailable(location) {
		return nil, scanFilter{}, ErrLibraryPathNotFound
	}
	root, err := os.OpenRoot(location)
	if err != nil {
		return nil, scanFilter{}, ErrLibraryPathNotFound
	}
	after, err := root.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		root.Close()
		return nil, scanFilter{}, ErrLibraryPathNotFound
	}
	return root, filter, nil
}

// openBrowseDirectory 接管 root，逐层打开真实目录。使用已打开的子目录作为下次查找的根。
func openBrowseDirectory(root *os.Root, relative string) (*os.Root, error) {
	if relative == "" || relative == "." {
		return root, nil
	}
	for name := range strings.SplitSeq(relative, "/") {
		before, err := root.Lstat(name)
		if err != nil || !before.IsDir() {
			root.Close()
			return nil, ErrLibraryPathNotFound
		}
		child, err := root.OpenRoot(name)
		root.Close()
		if err != nil {
			return nil, ErrLibraryPathNotFound
		}
		after, err := child.Stat(".")
		if err != nil || !os.SameFile(before, after) {
			child.Close()
			return nil, ErrLibraryPathNotFound
		}
		root = child
	}
	return root, nil
}

func (s *Service) openBrowseFile(ctx context.Context, rootID, relative string) (*os.File, int64, error) {
	if relative == "" {
		return nil, 0, ErrInvalidLibraryPath
	}
	root, _, err := s.openBrowseRoot(ctx, rootID, relative)
	if err != nil {
		return nil, 0, err
	}
	dir, err := openBrowseDirectory(root, path.Dir(relative))
	if err != nil {
		return nil, 0, err
	}
	defer dir.Close()
	name := path.Base(relative)
	before, err := dir.Lstat(name)
	if err != nil || !before.Mode().IsRegular() {
		return nil, 0, ErrLibraryPathNotFound
	}
	// 打开后核对句柄。非阻塞标记避免路径被换成管道时卡住。
	file, err := dir.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, 0, ErrLibraryPathNotFound
	}
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		file.Close()
		return nil, 0, ErrLibraryPathNotFound
	}
	return file, after.Size(), nil
}
