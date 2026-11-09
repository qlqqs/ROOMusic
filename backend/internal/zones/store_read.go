package zones

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/qlqq/roomusic/backend/internal/zones/queue"
)

// LoadUser 在显式只读可重复读事务内加载本人的全部设备、播放区和条目。
// 这是独立的读取边界；写命令通过 NewStore 共享调用者的事务。
func LoadUser(ctx context.Context, database *sql.DB, userID string) (UserState, error) {
	return loadUser(ctx, database, userID, slog.Default())
}

func loadUser(ctx context.Context, database *sql.DB, userID string, logger *slog.Logger) (UserState, error) {
	tx, err := database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return UserState{}, fmt.Errorf("begin playback snapshot: %w", err)
	}
	defer tx.Rollback()

	state := UserState{Devices: []PersistedDevice{}, Zones: []PersistedZone{}}
	if err := loadDevices(ctx, tx, userID, &state); err != nil {
		return UserState{}, err
	}
	if err := loadZones(ctx, tx, userID, &state, logger); err != nil {
		return UserState{}, err
	}
	if err := loadEntries(ctx, tx, userID, &state); err != nil {
		return UserState{}, err
	}
	if err := tx.Commit(); err != nil {
		return UserState{}, fmt.Errorf("commit playback snapshot: %w", err)
	}
	return state, nil
}

func loadDevices(ctx context.Context, tx *sql.Tx, userID string, state *UserState) error {
	rows, err := tx.QueryContext(ctx, `
SELECT id, kind, name, capabilities, volume, muted, created_at, last_seen_at
FROM playback_devices WHERE user_id=$1::uuid ORDER BY created_at, id`, userID)
	if err != nil {
		return fmt.Errorf("load playback devices: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		device := PersistedDevice{Capabilities: Capabilities{
			StreamFormats: []StreamFormatCapability{}, MaxChannels: 2, DSD: "none", Volume: "number",
		}}
		var capabilities []byte
		if err := rows.Scan(&device.DeviceID, &device.Kind, &device.Name, &capabilities,
			&device.Volume, &device.Muted, &device.CreatedAt, &device.LastSeenAt); err != nil {
			return fmt.Errorf("scan playback device: %w", err)
		}
		if err := json.Unmarshal(capabilities, &device.Capabilities); err != nil {
			return fmt.Errorf("decode playback device capabilities: %w", err)
		}
		state.Devices = append(state.Devices, device)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate playback devices: %w", err)
	}
	return nil
}

