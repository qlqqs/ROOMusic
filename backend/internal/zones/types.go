package zones

import (
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/zones/queue"
)

var (
	ErrDevicePairingRequired = errors.New("device_pairing_required")
	ErrPrecisionUnavailable  = errors.New("精确对时不可用")
	ErrDeviceUnsupported     = errors.New("device_unsupported")
	ErrDeviceGrouped         = errors.New("device_grouped")
	ErrGroupUnsupported      = errors.New("group_unsupported")
	ErrGroupFull             = errors.New("group_full")
	ErrInvalidInput          = errors.New("invalid_input")
	ErrRevisionConflict      = errors.New("revision_conflict")
	ErrTrackNotFound         = errors.New("track_not_found")
	ErrQueueFull             = queue.ErrQueueFull
	ErrZoneOffline           = errors.New("zone_offline")
	ErrDeviceOnline          = errors.New("device_online")
	ErrVolumeFixed           = errors.New("volume_fixed")
	ErrRendererUnavailable   = errors.New("renderer_unavailable")
	ErrDatabaseUnavailable   = errors.New("database_unavailable")
	ErrClosed                = errors.New("zones service closed")
)

// Timestamp 固定输出 UTC 毫秒，避免零小数部分被标准编码器省略。
type Timestamp struct{ time.Time }

func (t Timestamp) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.UTC().Format("2006-01-02T15:04:05.000Z"))
}
func timestamp(t time.Time) Timestamp { return Timestamp{t.UTC()} }

type RegisterDeviceInput struct {
	Name     string        `json:"name"`
	Kind     string        `json:"kind"`
	Volume   float64       `json:"volume"`
	Muted    bool          `json:"muted"`
	Settings SettingsPatch `json:"settings"`
}

type ImportEntry struct {
	EntryID string `json:"entry_id"`
	TrackID string `json:"track_id"`
}

type Command struct {
	Takeover        bool          `json:"takeover,omitempty"`
	Command         string        `json:"command"`
	TrackIDs        []string      `json:"track_ids"`
	StartIndex      int           `json:"start_index"`
	EntryID         string        `json:"entry_id"`
	ToIndex         int           `json:"to_index"`
	Mode            queue.Mode    `json:"mode"`
	PositionSeconds float64       `json:"position_seconds"`
	Settings        SettingsPatch `json:"settings"`
	TargetZoneID    string        `json:"target_zone_id"`
	Entries         []ImportEntry `json:"entries"`
	CurrentEntryID  *string       `json:"current_entry_id"`
	History         []string      `json:"history"`

	ExpectedRevision     *int64   `json:"-"`
	StartPositionSeconds *float64 `json:"-"`
}

type QueueEntry struct {
	EntryID string               `json:"entry_id"`
	Track   catalog.TrackSummary `json:"track"`
}

type Device struct {
	Group             *DeviceGroup    `json:"group,omitempty"`
	SyncDelayMS       *int            `json:"sync_delay_ms,omitempty"`
	ExternalID        string          `json:"external_id,omitempty"`
	MaxSampleRate     int             `json:"max_sample_rate,omitempty"`
	FormatDeclared    *bool           `json:"format_declared,omitempty"`
	UnavailableReason string          `json:"unavailable_reason,omitempty"`
	OccupiedBy        *DeviceOccupant `json:"occupied_by,omitempty"`
	DeviceID          string          `json:"device_id"`
	ZoneID            string          `json:"zone_id"`
	Name              string          `json:"name"`
	Kind              string          `json:"kind"`
	Online            bool            `json:"online"`
	LastSeenAt        Timestamp       `json:"last_seen_at"`
	Volume            float64         `json:"volume"`
	Muted             bool            `json:"muted"`
	Capabilities      Capabilities    `json:"capabilities"`
	OutputSessionID   *string         `json:"output_session_id"`
}

