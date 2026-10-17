// Package app 是唯一的组合根：把配置、数据库和日志装配成各能力的服务与 handler，
// 挂载全部路由，并提供数据库不可用时的降级 handler。业务策略不在这里。
package app

import (
	"context"
	"database/sql"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	catalogapi "github.com/qlqq/roomusic/backend/internal/catalog/httpapi"
	"github.com/qlqq/roomusic/backend/internal/identity"
	identityapi "github.com/qlqq/roomusic/backend/internal/identity/httpapi"
	"github.com/qlqq/roomusic/backend/internal/library"
	libraryapi "github.com/qlqq/roomusic/backend/internal/library/httpapi"
	"github.com/qlqq/roomusic/backend/internal/listening"
	listeningapi "github.com/qlqq/roomusic/backend/internal/listening/httpapi"
	operationsapi "github.com/qlqq/roomusic/backend/internal/operations/httpapi"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/playback"
	playbackapi "github.com/qlqq/roomusic/backend/internal/playback/httpapi"
)

// App 是装配完成的应用：请求 handler 加上需要在停机时等待的后台工作。
type App struct {
	mux     *http.ServeMux
	handler http.Handler
	library *library.Service
}

// Option 调整装配。生产路径不传任何选项；选项只用于测试把时间常量缩短到可以快速验证。
type Option func(*options)

type options struct {
	playbackRecheckInterval time.Duration
	playbackPreflightWait   time.Duration
	derivation              playback.Derivation
}

// WithPlaybackRecheckInterval 设置已建立的音频传输复核会话与目录的间隔（默认 playback.DefaultRecheckInterval）。
func WithPlaybackRecheckInterval(interval time.Duration) Option {
	return func(settings *options) { settings.playbackRecheckInterval = interval }
}

// WithPlaybackPreflightWait 设置预检等待派生完成的最长时间（默认 playback.DefaultPreflightWait）。
func WithPlaybackPreflightWait(wait time.Duration) Option {
	return func(settings *options) { settings.playbackPreflightWait = wait }
}

// WithDerivation 启用 ALAC 的无损派生：派生格式进入格式表，预检在缓存未命中时启动转换。不传时 ALAC 保持“不能直接播放”。
// 生产路径的实现来自 OpenDerivation；调用方不得传入持有 nil 指针的接口值（OpenDerivation 返回 nil 时就不要装配）。
func WithDerivation(port playback.Derivation) Option {
	return func(settings *options) { settings.derivation = port }
}

// New 装配应用。lifecycle 是应用的生命周期上下文（进程停机时取消）：扫描 worker 与已建立的音频传输都以它为界；
// frontend 是前端产物子树。database 为 nil 时路由表照常建立但 /readyz 报告未就绪，供不连接数据库的路由测试使用。
func New(lifecycle context.Context, settings config.Server, database *sql.DB, frontend fs.FS, logger *slog.Logger, applied ...Option) *App {
	var assembly options
	for _, apply := range applied {
		apply(&assembly)
	}
	guard := identity.NewGuard(database, settings.SecureCookies, settings.PublicURL)
	libraryService := library.NewService(lifecycle, database, settings.DataDirectory, logger)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpserver.HealthHandler)
	mux.HandleFunc("GET /readyz", httpserver.ReadyHandler(func() bool { return database != nil }))
	identityapi.New(database, guard).Register(mux)
	libraryapi.New(database, guard, libraryService, settings.AllowedLibraryRoots, logger).Register(mux)
	catalogapi.New(database, guard, settings.DataDirectory, logger).Register(mux)
	// 受治理操作的撤销与展示：每种操作类型的能力由拥有对应业务表的包提供，operations/httpapi 不导入业务包。
	operationsapi.New(database, guard, logger, catalog.CorrectionOperations(), catalog.GroupChangeOperations(), catalog.GroupDefaultOperations(),
		catalog.IssueReviewOperations(), catalog.ClaimOperations()).Register(mux)
	playbackService := playback.NewService(database, settings.AllowedLibraryRoots, playback.WithDerivation(assembly.derivation))
	playbackapi.New(guard, playbackService, lifecycle, logger, playbackapi.Options{
		RecheckInterval: assembly.playbackRecheckInterval, PreflightWait: assembly.playbackPreflightWait,
	}).Register(mux)
	listeningapi.New(guard, listening.NewService(database)).Register(mux)
	httpserver.RegisterAPIFallback(mux, httpserver.APINotFoundHandler(mux))
	mux.Handle("/", httpserver.Frontend(frontend))
	return &App{mux: mux, handler: httpserver.Middleware(logger, mux), library: libraryService}
}

// Handler 返回带请求 ID 与完成日志的完整请求 handler。
func (application *App) Handler() http.Handler { return application.handler }

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
