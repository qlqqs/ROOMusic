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
	"github.com/qlqq/roomusic/backend/internal/curation"
	curationapi "github.com/qlqq/roomusic/backend/internal/curation/httpapi"
	"github.com/qlqq/roomusic/backend/internal/curation/model"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/dspsettings"
	dspsettingsapi "github.com/qlqq/roomusic/backend/internal/dspsettings/httpapi"
	"github.com/qlqq/roomusic/backend/internal/dspsettings/irload"
	"github.com/qlqq/roomusic/backend/internal/identity"
	identityapi "github.com/qlqq/roomusic/backend/internal/identity/httpapi"
	"github.com/qlqq/roomusic/backend/internal/library"
	libraryapi "github.com/qlqq/roomusic/backend/internal/library/httpapi"
	"github.com/qlqq/roomusic/backend/internal/listening"
	listeningapi "github.com/qlqq/roomusic/backend/internal/listening/httpapi"
	"github.com/qlqq/roomusic/backend/internal/operations"
	operationsapi "github.com/qlqq/roomusic/backend/internal/operations/httpapi"
	"github.com/qlqq/roomusic/backend/internal/personal"
	personalapi "github.com/qlqq/roomusic/backend/internal/personal/httpapi"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/platform/diagnostics"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/playback"
	"github.com/qlqq/roomusic/backend/internal/playback/analysis"
	"github.com/qlqq/roomusic/backend/internal/playback/fingerprint"
	playbackapi "github.com/qlqq/roomusic/backend/internal/playback/httpapi"
	"github.com/qlqq/roomusic/backend/internal/playback/render/ffmpeg"
	"github.com/qlqq/roomusic/backend/internal/sources/acoustid"
	"github.com/qlqq/roomusic/backend/internal/sources/coverartarchive"
	"github.com/qlqq/roomusic/backend/internal/sources/lrclib"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
	"github.com/qlqq/roomusic/backend/internal/steward/runtime"
	"github.com/qlqq/roomusic/backend/internal/zones"
	zonesapi "github.com/qlqq/roomusic/backend/internal/zones/httpapi"
)

// App 是装配完成的应用：请求 handler 加上需要在停机时等待的后台工作。
type App struct {
	steward           *runtime.Supervisor
	mux               *http.ServeMux
	handler           http.Handler
	library           *library.Service
	catalogDone       <-chan struct{}
	analysisDone      <-chan struct{}
	programDone       <-chan struct{}
	modelDone         <-chan struct{}
	scanSchedulerDone <-chan struct{}
	zonesDone         <-chan struct{}
}

// Option 调整装配。生产路径不传任何选项；选项只用于测试把时间常量缩短到可以快速验证。
type Option func(*options)

type options struct {
	fingerprinter      *fingerprint.Fingerprinter
	acoustid           acoustid.Options
	diagnosticLogs     *diagnostics.LogRing
	startedAt          time.Time
	backups            *backup.Service
	loginThrottle      *identity.LoginThrottle
	analysis           *analysis.Analyzer
	renderer           *ffmpeg.Tool
	scanSchedulerClock library.ScanSchedulerClock
	// 测试直接插入旧目录夹具时暂停后台，搜索测试再显式回填。
	pauseCatalogMaintenance bool
	disableProgram          bool
	disableModel            bool
	model                   model.Options
	program                 program.Options
	music                   musicbrainz.Options
	covers                  coverartarchive.Options
	lyrics                  lrclib.Options
}

// WithCurationProgram 控制后台循环并注入时钟与轮询间隔。
func WithCurationProgram(enabled bool, opts program.Options) Option {
	return func(o *options) { o.disableProgram = !enabled; o.program = opts }
}

// WithCurationModel 控制后台模型调度并注入时钟与退避间隔。
func WithCurationModel(enabled bool, opts model.Options) Option {
	return func(o *options) { o.disableModel = !enabled; o.model = opts }
}

// WithCurationSources 为三个外部源注入地址、客户端工厂与限速器。
func WithCurationSources(music musicbrainz.Options, covers coverartarchive.Options, lyrics lrclib.Options) Option {
	return func(o *options) { o.music, o.covers, o.lyrics = music, covers, lyrics }
}

// WithScanSchedulerClock 为调度集成测试注入可控时钟与计时器。
func WithScanSchedulerClock(clock library.ScanSchedulerClock) Option {
	return func(settings *options) { settings.scanSchedulerClock = clock }
}

// WithLoginThrottle 为测试注入带可控时钟的限速器；默认每个应用使用独立实例。
func WithLoginThrottle(throttle *identity.LoginThrottle) Option {
	return func(settings *options) { settings.loginThrottle = throttle }
}

// WithAnalysis 启用已经由 OpenAnalysis 探测通过的后台音频分析器。
func WithAnalysis(analyzer *analysis.Analyzer) Option {
	return func(settings *options) { settings.analysis = analyzer }
}

// WithBackups 注入备份能力；不提供时不注册系统接口。
func WithBackups(service *backup.Service) Option { return func(o *options) { o.backups = service } }

