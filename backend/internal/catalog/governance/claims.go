package governance

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
)

// 认领（阶段 M4 第三项）：扫描在“证明不了是同一个版本”时新建发行，旧发行不可见，它上面的人工修改（字段修正、所在的专辑、
// 默认版本、问题核对）被冻结。这里是“待认领”的判定与推荐，只读；认领本身（预览、写入、撤销）在后续文件里。
//
// 扫描不读写认领涉及的四类人工状态，也不记录“旧发行变成了哪个新发行”。推荐只用库里仍在的线索：旧发行名下曲目的路径，
// 以及它最后一次被扫描时的归组依据（release_grouping_evidence.source_refs，最多 100 条）。成员增加时原有曲目整体改挂到新发行，
// 旧发行名下一首曲目都不剩，只有归组依据还记得原来的文件。

// maxClaimSuggestions 是详情里推荐的版本上限。
const maxClaimSuggestions = 5

// 推荐的理由码。
const (
	ClaimReasonSameFiles     = "same_files"
	ClaimReasonSameDirectory = "same_directory"
)

// releaseClaimablePredicate 在发行行存在、不可见（没有 present 曲目），并且至少留有一项人工状态时为真；引用外层的 releases。
// 组里“有别的发行”不看可见与否：扫描从不把新发行放进已有的组，所以组里有别的发行只可能来自人工归组，
// 而且同一张专辑的两个版本都被替换之后，先认领一个，另一个仍能认领回这张专辑。
const releaseClaimablePredicate = `(NOT ` + releasesql.ReleaseVisiblePredicate + `
	AND (
		EXISTS (SELECT 1 FROM release_corrections claim_correction WHERE claim_correction.release_id = releases.id)
		OR EXISTS (SELECT 1 FROM releases claim_sibling WHERE claim_sibling.group_id = releases.group_id AND claim_sibling.id <> releases.id)
		OR EXISTS (SELECT 1 FROM release_groups claim_default WHERE claim_default.default_release_id = releases.id)
		OR EXISTS (SELECT 1 FROM release_issue_reviews claim_review WHERE claim_review.release_id = releases.id)
	))`

// ClaimSuggestion 是一个推荐的新版本：可见版本的摘要与它所在的组（页面据此打开它的专辑）、版本说明、理由码与命中的路径数。
type ClaimSuggestion struct {
	Release catalog.ReleaseSummary `json:"release"`
	GroupID string                 `json:"group_id"`
	Edition *string                `json:"edition"`
	Reasons []string               `json:"reasons"`
	// SameFiles 是旧版本的路径集合里与这个版本的 present 曲目相同的路径数（没有命中 same_files 时为 0）。
	SameFiles int64 `json:"same_files"`
}

// claimPathSetFor 是旧版本 O 的路径集合：名下全部曲目的 (root, 相对路径)（原地替换后缺失的旧曲目仍保留路径），
// 再加上归组依据里 O 同一 root 的来源路径（最后一次被扫描时的文件清单，最多 100 条）。origin 是 O 的 ID 的 SQL 表达式；
// UNION 已去重。
func claimPathSetFor(origin string) string {
	return `(
	SELECT claim_track.source_root_id AS root_id, claim_track.relative_path
	FROM tracks claim_track
	JOIN media claim_medium ON claim_medium.id = claim_track.medium_id
	WHERE claim_medium.release_id = ` + origin + `
	UNION
	SELECT claim_origin.source_root_id, claim_ref.value
	FROM releases claim_origin
	JOIN release_grouping_evidence claim_evidence ON claim_evidence.release_id = claim_origin.id
	CROSS JOIN LATERAL jsonb_array_elements_text(
		CASE WHEN jsonb_typeof(claim_evidence.source_refs) = 'array' THEN claim_evidence.source_refs ELSE '[]'::jsonb END
	) AS claim_ref(value)
	WHERE claim_origin.id = ` + origin + ` AND claim_origin.source_root_id IS NOT NULL
)`
}

