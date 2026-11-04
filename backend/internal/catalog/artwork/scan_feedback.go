package artwork

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
)

// TxBeginner 只描述封面关联短事务所需的数据库能力。
type TxBeginner interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

// ScanFeedback 由扫描显式注入；文件读写不进入这些反馈/关联事务。
type ScanFeedback interface {
	CaptureArtworkBefore(context.Context, *sql.Tx, string) error
	CaptureArtworkAfter(context.Context, *sql.Tx, string) error
}

// SaveWithFeedback 先准备文件，再原子提交关联与扫描记账，最后清理不再引用的旧文件。
func (s *Store) SaveWithFeedback(ctx context.Context, database TxBeginner, releaseID string, data []byte, mimeType, sourceType string, feedback ScanFeedback) error {
	prepared, err := s.prepare(data, mimeType)
	if err != nil {
		return err
	}
	previous, err := s.bindWithFeedback(ctx, database, releaseID, &prepared, sourceType, feedback)
	if err != nil {
		if prepared.wroteManagedFile {
			if cleanupErr := cleanupArtwork(ctx, database, prepared.path, prepared.key); cleanupErr != nil {
				return errors.Join(err, cleanupErr)
			}
		}
		return err
	}
	if previous != "" && previous != prepared.key && safeStorageKey(previous) {
		return cleanupArtwork(ctx, database, filepath.Join(s.directory, previous), previous)
	}
	return nil
}

// RemoveWithFeedback 将删除关联与反馈放在同一短事务，文件清理只发生在提交之后。
func (s *Store) RemoveWithFeedback(ctx context.Context, database TxBeginner, releaseID string, feedback ScanFeedback) error {
	previous, err := s.bindWithFeedback(ctx, database, releaseID, nil, "", feedback)
	if err != nil {
		return err
	}
	if previous != "" && safeStorageKey(previous) {
		return cleanupArtwork(ctx, database, filepath.Join(s.directory, previous), previous)
	}
	return nil
}

func (s *Store) bindWithFeedback(ctx context.Context, database TxBeginner, releaseID string, prepared *preparedArtwork, sourceType string, feedback ScanFeedback) (string, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if feedback != nil {
		if err := feedback.CaptureArtworkBefore(ctx, tx, releaseID); err != nil {
			return "", err
		}
	}
	var previous string
	err = tx.QueryRowContext(ctx, `SELECT storage_key FROM release_artworks WHERE release_id=$1::uuid`, releaseID).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if prepared == nil {
		_, err = tx.ExecContext(ctx, `DELETE FROM release_artworks WHERE release_id=$1::uuid`, releaseID)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO release_artworks(release_id,content_hash,mime_type,width,height,storage_key,source_type)
   VALUES($1::uuid,$2,$3,$4,$5,$6,$7) ON CONFLICT(release_id) DO UPDATE SET content_hash=EXCLUDED.content_hash,
   mime_type=EXCLUDED.mime_type,width=EXCLUDED.width,height=EXCLUDED.height,storage_key=EXCLUDED.storage_key,source_type=EXCLUDED.source_type`,
			releaseID, prepared.hash, prepared.mimeType, prepared.width, prepared.height, prepared.key, sourceType)
	}
	if err != nil {
		return "", err
	}
	if feedback != nil {
		if err := feedback.CaptureArtworkAfter(ctx, tx, releaseID); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return previous, nil
}

// 清理使用另一个无 catalog 行锁的事务，只读取引用，沿用内容寻址存储的清理语义。
func cleanupArtwork(ctx context.Context, database TxBeginner, path, key string) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := removeUnreferencedArtwork(ctx, tx, path, key); err != nil {
		return fmt.Errorf("clean artwork: %w", err)
	}
	return tx.Commit()
}