// New 装配应用。lifecycle 是应用的生命周期上下文（进程停机时取消）：扫描 worker、播放区会话与已建立的音频传输都以它为界；
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
	libraryService := library.NewService(lifecycle, database, settings.DataDirectory, logger).WithScanConcurrency(settings.ScanWorkers)
	personalService := personal.NewService(database)
	dspSettingsService := dspsettings.NewService(database)
	dspSettingsService.ConfigureIR(irload.NewLoader(assembly.renderer))
	listeningService := listening.NewService(database)
	playbackService := newPlaybackService(database, settings, assembly)
	renderer, closeRenderer := assembleRenderer(database, playbackService, assembly.renderer, logger, dspSettingsService)
	zonesService := zones.NewService(database, zones.Options{Logger: logger, Renderer: renderer, IRReferences: zoneIRReferences{service: dspSettingsService}, PlayRecorder: zonePlayRecorder{service: listeningService}})
	zonesDone := make(chan struct{})
	if lifecycle != nil {
		context.AfterFunc(lifecycle, func() {
			zonesService.Close()
			closeRenderer()
			close(zonesDone)
		})
	} else {
		close(zonesDone)
	}
	libraryService.SetAlbumFavoriteMover(personalService.MoveAlbumFavorites)
	libraryService.WithScanSchedulerClock(assembly.scanSchedulerClock)
	scanSchedulerDone := make(chan struct{})
	if database != nil {
		go func() {
			defer close(scanSchedulerDone)
			libraryService.RunScanScheduler(lifecycle, settings.Backup.Location)
		}()
	} else {
		close(scanSchedulerDone)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpserver.HealthHandler)
	assembly.music.DataDirectory, assembly.music.Logger = settings.DataDirectory, logger
	assembly.covers.DataDirectory, assembly.covers.Logger = settings.DataDirectory, logger
	assembly.lyrics.DataDirectory, assembly.lyrics.Logger = settings.DataDirectory, logger
	instanceSecrets := newInstanceSecrets(settings.DataDirectory)
	var sourceGate *curation.Sources
	assembly.acoustid.DataDirectory, assembly.acoustid.Logger = settings.DataDirectory, logger
	assembly.acoustid.Keys = func(ctx context.Context) (map[string]string, error) {
		return sourceGate.Keys(ctx, "acoustid")
	}
	acoustic := acoustid.New(assembly.acoustid)
	music := musicbrainz.New(assembly.music)
	covers := coverartarchive.New(assembly.covers)
	lyrics := lrclib.New(assembly.lyrics)
	if lifecycle != nil {
		acoustic.StartCacheCleanup(lifecycle)
		covers.StartCacheCleanup(lifecycle)
		lyrics.StartCacheCleanup(lifecycle)
		music.StartCacheCleanup(lifecycle)
	}
	scanRunning := func(ctx context.Context) (bool, error) {
		scan, err := libraryService.ActiveScan(ctx)
		return scan != nil, err
	}
	if assembly.fingerprinter != nil {
		assembly.program.Fingerprints = programFingerprints{playback.NewFingerprints(database, settings.AllowedLibraryRoots, assembly.fingerprinter, logger)}
	}
	sourceGate = curation.NewSources(database,
		curation.SourceRegistration{Declaration: musicbrainz.Declaration(), Releases: music, Recordings: music, Artists: music},
		curation.SourceRegistration{Declaration: coverartarchive.Declaration(), Covers: covers},
		curation.SourceRegistration{Declaration: lrclib.Declaration(), Lyrics: lyrics},
		curation.SourceRegistration{
			Declaration: acoustid.Declaration(), Fingerprints: acoustic,
			Unavailable: func() string {
				switch {
				case assembly.program.Fingerprints == nil:
					return "fingerprint_unavailable"
				case acoustic.BundledKeyRejected():
					return "bundled_key_rejected"
				default:
					return ""
				}
			},
		},
	).WithScanCheck(scanRunning).WithKeyStore(instanceSecrets)
	artistGovernance := governance.NewArtistGovernance(artistResolutionAdapter{})
	curationWriter := curation.NewWriter(database, settings.DataDirectory, logger).WithArtists(artistGovernance)
	operationKinds := []operations.Kind{curation.ExternalLinkOperations(curationWriter), curation.ExternalArtworkOperations(curationWriter), curation.ExternalLyricsOperations(curationWriter),
		governance.AutoCorrectionOperations(curationWriter), governance.CorrectionOperations(), governance.TrackCorrectionOperations(), governance.TrackAutoCorrectionOperations(curationWriter), governance.GroupChangeOperations(curationWriter), governance.GroupDefaultOperations(),
		governance.IssueReviewOperations(), governance.ClaimOperations(),
		governance.ReleaseMergeOperations(operations.TypeReleaseMerge, curationWriter), governance.ReleaseMergeOperations(operations.TypeReleaseAutoMerge, curationWriter)}
	operationKinds = append(operationKinds, governance.ArtistOperations(artistGovernance, curationWriter)...)
	mux.HandleFunc("GET /readyz", httpserver.ReadyHandler(func() bool { return database != nil }))
	libraryapi.New(database, guard, libraryService, settings.AllowedLibraryRoots, logger).Register(mux)
	catalogapi.New(database, guard, settings.DataDirectory, logger, catalogapi.WithAnalysisVersion(analysis.Version)).Register(mux)
	catalogapi.NewArtistGovernance(database, guard, logger, artistGovernance).Register(mux)
	// 受治理操作的撤销与展示：每种操作类型的能力由拥有对应业务表的包提供，operations/httpapi 不导入业务包。
	operationsapi.New(database, guard, logger, operationKinds...).Register(mux)
	var programStatus curation.ProgramStatus
	programDone := make(chan struct{})
	if database != nil && !assembly.disableProgram {
		worker := program.NewWorker(database, curationWriter, sourceGate, scanRunning, logger, assembly.program)
		programStatus = worker
		go func() { defer close(programDone); worker.Run(lifecycle) }()
	} else {
		close(programDone)
	}
	var analysisWorker *playback.AnalysisWorker
	analysisDone := make(chan struct{})
	if database != nil && assembly.analysis != nil {
		analysisWorker = playback.NewAnalysisWorker(database, settings.AllowedLibraryRoots, assembly.analysis, scanRunning, logger)
		go func() { defer close(analysisDone); analysisWorker.Run(lifecycle) }()
	} else {
		close(analysisDone)
	}
	progress := playback.NewAnalysisProgressProvider(database, analysis.Version, analysisWorker)
	matcher := program.NewModelMatcher(database, curationWriter, sourceGate)
	supervisor, launcher := registerSteward(lifecycle, mux, database, guard, instanceSecrets, logger, libraryService, operationKinds, zonesService, listeningService, personalService, stewardConfig{writer: curationWriter, sources: sourceGate, matcher: matcher, music: music, covers: covers, diagnostics: newDiagnostics(database, settings, libraryService, assembly), backups: assembly.backups, allowedRoots: settings.AllowedLibraryRoots, audio: playbackService, progress: progress, dsp: dspSettingsService})
	scheduler := model.NewScheduler(database, curationWriter, sourceGate, launcher, logger, assembly.model)
	modelDone := make(chan struct{})
	if database != nil && launcher != nil && !assembly.disableModel {
		go func() { defer close(modelDone); scheduler.Run(lifecycle) }()
	} else {
		close(modelDone)
	}
	resolver := model.NewExceptionResolver(database, curationWriter, matcher)
	curationapi.New(curationWriter, curation.NewReverter(database, operationKinds...), sourceGate, programStatus, scheduler, resolver, program.StoredRuleVersion, guard, logger).Register(mux)
	playbackapi.New(guard, playbackService, lifecycle, logger, playbackapi.Options{
		AnalysisProgress: progress,
	}).Register(mux)
	listeningapi.New(guard, listeningService).Register(mux)
	personalapi.New(guard, personalService).Register(mux)
	zonesapi.New(guard, zonesService).Register(mux)
	dspsettingsapi.New(guard, dspSettingsService, dspsettingsapi.WithIRDeleter(zonesService)).Register(mux)
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
	identityapi.New(database, guard, assembly.loginThrottle, logger).WithSessionRevoked(func(userID string) {
		if supervisor != nil {
			supervisor.CancelUser(userID)
		}
	}).Register(mux)
	return &App{steward: supervisor, mux: mux, handler: httpserver.Middleware(logger, mux), library: libraryService, catalogDone: catalogDone, analysisDone: analysisDone, programDone: programDone, modelDone: modelDone, scanSchedulerDone: scanSchedulerDone, zonesDone: zonesDone}
}

// InitializeCatalog 在允许扫描与治理写入前短事务登记目标版本，不发现对象或等待回填。
// 数据库或结构错误返回调用方；规范化预检失败只禁用新投影，保留旧服务。
func InitializeCatalog(ctx context.Context, database *sql.DB) error {
	return maintenance.Initialize(ctx, database)
}

// WaitForZones 在关闭数据库前等待播放区释放会话并停止定时任务。
func (application *App) WaitForZones(ctx context.Context) error {
	select {
	case <-application.zonesDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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

// WaitForCurationProgram 等待程序层和模型层释放数据库与外部源。
func (application *App) WaitForCurationProgram(ctx context.Context) error {
	select {
	case <-application.programDone:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-application.modelDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WaitForScanScheduler 在生命周期取消后等待调度器退出，再等待扫描 worker，避免停机期间新增 worker。
func (application *App) WaitForScanScheduler(ctx context.Context) error {
	select {
	case <-application.scanSchedulerDone:
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

// ShutdownSteward 在数据库关闭前取消运行并等待内容落库。
func (app *App) ShutdownSteward(ctx context.Context) error {
	if app.steward == nil {
		return nil
	}
	return app.steward.Shutdown(ctx)
}
