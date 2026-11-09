package dspsettings

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
)

type irStore struct{ db presetDB }

const irColumns = `id::text,name,file_name,container,sample_rate,channels,paths,taps,created_at,updated_at`

func scanIR(row interface{ Scan(...any) error }) (IRFile, error) {
	var f IRFile
	err := row.Scan(&f.ID, &f.Name, &f.FileName, &f.Container, &f.SampleRate, &f.Channels, &f.Paths, &f.Taps, &f.CreatedAt, &f.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return f, err
}

func (s irStore) list(ctx context.Context, user string) ([]IRFile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+irColumns+` FROM dsp_ir_files WHERE user_id=$1 ORDER BY created_at,id LIMIT $2`, user, IRLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]IRFile, 0)
	for rows.Next() {
		f, err := scanIR(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, f)
	}
	return result, rows.Err()
}

func (s irStore) get(ctx context.Context, user, id string) (IRFile, []byte, error) {
	var f IRFile
	var digest []byte
	err := s.db.QueryRowContext(ctx, `SELECT `+irColumns+`,sha256 FROM dsp_ir_files WHERE user_id=$1 AND id=$2`, user, id).Scan(&f.ID, &f.Name, &f.FileName, &f.Container, &f.SampleRate, &f.Channels, &f.Paths, &f.Taps, &f.CreatedAt, &f.UpdatedAt, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return f, digest, err
}

func (s irStore) content(ctx context.Context, user, id string) ([]byte, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT content FROM dsp_ir_files WHERE user_id=$1 AND id=$2`, user, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return data, err
}

func (s irStore) lockOwner(ctx context.Context, user string) error {
	sum := sha256.Sum256([]byte(user))
	_, err := s.db.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1::integer,$2::integer)`, int32(0x49524649), int32(binary.BigEndian.Uint32(sum[:4])))
	return err
}

func (s irStore) create(ctx context.Context, user string, f IRFile, data []byte) (IRFile, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM dsp_ir_files WHERE user_id=$1`, user).Scan(&count); err != nil {
		return IRFile{}, err
	}
	if count >= IRLimit {
		return IRFile{}, ErrIRLimitReached
	}
	sum := sha256.Sum256(data)
	return scanIR(s.db.QueryRowContext(ctx, `INSERT INTO dsp_ir_files(id,user_id,name,file_name,container,sample_rate,channels,paths,taps,size_bytes,sha256,content) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING `+irColumns,
		f.ID, user, f.Name, f.FileName, f.Container, f.SampleRate, f.Channels, f.Paths, f.Taps, len(data), sum[:], data))
}
