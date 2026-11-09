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

// sourceAudioPath 与后台分析共用来源定位；虚拟路径不能作为音频文件打开。
func sourceAudioPath(kind, relative string, cueParent *string) (string, error) {
	if kind != "cue_virtual" {
		return relative, nil
	}
	if cueParent == nil || *cueParent == "" {
		return "", fmt.Errorf("%w: scan record has no CUE parent", ErrSourceChanged)
	}
	return *cueParent, nil
}

// openVerified 在扫描快照核对后检查容器；rootPath 必须已经通过允许根校验。
func openVerified(rootPath, relativePath string, expected expectedSource, kind container, trackID string) (*openedSource, error) {
	opened, err := openScannedSource(rootPath, relativePath, expected, trackID)
	if err != nil {
		return nil, err
	}
	matches, err := matchesContainer(opened.File, opened.Size, kind)
	if err != nil {
		_ = opened.File.Close()
		return nil, fmt.Errorf("%w: read file header: %w", ErrSourceUnavailable, err)
	}
	if !matches {
		_ = opened.File.Close()
		return nil, fmt.Errorf("%w: container does not match the format", ErrSourceChanged)
	}
	return opened, nil
}

// openScannedSource 与分析工作者共用安全打开和扫描快照核对，不要求分析输入在播放格式表内。
// os.Root 拒绝逃逸，O_NONBLOCK 防止 FIFO 阻塞；打开后只对句柄检查，不再次按路径访问。
func openScannedSource(rootPath, relativePath string, expected expectedSource, trackID string) (*openedSource, error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("%w: open root: %w", ErrSourceUnavailable, err)
	}
	defer root.Close()
	file, err := root.OpenFile(relativePath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: open file: %w", ErrSourceUnavailable, err)
	}
	opened, err := verifyScanSnapshot(file, expected, trackID)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return opened, nil
}

func verifyScanSnapshot(file *os.File, expected expectedSource, trackID string) (*openedSource, error) {
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
	return &openedSource{File: file, Size: info.Size(), ETag: entityTagValue(trackID, expected, info.Size())}, nil
}

// entityTagValue 返回强校验值的不透明部分：SHA-256(曲目 | 内容指纹 | 大小 | 修改时间纳秒) 的前 32 位十六进制。
// 文件变化会先被 ErrSourceChanged 拒绝，所以同一个值对应同一份字节；只用曲目 ID 或旧修改时间拼接不同版本的分段是不允许的。
func entityTagValue(trackID string, expected expectedSource, size int64) string {
	digest := sha256.Sum256([]byte(trackID + "|" + expected.ContentFingerprint + "|" + strconv.FormatInt(size, 10) + "|" + strconv.FormatInt(expected.ModifiedAt.UnixNano(), 10)))
	return hex.EncodeToString(digest[:])[:32]
}
