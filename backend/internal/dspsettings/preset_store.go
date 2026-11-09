package dspsettings

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

type presetDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type presetStore struct{ db presetDB }

const presetColumns = `id::text, name, peq, created_at, updated_at`

// 同一成员的配额检查与写入串行；独立命名空间 PEQP 不锁身份模块的表。
func (store presetStore) lockOwner(ctx context.Context, userID string) error {
	sum := sha256.Sum256([]byte(userID))
	_, err := store.db.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1::integer, $2::integer)`, int32(0x50455150), int32(binary.BigEndian.Uint32(sum[:4])))
	return err
}

func (store presetStore) list(ctx context.Context, userID string) ([]Preset, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT `+presetColumns+` FROM dsp_peq_presets WHERE user_id=$1 ORDER BY name, id LIMIT $2`, userID, PresetLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Preset, 0)
	for rows.Next() {
		preset, err := scanPreset(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, preset)
	}
	return result, rows.Err()
}

func (store presetStore) get(ctx context.Context, userID, id string) (Preset, error) {
	return scanPreset(store.db.QueryRowContext(ctx, `SELECT `+presetColumns+` FROM dsp_peq_presets WHERE user_id=$1 AND id=$2`, userID, id))
}

func (store presetStore) count(ctx context.Context, userID string) (int, error) {
	var count int
	err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM dsp_peq_presets WHERE user_id=$1`, userID).Scan(&count)
	return count, err
}

func (store presetStore) nameTaken(ctx context.Context, userID, name, exceptID string) (bool, error) {
	var exists bool
	err := store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dsp_peq_presets WHERE user_id=$1 AND name=$2 AND id<>$3)`, userID, name, exceptID).Scan(&exists)
	return exists, err
}

func (store presetStore) create(ctx context.Context, userID string, preset Preset) (Preset, error) {
	raw, err := preset.PEQ.MarshalJSON()
	if err != nil {
		return Preset{}, err
	}
	result, err := scanPreset(store.db.QueryRowContext(ctx, `INSERT INTO dsp_peq_presets(id,user_id,name,peq) VALUES($1,$2,$3,$4::jsonb) RETURNING `+presetColumns, preset.ID, userID, preset.Name, string(raw)))
	return result, classifyPresetWrite(err)
}

func (store presetStore) update(ctx context.Context, userID string, preset Preset) (Preset, error) {
	raw, err := preset.PEQ.MarshalJSON()
	if err != nil {
		return Preset{}, err
	}
	result, err := scanPreset(store.db.QueryRowContext(ctx, `UPDATE dsp_peq_presets SET name=$3, peq=$4::jsonb, updated_at=now() WHERE user_id=$1 AND id=$2 RETURNING `+presetColumns, userID, preset.ID, preset.Name, string(raw)))
	return result, classifyPresetWrite(err)
}

func (store presetStore) delete(ctx context.Context, userID, id string) error {
	result, err := store.db.ExecContext(ctx, `DELETE FROM dsp_peq_presets WHERE user_id=$1 AND id=$2`, userID, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func scanPreset(row interface{ Scan(...any) error }) (Preset, error) {
	var result Preset
	var raw []byte
	if err := row.Scan(&result.ID, &result.Name, &raw, &result.CreatedAt, &result.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Preset{}, ErrNotFound
		}
		return Preset{}, err
	}
	document, err := zones.ParsePEQDocument(raw)
	if err != nil {
		return Preset{}, fmt.Errorf("存储的 PEQ 预设文档无效")
	}
	result.PEQ = document
	return result, nil
}

func classifyPresetWrite(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" && pg.ConstraintName == "dsp_peq_presets_user_id_name_key" {
		return ErrPresetNameTaken
	}
	return err
}
