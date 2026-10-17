package catalog

// 元数据问题（阶段 M4 第二项）：“需要关注”按三类问题拆开——扫描拿不准的字段、同一目录的归组冲突、缺失的曲目——
// 每类再分“待处理”与“已核对”（被仍然有效的核对压下）。“需要关注”是三类待处理数目之和（read.go 的 releaseAttentionExpression），
// 问题列表与详情用同样的常量，所以各类数目之和总等于 attention_count。
//
// 核对存在 release_issue_reviews（迁移 0019），记住核对时的依据：扫描结论里决定“情况是否变了”的那部分。读取时与现在的依据比较，
// 相同才算有效；扫描从不读写核对行，依据变了问题就重新计入。没有核对行时，三类待处理数目之和与只看扫描结论的算法逐项相同。
// 下面的常量引用外层查询的 releases（发行）与各自子查询的别名 decision、grouping、missing_track、missing_medium、review。

const (
	// fieldIssueBasis 是来源类型、介质类型的依据：原因、扫描选出的值与候选。
	fieldIssueBasis = `jsonb_build_object('reason', decision.reason, 'value', decision.selected_value, 'candidates', decision.candidates)`
	// groupingIssueBasis 是归组冲突的依据：原因与来源路径（扫描已排序、最多 100 条）。
	groupingIssueBasis = `jsonb_build_object('reason', grouping.reason, 'source_refs', grouping.source_refs)`
	// missingTrackSeenAt 是缺失曲目最后一次被看到的时间（Unix 微秒）。缺失曲目的依据是 {"tracks": {曲目 ID: 这个时间}}，
	// 写入与比较用同一个表达式：曲目回来时扫描更新 observed_at，再缺失时时间已经不同。
	missingTrackSeenAt = `(EXTRACT(EPOCH FROM missing_track.observed_at) * 1000000)::bigint`

	// fieldIssueRows 是发行里拿不准、也没有人工修正的字段决定（artist 由专辑艺人的修正覆盖）。
	fieldIssueRows = `FROM release_field_decisions decision
		WHERE decision.release_id = releases.id
		  AND decision.action = 'uncertain_apply'
		  AND NOT EXISTS (
			SELECT 1 FROM release_corrections correction
			WHERE correction.release_id = decision.release_id
			  AND correction.field = CASE decision.field_key WHEN 'artist' THEN 'album_artist' ELSE decision.field_key END
		  )`
	// fieldReviewValid 在这个字段有依据相同的核对时为真（只有来源类型、介质类型可能有核对行）。
	fieldReviewValid = `EXISTS (
			SELECT 1 FROM release_issue_reviews review
			WHERE review.release_id = decision.release_id
			  AND review.issue = decision.field_key
			  AND review.basis = ` + fieldIssueBasis + `
		  )`
	releaseOpenFieldIssues     = `(SELECT COUNT(DISTINCT decision.field_key) ` + fieldIssueRows + ` AND NOT ` + fieldReviewValid + `)`
	releaseReviewedFieldIssues = `(SELECT COUNT(DISTINCT decision.field_key) ` + fieldIssueRows + ` AND ` + fieldReviewValid + `)`

	groupingIssueRows = `FROM release_grouping_evidence grouping
		WHERE grouping.release_id = releases.id
		  AND NULLIF(BTRIM(grouping.reason), '') IS NOT NULL`
	groupingReviewValid = `EXISTS (
			SELECT 1 FROM release_issue_reviews review
			WHERE review.release_id = grouping.release_id
			  AND review.issue = 'grouping'
			  AND review.basis = ` + groupingIssueBasis + `
		  )`
	releaseOpenGroupingIssue     = `(CASE WHEN EXISTS (SELECT 1 ` + groupingIssueRows + ` AND NOT ` + groupingReviewValid + `) THEN 1 ELSE 0 END)`
	releaseReviewedGroupingIssue = `(CASE WHEN EXISTS (SELECT 1 ` + groupingIssueRows + ` AND ` + groupingReviewValid + `) THEN 1 ELSE 0 END)`

	missingTrackRows = `FROM tracks missing_track
		JOIN media missing_medium ON missing_medium.id = missing_track.medium_id
		WHERE missing_medium.release_id = releases.id
		  AND missing_track.source_status = 'missing'`
	// missingTrackReviewValid 逐首判断：这首在核对的依据里，并且最后一次被看到的时间没变。核对之后新缺失的曲目不在依据里，照常计入。
	missingTrackReviewValid = `EXISTS (
			SELECT 1 FROM release_issue_reviews review
			WHERE review.release_id = missing_medium.release_id
			  AND review.issue = 'missing_tracks'
			  AND (review.basis -> 'tracks' -> (missing_track.id::text)) = to_jsonb(` + missingTrackSeenAt + `)
		  )`
	// missingTrackCount 按来源去重计数缺失曲目。
	missingTrackCount            = `COUNT(DISTINCT COALESCE(NULLIF(missing_track.source_identity, ''), missing_track.source_root_id::text || ':' || missing_track.relative_path))`
	releaseOpenMissingTracks     = `(SELECT ` + missingTrackCount + ` ` + missingTrackRows + ` AND NOT ` + missingTrackReviewValid + `)`
	releaseReviewedMissingTracks = `(SELECT ` + missingTrackCount + ` ` + missingTrackRows + ` AND ` + missingTrackReviewValid + `)`
)
