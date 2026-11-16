package app

import (
	"context"
	"database/sql"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/library"
	"github.com/qlqq/roomusic/backend/internal/platform/config"
	"github.com/qlqq/roomusic/backend/internal/platform/diagnostics"
)

// WithDiagnostics 把进程入口的日志环和启动时间注入应用。
func WithDiagnostics(logs *diagnostics.LogRing, startedAt time.Time) Option {
	return func(o *options) { o.diagnosticLogs = logs; o.startedAt = startedAt }
}
func newDiagnostics(database *sql.DB, settings config.Server, scans *library.Service, assembly options) *diagnostics.Service {
	logs := assembly.diagnosticLogs
	roots := append([]string{settings.DataDirectory, settings.Backup.Directory}, settings.AllowedLibraryRoots...)
	if logs == nil {
		logs = diagnostics.NewLogRing(roots...)
	}
	started := assembly.startedAt
	if started.IsZero() {
		started = time.Now()
	}
	return &diagnostics.Service{Database: database, StartedAt: started, Logs: logs, DataDirectory: settings.DataDirectory, BackupDirectory: settings.Backup.Directory,
		CatalogStats: func(ctx context.Context) (any, error) { return catalog.ReadCatalogStats(ctx, database) },
		MusicDirectories: func(ctx context.Context) ([]diagnostics.Directory, error) {
			registered, err := scans.ListRoots(ctx)
			if err != nil {
				return nil, err
			}
			out := []diagnostics.Directory{}
			known := append([]string(nil), roots...)
			for _, root := range registered {
				location, err := library.LoadRootLocation(ctx, database, root.ID)
				if err != nil {
					return nil, err
				}
				known = append(known, location.Path)
				if location.Active {
					out = append(out, diagnostics.Directory{Kind: "music", RootID: root.ID, Path: location.Path})
				}
			}
			logs.SetRoots(known...)
			return out, nil
		},
	}
}
