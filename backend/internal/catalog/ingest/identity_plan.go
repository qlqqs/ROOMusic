package ingest

import (
	"context"
	"errors"
	"fmt"
)

// 曲目身份规划：把 root 的暂存观察与在库曲目对上，决定每条保留下来的观察沿用哪条曲目、还是新建。
//
// 规则（详见任务 09-28-classical-i-identity-groups）：
//   - 曲目先按当前来源身份匹配；同一来源身份下两边指纹都已知且不同，视为换了内容，新内容建新曲目。
//   - 遍历完整的 root 中，没有匹配到身份的观察与“本轮消失”的曲目按指纹键配对，两侧都恰好一条才沿用原曲目。
//   - 遍历不完整时不重绑定，含疑似移动或换内容观察的 scope 暂缓写入，留给下一次完整遍历。
//   - Release 按曲目成员关系匹配：候选的保留曲目中严格过半当前属于同一个既有 Release 时沿用它，锚点只是位置信息。
//
// 规划只做 SQL 与读文件，不持有长事务；结果写在暂存行上，由 PersistCandidate 按行写入。

// Fingerprinter 返回 root 内某个来源文件与标签无关的内容指纹。空串表示该文件没有可识别的内容，
// 不参与配对与换内容判断；返回错误表示文件内容无法完整读取。
type Fingerprinter interface {
	Fingerprint(ctx context.Context, relativePath string) (string, error)
}

// 规划诊断的种类，由调用方写入扫描诊断。
const (
	PlanDiagnosticIdentityAmbiguous  = "identity_ambiguous"
	PlanDiagnosticContentReplaced    = "content_replaced"
	PlanDiagnosticIdentityDeferred   = "identity_deferred"
	PlanDiagnosticFingerprintFailure = "fingerprint_failure"
)

const (
	// maxPlanDiagnosticsPerKind 与每次扫描最多 100 条诊断的上限一致；再多的条目也写不进去。
	maxPlanDiagnosticsPerKind = 100
	// fingerprintPathPageSize 是每次从暂存表取出待计算指纹的文件数。
	fingerprintPathPageSize = 100
)

// PlanDiagnostic 是规划期间需要写入扫描诊断的一条记录；RelativePath 是 root 内的相对路径，不含绝对路径。
type PlanDiagnostic struct {
	RelativePath string
	Kind         string
	Message      string
}

// PlanOptions 是一个 root 的规划输入。
type PlanOptions struct {
	// WalkComplete 表示该 root 的遍历完整且没有被取消。只有它为真、且规划中没有指纹失败时才允许配对重绑定。
	WalkComplete bool
	// Fingerprinter 用来计算没有免费指纹、也不能复用已存指纹的来源文件；不能为空。
	Fingerprinter Fingerprinter
}

// RootPlan 是一个 root 的规划结果：计数用于日志，诊断由调用方写入扫描诊断。
type RootPlan struct {
	Matched              int // 同内容匹配、沿用原曲目的观察数
	Rebound              int // 配对重绑定、沿用原曲目的观察数（含换内容后与别处消失曲目配对的）
	AmbiguousKeys        int // 内容相同的多个来源导致没有重绑定的指纹键数
	ContentReplaced      int // 同一路径换了内容、按新曲目写入的观察数
	DeferredScopes       int // 遍历不完整时暂缓写入的 scope 数（不含因失败暂缓的）
	FingerprintsComputed int // 经 Fingerprinter 计算的文件数
	FingerprintsReused   int // 复用已存指纹的观察数
	FingerprintFailures  int // 指纹计算失败的文件数
	ScopeFailures        int // 读取或整理失败而暂缓的 scope 数
	Diagnostics          []PlanDiagnostic
}

