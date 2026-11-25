package auth

import (
	"context"
	"database/sql"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/identity"
)

var ErrJukeboxZoneNotFound = errors.New("jukebox zone not found")

const jukeboxZoneSelection = `(SELECT z.id FROM subsonic_jukebox_settings j
        JOIN playback_zones z ON z.id=j.jukebox_zone_id AND z.user_id=j.user_id
        WHERE j.user_id=$1)`

func (s *Service) JukeboxZone(ctx context.Context, userID string) (*string, error) {
	var zoneID *string
	err := s.db.QueryRowContext(ctx, `SELECT `+jukeboxZoneSelection, userID).Scan(&zoneID)
	return zoneID, err
}

func (s *Service) SetJukeboxZone(ctx context.Context, user identity.User, zoneID *string) (Status, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Status{}, err
	}
	defer tx.Rollback()
	var enabled bool
	if err := tx.QueryRowContext(ctx, `SELECT subsonic_enabled FROM subsonic_settings WHERE id=1 FOR SHARE`).Scan(&enabled); err != nil {
		return Status{}, err
	}
	if !enabled {
		return Status{}, ErrDisabled
	}
	if zoneID == nil {
		_, err = tx.ExecContext(ctx, `DELETE FROM subsonic_jukebox_settings WHERE user_id=$1`, user.ID)
	} else {
		var ownedID string
		err = tx.QueryRowContext(ctx, `SELECT z.id FROM playback_zones z JOIN playback_devices d ON d.id=z.device_id
            WHERE z.id=$2 AND z.user_id=$1 AND d.kind <> 'subsonic' FOR KEY SHARE OF z`, user.ID, *zoneID).Scan(&ownedID)
		if errors.Is(err, sql.ErrNoRows) {
			return Status{}, ErrJukeboxZoneNotFound
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO subsonic_jukebox_settings(user_id,jukebox_zone_id) VALUES($1,$2)
                ON CONFLICT (user_id) DO UPDATE SET jukebox_zone_id=EXCLUDED.jukebox_zone_id`, user.ID, ownedID)
		}
	}
	if err != nil {
		return Status{}, err
	}
	state, err := s.status(ctx, tx, user)
	if err != nil {
		return Status{}, err
	}
	return state, tx.Commit()
}
