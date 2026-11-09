package app

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/platform/config"
)

// mediaCacheDirectoryName 是派生缓存在数据目录下的子目录名。
const mediaCacheDirectoryName = "media-cache"

// RemoveLegacyMediaCache 在启动时清除旧派生缓存；目录不存在时不操作，也不记录删除日志。
// 删除只限数据目录内部，并继续要求缓存与所有允许的音乐根互不包含。
func RemoveLegacyMediaCache(settings config.Server, logger *slog.Logger) error {
	directory := filepath.Join(settings.DataDirectory, mediaCacheDirectoryName)
	if _, err := os.Lstat(directory); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat media cache directory: %w", err)
	}
	data, err := resolveForCreate(settings.DataDirectory)
	if err != nil {
		return fmt.Errorf("resolve data directory: %w", err)
	}
	cache, err := resolveForCreate(directory)
	if err != nil {
		return fmt.Errorf("resolve media cache directory: %w", err)
	}
	if cache == data || !isWithin(data, cache) {
		return fmt.Errorf("media cache directory must remain inside ROOMUSIC_DATA_DIR")
	}
	if err := checkCacheDirectoryIsolated(directory, settings.AllowedLibraryRoots); err != nil {
		return err
	}
	var bytes int64
	if err := filepath.WalkDir(directory, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// 不跟随符号链接，也不把目录元数据计入缓存文件字节数。
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			bytes += info.Size()
		}
		return nil
	}); err != nil {
		return fmt.Errorf("measure media cache: %w", err)
	}
	if err := os.RemoveAll(directory); err != nil {
		return fmt.Errorf("remove media cache: %w", err)
	}
	logger.Info("旧派生缓存已删除", "event", "playback.derivation.cache_removed", "module", "playback", "bytes", bytes)
	return nil
}

// checkCacheDirectoryIsolated 要求旧缓存目录与每个允许的音乐根互不包含，避免删除音乐文件；
// 比较用真实路径（符号链接已解析），尚不存在的部分接在最深的已存在祖先之后。
func checkCacheDirectoryIsolated(directory string, allowedRoots []string) error {
	cache, err := resolveForCreate(directory)
	if err != nil {
		return fmt.Errorf("resolve media cache directory: %w", err)
	}
	for _, configured := range allowedRoots {
		root, err := resolveForCreate(configured)
		if err != nil {
			// 解析不了的根下不会有可播放的曲目；退回字面路径，仍然比较重叠。
			if root, err = filepath.Abs(configured); err != nil {
				continue
			}
		}
		if isWithin(root, cache) || isWithin(cache, root) {
			return fmt.Errorf("media cache directory %q and library root %q must not contain each other; set ROOMUSIC_DATA_DIR outside the music library", cache, root)
		}
	}
	return nil
}

// resolveForCreate 返回 path 的规范绝对路径，允许末尾若干层尚不存在：对最深的已存在祖先解析符号链接，再接上其余部分。
func resolveForCreate(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	var missing []string
	for current := absolute; ; {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return resolved, nil
		}
		parent := filepath.Dir(current)
		if !errors.Is(err, fs.ErrNotExist) || parent == current {
			return "", err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

// isWithin 判断 child 是否等于 parent 或位于其内（两者都是已清理的绝对路径）。
func isWithin(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
