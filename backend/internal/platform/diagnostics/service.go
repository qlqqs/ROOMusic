package diagnostics

import (
	"context"
	"database/sql"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/buildinfo"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"golang.org/x/sys/unix"
)

// Directory 的 Path 只用于内部探测，永不序列化。
type Directory struct {
	Kind   string
	RootID string
	Path   string
}

type Service struct {
	Database         *sql.DB
	StartedAt        time.Time
	Logs             *LogRing
	DataDirectory    string
	BackupDirectory  string
	MusicDirectories func(context.Context) ([]Directory, error)
	CatalogStats     func(context.Context) (any, error)
}

type Status struct {
	Build             map[string]string `json:"build"`
	StartedAt         time.Time         `json:"started_at"`
	UptimeSeconds     int64             `json:"uptime_seconds"`
	DatabaseConnected bool              `json:"database_connected"`
	DatabasePool      *sql.DBStats      `json:"database_pool"`
	MigrationVersion  *int64            `json:"migration_version"`
	Catalog           any               `json:"catalog"`
	Errors            []string          `json:"errors"`
}

func (s *Service) SystemStatus(ctx context.Context) (Status, error) {
	build := buildinfo.Current()
	version := build.Version
	if version == "" {
		version = "dev"
	}
	out := Status{Build: map[string]string{"version": version, "commit": build.Commit}, StartedAt: s.StartedAt.UTC(), UptimeSeconds: max(0, int64(time.Since(s.StartedAt).Seconds())), Errors: []string{}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if s.Database == nil {
		out.Errors = append(out.Errors, "database_unavailable")
		return out, nil
	}
	stats := s.Database.Stats()
	out.DatabasePool = &stats
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	err := s.Database.PingContext(pingCtx)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		out.Errors = append(out.Errors, "database_unavailable")
		return out, nil
	}
	out.DatabaseConnected = true
	versionNumber, err := postgres.CurrentMigrationVersion(ctx, s.Database)
	if err != nil {
		out.Errors = append(out.Errors, "migration_unavailable")
	} else {
		out.MigrationVersion = &versionNumber
	}
	out.Catalog, err = s.CatalogStats(ctx)
	if err != nil {
		out.Catalog = nil
		out.Errors = append(out.Errors, "catalog_unavailable")
	}
	return out, ctx.Err()
}

type DiskUsage struct {
	Kind           string  `json:"kind"`
	RootID         string  `json:"root_id,omitempty"`
	Name           string  `json:"name"`
	TotalBytes     *uint64 `json:"total_bytes"`
	AvailableBytes *uint64 `json:"available_bytes"`
	ErrorCode      string  `json:"error_code,omitempty"`
}

func (s *Service) DiskUsage(ctx context.Context) ([]DiskUsage, error) {
	directories := []Directory{{Kind: "data", Path: s.DataDirectory}, {Kind: "backup", Path: s.BackupDirectory}}
	roots, err := s.MusicDirectories(ctx)
	if err != nil {
		return nil, err
	}
	directories = append(directories, roots...)
	out := make([]DiskUsage, 0, len(directories))
	for _, dir := range directories {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		item := DiskUsage{Kind: dir.Kind, RootID: dir.RootID, Name: safeBase(dir.Path)}
		stat, err := diskStat(ctx, dir.Path)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			item.ErrorCode = "disk_unavailable"
		} else {
			total, available := stat.Blocks*uint64(stat.Bsize), stat.Bavail*uint64(stat.Bsize)
			item.TotalBytes = &total
			item.AvailableBytes = &available
		}
		out = append(out, item)
	}
	return out, nil
}

// 系统调用不能被 context 中断；限制在途数量，超时后由完成的调用自行释放槽位。
var diskStatSlots = make(chan struct{}, 4)

func diskStat(ctx context.Context, path string) (unix.Statfs_t, error) {
	probe, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	select {
	case diskStatSlots <- struct{}{}:
	case <-probe.Done():
		return unix.Statfs_t{}, probe.Err()
	}
	type result struct {
		stat unix.Statfs_t
		err  error
	}
	done := make(chan result, 1)
	go func() {
		defer func() { <-diskStatSlots }()
		var out result
		out.err = unix.Statfs(path, &out.stat)
		done <- out
	}()
	select {
	case out := <-done:
		return out.stat, out.err
	case <-probe.Done():
		return unix.Statfs_t{}, probe.Err()
	}
}
