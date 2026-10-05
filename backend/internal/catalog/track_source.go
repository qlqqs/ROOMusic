package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrTrackNotFound 表示曲目不存在，或它的来源已不是 present（缺失的曲目对播放不可见）。
var ErrTrackNotFound = errors.New("track not found")

// TrackSource 是一条 present 曲目的来源事实：它位于哪个登记根目录的哪个相对路径、扫描时判定的类别与编码，
// 以及扫描时观察到的文件大小与修改时间。播放用它核对“磁盘上的文件仍是扫描时的那个”。
// Size 或 ModifiedAt 为 nil 表示这条曲目早于大小与修改时间的记录，需要重新扫描才能核对。
// SourceKind 与 Codec 取自扫描观察，不是当前文件可播性的实时证明。
type TrackSource struct {
	TrackID            string
	RootID             string
	RelativePath       string
	SourceKind         string
	Codec              string
	Size               *int64
	ModifiedAt         *time.Time
	ContentFingerprint string
}

// LoadTrackSource 读取 present 曲目的来源事实；曲目不存在或已缺失时返回 ErrTrackNotFound。
// 它不检查目录状态，也不触碰文件系统；trackID 必须是 UUID 文本，由调用方在传输层校验。
func (s *Store) LoadTrackSource(ctx context.Context, trackID string) (TrackSource, error) {
	var source TrackSource
	var codec, fingerprint sql.NullString
	var size sql.NullInt64
	var modifiedAt sql.NullTime
	err := s.database.QueryRowContext(ctx, `SELECT id::text, source_root_id::text, relative_path, source_kind,
		codec, source_size, source_modified_at, content_fingerprint
		FROM tracks
		WHERE id=$1::uuid AND source_status='present'`, trackID).Scan(
		&source.TrackID, &source.RootID, &source.RelativePath, &source.SourceKind,
		&codec, &size, &modifiedAt, &fingerprint,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return TrackSource{}, ErrTrackNotFound
	}
	if err != nil {
		return TrackSource{}, fmt.Errorf("load track source: %w", err)
	}
	source.Codec = codec.String
	source.ContentFingerprint = fingerprint.String
	if size.Valid {
		source.Size = &size.Int64
	}
	if modifiedAt.Valid {
		source.ModifiedAt = &modifiedAt.Time
	}
	return source, nil
}
