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
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/playback/analysis"
	"github.com/qlqq/roomusic/backend/internal/playback/derivation"
)

// frontendAssets is populated by the production build in this package's web
// directory. The development server remains the preferred local loop.
//
//go:embed web
var frontendAssets embed.FS

func main() {
	if len(os.Args) > 1 {
		os.Exit(runCLICommand(os.Args[1:]))
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
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
	backups := backup.NewService(settings.Backup, logger)
	database, databaseErr := openDatabase(applicationContext, settings.DatabaseURL, postgres.WithMigrationBackups(backups), postgres.WithLogger(logger))
	if databaseErr != nil {
		logger.Error("roomusic database unavailable", "event", "platform.database.unavailable", "error", databaseErr)
	}
	frontend := frontendSub()
	requestHandler := app.Degraded(frontend, logger)
	var application *app.App
	var derivations *derivation.Manager
	var analyzer *analysis.Analyzer
	if database != nil {
		var appOptions []app.Option
		var derivationErr error
		// ffmpeg 不可用不阻止启动（ALAC 保持不能直接播放）；缓存目录与音乐根重叠等部署配置错误则拒绝启动。
		derivations, derivationErr = app.OpenDerivation(applicationContext, settings, logger)
		if derivationErr != nil {
			logger.Error("roomusic media cache invalid", "event", "platform.config.invalid", "error", derivationErr)
			_ = database.Close()
			os.Exit(1)
		}
		if derivations != nil {
			appOptions = append(appOptions, app.WithDerivation(derivations))
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
		Handler:           requestHandler,
		ReadHeaderTimeout: 5 * time.Second,
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
	if derivations != nil {
		// HTTP 已停止接收请求：终止仍在运行的 ffmpeg（进程组）并删除未完成的临时文件。
		if err := derivations.Close(5 * time.Second); err != nil {
			logger.Error("roomusic derivation shutdown timed out", "event", "playback.derivation.shutdown_timeout", "error", err)
		}
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

// frontendSub 返回嵌入的前端产物子树。嵌入内容是编译期常量，取子树失败属于构建错误，因此直接 panic。
func frontendSub() fs.FS {
	webAssets, err := fs.Sub(frontendAssets, "web")
	if err != nil {
		panic(fmt.Errorf("load embedded frontend assets: %w", err))
	}
	return webAssets
}
