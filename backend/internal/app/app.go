// Package app 是唯一的组合根：把配置、数据库和日志装配成各能力的服务与 handler，
// 挂载全部路由，并提供数据库不可用时的降级 handler。业务策略不在这里。
package app

import (
	"context"
	"database/sql"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/qlqq/roomusic/backend/internal/backup"
	backupapi "github.com/qlqq/roomusic/backend/internal/backup/httpapi"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	catalogapi "github.com/qlqq/roomusic/backend/internal/catalog/httpapi"
	"github.com/qlqq/roomusic/backend/internal/catalog/maintenance"
	"github.com/qlqq/roomusic/backend/internal/identity"
	identityapi "github.com/qlqq/roomusic/backend/internal/identity/httpapi"
	"github.com/qlqq/roomusic/backend/internal/library"
	libraryapi "github.com/qlqq/roomusic/backend/internal/library/httpapi"
	"github.com/qlqq/roomusic/backend/internal/listening"
	listeningapi "github.com/qlqq/roomusic/backend/internal/listening/httpapi"
	operationsapi "github.com/qlqq/roomusic/backend/internal/operations/httpapi"
	"github.com/qlqq/roomusic/backend/internal/personal"
	personalapi "github.com/qlqq/roomusic/backend/internal/personal/httpapi"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/playback"
	"github.com/qlqq/roomusic/backend/internal/playback/analysis"
	playbackapi "github.com/qlqq/roomusic/backend/internal/playback/httpapi"
)

// App 是装配完成的应用：请求 handler 加上需要在停机时等待的后台工作。
type App struct {
	mux          *http.ServeMux
	handler      http.Handler
	library      *library.Service
	catalogDone  <-chan struct{}
	analysisDone <-chan struct{}
}

// Option 调整装配。生产路径不传任何选项；选项只用于测试把时间常量缩短到可以快速验证。
type Option func(*options)

type options struct {
	backups               *backup.Service
	loginThrottle         *identity.LoginThrottle
	playbackPreflightWait time.Duration
	derivation            playback.Derivation
	analysis              *analysis.Analyzer
	// 测试直接插入旧目录夹具时暂停后台，搜索测试再显式回填。
	pauseCatalogMaintenance bool
}

// WithLoginThrottle 为测试注入带可控时钟的限速器；默认每个应用使用独立实例。
func WithLoginThrottle(throttle *identity.LoginThrottle) Option {
	return func(settings *options) { settings.loginThrottle = throttle }
}

// WithPlaybackPreflightWait 设置预检等待派生完成的最长时间（默认 playback.DefaultPreflightWait）。
func WithPlaybackPreflightWait(wait time.Duration) Option {
	return func(settings *options) { settings.playbackPreflightWait = wait }
}

// WithAnalysis 启用已经由 OpenAnalysis 探测通过的后台音频分析器。
func WithAnalysis(analyzer *analysis.Analyzer) Option {
	return func(settings *options) { settings.analysis = analyzer }
}

// WithDerivation 启用 ALAC 的无损派生：派生格式进入格式表，预检在缓存未命中时启动转换。不传时 ALAC 保持“不能直接播放”。
// 生产路径的实现来自 OpenDerivation；调用方不得传入持有 nil 指针的接口值（OpenDerivation 返回 nil 时就不要装配）。
func WithDerivation(port playback.Derivation) Option {
	return func(settings *options) { settings.derivation = port }
}

// WithBackups 注入备份能力；不提供时不注册系统接口。
func WithBackups(service *backup.Service) Option { return func(o *options) { o.backups = service } }