type Fault struct {
	Kind string `json:"kind"`
}
type Failure struct {
	EntryID string `json:"entry_id"`
	Reason  string `json:"reason"`
}
type Position struct {
	Seconds         float64   `json:"seconds"`
	At              Timestamp `json:"at"`
	DurationSeconds *float64  `json:"duration_seconds"`
	Seekable        bool      `json:"seekable"`
}
type StreamAudioFormat struct {
	Codec      string `json:"codec"`
	SampleRate int    `json:"sample_rate"`
	BitDepth   *int   `json:"bit_depth"`
	Channels   int    `json:"channels"`
}
type StreamSegment struct {
	EntryID            string               `json:"entry_id"`
	Track              catalog.TrackSummary `json:"track"`
	StreamStartSeconds float64              `json:"stream_start_seconds"`
	TrackStartSeconds  float64              `json:"track_start_seconds"`
}
type Stream struct {
	Seq      int64              `json:"seq"`
	URL      string             `json:"url"`
	MimeType string             `json:"mime_type"`
	Quality  string             `json:"quality"`
	Format   *StreamAudioFormat `json:"format"`
	Segments []StreamSegment    `json:"segments"`
}
type ZoneEvent struct {
	Source   string `json:"source,omitempty"`
	Seq      int64  `json:"seq"`
	Kind     string `json:"kind"`
	EntryID  string `json:"entry_id,omitempty"`
	Reason   string `json:"reason,omitempty"`
	TrackID  string `json:"track_id,omitempty"`
	Restored *int   `json:"restored,omitempty"`
	Dropped  *int   `json:"dropped,omitempty"`
}
type Zone struct {
	LastPlayedAt  *time.Time   `json:"-"`
	Group         *ZoneGroup   `json:"group"`
	ZoneID        string       `json:"zone_id"`
	DeviceID      string       `json:"device_id"`
	Revision      int64        `json:"revision"`
	QueueRevision int64        `json:"queue_revision"`
	QueueLength   int          `json:"queue_length"`
	Mode          queue.Mode   `json:"mode"`
	Settings      ZoneSettings `json:"settings"`
	Current       *QueueEntry  `json:"current"`
	HasNext       bool         `json:"has_next"`
	Intent        string       `json:"intent"`
	Status        string       `json:"status"`
	Fault         *Fault       `json:"fault"`
	Failures      []Failure    `json:"failures"`
	Position      Position     `json:"position"`
	Stream        *Stream      `json:"stream"`
	NextStream    *Stream      `json:"next_stream"`
	PausedAt      *Timestamp   `json:"paused_at"`
	Signal        any          `json:"signal"`
	Event         *ZoneEvent   `json:"event"`
}
type Snapshot struct {
	ServerTime Timestamp `json:"server_time"`
	Protocol   int       `json:"protocol"`
	Devices    []Device  `json:"devices"`
	Zones      []Zone    `json:"zones"`
}
type QueueResult struct {
	ZoneID        string       `json:"zone_id"`
	QueueRevision int64        `json:"queue_revision"`
	Entries       []QueueEntry `json:"entries"`
}
type QueueChange struct {
	QueueRevision int64        `json:"queue_revision"`
	Kind          string       `json:"kind"`
	Entries       []QueueEntry `json:"entries,omitempty"`
	Index         *int         `json:"index,omitempty"`
	EntryID       string       `json:"entry_id,omitempty"`
	ToIndex       int          `json:"to_index,omitempty"`
}
type DeviceResult struct {
	ServerTime Timestamp `json:"server_time"`
	Device     Device    `json:"device"`
}
type RegisterDeviceResult struct {
	ServerTime Timestamp `json:"server_time"`
	Device     Device    `json:"device"`
	Zone       Zone      `json:"zone"`
}
type CommandResult struct {
	// AffectedEntries 在命令持锁时计数，供工具返回摘要，不改变播放区响应协议。
	AffectedEntries int          `json:"-"`
	ServerTime      Timestamp    `json:"server_time"`
	Zone            Zone         `json:"zone"`
	QueueChange     *QueueChange `json:"queue_change"`
	TargetZone      *Zone        `json:"target_zone,omitempty"`
	Imported        *bool        `json:"imported,omitempty"`
}

// Timer 与时钟、随机数和 ID 一起注入，不需要真实等待即可驱动边界。
type Timer interface{ Stop() bool }
type Options struct {
	NewMemberStreamFilter func(StreamFormat) MemberStreamFilter
	Logger                *slog.Logger
	Renderer              Renderer
	PlayRecorder          PlayRecorder
	IRReferences          IRReferences
	Now                   func() time.Time
	AfterFunc             func(time.Duration, func()) Timer
	Random                func() float64
	NewID                 func() string
}

// OutputState 是输出端管理器与控制面的窄接点，不实现认领或租约协议。
type OutputState struct {
	Online       bool
	Capabilities *Capabilities
}

func (c QueueChange) MarshalJSON() ([]byte, error) {
	fields := map[string]any{"queue_revision": c.QueueRevision, "kind": c.Kind}
	switch c.Kind {
	case "replace", "insert":
		entries := c.Entries
		if entries == nil {
			entries = []QueueEntry{}
		}
		fields["entries"] = entries
		if c.Kind == "insert" {
			fields["index"] = c.Index
		}
	case "remove":
		fields["entry_id"] = c.EntryID
	case "move":
		fields["entry_id"] = c.EntryID
		fields["to_index"] = c.ToIndex
	}
	return json.Marshal(fields)
}
