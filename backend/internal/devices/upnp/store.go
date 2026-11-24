package upnp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

func (s *Service) Settings(ctx context.Context) (Settings, error) {
	var v Settings
	err := s.db.QueryRowContext(ctx, `SELECT enabled, shared FROM upnp_settings WHERE id=1`).Scan(&v.Enabled, &v.Shared)
	return v, err
}

func (s *Service) manualDevices(ctx context.Context) ([]Device, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT udn,address,name,model,capabilities FROM upnp_manual_devices ORDER BY udn`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var devices []Device
	for rows.Next() {
		var d Device
		var caps []byte
		if err := rows.Scan(&d.UDN, &d.Address, &d.Name, &d.Model, &caps); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(caps, &d.Capabilities); err != nil {
			return nil, err
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

func (s *Service) saveManual(ctx context.Context, d Device) error {
	caps, err := json.Marshal(d.Capabilities)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO upnp_manual_devices(udn,address,name,model,capabilities)
	VALUES ($1,$2,$3,$4,$5) ON CONFLICT (udn) DO UPDATE SET address=excluded.address,
	name=excluded.name,model=excluded.model,capabilities=excluded.capabilities`, d.UDN, d.Address, d.Name, d.Model, caps)
	return err
}

func (s *Service) deleteManual(ctx context.Context, udn string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var removed string
	err = tx.QueryRowContext(ctx, `DELETE FROM upnp_manual_devices WHERE udn=$1 RETURNING udn`, udn).Scan(&removed)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrManualDevice
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM upnp_device_grants WHERE udn=$1`, udn); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) replaceGrants(ctx context.Context, udn string, ids []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM upnp_device_grants WHERE udn=$1`, udn); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO upnp_device_grants(udn,user_id) SELECT $1,unnest($2::text[])::uuid`, udn, ids)
	if err != nil {
		var p *pgconn.PgError
		if errors.As(err, &p) && p.Code == "23503" {
			return ErrGrantUser
		}
		return err
	}
	return tx.Commit()
}

func (s *Service) grants(ctx context.Context, filter string, args ...any) (map[string][]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT udn,user_id::text FROM upnp_device_grants`+filter+` ORDER BY udn,user_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	grants := make(map[string][]string)
	for rows.Next() {
		var udn, id string
		if err := rows.Scan(&udn, &id); err != nil {
			return nil, err
		}
		grants[udn] = append(grants[udn], id)
	}
	return grants, rows.Err()
}
