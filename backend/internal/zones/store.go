// Package zones 拥有用户的设备、播放区与队列持久化。
package zones

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/qlqq/roomusic/backend/internal/zones/queue"
)

// ErrNotFound 表示播放区或条目不存在，或不属于指定用户。
var ErrNotFound = errors.New("playback resource not found")

// StreamFormatCapability 是设备声明的一种可解码流格式。
type StreamFormatCapability struct {
	Codec         string `json:"codec"`
	MIMEType      string `json:"mime_type"`
	Lossless      bool   `json:"lossless"`
	MaxSampleRate *int   `json:"max_sample_rate"`
	MaxBitDepth   *int   `json:"max_bit_depth"`
}

// Capabilities 保存设备最近一次认领时声明的能力。
type Capabilities struct {
	StreamFormats []StreamFormatCapability `json:"stream_formats"`
	MaxChannels   int                      `json:"max_channels"`
	DSD           string                   `json:"dsd"`
	Exclusive     bool                     `json:"exclusive"`
	Volume        string                   `json:"volume"`
}

// PersistedDevice 不含在线状态、输出会话等仅属于运行时的字段。
type PersistedDevice struct {
	DeviceID     string
	Kind         string
	Name         string
	Capabilities Capabilities
	Volume       float64
	Muted        bool
	CreatedAt    time.Time
	LastSeenAt   time.Time
}

// PersistedZone 保存播放区的持久状态；空 CurrentEntryID 和 nil ImportedAt 表示未设置。
type PersistedZone struct {
	ZoneID          string
	DeviceID        string
	Queue           queue.State
	PositionSeconds float64
	PositionSavedAt time.Time
	Settings        PlaybackSettings
	Revision        int64
	QueueRevision   int64
	ImportedAt      *time.Time
}

// UserState 是同一快照内的设备与播放区，均按设备创建时间、设备 ID 升序排列。
type UserState struct {
	Devices []PersistedDevice
	Zones   []PersistedZone
}

// Store 将存储原语绑定到调用者的事务，不负责开始、提交或回滚事务。
// 调用者在播放区锁内组合条目写入与 UpdateZone；任一步失败必须回滚整个事务。
type Store struct {
	tx *sql.Tx
}

func NewStore(tx *sql.Tx) *Store { return &Store{tx: tx} }

// UpdateZone 写入播放区行的可变状态，不改设备归属或队列条目。
// Queue.Entries 不参与此操作；条目变更须在同一事务内调用对应原语。
// 版本号及队列语义由调用者计算，本层不自行递增或验证当前项的归属。
func (s *Store) UpdateZone(ctx context.Context, userID string, zone PersistedZone) error {
	document, err := NewHeadroomDocument(zone.Settings.Headroom)
	if err != nil {
		return fmt.Errorf("encode playback headroom: %w", err)
	}
	headroom, err := document.MarshalJSON()
	if err != nil {
		return fmt.Errorf("encode playback headroom: %w", err)
	}
	sampleRateDocument, err := ParseSampleRateDocument([]byte(zone.Settings.SampleRate.canonical))
	if err != nil {
		return fmt.Errorf("encode playback sample rate: %w", err)
	}
	sampleRate, err := sampleRateDocument.MarshalJSON()
	if err != nil {
		return fmt.Errorf("encode playback sample rate: %w", err)
	}
	result, err := s.tx.ExecContext(ctx, `
UPDATE playback_zones SET
 mode=$3, current_entry_id=NULLIF($4, '')::uuid,
 position_seconds=$5, position_saved_at=$6,
 history=COALESCE($7::text[]::uuid[], '{}'::uuid[]),
 shuffle_bag=COALESCE($8::text[]::uuid[], '{}'::uuid[]),
 leveling=$9, crossfade=$10, crossfade_seconds=$11, gapless=$12, quality=$13,
 revision=$14, queue_revision=$15, imported_at=$16, headroom=$17::jsonb, sample_rate=$18::jsonb
WHERE user_id=$1::uuid AND id=$2::uuid`,
		userID, zone.ZoneID, zone.Queue.Mode, zone.Queue.CurrentEntryID,
		zone.PositionSeconds, zone.PositionSavedAt, zone.Queue.History, zone.Queue.Bag,
		zone.Settings.Leveling, zone.Settings.Crossfade, zone.Settings.CrossfadeSeconds,
		zone.Settings.Gapless, zone.Settings.Quality, zone.Revision, zone.QueueRevision, zone.ImportedAt, headroom, sampleRate)
	if err != nil {
		return fmt.Errorf("update playback zone: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count updated playback zones: %w", err)
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}
