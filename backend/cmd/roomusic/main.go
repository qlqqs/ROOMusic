// 命令 roomusic 是进程入口：加载配置、连接数据库、装配应用并管理 HTTP 生命周期。
// 业务策略不在这里；路由与服务的装配见 internal/app。
package main

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/qlqq/roomusic/backend/internal/app"
	"github.com/qlqq/roomusic/backend/internal/backup"
	"github.com/qlqq/roomusic/backend/internal/library"
	"github.com/qlqq/roomusic/backend/internal/platform/buildinfo"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/platform/diagnostics"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/playback/analysis"
)

// frontendAssets 包含 web 下的说明文件与生产构建写入 web/dist 的前端产物。
// 本地开发使用 Vite 开发服务器。
//
//go:embed web
var frontendAssets embed.FS

func main() {
	if len(os.Args) > 1 {
		os.Exit(runCLICommand(os.Args[1:]))
	}
	startedAt := time.Now()
	diagnosticLogs := diagnostics.NewLogRing()
	logger := slog.New(diagnostics.NewHandler(slog.NewJSONHandler(os.Stdout, nil), diagnosticLogs))
	build := buildinfo.Current()
	version := build.Version
	if version == "" {
		version = "dev"
	}
	logger.Info("roomusic build", "event", "platform.build.info", "module", "platform", "version", version, "commit", build.Commit)
	applicationContext, stopApplication := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopApplication()
	settings, configErr := config.Load()
	if configErr != nil {
		logger.Error("roomusic configuration invalid", "event", "platform.config.invalid", "error", configErr)
		os.Exit(1)
	}
	diagnosticLogs.SetRoots(append([]string{settings.DataDirectory, settings.Backup.Directory}, settings.AllowedLibraryRoots...)...)
	if err := app.RemoveLegacyMediaCache(settings, logger); err != nil {
		logger.Error("旧音频缓存清理失败", "event", "platform.config.invalid", "error", err)
		os.Exit(1)
	}
	backups := backup.NewService(settings.Backup, logger)
	database, databaseErr := openDatabase(applicationContext, settings.DatabaseURL, postgres.WithMigrationBackups(backups), postgres.WithLogger(logger))
	if databaseErr != nil {
		logger.Error("roomusic database unavailable", "event", "platform.database.unavailable", "error", databaseErr)
	}
	frontend := frontendSub()
	requestHandler := app.Degraded(frontend, logger)
	var application *app.App
	var analyzer *analysis.Analyzer
	if database != nil {
		appOptions := []app.Option{app.WithDiagnostics(diagnosticLogs, startedAt)}
		renderTool, renderErr := app.OpenRenderer(applicationContext, settings, logger)
		if renderErr != nil {
			logger.Warn("渲染器启动中止", "event", "playback.render.unavailable", "module", "playback", "reason", "startup_canceled")
		}
		if renderTool != nil {
			appOptions = append(appOptions, app.WithRenderer(renderTool))
		}
		analyzer, _ = app.OpenAnalysis(applicationContext, settings, logger)
		if analyzer != nil {
			appOptions = append(appOptions, app.WithAnalysis(analyzer))
		}
		backups.Start(applicationContext, database)
		appOptions = append(appOptions, app.WithBackups(backups))
		application = app.New(applicationContext, settings, database, frontend, logger, appOptions...)
		requestHandler = application.Handler()
	}
	server := &http.Server{
		Addr:              settings.Address,
		Handler:           frontendHandler(frontend, requestHandler, logger),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	logger.Info("roomusic server starting", "event", "platform.server.starting", "module", "platform", "address", server.Addr, "version", version, "commit", build.Commit)
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.ListenAndServe()
	}()
	var serveErr error
	select {
	case <-applicationContext.Done():
	case serveErr = <-serveErrors:
		stopApplication()
	}
	if application != nil {
		stewardContext, cancelSteward := context.WithTimeout(context.Background(), 10*time.Second)
		if err := application.ShutdownSteward(stewardContext); err != nil {
			logger.Error("管家运行收尾超时", "event", "steward.shutdown_timeout", "module", "steward")
		}
		cancelSteward()
	}
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownContext); err != nil {
		logger.Error("roomusic HTTP shutdown failed", "event", "platform.http.shutdown_failed", "error", err)
	}
	if application != nil {
		if err := application.WaitForZones(shutdownContext); err != nil {
			logger.Error("播放区退出超时", "event", "zones.shutdown_timeout", "module", "zones")
		}
		if err := application.WaitForScanScheduler(shutdownContext); err != nil {
			logger.Error("定时扫描调度器未能及时退出", "event", "library.scan.scheduler_shutdown_timeout", "module", "library")
		}
		if err := application.WaitForScans(shutdownContext); err != nil {
			logger.Error("roomusic scan shutdown timed out", "event", "library.scan.shutdown_timeout", "error", err)
		}
	}
	if application != nil {
		if err := application.WaitForAnalysis(shutdownContext); err != nil {
			logger.Error("音频分析工作者退出超时", "event", "playback.analysis.shutdown_timeout", "error", err)
		}
	}
	if application != nil {
		if err := application.WaitForCatalogMaintenance(shutdownContext); err != nil {
			logger.Error("目录后台维护退出超时", "event", "catalog_projection_shutdown_timeout", "error", err)
		}
	}
	if err := backups.Wait(shutdownContext); err != nil {
		logger.Error("等待备份退出超时", "event", "backup.shutdown_timeout", "module", "backup")
	}
	if database != nil {
		if err := database.Close(); err != nil {
			logger.Error("roomusic database close failed", "event", "platform.database.close_failed", "error", err)
		}
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		logger.Error("roomusic server stopped", "error", serveErr)
		os.Exit(1)
	}
}

// openDatabase 连接并迁移数据库，再恢复上次进程遗留的运行中扫描。
func openDatabase(ctx context.Context, databaseURL string, options ...postgres.OpenOption) (*sql.DB, error) {
	connection, err := postgres.Open(ctx, databaseURL, options...)
	if err != nil {
		return nil, err
	}
	if err := app.InitializeCatalog(ctx, connection); err != nil {
		_ = connection.Close()
		return nil, err
	}
	if err := library.RecoverInterruptedScans(ctx, connection); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return connection, nil
}

// frontendSub 返回嵌入的前端产物子树；未构建入口页时返回 nil。
func frontendSub() fs.FS {
	webAssets, err := fs.Sub(frontendAssets, "web/dist")
	if err != nil {
		panic(fmt.Errorf("load embedded frontend assets: %w", err))
	}
	if _, err := fs.Stat(webAssets, "index.html"); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		panic(fmt.Errorf("load embedded frontend index: %w", err))
	}
	return webAssets
}

// frontendHandler 在产物缺失时提供开发提示，API 与健康检查仍交给原应用处理。
// webAssets 是以 dist 为根的文件系统，允许测试传入合成产物。
func frontendHandler(webAssets fs.FS, application http.Handler, logger *slog.Logger) http.Handler {
	if webAssets != nil {
		if _, err := fs.Stat(webAssets, "index.html"); err == nil {
			return application
		}
	}
	const message = "前端未构建，开发请使用 Vite 开发服务器（默认端口 5173）。"
	logger.Warn("frontend assets missing", "event", "platform.frontend.missing", "module", "platform")
	mux := http.NewServeMux()
	for _, route := range []string{"/api", "/api/", "GET /healthz", "GET /readyz"} {
		mux.Handle(route, application)
	}
	mux.HandleFunc("/", func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.Header().Set("Cache-Control", "no-store")
		http.Error(responseWriter, message, http.StatusServiceUnavailable)
	})
	return mux
}
