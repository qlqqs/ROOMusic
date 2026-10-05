package playback

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/library"
)

var (
	// ErrTrackNotFound 表示曲目不存在，或它的来源已不是 present。
	ErrTrackNotFound = errors.New("playback track not found")
	// ErrRootDisabled 表示曲目所在的登记根目录已停用；停用目录同时停止播放，专辑仍可浏览。
	ErrRootDisabled = errors.New("playback root disabled")
	// ErrNotDirectlyPlayable 表示曲目不能原样直出：CUE 虚拟轨（需要切分），或（来源类别、编码）不在格式表中。
	ErrNotDirectlyPlayable = errors.New("playback track is not directly playable")
	// ErrDatabase 表示核对所需的数据库读取失败；调用方应报告服务暂不可用，不能当作曲目或来源的问题。
	ErrDatabase = errors.New("playback database unavailable")
)

// Service 是直出播放用例：把曲目 ID 解析成一个已核对的只读文件句柄。
// 它只读取 catalog 与 library 的公开合同，不直接读它们的表。
type Service struct {
	sources      *catalog.Store
	database     library.RootQuerier
	allowedRoots []string
}

// NewService 返回服务。allowedRoots 是配置允许的音乐根目录，每次打开都会重新核对登记的根目录仍在其中。
func NewService(database catalog.Queryer, allowedRoots []string) *Service {
	return &Service{sources: catalog.NewStore(database), database: database, allowedRoots: allowedRoots}
}

// Prepared 是通过全部核对的一次直出：File 是已打开的只读句柄，调用方负责关闭。
type Prepared struct {
	TrackID string
	RootID  string
	Format  Format
	File    *os.File
	Size    int64
	// ETag 是强校验值的不透明部分（不含引号）。
	ETag string
}

// Prepare 按固定顺序核对并打开曲目的原文件：曲目存在且 present → 目录仍启用 → 在格式表中 →
// 目录仍在配置允许范围内 → 安全打开普通文件 → 大小与修改时间等于扫描记录 → 文件头与格式相符。
// 错误是 ErrTrackNotFound、ErrRootDisabled、ErrNotDirectlyPlayable、ErrSourceUnavailable、ErrSourceChanged 或 ErrDatabase 之一。
func (service *Service) Prepare(ctx context.Context, trackID string) (*Prepared, error) {
	source, err := service.sources.LoadTrackSource(ctx, trackID)
	if err != nil {
		return nil, classifyLookup(err)
	}
	location, err := service.loadRoot(ctx, source.RootID)
	if err != nil {
		return nil, err
	}
	if !location.Active {
		return nil, ErrRootDisabled
	}
	format, ok := FindFormat(source.SourceKind, source.Codec)
	if !ok {
		return nil, ErrNotDirectlyPlayable
	}
	// 登记时验证过的目录在播放时必须仍是配置允许范围内的真实目录；ValidatePath 返回的规范路径必须与登记路径一致，
	// 否则目录已被换成符号链接或移出允许范围。
	validated, err := library.ValidatePath(location.Path, service.allowedRoots)
	if err != nil || validated != location.Path {
		return nil, fmt.Errorf("%w: root is not an allowed directory", ErrSourceUnavailable)
	}
	opened, err := openVerified(validated, source.RelativePath, expectedSource{
		Size: source.Size, ModifiedAt: source.ModifiedAt, ContentFingerprint: source.ContentFingerprint,
	}, format.container, source.TrackID)
	if err != nil {
		return nil, err
	}
	return &Prepared{TrackID: source.TrackID, RootID: source.RootID, Format: format, File: opened.File, Size: opened.Size, ETag: opened.ETag}, nil
}

// StillPlayable 复核已建立的传输是否仍应继续：曲目仍是 present 且仍在原目录，目录仍启用。
// 它不重新打开文件：已打开的句柄指向同一个文件，内容被替换会在重新扫描后使曲目不再 present。
// 错误是 ErrTrackNotFound、ErrRootDisabled 或 ErrDatabase。
func (service *Service) StillPlayable(ctx context.Context, trackID, rootID string) error {
	source, err := service.sources.LoadTrackSource(ctx, trackID)
	if err != nil {
		return classifyLookup(err)
	}
	if source.RootID != rootID {
		return ErrTrackNotFound
	}
	location, err := service.loadRoot(ctx, rootID)
	if err != nil {
		return err
	}
	if !location.Active {
		return ErrRootDisabled
	}
	return nil
}

func (service *Service) loadRoot(ctx context.Context, rootID string) (library.RootLocation, error) {
	location, err := library.LoadRootLocation(ctx, service.database, rootID)
	switch {
	case errors.Is(err, library.ErrRootNotFound):
		return library.RootLocation{}, fmt.Errorf("%w: root is not registered", ErrSourceUnavailable)
	case err != nil:
		return library.RootLocation{}, fmt.Errorf("%w: %w", ErrDatabase, err)
	}
	return location, nil
}

func classifyLookup(err error) error {
	if errors.Is(err, catalog.ErrTrackNotFound) {
		return ErrTrackNotFound
	}
	return fmt.Errorf("%w: %w", ErrDatabase, err)
}