// PlanStagedRoot 为 rootID 在本次扫描中的暂存观察规划曲目身份，并把结果写回暂存行。它必须在遍历结束、
// 所有观察都已暂存（含 CUE scope 对齐）之后、写入之前调用。返回错误表示规划本身失败（数据库错误或被取消），
// 此时暂存行没有完整的规划，调用方不得写入；单个 scope 或文件的失败记在结果里，不阻断其它 scope。
func PlanStagedRoot(ctx context.Context, database StagingDB, scanID, rootID string, options PlanOptions) (RootPlan, error) {
	var plan RootPlan
	if options.Fingerprinter == nil {
		return plan, errors.New("identity planning requires a fingerprinter")
	}
	if err := assignCandidateKeys(ctx, database, scanID, rootID, &plan); err != nil {
		return plan, err
	}
	if _, err := database.ExecContext(ctx, `UPDATE scan_observations o SET matched_track_id=t.id
		FROM tracks t
		WHERE o.scan_run_id=$1::uuid AND o.root_id=$2::uuid AND o.candidate_key IS NOT NULL AND NOT o.withheld
		  AND t.source_identity=o.source_identity`, scanID, rootID); err != nil {
		return plan, err
	}
	if err := resolveFingerprints(ctx, database, scanID, rootID, options.Fingerprinter, &plan); err != nil {
		return plan, err
	}
	if _, err := database.ExecContext(ctx, `UPDATE scan_observations o SET
			content_changed=(t.content_fingerprint IS NOT NULL AND o.content_fingerprint IS NOT NULL AND t.content_fingerprint<>o.content_fingerprint),
			planned_track_id=CASE WHEN t.content_fingerprint IS NOT NULL AND o.content_fingerprint IS NOT NULL AND t.content_fingerprint<>o.content_fingerprint
				THEN NULL ELSE o.matched_track_id END
		FROM tracks t
		WHERE o.scan_run_id=$1::uuid AND o.root_id=$2::uuid AND o.candidate_key IS NOT NULL AND NOT o.withheld
		  AND o.matched_track_id=t.id`, scanID, rootID); err != nil {
		return plan, err
	}

	// 只有遍历完整、且规划中没有失败时，本轮缺失的证据才足以判断“移动”。
	if options.WalkComplete && plan.FingerprintFailures == 0 && plan.ScopeFailures == 0 {
		if err := pairVanishedTracks(ctx, database, scanID, rootID, &plan); err != nil {
			return plan, err
		}
	} else if err := withholdSuspiciousScopes(ctx, database, scanID, rootID, &plan); err != nil {
		return plan, err
	}
	if err := allocateReleases(ctx, database, scanID, rootID); err != nil {
		return plan, err
	}
	if err := summarizePlan(ctx, database, scanID, rootID, &plan); err != nil {
		return plan, err
	}
	// 规划完成：保留下来且没有被暂缓的观察才允许写入。
	if _, err := database.ExecContext(ctx, `UPDATE scan_observations SET planned=TRUE
		WHERE scan_run_id=$1::uuid AND root_id=$2::uuid AND candidate_key IS NOT NULL AND NOT withheld`, scanID, rootID); err != nil {
		return plan, err
	}
	return plan, nil
}

// assignCandidateKeys 逐 scope 读取暂存（不含封面字节）并整理，为 organizer 保留下来的观察写入候选键。
// 被隐藏的观察（例如多轨 CUE 的整轨镜像）没有候选键，不参与匹配、指纹与配对。
func assignCandidateKeys(ctx context.Context, database StagingDB, scanID, rootID string, plan *RootPlan) error {
	after := ""
	for {
		scopes, err := stagedScopePage(ctx, database, scanID, rootID, after, true)
		if err != nil {
			return err
		}
		if len(scopes) == 0 {
			return nil
		}
		for _, scope := range scopes {
			observations, err := loadStagedScope(ctx, database, scanID, rootID, scope, false)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				// 单个 scope 读不出来（超过上限或载荷损坏）：暂缓该 scope，不阻断其它 scope。
				plan.ScopeFailures++
				if _, err := database.ExecContext(ctx, `UPDATE scan_observations SET withheld=TRUE
					WHERE scan_run_id=$1::uuid AND root_id=$2::uuid AND organization_scope=$3`, scanID, rootID, scope); err != nil {
					return err
				}
				continue
			}
			for _, candidate := range OrganizeObservations(observations) {
				paths := make([]string, 0, len(candidate.Mediums))
				for _, tracks := range candidate.Mediums {
					for _, track := range tracks {
						paths = append(paths, NormalizedPath(track.Observation.RelativePath))
					}
				}
				if _, err := database.ExecContext(ctx, `UPDATE scan_observations SET candidate_key=$1
					WHERE scan_run_id=$2::uuid AND root_id=$3::uuid AND relative_path=ANY($4::text[])`, CandidateIdentity(rootID, candidate.Anchor), scanID, rootID, paths); err != nil {
					return err
				}
			}
		}
		after = scopes[len(scopes)-1]
	}
}

