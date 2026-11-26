package catalog

import (
	"context"
	"database/sql"
	"errors"
	"math"
)

// FingerprintSource 含扫描快照、整轨时长和同一版本仍可用的指纹结果。
type FingerprintSource struct {
	TrackAnalysisCandidate
	ExactDurationSeconds float64
	DurationSeconds      int
	Cached               *AcousticFingerprint
}

type AcousticFingerprint struct {
	Value           string
	DurationSeconds int
	FailureCategory string
}

// LoadFingerprintSource 按曲目读取；来源不在当前工作集时返回 sql.ErrNoRows。
// 成功和 too_short 按文件快照复用；解码失败与超时由发起查询的发行控制重试。
func (s *Store) LoadFingerprintSource(ctx context.Context, trackID string, version int16) (FingerprintSource, error) {
	var src FingerprintSource
	var status sql.NullString
	var cached AcousticFingerprint
	err := s.database.QueryRowContext(ctx, `SELECT t.id::text,t.source_root_id::text,t.relative_path,t.source_kind,
 t.content_fingerprint,t.source_size,t.source_modified_at,t.cue_parent_relative_path,t.cue_index_frames,t.cue_end_frames,
 CASE WHEN t.source_kind='cue_virtual' AND t.cue_end_frames IS NOT NULL AND t.cue_index_frames IS NOT NULL
      THEN (t.cue_end_frames-t.cue_index_frames)::float8/75
      WHEN dd.duration_seconds>0 AND dd.duration_seconds<'Infinity'::float8 THEN dd.duration_seconds
      ELSE -1 END,
 CASE WHEN f.status='done' OR f.failure_category='too_short'
      THEN f.status END,COALESCE(f.fingerprint,''),COALESCE(f.duration_seconds,0),COALESCE(f.failure_category,'')
 FROM tracks t JOIN library_roots lr ON lr.id=t.source_root_id
 LEFT JOIN track_display_durations dd ON dd.track_id=t.id
 LEFT JOIN track_acoustic_fingerprints f ON f.track_id=t.id AND f.pipeline_version=$2
  AND (f.content_fingerprint,f.source_size,f.source_modified_at,f.cue_index_frames,f.cue_end_frames)
      IS NOT DISTINCT FROM (t.content_fingerprint,t.source_size,t.source_modified_at,t.cue_index_frames,t.cue_end_frames)
 WHERE t.id=$1::uuid AND t.source_status='present' AND lr.status='active' AND t.source_size IS NOT NULL
 AND t.source_modified_at IS NOT NULL`, trackID, version).Scan(&src.TrackID, &src.RootID, &src.RelativePath, &src.SourceKind,
		&src.ContentFingerprint, &src.SourceSize, &src.SourceModifiedAt, &src.CueParentRelativePath, &src.CueIndexFrames, &src.CueEndFrames,
		&src.ExactDurationSeconds, &status, &cached.Value, &cached.DurationSeconds, &cached.FailureCategory)
	if err != nil {
		return FingerprintSource{}, err
	}
	src.DurationSeconds = int(math.Round(src.ExactDurationSeconds))
	if status.Valid {
		src.Cached = &cached
	}
	return src, nil
}

// SaveAcousticFingerprint 只为仍然匹配的曲目快照写入。时长低于门槛时会替换旧的指纹或其他失败结果。
// 返回 false 表示结果已过期或已有并发写入。
func (s *Store) SaveAcousticFingerprint(ctx context.Context, src FingerprintSource, version int16, result AcousticFingerprint, belowMinimumDuration bool) (bool, error) {
	status := "done"
	if result.FailureCategory != "" {
		status = "failed"
	}
	var saved bool
	err := s.database.QueryRowContext(ctx, `INSERT INTO track_acoustic_fingerprints AS old
 (track_id,pipeline_version,content_fingerprint,source_size,source_modified_at,cue_index_frames,cue_end_frames,
 status,fingerprint,duration_seconds,failure_category,attempts)
 SELECT t.id,$2,t.content_fingerprint,t.source_size,t.source_modified_at,t.cue_index_frames,t.cue_end_frames,
 $8,NULLIF($9,''),NULLIF($10::integer,0),NULLIF($11,''),1 FROM tracks t
 WHERE t.id=$1::uuid AND t.source_status='present'
 AND (t.content_fingerprint,t.source_size,t.source_modified_at,t.cue_index_frames,t.cue_end_frames)
 IS NOT DISTINCT FROM ($3::text,$4::bigint,$5::timestamptz,$6::integer,$7::integer)
 FOR SHARE OF t
 ON CONFLICT(track_id) DO UPDATE SET pipeline_version=EXCLUDED.pipeline_version,
 content_fingerprint=EXCLUDED.content_fingerprint,source_size=EXCLUDED.source_size,source_modified_at=EXCLUDED.source_modified_at,
 cue_index_frames=EXCLUDED.cue_index_frames,cue_end_frames=EXCLUDED.cue_end_frames,status=EXCLUDED.status,
 fingerprint=EXCLUDED.fingerprint,duration_seconds=EXCLUDED.duration_seconds,failure_category=EXCLUDED.failure_category,
 attempts=CASE WHEN old.status='failed' AND EXCLUDED.status='failed' AND `+sameFingerprintInput+`
               THEN old.attempts+1 ELSE 1 END,computed_at=NOW()
 WHERE NOT (`+sameFingerprintInput+`) OR ($12 AND old.failure_category IS DISTINCT FROM 'too_short')
 OR (old.status='failed' AND old.failure_category<>'too_short')
 RETURNING true`, src.TrackID, version, src.ContentFingerprint, src.SourceSize, src.SourceModifiedAt,
		src.CueIndexFrames, src.CueEndFrames, status, result.Value, result.DurationSeconds, result.FailureCategory, belowMinimumDuration).Scan(&saved)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return saved, err
}

const sameFingerprintInput = `(old.pipeline_version,old.content_fingerprint,old.source_size,old.source_modified_at,old.cue_index_frames,old.cue_end_frames)
 IS NOT DISTINCT FROM (EXCLUDED.pipeline_version,EXCLUDED.content_fingerprint,EXCLUDED.source_size,EXCLUDED.source_modified_at,EXCLUDED.cue_index_frames,EXCLUDED.cue_end_frames)`
