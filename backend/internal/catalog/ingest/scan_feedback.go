package ingest

import (
	"context"
	"database/sql"
	"errors"
)

// ScanFeedback 是单次扫描的显式记账健康状态，由持有扫描连接的 worker 串行使用。
// 一次反馈失败后永久降级，不影响后续核心目录写入或进度反馈。
type ScanFeedback struct {
	scanID  string
	broken  bool
	failure error
}

// NewScanFeedback 创建独立状态，不在不同扫描之间复用。
func NewScanFeedback(scanID string) *ScanFeedback { return &ScanFeedback{scanID: scanID} }

// Err 返回首个反馈故障，供扫描编排记录限频日志，不用于展示内部错误。
func (f *ScanFeedback) Err() error {
	if f == nil {
		return nil
	}
	return f.failure
}
func (f *ScanFeedback) Broken() bool { return f != nil && f.broken }
func (f *ScanFeedback) Break() {
	if f != nil {
		f.broken = true
	}
}

// 变化原因只描述扫描拥有的事实，不读取人工覆盖层。
const (
	ChangeMetadata = 1 << iota
	ChangeLocation
	ChangeMembership
	ChangeAvailability
	ChangeArtwork
	ChangeRestored
)

// ChangeCounts 是同一种实体的互斥分类计数。
type ChangeCounts struct{ Added, Updated, Missing int64 }
type ScanChangeSummary struct{ Albums, Tracks ChangeCounts }

// SummarizeScanChanges 在调用方的终态事务中冻结分类；调用方负责报告 savepoint。
// 分类只使用扫描记录，不通过现在的归组反推历史。
func SummarizeScanChanges(ctx context.Context, tx *sql.Tx, scanID string) (ScanChangeSummary, error) {
	var result ScanChangeSummary
	_, err := tx.ExecContext(ctx, `UPDATE scan_catalog_changes SET classification=CASE
  WHEN NOT existed_before THEN 'added'
  WHEN present_before AND NOT present_after THEN 'missing'
  ELSE 'updated' END WHERE scan_run_id=$1::uuid AND classification IS NULL`, scanID)
	if err != nil {
		return result, err
	}
	err = tx.QueryRowContext(ctx, `SELECT
  COUNT(*) FILTER (WHERE entity_kind='album' AND classification='added'),
  COUNT(*) FILTER (WHERE entity_kind='album' AND classification='updated'),
  COUNT(*) FILTER (WHERE entity_kind='album' AND classification='missing'),
  COUNT(*) FILTER (WHERE entity_kind='track' AND classification='added'),
  COUNT(*) FILTER (WHERE entity_kind='track' AND classification='updated'),
  COUNT(*) FILTER (WHERE entity_kind='track' AND classification='missing')
  FROM scan_catalog_changes WHERE scan_run_id=$1::uuid`, scanID).Scan(
		&result.Albums.Added, &result.Albums.Updated, &result.Albums.Missing,
		&result.Tracks.Added, &result.Tracks.Updated, &result.Tracks.Missing)
	return result, err
}