// resolveFingerprints 为保留下来、还没有指纹的观察补齐指纹：文件大小与修改时间都没变的匹配观察复用已存指纹，
// 其余（新出现的文件、文件已变化的、迁移后尚未保存指纹的）经 Fingerprinter 计算，同一文件只算一次。
// 计算失败的文件所在的 scope 暂缓写入。
func resolveFingerprints(ctx context.Context, database StagingDB, scanID, rootID string, fingerprinter Fingerprinter, plan *RootPlan) error {
	reused, err := database.ExecContext(ctx, `UPDATE scan_observations o SET content_fingerprint=t.content_fingerprint
		FROM tracks t
		WHERE o.scan_run_id=$1::uuid AND o.root_id=$2::uuid AND o.candidate_key IS NOT NULL AND NOT o.withheld
		  AND o.content_fingerprint IS NULL AND o.matched_track_id=t.id AND t.content_fingerprint IS NOT NULL
		  AND o.source_size IS NOT NULL AND o.source_modified_at IS NOT NULL
		  AND o.source_size=t.source_size AND o.source_modified_at=t.source_modified_at`, scanID, rootID)
	if err != nil {
		return err
	}
	if count, err := reused.RowsAffected(); err == nil {
		plan.FingerprintsReused = int(count)
	}
	after := ""
	for {
		rows, err := database.QueryContext(ctx, `SELECT DISTINCT fingerprint_path FROM scan_observations
			WHERE scan_run_id=$1::uuid AND root_id=$2::uuid AND candidate_key IS NOT NULL AND NOT withheld
			  AND content_fingerprint IS NULL AND fingerprint_path IS NOT NULL AND fingerprint_path>$3
			ORDER BY fingerprint_path LIMIT $4`, scanID, rootID, after, fingerprintPathPageSize)
		if err != nil {
			return err
		}
		paths := make([]string, 0, fingerprintPathPageSize)
		for rows.Next() {
			var path string
			if err := rows.Scan(&path); err != nil {
				rows.Close()
				return err
			}
			paths = append(paths, path)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(paths) == 0 {
			return nil
		}
		for _, path := range paths {
			if err := computeFingerprint(ctx, database, scanID, rootID, fingerprinter, path, plan); err != nil {
				return err
			}
		}
		after = paths[len(paths)-1]
	}
}

func computeFingerprint(ctx context.Context, database StagingDB, scanID, rootID string, fingerprinter Fingerprinter, path string, plan *RootPlan) error {
	fingerprint, err := fingerprinter.Fingerprint(ctx, path)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		plan.FingerprintFailures++
		plan.addDiagnostic(PlanDiagnosticFingerprintFailure, path, "无法读取音频内容以计算指纹")
		_, err := database.ExecContext(ctx, `UPDATE scan_observations SET withheld=TRUE
			WHERE scan_run_id=$1::uuid AND root_id=$2::uuid AND organization_scope IN (
				SELECT organization_scope FROM scan_observations
				WHERE scan_run_id=$1::uuid AND root_id=$2::uuid AND fingerprint_path=$3)`, scanID, rootID, path)
		return err
	}
	plan.FingerprintsComputed++
	if fingerprint == "" {
		return nil
	}
	_, err = database.ExecContext(ctx, `UPDATE scan_observations SET content_fingerprint=$3
		WHERE scan_run_id=$1::uuid AND root_id=$2::uuid AND candidate_key IS NOT NULL AND NOT withheld
		  AND content_fingerprint IS NULL AND fingerprint_path=$4`, scanID, rootID, fingerprint, path)
	return err
}

// 配对键：(指纹, CUE 起点, CUE 终点)，空值按相等处理。曲目侧只有 CUE 虚拟轨才带区间——物理轨的 cue_* 列可能保存着
// 单轨 CUE 合并进来的证据，而暂存的物理观察没有这些值。
const (
	vanishedTracksCTE = `vanished AS (
		SELECT t.id, t.content_fingerprint AS fingerprint,
		       CASE WHEN t.source_kind='cue_virtual' THEN t.cue_index_frames END AS index_frames,
		       CASE WHEN t.source_kind='cue_virtual' THEN t.cue_end_frames END AS end_frames
		FROM tracks t
		WHERE t.source_root_id=$2::uuid AND t.source_status='present' AND t.content_fingerprint IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM scan_observations used
		                  WHERE used.scan_run_id=$1::uuid AND used.root_id=$2::uuid AND used.planned_track_id=t.id)
	)`
	newSourcesCTE = `new_sources AS (
		SELECT o.id, o.organization_scope, COALESCE(o.fingerprint_path, o.relative_path) AS path,
		       o.content_fingerprint AS fingerprint, o.cue_index_frames AS index_frames, o.cue_end_frames AS end_frames
		FROM scan_observations o
		WHERE o.scan_run_id=$1::uuid AND o.root_id=$2::uuid AND o.candidate_key IS NOT NULL AND NOT o.withheld
		  AND o.planned_track_id IS NULL AND o.content_fingerprint IS NOT NULL
	)`
	sameKey = `v.fingerprint=n.fingerprint AND v.index_frames IS NOT DISTINCT FROM n.index_frames AND v.end_frames IS NOT DISTINCT FROM n.end_frames`
)

