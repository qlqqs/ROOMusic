package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"
)

var ErrAnalysisTrackNotFound = errors.New("analysis track not found")

// TrackAnalysisCandidate 是工作者读取的一首曲目及其分析输入快照。
type TrackAnalysisCandidate struct {
	TrackID, RootID, RootPath, RelativePath, SourceKind, ReleaseID string
	ContentFingerprint                                             *string
	SourceSize                                                     int64
	SourceModifiedAt                                               time.Time
	CueParentRelativePath                                          *string
	CueIndexFrames, CueEndFrames                                   *int32
	DiscNumber, Position                                           int
}

// TrackAnalysisResult 是一次分析器的输出；Failed 时只使用 FailureCategory。
type TrackAnalysisResult struct {
	LoudnessRangeLU, SamplePeakDBFS                 *float64
	Status, FailureCategory                         string
	IntegratedLUFS, TruePeakDBTP                    *float64
	DurationMS, LeadingSilenceMS, TrailingSilenceMS *int32
	Waveform                                        []byte
}

// TrackAnalysisProgress 是候选总数及已完成、当前不可重试失败的数量。
type TrackAnalysisProgress struct {
	Total, Analyzed, Failed int64
}

type TrackAnalysisWaveform struct {
	Points int    `json:"points"`
	Peaks  []byte `json:"peaks"`
}

type TrackAnalysisData struct {
	LoudnessRangeLU   *float64              `json:"loudness_range_lu"`
	SamplePeakDBFS    *float64              `json:"sample_peak_dbfs"`
	IntegratedLUFS    *float64              `json:"integrated_lufs"`
	TruePeakDBTP      *float64              `json:"true_peak_dbtp"`
	TrackGainDB       *float64              `json:"track_gain_db"`
	DurationMS        *int32                `json:"duration_ms"`
	LeadingSilenceMS  *int32                `json:"leading_silence_ms"`
	TrailingSilenceMS *int32                `json:"trailing_silence_ms"`
	Waveform          TrackAnalysisWaveform `json:"waveform"`
}

type AlbumAnalysis struct {
	ReleaseID string  `json:"release_id"`
	GainDB    float64 `json:"gain_db"`
	PeakDBTP  float64 `json:"peak_dbtp"`
}

type TrackAnalysisResponse struct {
	TrackID  string             `json:"track_id"`
	Status   string             `json:"status"`
	Analysis *TrackAnalysisData `json:"analysis"`
	Album    *AlbumAnalysis     `json:"album"`
}