// LoadClaimSuggestions 返回旧版本 sourceID 的推荐版本，最多 maxClaimSuggestions 个，只含可见版本。
// 理由是下面任一条（可以同时命中）：same_files（路径集合里有路径与候选的某首 present 曲目相同，同一 root）；
// same_directory（同一 root、同一相对目录）。两个理由都命中的在前，然后按 same_files 命中的路径数、有效标题与 ID。
// 路径比较走 tracks_bound_source_path_uq（只含持有来源身份的曲目，扫描里 present 的曲目都持有），每条路径一次索引查找；
// same_directory 没有可用的索引，只在打开一个旧版本的详情时算一次。
func (s *Store) LoadClaimSuggestions(ctx context.Context, sourceID string) ([]ClaimSuggestion, error) {
	rows, err := s.database.QueryContext(ctx, `
		WITH origin AS (
			SELECT id, source_root_id, relative_directory FROM releases WHERE id = $1::uuid
		), path_set AS (SELECT root_id, relative_path FROM `+claimPathSetFor("$1::uuid")+` WHERE relative_path <> ''),
		same_files AS (
			SELECT claim_medium.release_id AS release_id, COUNT(*) AS hits
			FROM path_set
			JOIN tracks claim_track ON claim_track.source_root_id = path_set.root_id
				AND claim_track.relative_path = path_set.relative_path AND claim_track.source_identity IS NOT NULL
			JOIN media claim_medium ON claim_medium.id = claim_track.medium_id
			WHERE claim_track.source_status = 'present'
			GROUP BY claim_medium.release_id
		), same_directory AS (
			SELECT releases.id AS release_id
			FROM origin JOIN `+releasesql.ReleaseSource+` ON releases.source_root_id = origin.source_root_id
				AND releases.relative_directory = origin.relative_directory
			WHERE origin.source_root_id IS NOT NULL AND origin.relative_directory IS NOT NULL
		)
		SELECT `+releasesql.ReleaseSummaryProjection+`, releases.group_id::text, NULLIF(BTRIM(releases.edition), ''),
			COALESCE(same_files.hits, 0), same_files.release_id IS NOT NULL, same_directory.release_id IS NOT NULL
		FROM `+releasesql.ReleaseSource+releasesql.ReleaseSummaryArtworkJoin+`
		LEFT JOIN same_files ON same_files.release_id = releases.id
		LEFT JOIN same_directory ON same_directory.release_id = releases.id
		WHERE releases.id <> $1::uuid AND `+releasesql.ReleaseVisiblePredicate+`
		  AND (same_files.release_id IS NOT NULL OR same_directory.release_id IS NOT NULL)
		ORDER BY (same_files.release_id IS NOT NULL AND same_directory.release_id IS NOT NULL) DESC,
			COALESCE(same_files.hits, 0) DESC, LOWER(releases.title), releases.id
		LIMIT $2`, sourceID, maxClaimSuggestions)
	if err != nil {
		return nil, fmt.Errorf("%w: claim suggestions: %w", catalog.ErrQuery, err)
	}
	defer rows.Close()
	suggestions := []ClaimSuggestion{}
	for rows.Next() {
		var columns catalog.ReleaseSummaryColumns
		var suggestion ClaimSuggestion
		var edition sql.NullString
		var byFiles, byDirectory bool
		if err := rows.Scan(append(columns.ScanTargets(), &suggestion.GroupID, &edition, &suggestion.SameFiles, &byFiles, &byDirectory)...); err != nil {
			return nil, err
		}
		if suggestion.Release, err = columns.Summary(); err != nil {
			return nil, err
		}
		suggestion.Edition = releasesql.StringPointerFromNull(edition)
		suggestion.Reasons = []string{}
		if byFiles {
			suggestion.Reasons = append(suggestion.Reasons, ClaimReasonSameFiles)
		}
		if byDirectory {
			suggestion.Reasons = append(suggestion.Reasons, ClaimReasonSameDirectory)
		}
		suggestions = append(suggestions, suggestion)
	}
	return suggestions, rows.Err()
}
