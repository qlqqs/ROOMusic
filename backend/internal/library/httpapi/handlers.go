// Package httpapi 是目录与扫描能力的 REST 传输层：允许根的登记、停用、恢复、改路径与操作历史，
// 以及扫描的启动、查询、取消和诊断。
//
// 目录列表、写操作与扫描用例在 library.Service；handler 负责解析、鉴权和响应映射。
// 目录操作历史查询仍在传输层。
package httpapi

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/library"
)

// Handlers 汇集目录与扫描相关的 HTTP handler。
type Handlers struct {
	database     *sql.DB
	guard        *identity.Guard
	service      *library.Service
	allowedRoots []string
	logger       *slog.Logger
}

// New 返回目录与扫描 handler；allowedRoots 是启动配置中允许登记的音乐根。
func New(database *sql.DB, guard *identity.Guard, service *library.Service, allowedRoots []string, logger *slog.Logger) *Handlers {
	return &Handlers{database: database, guard: guard, service: service, allowedRoots: allowedRoots, logger: logger}
}

// Register 在 mux 上注册目录与扫描路由。
func (handlers *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/library-roots", handlers.listRoots)
	mux.HandleFunc("GET /api/v1/library-folders", handlers.listFolders)
	mux.HandleFunc("POST /api/v1/library-roots", handlers.addRoot)
	mux.HandleFunc("PUT /api/v1/library-roots/{id}/scan-interval", handlers.saveRootScanInterval)
	mux.HandleFunc("POST /api/v1/library-roots/{id}/exclusions", handlers.addExclusion)
	mux.HandleFunc("DELETE /api/v1/library-roots/{id}/exclusions", handlers.removeExclusion)
	mux.HandleFunc("PATCH /api/v1/library-roots/{id}", handlers.updateRoot)
	mux.HandleFunc("POST /api/v1/library-roots/{id}/restore", handlers.restoreRoot)
	mux.HandleFunc("POST /api/v1/library-roots/{id}/relocate", handlers.relocateRoot)
	mux.HandleFunc("GET /api/v1/library-root-operations", handlers.rootOperations)
	mux.HandleFunc("GET /api/v1/scan-schedule", handlers.loadScanSchedule)
	mux.HandleFunc("PUT /api/v1/scan-schedule", handlers.saveScanSchedule)
	mux.HandleFunc("GET /api/v1/scans", handlers.listScans)
	mux.HandleFunc("POST /api/v1/scans", handlers.startScan)
	mux.HandleFunc("GET /api/v1/scans/active", handlers.activeScan)
	mux.HandleFunc("GET /api/v1/scans/{id}", handlers.scanStatus)
	mux.HandleFunc("POST /api/v1/scans/{id}/cancel", handlers.cancelScan)
	mux.HandleFunc("GET /api/v1/scans/{id}/diagnostics", handlers.diagnostics)
	mux.HandleFunc("GET /api/v1/scans/{id}/report", handlers.scanReport)
}