// ListAnalysisCandidates 优先返回没有可用结果的曲目；同类中 CUE 虚拟轨排在普通曲目之后。
func (s *Store) ListAnalysisCandidates(ctx context.Context, version int16, limit int) ([]TrackAnalysisCandidate, error) {
	rows, err := s.database.QueryContext(ctx, `SELECT t.id::text,t.source_root_id::text,lr.path,t.relative_path,t.source_kind,
 t.content_fingerprint,t.source_size,t.source_modified_at,t.cue_parent_relative_path,t.cue_index_frames,t.cue_end_frames,
 m.release_id::text,t.disc_number,t.position
 FROM tracks t JOIN media m ON m.id=t.medium_id JOIN library_roots lr ON lr.id=t.source_root_id
 LEFT JOIN track_analyses a ON a.track_id=t.id
 WHERE t.source_status='present' AND lr.status='active' AND t.source_size IS NOT NULL AND t.source_modified_at IS NOT NULL
 AND (a.track_id IS NULL OR a.analyzer_version IS DISTINCT FROM $1 OR
      a.content_fingerprint IS DISTINCT FROM t.content_fingerprint OR a.source_size IS DISTINCT FROM t.source_size OR
      a.source_modified_at IS DISTINCT FROM t.source_modified_at OR a.cue_index_frames IS DISTINCT FROM t.cue_index_frames OR
      a.cue_end_frames IS DISTINCT FROM t.cue_end_frames OR
      (a.status='failed' AND a.attempts < 3 AND a.analyzed_at < NOW() - INTERVAL '24 hours'))
 ORDER BY COALESCE(a.status='done'
  AND a.content_fingerprint IS NOT DISTINCT FROM t.content_fingerprint
  AND a.source_size IS NOT DISTINCT FROM t.source_size
  AND a.source_modified_at IS NOT DISTINCT FROM t.source_modified_at
  AND a.cue_index_frames IS NOT DISTINCT FROM t.cue_index_frames
  AND a.cue_end_frames IS NOT DISTINCT FROM t.cue_end_frames,FALSE),
 (t.source_kind='cue_virtual'),m.release_id,t.disc_number,t.position,t.id LIMIT $2`, version, limit)
	if err != nil {
		return nil, fmt.Errorf("list analysis candidates: %w", err)
	}
	defer rows.Close()
	result := make([]TrackAnalysisCandidate, 0, limit)
	for rows.Next() {
		var item TrackAnalysisCandidate
		var fingerprint, cueParent sql.NullString
		var cueIndex, cueEnd sql.NullInt32
		if err := rows.Scan(&item.TrackID, &item.RootID, &item.RootPath, &item.RelativePath, &item.SourceKind,
			&fingerprint, &item.SourceSize, &item.SourceModifiedAt, &cueParent, &cueIndex, &cueEnd,
			&item.ReleaseID, &item.DiscNumber, &item.Position); err != nil {
			return nil, err
		}
		if fingerprint.Valid {
			item.ContentFingerprint = &fingerprint.String
		}
		if cueParent.Valid {
			item.CueParentRelativePath = &cueParent.String
		}
		if cueIndex.Valid {
			item.CueIndexFrames = &cueIndex.Int32
		}
		if cueEnd.Valid {
			item.CueEndFrames = &cueEnd.Int32
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// SaveTrackAnalysis 按输入快照 UPSERT 结果；同版本、同快照的连续失败递增次数，其余情况从 1 开始。
func (s *Store) SaveTrackAnalysis(ctx context.Context, candidate TrackAnalysisCandidate, version int16, result TrackAnalysisResult) error {
	var waveform, failureCategory any
	if result.Status == "done" {
		waveform = result.Waveform
	}
	if result.FailureCategory != "" {
		failureCategory = result.FailureCategory
	}
	writer, ok := s.database.(interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	})
	if !ok {
		return fmt.Errorf("save track analysis: database is not writable")
	}
	_, err := writer.ExecContext(ctx, `INSERT INTO track_analyses(
 track_id,analyzer_version,content_fingerprint,source_size,source_modified_at,cue_index_frames,cue_end_frames,status,
 integrated_lufs,true_peak_dbtp,duration_ms,leading_silence_ms,trailing_silence_ms,waveform,failure_category,
 loudness_range_lu,sample_peak_dbfs,attempts,analyzed_at)
 VALUES($1::uuid,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,1,NOW())
 ON CONFLICT(track_id) DO UPDATE SET analyzer_version=EXCLUDED.analyzer_version,
 content_fingerprint=EXCLUDED.content_fingerprint,source_size=EXCLUDED.source_size,source_modified_at=EXCLUDED.source_modified_at,
 cue_index_frames=EXCLUDED.cue_index_frames,cue_end_frames=EXCLUDED.cue_end_frames,status=EXCLUDED.status,
 integrated_lufs=EXCLUDED.integrated_lufs,true_peak_dbtp=EXCLUDED.true_peak_dbtp,duration_ms=EXCLUDED.duration_ms,
 leading_silence_ms=EXCLUDED.leading_silence_ms,trailing_silence_ms=EXCLUDED.trailing_silence_ms,waveform=EXCLUDED.waveform,
 failure_category=EXCLUDED.failure_category,
 loudness_range_lu=EXCLUDED.loudness_range_lu,sample_peak_dbfs=EXCLUDED.sample_peak_dbfs,
 attempts=CASE WHEN track_analyses.analyzer_version=EXCLUDED.analyzer_version AND
  track_analyses.content_fingerprint IS NOT DISTINCT FROM EXCLUDED.content_fingerprint AND
  track_analyses.source_size IS NOT DISTINCT FROM EXCLUDED.source_size AND
  track_analyses.source_modified_at IS NOT DISTINCT FROM EXCLUDED.source_modified_at AND
  track_analyses.cue_index_frames IS NOT DISTINCT FROM EXCLUDED.cue_index_frames AND
  track_analyses.cue_end_frames IS NOT DISTINCT FROM EXCLUDED.cue_end_frames AND
  track_analyses.status='failed' AND EXCLUDED.status='failed' THEN track_analyses.attempts+1 ELSE 1 END,
 analyzed_at=NOW()`, candidate.TrackID, version, candidate.ContentFingerprint, candidate.SourceSize, candidate.SourceModifiedAt,
		candidate.CueIndexFrames, candidate.CueEndFrames, result.Status, result.IntegratedLUFS, result.TruePeakDBTP, result.DurationMS,
		result.LeadingSilenceMS, result.TrailingSilenceMS, waveform, failureCategory, result.LoudnessRangeLU, result.SamplePeakDBFS)
	if err != nil {
		return fmt.Errorf("save track analysis: %w", err)
	}
	return nil
}

// AnalysisProgress 计算与候选工作集同口径的计数，且不依赖 worker 是否启用。
func (s *Store) AnalysisProgress(ctx context.Context, version int16) (TrackAnalysisProgress, error) {
	var result TrackAnalysisProgress
	err := s.database.QueryRowContext(ctx, `WITH eligible AS (
 SELECT t.id,a.status,a.attempts,a.analyzed_at
 FROM tracks t JOIN library_roots lr ON lr.id=t.source_root_id
 LEFT JOIN track_analyses a ON a.track_id=t.id
 WHERE t.source_status='present' AND lr.status='active' AND t.source_size IS NOT NULL AND t.source_modified_at IS NOT NULL
   AND a.analyzer_version=$1 AND a.content_fingerprint IS NOT DISTINCT FROM t.content_fingerprint
   AND a.source_size IS NOT DISTINCT FROM t.source_size AND a.source_modified_at IS NOT DISTINCT FROM t.source_modified_at
   AND a.cue_index_frames IS NOT DISTINCT FROM t.cue_index_frames AND a.cue_end_frames IS NOT DISTINCT FROM t.cue_end_frames
 ) SELECT
 (SELECT COUNT(*) FROM tracks t JOIN library_roots lr ON lr.id=t.source_root_id WHERE t.source_status='present' AND lr.status='active' AND t.source_size IS NOT NULL AND t.source_modified_at IS NOT NULL),
 COUNT(*) FILTER (WHERE status='done'),
 COUNT(*) FILTER (WHERE status='failed' AND (attempts>=3 OR analyzed_at>=NOW()-INTERVAL '24 hours'))
 FROM eligible`, version).Scan(&result.Total, &result.Analyzed, &result.Failed)
	if err != nil {
		return TrackAnalysisProgress{}, fmt.Errorf("read analysis progress: %w", err)
	}
	return result, nil
}

// LoadTrackAnalysis 返回当前快照的分析状态；任意版本的成功结果均可使用，专辑就绪时附带增益。
func (s *Store) LoadTrackAnalysis(ctx context.Context, trackID string, version int16) (TrackAnalysisResponse, error) {
	return readWithLocalJITOff(ctx, s.database.(ReadTransactionBeginner), func(tx *sql.Tx) (TrackAnalysisResponse, error) {
		return NewStore(tx).loadTrackAnalysis(ctx, trackID, version)
	})
}

func (s *Store) loadTrackAnalysis(ctx context.Context, trackID string, version int16) (TrackAnalysisResponse, error) {
	response := TrackAnalysisResponse{TrackID: trackID, Status: "pending"}
	var trackFingerprint, analysisFingerprint sql.NullString
	var trackSize, analysisSize sql.NullInt64
	var trackModified, analysisModified sql.NullTime
	var trackCueIndex, trackCueEnd, analysisCueIndex, analysisCueEnd sql.NullInt32
	var rowVersion sql.NullInt16
	var status sql.NullString
	var failedTerminal bool
	var integrated, peak, loudnessRange, samplePeak sql.NullFloat64
	var duration, leading, trailing sql.NullInt32
	var waveform []byte
	var releaseID string
	err := s.database.QueryRowContext(ctx, `SELECT t.content_fingerprint,t.source_size,t.source_modified_at,t.cue_index_frames,t.cue_end_frames,m.release_id::text,
 a.analyzer_version,a.content_fingerprint,a.source_size,a.source_modified_at,a.cue_index_frames,a.cue_end_frames,a.status,
 COALESCE(a.status='failed' AND (a.attempts>=3 OR a.analyzed_at>=NOW()-INTERVAL '24 hours'),FALSE),
 a.integrated_lufs,a.true_peak_dbtp,a.duration_ms,a.leading_silence_ms,a.trailing_silence_ms,a.waveform,
 a.loudness_range_lu,a.sample_peak_dbfs
 FROM tracks t JOIN media m ON m.id=t.medium_id LEFT JOIN track_analyses a ON a.track_id=t.id
 WHERE t.id=$1::uuid AND t.source_status='present'`, trackID).Scan(
		&trackFingerprint, &trackSize, &trackModified, &trackCueIndex, &trackCueEnd, &releaseID,
		&rowVersion, &analysisFingerprint, &analysisSize, &analysisModified, &analysisCueIndex, &analysisCueEnd,
		&status, &failedTerminal, &integrated, &peak, &duration, &leading, &trailing, &waveform, &loudnessRange, &samplePeak)
	if errors.Is(err, sql.ErrNoRows) {
		return response, ErrAnalysisTrackNotFound
	}
	if err != nil {
		return response, fmt.Errorf("load track analysis: %w", err)
	}
	if !rowVersion.Valid || !sameAnalysisSnapshot(
		trackFingerprint, analysisFingerprint, trackSize, analysisSize, trackModified, analysisModified,
		trackCueIndex, analysisCueIndex, trackCueEnd, analysisCueEnd) {
		return response, nil
	}
	if status.String == "failed" {
		if rowVersion.Int16 == version && failedTerminal {
			response.Status = "failed"
		}
		return response, nil
	}
	response.Status = "done"
	data := &TrackAnalysisData{
		IntegratedLUFS: roundedDBNullable(integrated), TruePeakDBTP: roundedDBNullable(peak),
		LoudnessRangeLU: roundedDBNullable(loudnessRange), SamplePeakDBFS: roundedDBNullable(samplePeak),
		DurationMS: nullInt32Pointer(duration), LeadingSilenceMS: nullInt32Pointer(leading),
		TrailingSilenceMS: nullInt32Pointer(trailing), Waveform: TrackAnalysisWaveform{Points: len(waveform), Peaks: waveform},
	}
	if integrated.Valid && peak.Valid {
		// 计算增益使用存储精度；仅面向 API 展示的 LUFS/峰值按一位小数舍入。
		gain := math.Min(-14-integrated.Float64, -0.5-peak.Float64)
		data.TrackGainDB = roundedDB(gain)
	}
	response.Analysis = data
	album, err := s.albumAnalysis(ctx, releaseID, version)
	if err != nil {
		return TrackAnalysisResponse{}, err
	}
	response.Album = album
	return response, nil
}

func sameAnalysisSnapshot(trackFingerprint, analysisFingerprint sql.NullString, trackSize, analysisSize sql.NullInt64,
	trackModified, analysisModified sql.NullTime, trackCueIndex, analysisCueIndex, trackCueEnd, analysisCueEnd sql.NullInt32) bool {
	return trackFingerprint.Valid == analysisFingerprint.Valid && (!trackFingerprint.Valid || trackFingerprint.String == analysisFingerprint.String) &&
		trackSize.Valid == analysisSize.Valid && (!trackSize.Valid || trackSize.Int64 == analysisSize.Int64) &&
		trackModified.Valid == analysisModified.Valid && (!trackModified.Valid || trackModified.Time.Equal(analysisModified.Time)) &&
		trackCueIndex.Valid == analysisCueIndex.Valid && (!trackCueIndex.Valid || trackCueIndex.Int32 == analysisCueIndex.Int32) &&
		trackCueEnd.Valid == analysisCueEnd.Valid && (!trackCueEnd.Valid || trackCueEnd.Int32 == analysisCueEnd.Int32)
}

func nullFloatPointer(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	return &value.Float64
}

func nullInt32Pointer(value sql.NullInt32) *int32 {
	if !value.Valid {
		return nil
	}
	return &value.Int32
}

func roundedDB(value float64) *float64 {
	rounded := math.Round(value*10) / 10
	return &rounded
}

func roundedDBNullable(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	return roundedDB(value.Float64)
}

func (s *Store) albumAnalysis(ctx context.Context, releaseID string, version int16) (*AlbumAnalysis, error) {
	rows, err := s.database.QueryContext(ctx, `SELECT t.content_fingerprint,t.source_size,t.source_modified_at,t.cue_index_frames,t.cue_end_frames,
 a.analyzer_version,a.content_fingerprint,a.source_size,a.source_modified_at,a.cue_index_frames,a.cue_end_frames,a.status,
 a.attempts,a.analyzed_at,NOW(),a.integrated_lufs,a.true_peak_dbtp,a.duration_ms
 FROM tracks t JOIN media m ON m.id=t.medium_id JOIN library_roots lr ON lr.id=t.source_root_id
 LEFT JOIN track_analyses a ON a.track_id=t.id
 WHERE m.release_id=$1::uuid AND t.source_status='present' AND lr.status='active'
 AND t.source_size IS NOT NULL AND t.source_modified_at IS NOT NULL`, releaseID)
	if err != nil {
		return nil, fmt.Errorf("load album analysis: %w", err)
	}
	defer rows.Close()
	var energy, durationSum float64
	var peakMax float64
	var hasPeak, pending bool
	for rows.Next() {
		var tf, af sql.NullString
		var ts, as sql.NullInt64
		var tm, am sql.NullTime
		var ti, ai, te, ae sql.NullInt32
		var av sql.NullInt16
		var status sql.NullString
		var attempts sql.NullInt32
		var analyzedAt, databaseNow sql.NullTime
		var lufs, peak sql.NullFloat64
		var duration sql.NullInt32
		if err := rows.Scan(&tf, &ts, &tm, &ti, &te, &av, &af, &as, &am, &ai, &ae, &status,
			&attempts, &analyzedAt, &databaseNow, &lufs, &peak, &duration); err != nil {
			return nil, err
		}
		snapshotMatches := av.Valid && sameAnalysisSnapshot(tf, af, ts, as, tm, am, ti, ai, te, ae)
		if !snapshotMatches {
			pending = true
			continue
		}
		if status.String == "failed" {
			if av.Int16 != version || (attempts.Valid && attempts.Int32 < 3 && analyzedAt.Valid && databaseNow.Valid && analyzedAt.Time.Before(databaseNow.Time.Add(-24*time.Hour))) {
				pending = true
			}
			continue
		}
		if status.String != "done" {
			pending = true
			continue
		}
		if !lufs.Valid || !peak.Valid || !duration.Valid || duration.Int32 <= 0 {
			continue
		}
		weight := float64(duration.Int32)
		energy += weight * math.Pow(10, lufs.Float64/10)
		durationSum += weight
		if !hasPeak || peak.Float64 > peakMax {
			peakMax = peak.Float64
			hasPeak = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if pending || durationSum == 0 || !hasPeak {
		return nil, nil
	}
	integrated := 10 * math.Log10(energy/durationSum)
	gain := math.Min(-14-integrated, -0.5-peakMax)
	return &AlbumAnalysis{ReleaseID: releaseID, GainDB: math.Round(gain*10) / 10, PeakDBTP: math.Round(peakMax*10) / 10}, nil
}