// pairVanishedTracks 在遍历完整的 root 中做保守重绑定：本轮没有匹配到身份（或换了内容）的观察，与本 root 中
// 本轮消失的曲目按指纹键配对；两侧都恰好一条才沿用原曲目。其余不重绑定，逐键记 identity_ambiguous；
// 配对后仍为空的换内容观察写为新曲目，记 content_replaced。
func pairVanishedTracks(ctx context.Context, database StagingDB, scanID, rootID string, plan *RootPlan) error {
	if _, err := database.ExecContext(ctx, `WITH `+vanishedTracksCTE+`, `+newSourcesCTE+`,
		keyed_vanished AS (SELECT v.*, COUNT(*) OVER (PARTITION BY v.fingerprint, v.index_frames, v.end_frames) AS peers FROM vanished v),
		keyed_new AS (SELECT n.*, COUNT(*) OVER (PARTITION BY n.fingerprint, n.index_frames, n.end_frames) AS peers FROM new_sources n)
		UPDATE scan_observations o SET planned_track_id=pair.track_id
		FROM (
			SELECT n.id AS observation_id, v.id AS track_id
			FROM keyed_new n JOIN keyed_vanished v ON `+sameKey+`
			WHERE n.peers=1 AND v.peers=1
		) pair
		WHERE o.id=pair.observation_id`, scanID, rootID); err != nil {
		return err
	}
	// 配对完成后，仍在两侧都非空的键就是歧义键。
	rows, err := database.QueryContext(ctx, `WITH `+vanishedTracksCTE+`, `+newSourcesCTE+`
		SELECT first_path, COUNT(*) OVER () FROM (
			SELECT MIN(n.path) AS first_path
			FROM new_sources n
			WHERE EXISTS (SELECT 1 FROM vanished v WHERE `+sameKey+`)
			GROUP BY n.fingerprint, n.index_frames, n.end_frames
		) ambiguous
		ORDER BY first_path LIMIT `+fmt.Sprint(maxPlanDiagnosticsPerKind), scanID, rootID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var path string
		if err := rows.Scan(&path, &plan.AmbiguousKeys); err != nil {
			return err
		}
		plan.addDiagnostic(PlanDiagnosticIdentityAmbiguous, path, "多个来源的音频内容相同，没有沿用原身份")
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	replaced, err := database.QueryContext(ctx, `SELECT DISTINCT COALESCE(fingerprint_path, relative_path) FROM scan_observations
		WHERE scan_run_id=$1::uuid AND root_id=$2::uuid AND candidate_key IS NOT NULL AND NOT withheld
		  AND content_changed AND planned_track_id IS NULL
		ORDER BY 1 LIMIT `+fmt.Sprint(maxPlanDiagnosticsPerKind), scanID, rootID)
	if err != nil {
		return err
	}
	defer replaced.Close()
	for replaced.Next() {
		var path string
		if err := replaced.Scan(&path); err != nil {
			return err
		}
		plan.addDiagnostic(PlanDiagnosticContentReplaced, path, "同一路径的音频内容已变化，已作为新曲目")
	}
	return replaced.Err()
}

// withholdSuspiciousScopes 在遍历不完整时使用：不重绑定，凡含有“键落在消失集合中的未匹配观察”或“换内容观察”的
// scope 整体暂缓写入，记 identity_deferred。以 scope 为单位，是为了不只扣掉 CUE 父子关系中的一方而改变
// organizer 的去重结果。这样一次不完整的扫描不会把移动后的文件写成新曲目，下一次完整遍历仍能配对。
func withholdSuspiciousScopes(ctx context.Context, database StagingDB, scanID, rootID string, plan *RootPlan) error {
	const suspiciousCTE = `suspicious AS (
		SELECT o.organization_scope, MIN(COALESCE(o.fingerprint_path, o.relative_path)) AS first_path
		FROM scan_observations o
		WHERE o.scan_run_id=$1::uuid AND o.root_id=$2::uuid AND o.candidate_key IS NOT NULL AND NOT o.withheld
		  AND (o.content_changed OR (o.planned_track_id IS NULL AND o.content_fingerprint IS NOT NULL AND EXISTS (
		         SELECT 1 FROM vanished v
		         WHERE v.fingerprint=o.content_fingerprint AND v.index_frames IS NOT DISTINCT FROM o.cue_index_frames AND v.end_frames IS NOT DISTINCT FROM o.cue_end_frames)))
		GROUP BY o.organization_scope
	)`
	rows, err := database.QueryContext(ctx, `WITH `+vanishedTracksCTE+`, `+suspiciousCTE+`
		SELECT first_path, COUNT(*) OVER () FROM suspicious
		ORDER BY organization_scope LIMIT `+fmt.Sprint(maxPlanDiagnosticsPerKind), scanID, rootID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var path string
		if err := rows.Scan(&path, &plan.DeferredScopes); err != nil {
			return err
		}
		plan.addDiagnostic(PlanDiagnosticIdentityDeferred, path, "扫描不完整，疑似移动或内容变化的来源留待完整扫描处理")
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	_, err = database.ExecContext(ctx, `WITH `+vanishedTracksCTE+`, `+suspiciousCTE+`
		UPDATE scan_observations SET withheld=TRUE
		WHERE scan_run_id=$1::uuid AND root_id=$2::uuid AND organization_scope IN (SELECT organization_scope FROM suspicious)`, scanID, rootID)
	return err
}

// allocateReleases 在曲目规划之后、写入之前，对未暂缓的全部候选一次性分配要沿用的 Release：
//   - 候选的总数是它保留下来的观察数；与既有 Release R 的重叠，是其中已规划沿用的曲目当前属于 R 的条数。
//   - 重叠严格超过总数一半时，候选倾向 R；严格过半保证每个候选至多倾向一个 Release。
//   - 多个候选倾向同一个 R 时，重叠多者得；平局时候选键等于 R 当前锚点者（留在原位置的一方）优先，
//     再平局按候选键升序。
//   - 获胜候选的全部暂存行写入 planned_release_id；落选或没有倾向的候选留空，写入时新建 Release 与组。
//
// 候选键即 CandidateIdentity(rootID, 锚点)，与 releases.candidate_anchor 同一格式；锚点不再用于查找，
// 只在这里用来打破平局，并在写入时更新为最近一次的位置。
func allocateReleases(ctx context.Context, database StagingDB, scanID, rootID string) error {
	_, err := database.ExecContext(ctx, `WITH candidate_rows AS (
			SELECT o.candidate_key, o.planned_track_id
			FROM scan_observations o
			WHERE o.scan_run_id=$1::uuid AND o.root_id=$2::uuid AND o.candidate_key IS NOT NULL AND NOT o.withheld
		), totals AS (
			SELECT candidate_key, COUNT(*) AS total FROM candidate_rows GROUP BY candidate_key
		), candidate_overlaps AS (
			SELECT c.candidate_key, m.release_id, COUNT(*) AS overlap
			FROM candidate_rows c
			JOIN tracks t ON t.id=c.planned_track_id
			JOIN media m ON m.id=t.medium_id
			GROUP BY c.candidate_key, m.release_id
		), preferences AS (
			SELECT o.candidate_key, o.release_id, o.overlap, COALESCE(r.candidate_anchor=o.candidate_key, FALSE) AS stays
			FROM candidate_overlaps o
			JOIN totals tt ON tt.candidate_key=o.candidate_key
			JOIN releases r ON r.id=o.release_id
			WHERE o.overlap*2>tt.total
		), winners AS (
			SELECT DISTINCT ON (release_id) release_id, candidate_key
			FROM preferences
			ORDER BY release_id, overlap DESC, stays DESC, candidate_key ASC
		)
		UPDATE scan_observations o SET planned_release_id=w.release_id
		FROM winners w
		WHERE o.scan_run_id=$1::uuid AND o.root_id=$2::uuid AND o.candidate_key=w.candidate_key AND NOT o.withheld`, scanID, rootID)
	return err
}

// summarizePlan 统计最终沿用原曲目与换内容的观察数。
func summarizePlan(ctx context.Context, database StagingDB, scanID, rootID string, plan *RootPlan) error {
	return database.QueryRowContext(ctx, `SELECT
			COUNT(*) FILTER (WHERE planned_track_id IS NOT NULL AND planned_track_id=matched_track_id),
			COUNT(*) FILTER (WHERE planned_track_id IS NOT NULL AND matched_track_id IS DISTINCT FROM planned_track_id),
			COUNT(*) FILTER (WHERE content_changed AND planned_track_id IS NULL)
		FROM scan_observations
		WHERE scan_run_id=$1::uuid AND root_id=$2::uuid AND candidate_key IS NOT NULL AND NOT withheld`, scanID, rootID).
		Scan(&plan.Matched, &plan.Rebound, &plan.ContentReplaced)
}

func (plan *RootPlan) addDiagnostic(kind, path, message string) {
	plan.Diagnostics = append(plan.Diagnostics, PlanDiagnostic{RelativePath: path, Kind: kind, Message: message})
}
