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
	"github.com/qlqq/roomusic/backend/internal/library"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
)

// frontendAssets is populated by the production build in this package's web
// directory. The development server remains the preferred local loop.
//
//go:embed web
var frontendAssets embed.FS

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	applicationContext, stopApplication := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopApplication()
	settings, configErr := config.Load()
	if configErr != nil {
		logger.Error("roomusic configuration invalid", "event", "platform.config.invalid", "error", configErr)
		os.Exit(1)
	}
	database, databaseErr := openDatabase(applicationContext, settings.DatabaseURL)
	if databaseErr != nil {
		logger.Error("roomusic database unavailable", "event", "platform.database.unavailable", "error", databaseErr)
	}
	frontend := frontendSub()
	requestHandler := app.Degraded(frontend, logger)
	var application *app.App
	if database != nil {
		application = app.New(applicationContext, settings, database, frontend, logger)
		requestHandler = application.Handler()
	}
	server := &http.Server{
		Addr:              settings.Address,
		Handler:           requestHandler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	logger.Info("roomusic server starting", "address", server.Addr)
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
		if err := application.WaitForScans(shutdownContext); err != nil {
			logger.Error("roomusic scan shutdown timed out", "event", "library.scan.shutdown_timeout", "error", err)
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
func openDatabase(ctx context.Context, databaseURL string) (*sql.DB, error) {
	connection, err := postgres.Open(ctx, databaseURL)
	if err != nil {
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
