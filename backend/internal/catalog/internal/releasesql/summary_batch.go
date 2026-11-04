package releasesql

// BatchSummaryCountsCTE 要求调用方先定义 summary_release_ids(id)，只含本批去重后的发行 UUID。
// 三类曲目计数合并为一次受发行集合限定的分组聚合，避免每个发行三个相关子查询的重复扫描与高成本 JIT。
// 缺失曲目继续按来源身份去重、逐轨匹配核对依据；复用既有表达式，不改变 attention 规则。
const BatchSummaryCountsCTE = `summary_counts AS MATERIALIZED (
 SELECT missing_medium.release_id,
 COUNT(DISTINCT missing_medium.id) FILTER (WHERE missing_track.source_status='present') AS medium_count,
 COUNT(*) FILTER (WHERE missing_track.source_status='present') AS track_count,
 ` + missingTrackCount + ` FILTER (WHERE missing_track.source_status='missing' AND NOT ` + MissingTrackReviewValid + `) AS missing_count
 FROM summary_release_ids
 JOIN media missing_medium ON missing_medium.release_id=summary_release_ids.id
 JOIN tracks missing_track ON missing_track.medium_id=missing_medium.id
 GROUP BY missing_medium.release_id
)`

// BatchSummaryProjection 与 ReleaseSummaryProjection 的列顺序、有效值和安全封面合同完全相同。
// 仅曲目计数改从 BatchSummaryCountsCTE 读取，字段问题与归组问题继续由原有规则解释。
const BatchSummaryProjection = releaseSummaryIdentityProjection + `,
 COALESCE(summary_counts.medium_count,0),COALESCE(summary_counts.track_count,0),
 (` + ReleaseOpenFieldIssues + ` + ` + ReleaseOpenGroupingIssue + ` + COALESCE(summary_counts.missing_count,0)),
 ` + releaseSummaryArtworkProjection

const BatchSummaryJoins = ` LEFT JOIN summary_counts ON summary_counts.release_id=releases.id` + ReleaseSummaryArtworkJoin
