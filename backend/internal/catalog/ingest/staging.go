package ingest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// scan_observations 是一次扫描中某个 root 的暂存表：library 遍历时写入，catalog 按 organization scope
// 分页读取并整理，再逐候选写入 Release 图。暂存行随扫描清理，不设外键。除 JSON 载荷外，行上还有
// 身份规划用的专用列：暂存时写入来源身份与指纹依据，其余由 PlanStagedRoot 填写。

const (
	maxStagedObservationBytes     = 24 << 20
	stagedScopePageSize           = 256
	maxStagedObservationsPerScope = 10000
)

// StagingDB 是读写扫描暂存表所需的最小数据库能力。
type StagingDB interface {
	Execer
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// queryer 是读取扫描暂存表所需的最小数据库能力。
type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// StageObservation 把 observation 写入 scanID 中 rootID 的暂存表；同一相对路径再次暂存会覆盖，并清除该行
// 已有的规划结果。调用方必须先校验 observation 里的相对路径与 CUE 引用是安全的（路径校验属于 library）。
func StageObservation(ctx context.Context, database StagingDB, scanID, rootID string, observation SourceObservation) error {
	scope := OrganizationScope(observation)
	// 每个逻辑 scope 只暂存一份确定性的 embedded artwork；folder artwork 要等到
	// Release id 确定后再发现和绑定。
	if len(observation.Artwork) > 0 {
		var alreadyStaged bool
		if err := database.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM scan_observations WHERE scan_run_id=$1::uuid AND root_id=$2::uuid
			AND organization_scope=$3 AND observation ? 'artwork')`, scanID, rootID, scope).Scan(&alreadyStaged); err != nil {
			return err
		}
		if alreadyStaged {
			observation.Artwork = nil
			observation.ArtworkMIME = ""
		}
	}
	payload, err := json.Marshal(observation)
	if err != nil {
		return fmt.Errorf("marshal staged observation: %w", err)
	}
	if len(payload) > maxStagedObservationBytes {
		return errors.New("staged observation exceeds bound")
	}
	// 指纹依据：物理轨取自身文件，CUE 虚拟轨取父文件。没有文件状态时大小与修改时间都记为空。
	fingerprintPath := NormalizedPath(observation.RelativePath)
	if isCueObservation(observation) {
		fingerprintPath = cueParentPath(observation)
	}
	var size, modifiedAt any
	if !observation.SourceModifiedAt.IsZero() {
		size = observation.SourceSize
		// PostgreSQL 的时间精度是微秒；先截断，之后与曲目表里的已存值才能按相等比较。
		modifiedAt = observation.SourceModifiedAt.UTC().Truncate(time.Microsecond)
	}
	_, err = database.ExecContext(ctx, `INSERT INTO scan_observations(scan_run_id,root_id,organization_scope,relative_path,observation,
			source_identity,fingerprint_path,source_size,source_modified_at,content_fingerprint,cue_index_frames,cue_end_frames)
		VALUES($1::uuid,$2::uuid,$3,$4,$5::jsonb,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT(scan_run_id,root_id,relative_path) DO UPDATE SET organization_scope=EXCLUDED.organization_scope,observation=EXCLUDED.observation,
			source_identity=EXCLUDED.source_identity,fingerprint_path=EXCLUDED.fingerprint_path,source_size=EXCLUDED.source_size,
			source_modified_at=EXCLUDED.source_modified_at,content_fingerprint=EXCLUDED.content_fingerprint,
			cue_index_frames=EXCLUDED.cue_index_frames,cue_end_frames=EXCLUDED.cue_end_frames,
			candidate_key=NULL,matched_track_id=NULL,planned_track_id=NULL,planned_release_id=NULL,content_changed=FALSE,withheld=FALSE,planned=FALSE,observed_at=NOW()`,
		scanID, rootID, scope, observation.RelativePath, payload,
		SourceIdentityForObservation(rootID, observation), nullableString(fingerprintPath), size, modifiedAt,
		nullableString(observation.ContentFingerprint), nullableCueFrame(observation.CueIndexFrames, observation.CueIndexPresent), nullableCueFrame(observation.CueEndFrames, observation.CueEndPresent))
	return err
}

// StagedScopes 返回 rootID 在本次扫描中 organization_scope 大于 after 的下一页待写入 scope（升序，页大小有界）。
// 被规划暂缓的 scope 不在其中。没有更多 scope 时返回空切片；逐页传入上一页最后一个 scope 即可遍历全部。
func StagedScopes(ctx context.Context, database queryer, scanID, rootID, after string) ([]string, error) {
	return stagedScopePage(ctx, database, scanID, rootID, after, false)
}

// CountPlannedScopes 返回本根已规划且未暂缓的不同 scope 数，与保存分页使用同一谓词。
func CountPlannedScopes(ctx context.Context, database queryer, scanID, rootID string) (int64, error) {
	var count int64
	err := database.QueryRowContext(ctx, `SELECT COUNT(DISTINCT organization_scope) FROM scan_observations
  WHERE scan_run_id=$1::uuid AND root_id=$2::uuid AND planned AND NOT withheld`, scanID, rootID).Scan(&count)
	return count, err
}

// PlannedStagedScopes 用于带反馈的保存阶段，只返回固定分母包含的已规划 scope。
func PlannedStagedScopes(ctx context.Context, database queryer, scanID, rootID, after string) ([]string, error) {
	return scopePage(ctx, database, scanID, rootID, after, false, true)
}

func stagedScopePage(ctx context.Context, database queryer, scanID, rootID, after string, includeWithheld bool) ([]string, error) {
	return scopePage(ctx, database, scanID, rootID, after, includeWithheld, false)
}

func scopePage(ctx context.Context, database queryer, scanID, rootID, after string, includeWithheld, plannedOnly bool) ([]string, error) {
	rows, err := database.QueryContext(ctx, `SELECT DISTINCT organization_scope FROM scan_observations
		WHERE scan_run_id=$1::uuid AND root_id=$2::uuid AND organization_scope>$3 AND ($4 OR NOT withheld) AND (NOT $6 OR planned)
		ORDER BY organization_scope LIMIT $5`, scanID, rootID, after, includeWithheld, stagedScopePageSize, plannedOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	scopes := make([]string, 0, stagedScopePageSize)
	for rows.Next() {
		var scope string
		if err := rows.Scan(&scope); err != nil {
			return nil, err
		}
		scopes = append(scopes, scope)
	}
	return scopes, rows.Err()
}

// LoadStagedScope 按相对路径顺序读取一个 organization scope 的全部暂存观察；超过上限时返回错误而不是无界读取。
func LoadStagedScope(ctx context.Context, database queryer, scanID, rootID, scope string) ([]SourceObservation, error) {
	return loadStagedScope(ctx, database, scanID, rootID, scope, true)
}

// loadStagedScope 的 withArtwork 为假时不读取封面字节：规划只需要整理结果，不需要封面。
func loadStagedScope(ctx context.Context, database queryer, scanID, rootID, scope string, withArtwork bool) ([]SourceObservation, error) {
	column := "observation"
	if !withArtwork {
		column = "observation - 'artwork'"
	}
	rows, err := database.QueryContext(ctx, `SELECT `+column+` FROM scan_observations
		WHERE scan_run_id=$1::uuid AND root_id=$2::uuid AND organization_scope=$3
		ORDER BY relative_path LIMIT $4`, scanID, rootID, scope, maxStagedObservationsPerScope+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	observations := make([]SourceObservation, 0, 64)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var observation SourceObservation
		if err := json.Unmarshal(raw, &observation); err != nil {
			return nil, fmt.Errorf("decode staged observation: %w", err)
		}
		observations = append(observations, observation)
		if len(observations) > maxStagedObservationsPerScope {
			return nil, errors.New("staged organization scope exceeds bound")
		}
	}
	return observations, rows.Err()
}

// ClearScansStaging 清理调用方已经确认终态的扫描暂存；终态判断与协调锁由 library 负责。
// 分块传参避免把恢复清理变成无界的 SQL 参数集合，不读取其他能力的私有表。
func ClearScansStaging(ctx context.Context, database Execer, scanIDs []string) error {
	for start := 0; start < len(scanIDs); start += 256 {
		if _, err := database.ExecContext(ctx, `DELETE FROM scan_observations WHERE scan_run_id=ANY($1::uuid[])`, scanIDs[start:min(start+256, len(scanIDs))]); err != nil {
			return err
		}
		if _, err := database.ExecContext(ctx, `DELETE FROM scan_held_sources WHERE scan_run_id=ANY($1::uuid[])`, scanIDs[start:min(start+256, len(scanIDs))]); err != nil {
			return err
		}
	}
	return nil
}

// ClearStagedObservations 删除 rootID 在本次扫描中的全部暂存行。
func ClearStagedObservations(ctx context.Context, database Execer, scanID, rootID string) error {
	_, err := database.ExecContext(ctx, "DELETE FROM scan_observations WHERE scan_run_id=$1::uuid AND root_id=$2::uuid", scanID, rootID)
	if err != nil {
		return err
	}
	_, err = database.ExecContext(ctx, "DELETE FROM scan_held_sources WHERE scan_run_id=$1::uuid AND root_id=$2::uuid", scanID, rootID)
	return err
}

// AlignCueStagingScopes 把 CUE 虚拟轨的显式父来源对齐到虚拟轨所在的 scope，使 organizer 能在同一 scope 内
// 按明确的父子关系消除虚拟/物理重复。
func AlignCueStagingScopes(ctx context.Context, database Execer, scanID, rootID string) error {
	_, err := database.ExecContext(ctx, `UPDATE scan_observations parent
		SET organization_scope=cue.organization_scope
		FROM (
			SELECT observation->>'cue_parent_relative_path' AS parent_path, MIN(organization_scope) AS organization_scope
			FROM scan_observations
			WHERE scan_run_id=$1::uuid AND root_id=$2::uuid AND observation->>'source_kind'='cue_virtual'
			GROUP BY observation->>'cue_parent_relative_path'
		) cue
		WHERE parent.scan_run_id=$1::uuid AND parent.root_id=$2::uuid
		  AND parent.relative_path=cue.parent_path`, scanID, rootID)
	return err
}
