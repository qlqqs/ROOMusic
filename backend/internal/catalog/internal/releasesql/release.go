package releasesql

const (
	// ReleaseSource 是所有读取投影的 Release 来源：视图 release_effective 在扫描值之上叠加人工修正（迁移 0017），
	// 别名仍叫 releases，所以下面的谓词、投影与 ../../groups.go、../../track_placement.go 里的 releases.* 列都解析到有效值。
	// 没有修正时它与 releases 表的读取结果逐列相同。扫描与身份规划不经过它，仍读写 releases 本表。
	ReleaseSource = `release_effective releases`

	ReleaseVisiblePredicate = `EXISTS (
		SELECT 1
		FROM tracks visible_tracks
		JOIN media visible_media ON visible_media.id = visible_tracks.medium_id
		WHERE visible_media.release_id = releases.id
		  AND visible_tracks.source_status = 'present'
	)`

	// ReleaseAttentionExpression 是“需要关注”：三类待处理问题的数目之和（见 issues.go）。
	ReleaseAttentionExpression = `(` + ReleaseOpenFieldIssues + ` + ` + ReleaseOpenGroupingIssue + ` + ` + ReleaseOpenMissingTracks + `)`

	releaseSummaryIdentityProjection = `releases.id::text,
		releases.title,
		releases.artist,
		NULLIF(BTRIM(releases.album_artist), ''),
		NULLIF(releases.year, 0),
		NULLIF(BTRIM(releases.source_type), ''),
		NULLIF(BTRIM(releases.media_type), '')`

	releaseSummaryArtworkProjection = `release_artwork.storage_key,
		release_artwork.mime_type,
		release_artwork.width,
		release_artwork.height`

	ReleaseSummaryProjection = releaseSummaryIdentityProjection + `,
		(SELECT COUNT(DISTINCT present_medium.id)
		 FROM media present_medium
		 JOIN tracks present_track ON present_track.medium_id = present_medium.id
		 WHERE present_medium.release_id = releases.id
		   AND present_track.source_status = 'present'),
		(SELECT COUNT(*)
		 FROM tracks present_track
		 JOIN media present_medium ON present_medium.id = present_track.medium_id
		 WHERE present_medium.release_id = releases.id
		   AND present_track.source_status = 'present'),
		` + ReleaseAttentionExpression + `,` + releaseSummaryArtworkProjection + `,` + releaseSummaryArtistRefsProjection

	ReleaseSummaryArtworkJoin = ` LEFT JOIN release_artworks release_artwork
		ON release_artwork.release_id = releases.id`

	// ReleaseGroupMemberOrder 是代表版本规则：先是管理员指定的默认版本（release_groups.default_release_id），
	// 再是年份最早（没有年份排最后）、创建时间、ID。第一项只有在默认版本是这个组的可见成员时才会让某一行排到最前：
	// 没有指定、指定的版本不可见或已被移出这个组时，它对所有成员都为真，等于没有指定（静默失效，回来时重新生效），
	// 所以生效条件不需要另写。
	ReleaseGroupMemberOrder = `(releases.id IS DISTINCT FROM (SELECT default_group.default_release_id FROM release_groups default_group WHERE default_group.id = releases.group_id)),
		(NULLIF(releases.year, 0) IS NULL), NULLIF(releases.year, 0), releases.created_at, releases.id`
)
