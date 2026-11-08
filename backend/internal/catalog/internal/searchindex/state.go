// Package searchindex 管理目录搜索的派生值、版本状态与共用匹配辅助。
package searchindex

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

const ProjectionVersion = 2

var ErrVersionMismatch = errors.New("catalog search projection version mismatch")

// Queryer 可由只读快照事务或维护专用连接实现。
type Queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// State 必须在执行搜索的同一只读快照中加载，不能作为跨请求缓存。
type State struct {
	TargetGeneration, PublishedGeneration                      int64
	TextnormVersion, SearchProjectionVersion                   int64
	PublishedTextnormVersion, PublishedSearchProjectionVersion int64
	CatalogEpoch                                               int64
	BuildID, Status                                            string
	ManifestSealed                                             bool
	LiveUpdated                                                int64
	StartedAt                                                  time.Time
}

func LoadState(ctx context.Context, q Queryer) (State, error) {
	var s State
	err := q.QueryRowContext(ctx, `SELECT target_generation,published_generation,textnorm_version,search_projection_version,
  published_textnorm_version,published_search_projection_version,catalog_epoch,build_id::text,status,manifest_sealed,live_updated,started_at
  FROM catalog_projection_state WHERE capability='search'`).Scan(
		&s.TargetGeneration, &s.PublishedGeneration, &s.TextnormVersion, &s.SearchProjectionVersion,
		&s.PublishedTextnormVersion, &s.PublishedSearchProjectionVersion, &s.CatalogEpoch, &s.BuildID, &s.Status, &s.ManifestSealed, &s.LiveUpdated, &s.StartedAt)
	return s, err
}

func (s State) Ready() bool {
	return s.Status == "ready" && s.ManifestSealed && s.TargetGeneration > 0 &&
		s.TargetGeneration == s.PublishedGeneration && s.TextnormVersion == s.PublishedTextnormVersion &&
		s.SearchProjectionVersion == s.PublishedSearchProjectionVersion && s.CurrentVersion()
}

// CurrentVersion 阻止不知道目标版本的进程写入或读取新投影。
func (s State) CurrentVersion() bool {
	return s.TextnormVersion == int64(textnorm.Version) && s.SearchProjectionVersion == ProjectionVersion
}