// attempt 只包围反馈 SQL；核心写入绝不放进可被降级回滚的 savepoint。
// 返回错误只表示无法恢复事务，普通反馈错误则关闭本次扫描的报告。
func (f *ScanFeedback) attempt(ctx context.Context, tx *sql.Tx, work func() error) error {
	if f == nil || f.broken {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `SAVEPOINT scan_feedback`); err != nil {
		f.Break()
		return err
	}
	var previousTimeout string
	err := tx.QueryRowContext(ctx, `SELECT current_setting('lock_timeout')`).Scan(&previousTimeout)
	if err == nil {
		_, err = tx.ExecContext(ctx, `SELECT set_config('lock_timeout','500ms',true)`)
	}
	if err == nil {
		err = work()
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, `SELECT set_config('lock_timeout',$1,true)`, previousTimeout)
	}
	if err != nil {
		f.failure = err
		f.Break()
		if _, recoveryErr := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT scan_feedback`); recoveryErr != nil {
			return errors.Join(err, recoveryErr)
		}
	}
	_, releaseErr := tx.ExecContext(ctx, `RELEASE SAVEPOINT scan_feedback`)
	return releaseErr
}

func (f *ScanFeedback) beginCandidate(ctx context.Context, tx *sql.Tx, plans map[string]trackPlan, releaseID, rootID string, candidate OrganizedCandidate) error {
	return f.attempt(ctx, tx, func() error {
		if err := createFeedbackTables(ctx, tx); err != nil {
			return err
		}
		trackIDs, identities := []string{}, []string{}
		for _, plan := range plans {
			if plan.TrackID != "" {
				trackIDs = append(trackIDs, plan.TrackID)
			}
		}
		for _, track := range candidate.orderedTracks() {
			identities = append(identities, SourceIdentityForObservation(rootID, track.Observation))
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO scan_feedback_tracks(id)
   SELECT id FROM tracks WHERE id=ANY($1::uuid[]) OR source_identity=ANY($2::text[])`, trackIDs, identities); err != nil {
			return err
		}
		if err := collectFeedbackReleases(ctx, tx, releaseID); err != nil {
			return err
		}
		if err := lockFeedbackGraph(ctx, tx); err != nil {
			return err
		}
		return snapshotFeedbackGraph(ctx, tx, false)
	})
}

func (f *ScanFeedback) finishCandidate(ctx context.Context, tx *sql.Tx, releaseID string, trackIDs []string) error {
	return f.attempt(ctx, tx, func() error {
		for start := 0; start < len(trackIDs); start += 256 {
			end := min(start+256, len(trackIDs))
			if _, err := tx.ExecContext(ctx, `INSERT INTO scan_feedback_tracks(id) SELECT unnest($1::uuid[]) ON CONFLICT DO NOTHING`, trackIDs[start:end]); err != nil {
				return err
			}
		}
		// 这里只加入本事务新建的发行；既有发行集合已在核心写入前锁定并验证。
		if err := collectFeedbackReleases(ctx, tx, releaseID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO scan_feedback_groups(id) SELECT DISTINCT r.group_id FROM releases r
   JOIN scan_feedback_releases s ON s.id=r.id ON CONFLICT DO NOTHING`); err != nil {
			return err
		}
		if err := snapshotFeedbackGraph(ctx, tx, true); err != nil {
			return err
		}
		if err := f.recordChanges(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO scan_added_releases(scan_run_id,release_id,group_id_at_creation,added_at,title_at_creation,artist_at_creation)
   SELECT $1::uuid,r.id,r.group_id,r.created_at,r.title,r.artist FROM releases r WHERE r.id=$2::uuid
   AND NOT EXISTS(SELECT 1 FROM scan_feedback_before b WHERE b.kind='release' AND b.id=r.id)
   ON CONFLICT DO NOTHING`, f.scanID, releaseID)
		return err
	})
}

// CaptureArtworkBefore/After 实现 artwork 消费者定义的窄接口；只覆盖关联数据库事务。
func (f *ScanFeedback) CaptureArtworkBefore(ctx context.Context, tx *sql.Tx, releaseID string) error {
	return f.attempt(ctx, tx, func() error {
		if err := createFeedbackTables(ctx, tx); err != nil {
			return err
		}
		if err := collectFeedbackReleases(ctx, tx, releaseID); err != nil {
			return err
		}
		if err := lockFeedbackGraph(ctx, tx); err != nil {
			return err
		}
		return snapshotFeedbackGraph(ctx, tx, false)
	})
}
func (f *ScanFeedback) CaptureArtworkAfter(ctx context.Context, tx *sql.Tx, releaseID string) error {
	return f.attempt(ctx, tx, func() error {
		if err := snapshotFeedbackGraph(ctx, tx, true); err != nil {
			return err
		}
		return f.recordChanges(ctx, tx)
	})
}
