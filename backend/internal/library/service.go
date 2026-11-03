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
	database      *sql.DB
	dataDirectory string
	logger        *slog.Logger
	scanContext   context.Context
	scanWorkers   sync.WaitGroup
	scanMutex     sync.Mutex
	runningScan   string
	// fingerprinter 为 root 提供指纹计算；nil 时使用 root 内的文件实现。仅供测试注入可计数或会失败的实现。
	fingerprinter func(registeredRoot) ingest.Fingerprinter
}

// NewService 返回服务。scanContext 是扫描 worker 的生命周期上下文（进程停机时取消）；nil 时使用 Background。
func NewService(scanContext context.Context, database *sql.DB, dataDirectory string, logger *slog.Logger) *Service {
	if scanContext == nil {
		scanContext = context.Background()
	}
	return &Service{database: database, dataDirectory: dataDirectory, logger: logger, scanContext: scanContext}
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
