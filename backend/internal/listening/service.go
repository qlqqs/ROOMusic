// Package listening 拥有播放历史：每个用户各自的真实播放记录（play_history 表）、计一次播放的服务端校验、
// “最近播放的发行”、按曲目汇总的历史读取与删除。记录引用曲目 ID，发行归属属于 catalog，只经 catalog 的公开读取函数换算，
// 这里不读目录的私有表。本包不含 HTTP；用户 ID、幂等键与曲目 ID 都必须是 UUID 文本，由传输层校验。
package listening

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/qlqq/roomusic/backend/internal/catalog"
)

var (
	// ErrTrackNotFound 表示曲目不存在，或它的来源已不是 present（与 catalog 的同名错误是同一个值）。
	ErrTrackNotFound = catalog.ErrTrackNotFound
	// ErrInvalidPlay 表示 played_seconds 不是合理的数值（见 ValidPlayedSeconds）。
	ErrInvalidPlay = errors.New("invalid play")
	// ErrPlayTooShort 表示曲目时长已知，而累计出声秒数低于门槛（见 Threshold）。
	ErrPlayTooShort = errors.New("play too short")
	// ErrIdempotencyConflict 表示同一用户的幂等键已经用于另一个请求（曲目或秒数不同）。
	ErrIdempotencyConflict = errors.New("idempotency key reused with a different play")
)

// RecentReleaseLimit 是“最近播放”最多返回的发行数。
const RecentReleaseLimit = 8

// 最近播放按曲目的最后播放时间倒序分批读取，直到凑满 RecentReleaseLimit 个发行：发行归属属于 catalog，
// 这里不能在自己的 SQL 里联表分组。一批 200 首不同曲目、最多 10 批，即最多读 2,000 首不同曲目。
const (
	recentBatchSize  = 200
	recentMaxBatches = 10
)

// Database 是播放历史需要的最小数据库能力；*sql.DB 满足它。
type Database interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Catalog 是播放历史从目录读取的公开能力；*catalog.Store 满足它。
type Catalog interface {
	LoadTrackDuration(ctx context.Context, trackID string) (float64, error)
	LoadTrackPlacements(ctx context.Context, trackIDs []string) (map[string]catalog.TrackPlacement, error)
	LoadTrackSummaries(ctx context.Context, trackIDs []string) (map[string]catalog.TrackSummary, error)
}

// Service 是播放历史的应用服务。它不做鉴权：调用方（传输层）把当前会话用户的 ID 传进来，所有 SQL 都带 user_id。
type Service struct {
	database Database
	catalog  Catalog
	// 分批参数只在测试里调小，用来覆盖多批读取。
	batchSize, maxBatches int
}

// NewService 返回读取 database 的 Service；曲目时长与所在发行经 catalog.Store 读取。
func NewService(database *sql.DB) *Service {
	return newService(database, catalog.NewStore(database))
}

// newService 允许测试替换目录端口。
func newService(database Database, tracks Catalog) *Service {
	return &Service{database: database, catalog: tracks, batchSize: recentBatchSize, maxBatches: recentMaxBatches}
}

// Recorded 是一条已保存的播放记录，也是记录接口的响应。
type Recorded struct {
	ID       string    `json:"id"`
	TrackID  string    `json:"track_id"`
	PlayedAt time.Time `json:"played_at"`
}

// Record 为用户记下一次播放。检查顺序：数值合理（ErrInvalidPlay）→ 曲目是 present（ErrTrackNotFound）→
// 时长已知时达到门槛（ErrPlayTooShort）→ 写入。写入是一条 INSERT … ON CONFLICT (user_id, idempotency_key) DO NOTHING：
// 同一用户同一个键重放时，曲目与秒数都相同则返回首次记录（调用方照常回 201），否则 ErrIdempotencyConflict，都不写新行；
// 并发的同键请求由唯一约束串行，只会写一行。played_at 是服务端收到的时间。
func (s *Service) Record(ctx context.Context, userID, idempotencyKey, trackID string, playedSeconds float64) (Recorded, error) {
	if !ValidPlayedSeconds(playedSeconds) {
		return Recorded{}, ErrInvalidPlay
	}
	duration, err := s.catalog.LoadTrackDuration(ctx, trackID)
	if err != nil {
		return Recorded{}, err
	}
	if !meetsThreshold(duration, playedSeconds) {
		return Recorded{}, ErrPlayTooShort
	}
	var recorded Recorded
	err = s.database.QueryRowContext(ctx, `INSERT INTO play_history (user_id, track_id, idempotency_key, played_seconds)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4)
		ON CONFLICT (user_id, idempotency_key) DO NOTHING
		RETURNING id::text, track_id::text, played_at`, userID, trackID, idempotencyKey, playedSeconds).Scan(&recorded.ID, &recorded.TrackID, &recorded.PlayedAt)
	switch {
	case err == nil:
		recorded.PlayedAt = recorded.PlayedAt.UTC()
		return recorded, nil
	case errors.Is(err, sql.ErrNoRows):
		return s.replay(ctx, userID, idempotencyKey, trackID, playedSeconds)
	case isForeignKeyViolation(err):
		return Recorded{}, ErrTrackNotFound
	default:
		return Recorded{}, fmt.Errorf("record play: %w", err)
	}
}

// replay 处理键已存在的情形：ON CONFLICT 返回时冲突的那一行已经提交，所以这里一定读得到它（除非被并发的清除删掉，
// 那就按一般的数据库错误报告，调用方稍后重试会写成新记录）。
func (s *Service) replay(ctx context.Context, userID, idempotencyKey, trackID string, playedSeconds float64) (Recorded, error) {
	var existing Recorded
	var storedSeconds float64
	err := s.database.QueryRowContext(ctx, `SELECT id::text, track_id::text, played_seconds, played_at
		FROM play_history WHERE user_id = $1::uuid AND idempotency_key = $2::uuid`, userID, idempotencyKey).Scan(&existing.ID, &existing.TrackID, &storedSeconds, &existing.PlayedAt)
	if err != nil {
		return Recorded{}, fmt.Errorf("read the play recorded under the idempotency key: %w", err)
	}
	if !strings.EqualFold(existing.TrackID, trackID) || storedSeconds != playedSeconds {
		return Recorded{}, ErrIdempotencyConflict
	}
	existing.PlayedAt = existing.PlayedAt.UTC()
	return existing, nil
}

// Clear 删除用户的全部播放记录，返回删除的行数；没有记录时是 0，所以天然幂等。
func (s *Service) Clear(ctx context.Context, userID string) (int64, error) {
	result, err := s.database.ExecContext(ctx, `DELETE FROM play_history WHERE user_id = $1::uuid`, userID)
	if err != nil {
		return 0, fmt.Errorf("clear play history: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("clear play history: %w", err)
	}
	return deleted, nil
}

// isForeignKeyViolation 判断错误是否是外键冲突（SQLSTATE 23503）。曲目从不物理删除，这里只是兜底，不让它变成 500。
func isForeignKeyViolation(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "23503"
}
