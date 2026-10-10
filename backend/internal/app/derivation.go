package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/playback/derivation"
)

// mediaCacheDirectoryName 是派生缓存在数据目录下的子目录名。
const mediaCacheDirectoryName = "media-cache"

// OpenDerivation 准备 ALAC 的无损派生：先核对缓存目录与允许的音乐根互不包含，再打开缓存、定位并自检 ffmpeg。
// 返回 (管理器, nil) 表示派生可用，调用方用 WithDerivation 装配并在停机时 Close。
// ffmpeg 找不到或自检不过不是错误：记录 playback.derivation.unavailable（只含原因类别）并返回 (nil, nil)，
// 应用照常启动，ALAC 保持“不能直接播放”。缓存目录与音乐根重叠、或缓存目录无法建立是部署配置错误，返回错误，调用方应拒绝启动。
func OpenDerivation(ctx context.Context, settings config.Server, logger *slog.Logger) (*derivation.Manager, error) {
	directory := filepath.Join(settings.DataDirectory, mediaCacheDirectoryName)
	if err := checkCacheDirectoryIsolated(directory, settings.AllowedLibraryRoots); err != nil {
		return nil, err
	}
	manager, err := derivation.Open(ctx, derivation.Config{
		Dir: directory, MaxBytes: settings.MediaCacheMaxBytes, FFmpegPath: settings.FFmpegPath, Logger: logger,
	})
	var unavailable *derivation.UnavailableError
	if errors.As(err, &unavailable) {
		logger.Warn("playback derivation unavailable", "event", "playback.derivation.unavailable", "reason", unavailable.Reason)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open media cache: %w", err)
	}
	stats := manager.Stats()
	logger.Info("playback derivation ready", "event", "playback.derivation.ready",
		"ffmpeg_version", stats.Version, "max_bytes", stats.MaxBytes, "entries", stats.Entries, "bytes", stats.Bytes)
	return manager, nil
}

// checkCacheDirectoryIsolated 要求派生缓存目录与每个允许的音乐根互不包含（ADR 0004：派生文件只写入与音乐根隔离的受管目录）。
// 缓存目录通常还不存在，而在音乐根内创建目录本身就违背“不写音乐目录”，所以先比较再创建；
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