// New 装配应用。lifecycle 是应用的生命周期上下文（进程停机时取消）：扫描 worker 与已建立的音频传输都以它为界；
// 调用方先完成 InitializeCatalog；正常服务不等待后台投影完成。
// frontend 是前端产物子树。database 为 nil 时路由表照常建立但 /readyz 报告未就绪，供不连接数据库的路由测试使用。
func New(lifecycle context.Context, settings config.Server, database *sql.DB, frontend fs.FS, logger *slog.Logger, applied ...Option) *App {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	var assembly options
	for _, apply := range applied {
		apply(&assembly)
	}
	var guard *identity.Guard
	switch settings.OriginMode {
	case config.OriginModeAllowList:
		guard = identity.NewGuardWithAllowedOrigins(database, settings.AllowedOrigins)
	case config.OriginModeSameHost:
		guard = identity.NewGuardWithSameHost(database)
	case "", config.OriginModeLegacy:
		guard = identity.NewGuard(database, settings.SecureCookies, settings.PublicURL)
	default:
		panic("app: invalid origin mode")
	}
	guard.WithLogger(logger)
	libraryService := library.NewService(lifecycle, database, settings.DataDirectory, logger)
	personalService := personal.NewService(database)
	libraryService.SetAlbumFavoriteMover(personalService.MoveAlbumFavorites)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpserver.HealthHandler)
	mux.HandleFunc("GET /readyz", httpserver.ReadyHandler(func() bool { return database != nil }))
	identityapi.New(database, guard, assembly.loginThrottle, logger).Register(mux)
	libraryapi.New(database, guard, libraryService, settings.AllowedLibraryRoots, logger).Register(mux)
	catalogapi.New(database, guard, settings.DataDirectory, logger, catalogapi.WithAnalysisVersion(analysis.Version)).Register(mux)
	catalogapi.NewArtistGovernance(database, guard, logger, artistResolutionAdapter{}).Register(mux)
	// 受治理操作的撤销与展示：每种操作类型的能力由拥有对应业务表的包提供，operations/httpapi 不导入业务包。
	operationsapi.New(database, guard, logger, governance.CorrectionOperations(), governance.GroupChangeOperations(), governance.GroupDefaultOperations(),
		governance.IssueReviewOperations(), governance.ClaimOperations(), governance.ArtistMergeKind{}, governance.ArtistSplitKind{}, governance.ArtistRenameKind{}, governance.ArtistCandidateIgnoreKind{}).Register(mux)
	playbackService := playback.NewService(database, settings.AllowedLibraryRoots, playback.WithDerivation(assembly.derivation))
	var analysisWorker *playback.AnalysisWorker
	analysisDone := make(chan struct{})
	if database != nil && assembly.analysis != nil {
		analysisWorker = playback.NewAnalysisWorker(database, settings.AllowedLibraryRoots, assembly.analysis, func(ctx context.Context) (bool, error) {
			scan, err := libraryService.ActiveScan(ctx)
			return scan != nil, err
		}, logger)
		go func() { defer close(analysisDone); analysisWorker.Run(lifecycle) }()
	} else {
		close(analysisDone)
	}
	progress := playback.NewAnalysisProgressProvider(database, analysis.Version, analysisWorker)
	playbackapi.New(guard, playbackService, lifecycle, logger, playbackapi.Options{
		PreflightWait:    assembly.playbackPreflightWait,
		AnalysisProgress: progress,
	}).Register(mux)
	listeningapi.New(guard, listening.NewService(database)).Register(mux)
	personalapi.New(guard, personalService).Register(mux)
	if assembly.backups != nil {
		backupapi.New(assembly.backups, database, guard, lifecycle).Register(mux)
	}
	httpserver.RegisterAPIFallback(mux, httpserver.APINotFoundHandler(mux))
	mux.Handle("/", httpserver.Frontend(frontend))
	catalogDone := make(chan struct{})
	if database != nil && !assembly.pauseCatalogMaintenance {
		go func() {
			defer close(catalogDone)
			if err := maintenance.Run(lifecycle, database, logger, maintenance.RunOptions{RefreshArtistResolutions: personal.RefreshArtistResolution}); err != nil && lifecycle.Err() == nil {
				logger.Error("目录后台维护停止", "event", "catalog_projection_worker_stopped", "module", "catalog")
			}
		}()
	} else {
		close(catalogDone)
	}
	return &App{mux: mux, handler: httpserver.Middleware(logger, mux), library: libraryService, catalogDone: catalogDone, analysisDone: analysisDone}
}

// InitializeCatalog 在允许扫描与治理写入前短事务登记目标版本，不发现对象或等待回填。
// 数据库或结构错误返回调用方；规范化预检失败只禁用新投影，保留旧服务。
func InitializeCatalog(ctx context.Context, database *sql.DB) error {
	return maintenance.Initialize(ctx, database)
}

// WaitForCatalogMaintenance 在关闭数据库前有界等待已取消的后台维护退出。
func (application *App) WaitForCatalogMaintenance(ctx context.Context) error {
	select {
	case <-application.catalogDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Handler 返回带请求 ID 与完成日志的完整请求 handler。
func (application *App) Handler() http.Handler { return application.handler }

// WaitForAnalysis 等待分析工作者退出；生命周期取消会中止正在运行的 ffmpeg 进程组。
func (application *App) WaitForAnalysis(ctx context.Context) error {
	select {
	case <-application.analysisDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WaitForScans 等待所有扫描 worker 退出，或在 ctx 结束时返回其错误。
func (application *App) WaitForScans(ctx context.Context) error {
	return application.library.WaitForScans(ctx)
}

// Degraded 返回数据库不可用时的 handler：健康检查与前端页面照常，/readyz 报告未就绪，所有 /api 请求返回 503。
func Degraded(frontend fs.FS, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpserver.HealthHandler)
	mux.HandleFunc("GET /readyz", httpserver.ReadyHandler(func() bool { return false }))
	httpserver.RegisterAPIFallback(mux, http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		httpserver.WriteError(responseWriter, request, http.StatusServiceUnavailable, "database_unavailable", "服务暂不可用")
	}))
	mux.Handle("/", httpserver.Frontend(frontend))
	return httpserver.Middleware(logger, mux)
}

// artistResolutionAdapter 将收藏owner的窄事务端口注入治理，不扩大包依赖。
type artistResolutionAdapter struct{}

func (artistResolutionAdapter) RefreshArtistResolution(ctx context.Context, tx *sql.Tx, mapping map[string]string) error {
	return personal.RefreshArtistResolution(ctx, tx, mapping)
}
