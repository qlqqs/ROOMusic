// Package personal 拥有成员自己的收藏与歌单，不参与共享目录治理。
package personal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

const TrackLimit = 10000
const MaxRevision int64 = 9007199254740991

var (
	ErrInvalidInput           = errors.New("invalid personal input")
	ErrNotFound               = errors.New("personal resource not found")
	ErrRevisionConflict       = errors.New("playlist revision conflict")
	ErrFavoritesChanged       = errors.New("favorites changed")
	ErrIdempotencyConflict    = errors.New("personal idempotency conflict")
	ErrPlaylistLimit          = errors.New("playlist limit reached")
	ErrTrackLimit             = errors.New("playlist track limit reached")
	ErrArtistCatalogPreparing = errors.New("artist catalog preparing")
	ErrArtistCatalogNotReady  = errors.New("artist catalog not ready")
)

// Catalog 是个人关系消费的只读目录端口；事务内实例共享同一个快照。
type Catalog interface {
	LoadTrackFacts(context.Context, []string) (map[string]catalog.TrackFact, error)
	LoadTrackCollectionStats(context.Context, []string) (catalog.TrackCollectionStats, error)
	LoadTrackSummaries(context.Context, []string) (map[string]catalog.TrackSummary, error)
	LoadReleaseGroupSummaries(context.Context, []string) (map[string]catalog.PersonalReleaseGroupSummary, error)
}

type Service struct {
	database         *sql.DB
	catalogFor       func(*sql.Tx) Catalog
	artistCatalogFor func(*sql.Tx) ArtistCatalog
}

func NewService(db *sql.DB) *Service {
	return &Service{
		database:         db,
		catalogFor:       func(tx *sql.Tx) Catalog { return catalog.NewStore(tx) },
		artistCatalogFor: func(tx *sql.Tx) ArtistCatalog { return catalog.NewStore(tx) },
	}
}

// Page 是经传输层校验的分页参数，Revision 为空表示读取当前版本。
type Page struct {
	Number, Size int
	Revision     *int64
}
type Pagination struct {
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Total    int64 `json:"total"`
}

func (p Page) pagination(total int64) Pagination { return Pagination{p.Number, p.Size, total} }
func (s *Service) readTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	if err := setLocalReadJITOff(ctx, tx); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("configure personal read transaction: %w", err)
	}
	return tx, nil
}

func setLocalReadJITOff(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `SET LOCAL jit = off`); err != nil {
		return fmt.Errorf("set transaction-local JIT: %w", err)
	}
	return nil
}

// Command 是歌单写入的规范化意图，字段仅供相应命令使用；不包含 HTTP 状态或鉴权上下文。
type Command struct {
	Kind             string   `json:"command"`
	PlaylistID       string   `json:"playlist_id"`
	ItemID           string   `json:"item_id"`
	ExpectedRevision int64    `json:"expected_revision"`
	Name             string   `json:"name"`
	TrackIDs         []string `json:"track_ids"`
	Token            string   `json:"token"`
	Action           string   `json:"action"`
	BeforeItemID     *string  `json:"before_item_id"`
}

// Result 保存首次业务响应；传输层按 Status 和 Body 原样呈现，重放不重新投影。
type Result struct {
	Status int
	Body   json.RawMessage
}

func NormalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 100 {
		return "", ErrInvalidInput
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", ErrInvalidInput
		}
	}
	return name, nil
}
func normalizeIDs(ids []string, limit int) ([]string, error) {
	if len(ids) > limit {
		return nil, ErrInvalidInput
	}
	result := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if !uuid.Valid(id) {
			return nil, ErrInvalidInput
		}
		id = strings.ToLower(id)
		if !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	return result, nil
}
