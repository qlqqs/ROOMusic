package playback

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"
	"time"
)

var (
	// ErrSourceUnavailable 表示文件无法读取：缺失、权限不足、不是普通文件、链接逃出根目录，或根目录不再被配置允许。
	ErrSourceUnavailable = errors.New("playback source unavailable")
	// ErrSourceChanged 表示文件与扫描记录不符：大小或修改时间不同、记录为空，或文件头与格式表中的容器不符。需要重新扫描。
	ErrSourceChanged = errors.New("playback source changed since the last scan")
)

// expectedSource 是扫描时记录的文件版本。
type expectedSource struct {
	Size               *int64
	ModifiedAt         *time.Time
	ContentFingerprint string
}

// openedSource 是已通过全部核对的只读句柄。调用方负责关闭 File。
type openedSource struct {
	File *os.File
	Size int64
	// ETag 是强校验值的不透明部分（不含引号）：同一个曲目、内容指纹、大小与修改时间必然得到同一个值。
	ETag string
}

// openVerified 在 rootPath 内只读打开 relativePath，并核对它仍是扫描时的那个文件。rootPath 必须已经通过允许根校验。
//
// 安全性依赖三点：os.Root 拒绝 ".." 与逃出根目录的符号链接；O_NONBLOCK 让检查之后被换成 FIFO 的路径也不会阻塞打开；
// 之后只对已打开的句柄做 fstat 与读取，不再按路径访问，所以“检查后替换”不能让读取越界。源文件始终只读。
func openVerified(rootPath, relativePath string, expected expectedSource, kind container, trackID string) (*openedSource, error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("%w: open root: %w", ErrSourceUnavailable, err)
	}
	defer root.Close()
	file, err := root.OpenFile(relativePath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: open file: %w", ErrSourceUnavailable, err)
	}
	opened, err := verifyHandle(file, expected, kind, trackID)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return opened, nil
}

func verifyHandle(file *os.File, expected expectedSource, kind container, trackID string) (*openedSource, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("%w: stat file: %w", ErrSourceUnavailable, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: not a regular file", ErrSourceUnavailable)
	}
	// 严格版本核对：大小与修改时间（截到微秒，与数据库的 TIMESTAMPTZ 一致）都必须等于扫描记录。
	if expected.Size == nil || expected.ModifiedAt == nil {
		return nil, fmt.Errorf("%w: scan record has no size or modification time", ErrSourceChanged)
	}
	if info.Size() != *expected.Size || !info.ModTime().UTC().Truncate(time.Microsecond).Equal(expected.ModifiedAt.UTC()) {
		return nil, fmt.Errorf("%w: size or modification time differs", ErrSourceChanged)
	}
	matches, err := matchesContainer(file, info.Size(), kind)
	if err != nil {
		return nil, fmt.Errorf("%w: read file header: %w", ErrSourceUnavailable, err)
	}
	if !matches {
		return nil, fmt.Errorf("%w: container does not match the format", ErrSourceChanged)
	}
	return &openedSource{File: file, Size: info.Size(), ETag: entityTagValue(trackID, expected, info.Size())}, nil
}

// entityTagValue 返回强校验值的不透明部分：SHA-256(曲目 | 内容指纹 | 大小 | 修改时间纳秒) 的前 32 位十六进制。
// 文件变化会先被 ErrSourceChanged 拒绝，所以同一个值对应同一份字节；只用曲目 ID 或旧修改时间拼接不同版本的分段是不允许的。
func entityTagValue(trackID string, expected expectedSource, size int64) string {
	digest := sha256.Sum256([]byte(trackID + "|" + expected.ContentFingerprint + "|" + strconv.FormatInt(size, 10) + "|" + strconv.FormatInt(expected.ModifiedAt.UnixNano(), 10)))
	return hex.EncodeToString(digest[:])[:32]
}
