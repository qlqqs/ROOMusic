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
	// ErrRootDisabled 表示曲目所在的登记根目录已停用；拒绝新的播放请求，不切断已建立传输，专辑仍可浏览。
	ErrRootDisabled = errors.New("playback root disabled")
	// ErrNotDirectlyPlayable 表示曲目不在旧接口的直出表中（包括 ALAC 等原派生格式与 CUE 虚拟轨）。
	ErrNotDirectlyPlayable = errors.New("playback track is not directly playable")
	// ErrDatabase 表示核对所需的数据库读取失败；调用方应报告服务暂不可用，不能当作曲目或来源的问题。
	ErrDatabase = errors.New("playback database unavailable")
)

// Service 是播放用例：把曲目 ID 解析成一个已核对的原文件。
// 它只读取 catalog 与 library 的公开合同，不直接读它们的表。
type Service struct {
	sources        *catalog.Store
	database       library.RootQuerier
	allowedRoots   []string
	renderDecoders RenderDecoders
}

// ServiceOption 调整服务的装配。
type ServiceOption func(*Service)

// NewService 返回服务。allowedRoots 是配置允许的音乐根目录，每次打开都会重新核对登记的根目录仍在其中。
func NewService(database catalog.Queryer, allowedRoots []string, options ...ServiceOption) *Service {
	service := &Service{sources: catalog.NewStore(database), database: database, allowedRoots: allowedRoots}
	for _, option := range options {
		option(service)
	}
	return service
}

// Prepared 是通过全部核对的一次交付：File 是已打开的只读句柄，调用方负责关闭。
// Size 与 ETag 描述源文件。
type Prepared struct {
	TrackID string
	Format  Format
	File    *os.File
	Size    int64
	// ETag 是强校验值的不透明部分（不含引号）。
	ETag string
}

// resolved 是通过前几项核对的来源：曲目存在且 present、目录仍启用、格式在当前格式表中、目录仍在配置允许范围内。
type resolved struct {
	source catalog.TrackSource
	format Format
	// rootPath 是已通过允许根校验的目录规范路径。
	rootPath string
}

// resolve 按固定顺序核对：曲目存在且 present → 目录仍启用 → 在格式表中 → 目录仍在配置允许范围内。
// 错误是 ErrTrackNotFound、ErrRootDisabled、ErrNotDirectlyPlayable、ErrSourceUnavailable 或 ErrDatabase 之一。
func (service *Service) resolve(ctx context.Context, trackID string) (resolved, error) {
	return service.resolveFormat(ctx, trackID, service.deliveryFormat)
}

// deliveryFormat 保留旧接口的直出格式限制，不受渲染能力装配影响。
func (service *Service) deliveryFormat(source catalog.TrackSource) (Format, error) {
	format, ok := service.findFormat(source.SourceKind, source.Codec)
	if !ok {
		return Format{}, ErrNotDirectlyPlayable
	}
	return format, nil
}

// resolveFormat 共用来源、目录状态与允许根的检查顺序，由入口选择自己的格式策略。
func (service *Service) resolveFormat(ctx context.Context, trackID string, selectFormat func(catalog.TrackSource) (Format, error)) (resolved, error) {
	source, err := service.sources.LoadTrackSource(ctx, trackID)
	if err != nil {
		return resolved{}, classifyLookup(err)
	}
	location, err := service.loadRoot(ctx, source.RootID)
	if err != nil {
		return resolved{}, err
	}
	if !location.Active {
		return resolved{}, ErrRootDisabled
	}
	format, err := selectFormat(source)
	if err != nil {
		return resolved{}, err
	}
	// 登记时验证过的目录在播放时必须仍是配置允许范围内的真实目录；ValidatePath 返回的规范路径必须与登记路径一致，
	// 否则目录已被换成符号链接或移出允许范围。
	validated, err := library.ValidatePath(location.Path, service.allowedRoots)
	if err != nil || validated != location.Path {
		return resolved{}, fmt.Errorf("%w: root is not an allowed directory", ErrSourceUnavailable)
	}
	return resolved{source: source, format: format, rootPath: validated}, nil
}

func (r resolved) expected() expectedSource {
	return expectedSource{Size: r.source.Size, ModifiedAt: r.source.ModifiedAt, ContentFingerprint: r.source.ContentFingerprint}
}

// openSource 安全地只读打开源文件并核对它仍是扫描时的那个（大小、修改时间、容器）。调用方负责关闭 File。
func (r resolved) openSource() (*openedSource, error) {
	return openVerified(r.rootPath, r.source.RelativePath, r.expected(), r.format.container, r.source.TrackID)
}

// Prepare 按固定顺序核对并打开曲目要交付的文件：曲目存在且 present → 目录仍启用 → 在格式表中 →
// 目录仍在配置允许范围内 → 安全打开普通文件 → 大小与修改时间等于扫描记录 → 文件头与格式相符。
// 错误是 ErrTrackNotFound、ErrRootDisabled、ErrNotDirectlyPlayable、ErrSourceUnavailable、ErrSourceChanged、
// 或 ErrDatabase 之一。
func (service *Service) Prepare(ctx context.Context, trackID string) (*Prepared, error) {
	target, err := service.resolve(ctx, trackID)
	if err != nil {
		return nil, err
	}
	opened, err := target.openSource()
	if err != nil {
		return nil, err
	}
	return &Prepared{TrackID: target.source.TrackID, Format: target.format, File: opened.File, Size: opened.Size, ETag: opened.ETag}, nil
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
