package artwork

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// TxBeginner 只描述封面关联短事务所需的数据库能力。
type TxBeginner interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

// ScanFeedback 由扫描注入，记录绑定前后的封面。
type ScanFeedback interface {
	CaptureArtworkBefore(context.Context, *sql.Tx, string) error
	CaptureArtworkAfter(context.Context, *sql.Tx, string) error
}

// SaveWithFeedback 在绑定关联的事务内写受管文件，提交后清理旧键。
func (s *Store) SaveWithFeedback(ctx context.Context, database TxBeginner, releaseID string, data []byte, sourceType string, feedback ScanFeedback) error {
	stored, err := s.Describe(data)
	if err != nil {
		return err
	}
	return s.bindWithFeedback(ctx, database, releaseID, data, stored, sourceType, feedback)
}

func (s *Store) RemoveWithFeedback(ctx context.Context, database TxBeginner, releaseID string, feedback ScanFeedback) error {
	return s.bindWithFeedback(ctx, database, releaseID, nil, Stored{}, "", feedback)
}

func (s *Store) bindWithFeedback(ctx context.Context, database TxBeginner, releaseID string, data []byte, stored Stored, sourceType string, feedback ScanFeedback) (err error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	wrote := false
	defer func() {
		tx.Rollback()
		if err != nil && wrote {
			err = errors.Join(err, s.Discard(context.WithoutCancel(ctx), database, stored.Key))
		}
	}()
	if feedback != nil {
		if err := feedback.CaptureArtworkBefore(ctx, tx, releaseID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `SELECT id FROM releases WHERE id=$1::uuid FOR UPDATE`, releaseID); err != nil {
		return err
	}
	var previous string
	err = tx.QueryRowContext(ctx, `SELECT storage_key FROM release_artworks WHERE release_id=$1::uuid`, releaseID).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if stored.Key == "" {
		_, err = tx.ExecContext(ctx, `DELETE FROM release_artworks WHERE release_id=$1::uuid`, releaseID)
	} else {
		wrote, err = s.WriteIfChanged(ctx, tx, data, stored)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO release_artworks(release_id,content_hash,mime_type,width,height,storage_key,source_type)
   VALUES($1::uuid,$2,$3,$4,$5,$6,$7) ON CONFLICT(release_id) DO UPDATE SET content_hash=EXCLUDED.content_hash,
   mime_type=EXCLUDED.mime_type,width=EXCLUDED.width,height=EXCLUDED.height,storage_key=EXCLUDED.storage_key,source_type=EXCLUDED.source_type`,
			releaseID, stored.Hash, stored.MIMEType, stored.Width, stored.Height, stored.Key, sourceType)
	}
	if err != nil {
		return err
	}
	if feedback != nil {
		if err := feedback.CaptureArtworkAfter(ctx, tx, releaseID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	wrote = false
	if previous != stored.Key && safeStorageKey(previous) {
		if cleanupErr := s.Discard(context.WithoutCancel(ctx), database, previous); cleanupErr != nil {
			if stored.Key == "" {
				return fmt.Errorf("remove released artwork: %w", cleanupErr)
			}
			return fmt.Errorf("remove replaced artwork: %w", cleanupErr)
		}
	}
	return nil
}
