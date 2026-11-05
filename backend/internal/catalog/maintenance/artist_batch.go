package maintenance

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
)

const artistGapPredicate = `NOT EXISTS (SELECT 1 FROM artist_projection_releases p
 WHERE p.release_id=r.id AND p.applied_generation=$1)`

// 初始清单含全部发行；修复阶段只登记缺口。发现阶段不占用 CATP，不在启动路径运行。
func discoverArtists(ctx context.Context, conn *sql.Conn, state artistState, repair bool) error {
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := loadArtistState(ctx, tx)
	if err != nil {
		return err
	}
	if current.BuildID != state.BuildID || current.TargetGeneration != state.TargetGeneration || !current.CurrentVersion() {
		return errBuildChanged
	}
	if current.Ready() {
		return nil
	}
	if err = discoverArtistProfiles(ctx, tx, state, "artist_profiles", false); err != nil {
		return err
	}
	if current.ManifestSealed && !repair {
		return tx.Commit()
	}
	phase, predicate := "artist_releases", "$1::bigint > 0"
	if repair {
		var suffix string
		if err = tx.QueryRowContext(ctx, `SELECT gen_random_uuid()::text`).Scan(&suffix); err != nil {
			return err
		}
		phase, predicate = "repair_artists_"+suffix, artistGapPredicate
		if err = discoverArtistProfiles(ctx, tx, state, "repair_artist_profiles_"+suffix, true); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `WITH inserted AS (
 INSERT INTO catalog_projection_work(build_id,phase,subject_kind,subject_id)
 SELECT $2::uuid,$3,'release',r.id FROM releases r WHERE `+predicate+` RETURNING subject_id)
 INSERT INTO catalog_projection_phases(build_id,phase,subject_kind,total)
 SELECT $2::uuid,$3,'release',count(*) FROM inserted`, state.TargetGeneration, state.BuildID, phase)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE catalog_projection_state SET manifest_sealed=TRUE,status='building',updated_at=NOW()
 WHERE capability='artists' AND build_id=$1::uuid AND target_generation=$2`, state.BuildID, state.TargetGeneration)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errBuildChanged
	}
	return tx.Commit()
}

func processArtistBatch(ctx context.Context, conn *sql.Conn, state artistState, releaseLimit, profileLimit int, workloads ...*artistReleaseBatch) (batchResult, error) {
	started := time.Now()
	result := batchResult{}
	workload := &artistReleaseBatch{Budget: initialArtistReleaseBudget()}
	if len(workloads) > 1 {
		return result, errors.New("artist batch accepts one workload budget")
	}
	if len(workloads) == 1 {
		workload = workloads[0]
	}
	if workload == nil || workload.Budget.Tracks < 1 || workload.Budget.Names < 1 {
		return result, errors.New("artist workload budgets must be positive")
	}
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err = lockArtistBuild(ctx, tx, state); err != nil {
		return result, err
	}
	err = tx.QueryRowContext(ctx, `SELECT phase,subject_kind,total,completed FROM catalog_projection_phases
 WHERE build_id=$1::uuid AND completed<total ORDER BY CASE subject_kind WHEN 'release' THEN 0 ELSE 1 END,phase LIMIT 1`, state.BuildID).
		Scan(&result.Phase, &result.Kind, &result.Total, &result.Completed)
	if errors.Is(err, sql.ErrNoRows) {
		return result, tx.Commit()
	}
	if err != nil {
		return result, err
	}
	// 锁内按本阶段种类重读 marker；不补齐本批 ID 以外的孤立身份。
	limit, source, subject, predicate := releaseLimit, "releases r", "r.id", artistGapPredicate
	refreshFunc := artistgraph.Refresh
	if result.Kind == "artist" {
		limit, source, subject, predicate = profileLimit, "artists a", "a.id", artistProfileGapPredicate
		refreshFunc = artistgraph.RefreshProfiles
	} else if result.Kind != "release" {
		return result, errors.New("unexpected artist projection subject kind")
	}
	if limit < 1 {
		return result, errors.New("artist projection batch limit must be positive")
	}
	rows, err := tx.QueryContext(ctx, `SELECT w.subject_id::text,`+subject+` IS NOT NULL AND `+predicate+`
 FROM catalog_projection_work w LEFT JOIN `+source+` ON `+subject+`=w.subject_id
 WHERE w.build_id=$2::uuid AND w.phase=$3 AND NOT w.completed ORDER BY w.subject_id LIMIT $4`, state.TargetGeneration, state.BuildID, result.Phase, limit)
	if err != nil {
		return result, err
	}
	var ids, refresh []string
	var candidates []artistReleaseCandidate
	for rows.Next() {
		var id string
		var stale bool
		if err = rows.Scan(&id, &stale); err != nil {
			rows.Close()
			return result, err
		}
		candidates = append(candidates, artistReleaseCandidate{ID: id, Stale: stale})
		ids = append(ids, id)
		if stale {
			refresh = append(refresh, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	selection := artistReleaseSelection{}
	if result.Kind == "release" {
		selection, err = selectArtistReleaseWork(candidates, workload.Budget, func(id string, remaining artistReleaseBudget) (artistReleaseCost, error) {
			return estimateArtistReleaseWork(ctx, tx, id, remaining)
		})
		if err != nil {
			return result, err
		}
		ids, refresh = selection.IDs, selection.Refresh
	}
	if len(ids) == 0 {
		return result, errors.New("artist projection phase has inconsistent progress")
	}
	if len(refresh) > 0 {
		if err = refreshFunc(ctx, tx, refresh); err != nil {
			return result, err
		}
		// 零署名发行与零搜索行身份也必须有完成标记；端口漏写则整批回滚。
		var gap bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+source+` WHERE `+subject+`=ANY($2::uuid[]) AND `+predicate+`)`, state.TargetGeneration, refresh).Scan(&gap); err != nil {
			return result, err
		}
		if gap {
			return result, errors.New("artist refresh left incomplete subject markers")
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_projection_work SET completed=TRUE
 WHERE build_id=$1::uuid AND phase=$2 AND subject_id=ANY($3::uuid[]) AND NOT completed`, state.BuildID, result.Phase, ids)
	if err != nil {
		return result, err
	}
	result.BatchSize = len(ids)
	result.Written = len(refresh)
	result.Completed += int64(len(ids))
	_, err = tx.ExecContext(ctx, `UPDATE catalog_projection_phases SET completed=$3 WHERE build_id=$1::uuid AND phase=$2`, state.BuildID, result.Phase, result.Completed)
	if err != nil {
		return result, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_projection_state SET status='building',updated_at=NOW() WHERE capability='artists'`)
	if err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return batchResult{}, err
	}
	result.Duration = time.Since(started)
	workload.Selected = selection
	return result, nil
}

// 发行数不能代表写入工作量：原规模实测10个普通发行约1.07s，10个百轨missing发行约3.4s。
// 起步一个典型十轨发行；轨数与原输入署名两道预算各自自适应，均不省略missing。
const (
	maxArtistBatchTracks = 50
	maxArtistBatchNames  = 256
)

type artistReleaseBudget struct{ Tracks, Names int }

func initialArtistReleaseBudget() artistReleaseBudget {
	return artistReleaseBudget{Tracks: 10, Names: 64}
}

type artistReleaseCost struct {
	Tracks, Names    int
	EstimateComplete bool
}

type artistReleaseCandidate struct {
	ID    string
	Stale bool
}

type artistReleaseSelection struct {
	IDs, Refresh []string
	artistReleaseCost
	Oversized bool
}

type artistReleaseBatch struct {
	Budget   artistReleaseBudget
	Selected artistReleaseSelection
}

// 只选待处理清单的连续前缀。在线已完成项不消耗刷新预算；首个昂贵subject仍完整处理，不能饿死。
func selectArtistReleaseWork(candidates []artistReleaseCandidate, budget artistReleaseBudget, estimate func(string, artistReleaseBudget) (artistReleaseCost, error)) (artistReleaseSelection, error) {
	selected := artistReleaseSelection{artistReleaseCost: artistReleaseCost{EstimateComplete: true}}
	for _, candidate := range candidates {
		if !candidate.Stale {
			selected.IDs = append(selected.IDs, candidate.ID)
			continue
		}
		remaining := artistReleaseBudget{Tracks: budget.Tracks - selected.Tracks, Names: budget.Names - selected.Names}
		cost, err := estimate(candidate.ID, remaining)
		if err != nil {
			return artistReleaseSelection{}, err
		}
		over := cost.Tracks > remaining.Tracks || cost.Names > remaining.Names || !cost.EstimateComplete
		if over && len(selected.Refresh) > 0 {
			break
		}
		selected.IDs = append(selected.IDs, candidate.ID)
		selected.Refresh = append(selected.Refresh, candidate.ID)
		selected.Tracks += cost.Tracks
		selected.Names += cost.Names
		selected.EstimateComplete = selected.EstimateComplete && cost.EstimateComplete
		selected.Oversized = over
		if over {
			break
		}
	}
	return selected, nil
}

// 参数化逐发行/逐track索引入口；不依赖尚未生成的effective，也不全库聚合轨数或署名。
const artistReleaseTrackCountSQL = `SELECT count(*) FROM media m
 CROSS JOIN LATERAL (SELECT t.id FROM tracks t WHERE t.medium_id=m.id OFFSET 0) t
 WHERE m.release_id=$1::uuid`

// 原始展示、可信观察、重复标签证据和四角色credit都计入输入成本。
// 有意保守地重复计算可能被证据替代的展示串；不在maintenance复制graph的事实选择/归属语义。
const artistReleaseNameInputsSQL = `WITH scoped_tracks AS MATERIALIZED (
 SELECT t.id,t.artist FROM media m
 CROSS JOIN LATERAL (SELECT t.id,t.artist FROM tracks t WHERE t.medium_id=m.id OFFSET 0) t
 WHERE m.release_id=$1::uuid
), inputs(raw) AS (
 SELECT artist FROM releases WHERE id=$1::uuid
 UNION ALL SELECT album_artist FROM releases WHERE id=$1::uuid
 UNION ALL SELECT text_value FROM release_corrections WHERE release_id=$1::uuid AND field='album_artist'
 UNION ALL SELECT text_value FROM release_corrections WHERE release_id=$1::uuid AND field='album_artist'
 UNION ALL SELECT artist FROM scoped_tracks
 UNION ALL SELECT o.value FROM scoped_tracks t CROSS JOIN LATERAL (
 SELECT value FROM track_observations WHERE track_id=t.id AND field_name='artist' OFFSET 0) o
 UNION ALL SELECT e.raw_value FROM scoped_tracks t CROSS JOIN LATERAL (
 SELECT raw_value FROM artist_tag_evidence WHERE track_id=t.id OFFSET 0) e
 UNION ALL SELECT c.name FROM scoped_tracks t CROSS JOIN LATERAL (
 SELECT name FROM track_credits WHERE track_id=t.id OFFSET 0) c
) SELECT raw FROM inputs WHERE raw IS NOT NULL AND btrim(raw)<>'' LIMIT $2`

func estimateArtistReleaseWork(ctx context.Context, tx *sql.Tx, releaseID string, budget artistReleaseBudget) (artistReleaseCost, error) {
	cost := artistReleaseCost{EstimateComplete: true}
	if err := tx.QueryRowContext(ctx, artistReleaseTrackCountSQL, releaseID).Scan(&cost.Tracks); err != nil {
		return cost, err
	}
	if cost.Tracks > budget.Tracks {
		// 已足以判定超额，避免为巨大subject预读所有标签；日志明确署名估计未完成。
		cost.EstimateComplete = false
		return cost, nil
	}
	rows, err := tx.QueryContext(ctx, artistReleaseNameInputsSQL, releaseID, budget.Names+1)
	if err != nil {
		return cost, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return cost, err
		}
		cost.Names += artistgraph.ArtistInputNameCost(raw)
		if cost.Names > budget.Names {
			cost.EstimateComplete = false
			return cost, nil
		}
	}
	return cost, rows.Err()
}