func loadZones(ctx context.Context, tx *sql.Tx, userID string, state *UserState, logger *slog.Logger) error {
	rows, err := tx.QueryContext(ctx, `
SELECT z.id, z.device_id, z.mode, z.current_entry_id,
 z.position_seconds, z.position_saved_at, to_json(z.history), to_json(z.shuffle_bag),
 z.leveling, z.crossfade, z.crossfade_seconds, z.gapless, z.quality,
 z.revision, z.queue_revision, z.imported_at, z.headroom, z.sample_rate, z.downmix
FROM playback_zones z
JOIN playback_devices d ON d.id=z.device_id AND d.user_id=z.user_id
WHERE z.user_id=$1::uuid ORDER BY d.created_at, d.id, z.id`, userID)
	if err != nil {
		return fmt.Errorf("load playback zones: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		zone := PersistedZone{Queue: queue.State{Entries: []queue.Entry{}}, Settings: defaultSettings()}
		var current sql.NullString
		var imported sql.NullTime
		var history, bag, headroom, sampleRate, downmix []byte
		if err := rows.Scan(&zone.ZoneID, &zone.DeviceID, &zone.Queue.Mode, &current,
			&zone.PositionSeconds, &zone.PositionSavedAt, &history, &bag,
			&zone.Settings.Leveling, &zone.Settings.Crossfade, &zone.Settings.CrossfadeSeconds,
			&zone.Settings.Gapless, &zone.Settings.Quality,
			&zone.Revision, &zone.QueueRevision, &imported, &headroom, &sampleRate, &downmix); err != nil {
			return fmt.Errorf("scan playback zone: %w", err)
		}
		zone.Settings.Headroom = readHeadroom(ctx, logger, zone.ZoneID, headroom)
		zone.Settings.SampleRate = readSampleRate(ctx, logger, zone.ZoneID, sampleRate)
		zone.Settings.Downmix = readDownmix(ctx, logger, zone.ZoneID, downmix)
		zone.Queue.CurrentEntryID = current.String
		if imported.Valid {
			zone.ImportedAt = &imported.Time
		}
		if err := json.Unmarshal(history, &zone.Queue.History); err != nil {
			return fmt.Errorf("decode playback history: %w", err)
		}
		if err := json.Unmarshal(bag, &zone.Queue.Bag); err != nil {
			return fmt.Errorf("decode playback shuffle bag: %w", err)
		}
		state.Zones = append(state.Zones, zone)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate playback zones: %w", err)
	}
	return nil
}

func loadEntries(ctx context.Context, tx *sql.Tx, userID string, state *UserState) error {
	rows, err := tx.QueryContext(ctx, `
SELECT e.zone_id, e.entry_id, e.track_id
FROM playback_queue_entries e
JOIN playback_zones z ON z.id=e.zone_id
JOIN playback_devices d ON d.id=z.device_id AND d.user_id=z.user_id
WHERE z.user_id=$1::uuid ORDER BY d.created_at, d.id, z.id, e.position, e.entry_id`, userID)
	if err != nil {
		return fmt.Errorf("load playback queue entries: %w", err)
	}
	defer rows.Close()
	zoneIndexes := make(map[string]int, len(state.Zones))
	for index, zone := range state.Zones {
		zoneIndexes[zone.ZoneID] = index
	}
	for rows.Next() {
		var zoneID string
		var entry queue.Entry
		if err := rows.Scan(&zoneID, &entry.EntryID, &entry.TrackID); err != nil {
			return fmt.Errorf("scan playback queue entry: %w", err)
		}
		zone := &state.Zones[zoneIndexes[zoneID]]
		zone.Queue.Entries = append(zone.Queue.Entries, entry)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate playback queue entries: %w", err)
	}
	return nil
}

// readHeadroom 只降级当前键，不写库，不吞掉查询或队列损坏错误。
func readHeadroom(ctx context.Context, logger *slog.Logger, zoneID string, raw []byte) Headroom {
	document, err := ParseHeadroomDocument(raw)
	if err == nil {
		var headroom Headroom
		headroom, err = DecodeSettingsDocument[Headroom](document)
		if err == nil {
			return headroom
		}
	}
	logger.WarnContext(ctx, "播放区设置无效，使用默认值",
		"event", "zones.settings.invalid", "module", "zones",
		"zone_id", zoneID, "key", "headroom", "reason", "invalid_document")
	return defaultHeadroom()
}

// readSampleRate 的坏值仅回退当前键，日志不包含文档内容。
func readSampleRate(ctx context.Context, logger *slog.Logger, zoneID string, raw []byte) SettingsDocument {
	document, err := ParseSampleRateDocument(raw)
	if err == nil {
		return document
	}
	logger.WarnContext(ctx, "播放区设置无效，使用默认值",
		"event", "zones.settings.invalid", "module", "zones",
		"zone_id", zoneID, "key", "sample_rate", "reason", "invalid_document")
	return DefaultSampleRateDocument()
}

// readDownmix 的坏值仅回退当前键，日志不包含文档内容。
func readDownmix(ctx context.Context, logger *slog.Logger, zoneID string, raw []byte) SettingsDocument {
	document, err := ParseDownmixDocument(raw)
	if err == nil {
		return document
	}
	logger.WarnContext(ctx, "播放区设置无效，使用默认值",
		"event", "zones.settings.invalid", "module", "zones",
		"zone_id", zoneID, "key", "downmix", "reason", "invalid_document")
	return DefaultDownmixDocument()
}
