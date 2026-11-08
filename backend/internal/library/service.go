package library

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
)

// Service 是目录与扫描能力的应用服务：持有数据库、数据目录（受管封面）、日志，以及扫描生命周期状态。
// 零值可用于只执行 scanRoot 等不依赖这些字段的流程。
type Service struct {
	database        *sql.DB
	dataDirectory   string
	logger          *slog.Logger
	scanContext     context.Context
	scanWorkers     sync.WaitGroup
	scanConcurrency int
	scanMutex       sync.Mutex
	runningScan     string
	// albumFavoriteMover 由 app 在扫描启动前注入。
	albumFavoriteMover AlbumFavoriteMover
	// fingerprinter 为 root 提供指纹计算；nil 时使用 root 内的文件实现。仅供测试注入可计数或会失败的实现。
	fingerprinter func(registeredRoot) ingest.Fingerprinter
}

// NewService 返回服务。scanContext 是扫描 worker 的生命周期上下文（进程停机时取消）；nil 时使用 Background。
func NewService(scanContext context.Context, database *sql.DB, dataDirectory string, logger *slog.Logger) *Service {
	if scanContext == nil {
		scanContext = context.Background()
	}
	return &Service{database: database, dataDirectory: dataDirectory, logger: logger, scanContext: scanContext, scanConcurrency: 4}
}

// WithScanConcurrency 在扫描启动前配置读盘并发度，由组合根传入已校验的配置。
func (service *Service) WithScanConcurrency(workers int) *Service {
	service.scanConcurrency = workers
	return service
}

// scanWorkerCount 使直接构造的零值服务保持串行，正式服务由配置决定并发度。
func (service *Service) scanWorkerCount() int {
	if service.scanConcurrency <= 0 {
		return 1
	}
	return service.scanConcurrency
}

// WaitForScans 等待所有扫描 worker 退出，或在 ctx 结束时返回其错误。
func (service *Service) WaitForScans(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		service.scanWorkers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
