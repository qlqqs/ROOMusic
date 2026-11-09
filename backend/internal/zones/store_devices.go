package zones

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

func (s *Store) CreateDevice(ctx context.Context, userID string, d PersistedDevice, p PersistedZone) error {
	caps, err := json.Marshal(d.Capabilities)
	if err != nil {
		return err
	}
	_, err = s.tx.ExecContext(ctx, `INSERT INTO playback_devices(id,user_id,kind,name,capabilities,volume,muted,created_at,last_seen_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, d.DeviceID, userID, d.Kind, d.Name, caps, d.Volume, d.Muted, d.CreatedAt, d.LastSeenAt)
	if err != nil {
		return fmt.Errorf("create playback device: %w", err)
	}
	_, err = s.tx.ExecContext(ctx, `INSERT INTO playback_zones(id,user_id,device_id,position_saved_at) VALUES($1,$2,$3,$4)`, p.ZoneID, userID, p.DeviceID, p.PositionSavedAt)
	if err != nil {
		return fmt.Errorf("create playback zone: %w", err)
	}
	return s.UpdateZone(ctx, userID, p)
}
func (s *Store) UpdateDevice(ctx context.Context, userID string, d PersistedDevice) error {
	caps, err := json.Marshal(d.Capabilities)
	if err != nil {
		return err
	}
	result, err := s.tx.ExecContext(ctx, `UPDATE playback_devices SET name=$3,volume=$4,muted=$5,capabilities=$6,last_seen_at=$7 WHERE user_id=$1::uuid AND id=$2::uuid`, userID, d.DeviceID, d.Name, d.Volume, d.Muted, caps, d.LastSeenAt)
	if err != nil {
		return fmt.Errorf("update playback device: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) DeleteDevice(ctx context.Context, userID, deviceID string, before *time.Time) (bool, error) {
	result, err := s.tx.ExecContext(ctx, `DELETE FROM playback_devices WHERE user_id=$1::uuid AND id=$2::uuid AND ($3::timestamptz IS NULL OR last_seen_at<$3)`, userID, deviceID, before)
	if err != nil {
		return false, fmt.Errorf("delete playback device: %w", err)
	}
	n, err := result.RowsAffected()
	return n > 0, err
}
func (s *Store) TransferEntries(ctx context.Context, userID, sourceID, targetID string) error {
	if err := s.ReplaceEntries(ctx, userID, targetID, nil); err != nil {
		return err
	}
	_, err := s.tx.ExecContext(ctx, `UPDATE playback_queue_entries e SET zone_id=target.id FROM playback_zones source,playback_zones target WHERE source.user_id=$1::uuid AND target.user_id=$1::uuid AND source.id=$2::uuid AND target.id=$3::uuid AND e.zone_id=source.id`, userID, sourceID, targetID)
	if err != nil {
		return fmt.Errorf("transfer playback entries: %w", err)
	}
	return nil
}
