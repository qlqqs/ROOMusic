package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	pathpkg "path"
	"strings"
	"time"
)

// 待认领的读取（阶段 M4 第三项，只有管理员）：列表、一个旧版本的详情（留着的修改明细与推荐），以及专辑详情用的“可能接替了……”提示。
// 判定与推荐的规则在 claims.go；这里只做投影。路径一律经过 SafeRelativeSourceRef，不返回绝对路径。

// maxClaimHints 是专辑详情里“可能接替了已不可见的《…》”的旧版本上限。
const maxClaimHints = 3

// claimRootName 是 library_roots 的显示名：路径末段，与扫描目录列表（filepath.Base）一致，不暴露绝对路径。
const claimRootName = `NULLIF(regexp_replace(claim_root.path, '^.*/', ''), '')`

// claimLastSeen 是旧版本最后一次被看到的时间：名下曲目 observed_at 的最大值；名下没有曲目时取归组依据的 observed_at，都没有时取发行的创建时间。
// 引用外层的 releases。
const claimLastSeen = `COALESCE(
	(SELECT MAX(last_track.observed_at) FROM tracks last_track JOIN media last_medium ON last_medium.id = last_track.medium_id WHERE last_medium.release_id = releases.id),
	(SELECT last_evidence.observed_at FROM release_grouping_evidence last_evidence WHERE last_evidence.release_id = releases.id),
	releases.created_at)`

// ClaimRelease 是待认领列表与详情里旧版本的摘要：有效标题（有修正时就是修正值，这正是管理员认得出的名字）、艺人、年份与版本说明。
type ClaimRelease struct {
	ID      string  `json:"id"`
	Title   string  `json:"title"`
	Artist  string  `json:"artist"`
	Year    *int    `json:"year"`
	Edition *string `json:"edition"`
}

// ClaimAlbum 是旧版本原来所在的专辑：组 ID、专辑的标题（组里一个发行的有效标题）与组里别的发行数。
type ClaimAlbum struct {
	ID                string `json:"id"`
	Title             string `json:"title"`
	OtherReleaseCount int64  `json:"other_release_count"`
}

// ClaimLocation 是旧版本最后所在的位置：扫描目录的显示名与相对目录；不安全的目录不返回（空串）。
type ClaimLocation struct {
	RootName  string `json:"root_name"`
	Directory string `json:"directory"`
}

// ClaimHolds 是旧版本留着的人工状态：有修正的字段、是否与别的发行同在一张专辑里、是否是某张专辑的默认版本，以及有核对的问题。
type ClaimHolds struct {
	Corrections []string `json:"corrections"`
	Album       bool     `json:"album"`
	Default     bool     `json:"default"`
	Reviews     []string `json:"reviews"`
}

// ClaimEntry 是待认领列表的一项：旧版本、原来的专辑、最后所在的位置、最后一次被看到的时间与留着的人工状态。
type ClaimEntry struct {
	Release    ClaimRelease  `json:"release"`
	Album      *ClaimAlbum   `json:"album"`
	Location   ClaimLocation `json:"location"`
	LastSeenAt time.Time     `json:"last_seen_at"`
	Holds      ClaimHolds    `json:"holds"`
}

// ClaimPage 是一页待认领的旧版本和总数；Items 永远非 nil。
type ClaimPage struct {
	Items []ClaimEntry
	Total int64
}

// ClaimListParams 是待认领列表的已校验参数：Page 与 PageSize 都大于 0。
type ClaimListParams struct{ Page, PageSize int }

// claimEntryProjection 是列表项的列；FROM 子句必须提供 releases（有效视图）与 claim_root（library_roots，左连接）。
const claimEntryProjection = `releases.id::text, releases.title, releases.artist, NULLIF(releases.year, 0), NULLIF(BTRIM(releases.edition), ''),
	releases.group_id::text, ` + claimRootName + `, releases.relative_directory, ` + claimLastSeen + `,
	COALESCE((SELECT string_agg(claim_correction.field, ',' ORDER BY claim_correction.field) FROM release_corrections claim_correction WHERE claim_correction.release_id = releases.id), ''),
	(SELECT COUNT(*) FROM releases claim_sibling WHERE claim_sibling.group_id = releases.group_id AND claim_sibling.id <> releases.id),
	EXISTS (SELECT 1 FROM release_groups claim_default WHERE claim_default.default_release_id = releases.id),
	COALESCE((SELECT string_agg(claim_review.issue, ',' ORDER BY claim_review.issue) FROM release_issue_reviews claim_review WHERE claim_review.release_id = releases.id), '')`

const claimEntrySource = ` FROM ` + releaseSource + ` LEFT JOIN library_roots claim_root ON claim_root.id = releases.source_root_id`

// albumTitleOf 返回一个组的标题：组里一个发行的有效标题，先取可见成员里按代表版本规则排第一的，没有可见成员就取最早创建的成员。
// excluding 是不参与取名的发行（旧版本自己）：它的名字已经显示在行的标题里，专辑的名字要取别的成员的。
func albumTitleOf(ctx context.Context, database Queryer, groupID, excluding string) (string, error) {
	var title string
	err := database.QueryRowContext(ctx, `SELECT releases.title FROM `+releaseSource+` WHERE releases.group_id=$1::uuid AND releases.id<>$2::uuid
		ORDER BY (CASE WHEN `+releaseVisiblePredicate+` THEN 0 ELSE 1 END), `+releaseGroupMemberOrder+`, releases.created_at, releases.id LIMIT 1`, groupID, excluding).Scan(&title)
	return title, err
}

type claimEntryColumns struct {
	entry                        ClaimEntry
	year                         sql.NullInt64
	edition, rootName, directory sql.NullString
	groupID                      string
	corrections, reviews         string // 逗号分隔的标识符
	siblings                     int64
}

func (columns *claimEntryColumns) targets() []any {
	return []any{&columns.entry.Release.ID, &columns.entry.Release.Title, &columns.entry.Release.Artist, &columns.year, &columns.edition,
		&columns.groupID, &columns.rootName, &columns.directory, &columns.entry.LastSeenAt,
		&columns.corrections, &columns.siblings, &columns.entry.Holds.Default, &columns.reviews}
}

// identifiers 把逗号分隔的标识符拆成切片；空串是空切片（永远非 nil）。
func identifiers(joined string) []string {
	if joined == "" {
		return []string{}
	}
	return strings.Split(joined, ",")
}

// finish 解释 nullable 列，补上专辑与位置；album 只在组里有别的发行时有。
func (columns *claimEntryColumns) finish(ctx context.Context, database Queryer) (ClaimEntry, error) {
	entry := columns.entry
	entry.Release.Year = intPointerFromNull(columns.year)
	entry.Release.Edition = stringPointerFromNull(columns.edition)
	entry.Location.RootName, entry.Location.Directory = claimLocation(columns.rootName, columns.directory)
	entry.Holds.Corrections, entry.Holds.Reviews = identifiers(columns.corrections), identifiers(columns.reviews)
	if columns.siblings > 0 {
		title, err := albumTitleOf(ctx, database, columns.groupID, entry.Release.ID)
		if err != nil {
			return ClaimEntry{}, err
		}
		entry.Album = &ClaimAlbum{ID: columns.groupID, Title: title, OtherReleaseCount: columns.siblings}
		entry.Holds.Album = true
	}
	return entry, nil
}

// claimLocation 返回旧版本最后所在的位置：扫描目录的显示名，以及经 SafeRelativeSourceRef 清理过的相对目录（不安全或为空时是空串）。
func claimLocation(rootName, directory sql.NullString) (string, string) {
	name := strings.TrimSpace(rootName.String)
	if name == "" {
		name = "未命名的扫描目录"
	}
	if !directory.Valid || strings.TrimSpace(directory.String) == "" {
		return name, ""
	}
	if safe, ok := SafeRelativeSourceRef(directory.String); ok {
		return name, pathpkg.Clean(safe)
	}
	return name, ""
}

// ListClaims 返回待认领的旧版本的一页，按最后一次被看到的时间倒序（没有接替者的会沉到后面），再按 ID。查询执行失败时错误包含 ErrQuery。
func (s *Store) ListClaims(ctx context.Context, params ClaimListParams) (ClaimPage, error) {
	page := ClaimPage{Items: []ClaimEntry{}}
	if err := s.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+releaseSource+` WHERE `+releaseClaimablePredicate).Scan(&page.Total); err != nil {
		return ClaimPage{}, fmt.Errorf("%w: count claims: %w", ErrQuery, err)
	}
	rows, err := s.database.QueryContext(ctx, `SELECT `+claimEntryProjection+claimEntrySource+` WHERE `+releaseClaimablePredicate+`
		ORDER BY `+claimLastSeen+` DESC, releases.id LIMIT $1 OFFSET $2`, params.PageSize, (params.Page-1)*params.PageSize)
	if err != nil {
		return ClaimPage{}, fmt.Errorf("%w: list claims: %w", ErrQuery, err)
	}
	defer rows.Close()
	var all []claimEntryColumns
	for rows.Next() {
		var columns claimEntryColumns
		if err := rows.Scan(columns.targets()...); err != nil {
			return ClaimPage{}, err
		}
		all = append(all, columns)
	}
	if err := rows.Err(); err != nil {
		return ClaimPage{}, err
	}
	rows.Close()
	for index := range all {
		entry, err := all[index].finish(ctx, s.database)
		if err != nil {
			return ClaimPage{}, err
		}
		page.Items = append(page.Items, entry)
	}
	return page, nil
}

// ClaimCorrection 是旧版本上的一个字段修正：字段、修正意图与扫描值。
type ClaimCorrection struct {
	Field      string           `json:"field"`
	Correction CorrectionIntent `json:"correction"`
	ScanValue  any              `json:"scan_value"`
}

// ClaimAlbumDetail 是旧版本原来所在的专辑里的别的发行：标题、是否可见。
type ClaimAlbumSibling struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Visible bool   `json:"visible"`
}

// ClaimReviewDetail 是旧版本上的一条核对：问题类别与设下它的操作和时间（核对人的用户名由传输层补上）。
type ClaimReviewDetail struct {
	Issue      string    `json:"issue"`
	ReviewedAt time.Time `json:"reviewed_at"`
	Reviewer   Reviewer  `json:"reviewer"`
}

// ClaimDetail 是一个旧版本的详情：列表项的全部内容，加上留着的修改明细、版本号与推荐。
type ClaimDetail struct {
	ClaimEntry
	Revision    int64               `json:"revision"`
	Corrections []ClaimCorrection   `json:"corrections"`
	Siblings    []ClaimAlbumSibling `json:"siblings"`
	DefaultOf   []ClaimAlbumRef     `json:"default_of"`
	Reviews     []ClaimReviewDetail `json:"reviews"`
	Suggestions []ClaimSuggestion   `json:"suggestions"`
}

// ClaimAlbumRef 是旧版本作为默认版本的一张专辑。
type ClaimAlbumRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// ReviewerIDs 返回详情里全部核对人的 ID，传输层用它批量补上用户名。
func (detail *ClaimDetail) ReviewerIDs() []string {
	ids := make([]string, 0, len(detail.Reviews))
	for _, review := range detail.Reviews {
		ids = append(ids, review.Reviewer.ID)
	}
	return ids
}

// SetReviewerNames 按 ID 补上核对人的用户名。
func (detail *ClaimDetail) SetReviewerNames(names map[string]string) {
	for index := range detail.Reviews {
		detail.Reviews[index].Reviewer.Username = names[detail.Reviews[index].Reviewer.ID]
	}
}

// LoadClaim 读取一个旧版本的详情；它不满足待认领的判定时返回 ErrReleaseNotFound。
func (s *Store) LoadClaim(ctx context.Context, releaseID string) (ClaimDetail, error) {
	var columns claimEntryColumns
	var revision int64
	err := s.database.QueryRowContext(ctx, `SELECT `+claimEntryProjection+`, releases.edit_revision`+claimEntrySource+` WHERE releases.id=$1::uuid AND `+releaseClaimablePredicate, releaseID).
		Scan(append(columns.targets(), &revision)...)
	if errors.Is(err, sql.ErrNoRows) {
		return ClaimDetail{}, ErrReleaseNotFound
	}
	if err != nil {
		return ClaimDetail{}, fmt.Errorf("%w: load claim: %w", ErrQuery, err)
	}
	entry, err := columns.finish(ctx, s.database)
	if err != nil {
		return ClaimDetail{}, err
	}
	detail := ClaimDetail{ClaimEntry: entry, Revision: revision,
		Corrections: []ClaimCorrection{}, Siblings: []ClaimAlbumSibling{}, DefaultOf: []ClaimAlbumRef{}, Reviews: []ClaimReviewDetail{}}

	intents, err := loadCorrectionIntents(ctx, s.database, releaseID)
	if err != nil {
		return ClaimDetail{}, err
	}
	scanned, err := loadFieldValues(ctx, s.database, "releases", releaseID)
	if err != nil {
		return ClaimDetail{}, err
	}
	for index, field := range correctableFields {
		if intent, ok := intents[field.name]; ok {
			detail.Corrections = append(detail.Corrections, ClaimCorrection{Field: field.name, Correction: intent, ScanValue: scanned[index]})
		}
	}
	siblings, err := s.database.QueryContext(ctx, `SELECT releases.id::text, releases.title, `+releaseVisiblePredicate+` FROM `+releaseSource+`
		WHERE releases.group_id=(SELECT group_id FROM releases WHERE id=$1::uuid) AND releases.id<>$1::uuid ORDER BY releases.created_at, releases.id LIMIT 20`, releaseID)
	if err != nil {
		return ClaimDetail{}, err
	}
	defer siblings.Close()
	for siblings.Next() {
		var sibling ClaimAlbumSibling
		if err := siblings.Scan(&sibling.ID, &sibling.Title, &sibling.Visible); err != nil {
			return ClaimDetail{}, err
		}
		detail.Siblings = append(detail.Siblings, sibling)
	}
	if err := siblings.Err(); err != nil {
		return ClaimDetail{}, err
	}
	siblings.Close()
	groups, err := s.database.QueryContext(ctx, `SELECT id::text FROM release_groups WHERE default_release_id=$1::uuid ORDER BY id`, releaseID)
	if err != nil {
		return ClaimDetail{}, err
	}
	defer groups.Close()
	var groupIDs []string
	for groups.Next() {
		var id string
		if err := groups.Scan(&id); err != nil {
			return ClaimDetail{}, err
		}
		groupIDs = append(groupIDs, id)
	}
	if err := groups.Err(); err != nil {
		return ClaimDetail{}, err
	}
	groups.Close()
	for _, id := range groupIDs {
		title, err := albumTitleOf(ctx, s.database, id, releaseID)
		if errors.Is(err, sql.ErrNoRows) {
			// 这张专辑里只有旧版本自己：没有别的名字可取，用它自己的有效标题。
			title, err = detail.Release.Title, nil
		}
		if err != nil {
			return ClaimDetail{}, err
		}
		detail.DefaultOf = append(detail.DefaultOf, ClaimAlbumRef{ID: id, Title: title})
	}
	if detail.Reviews, err = s.loadClaimReviews(ctx, releaseID); err != nil {
		return ClaimDetail{}, err
	}
	if detail.Suggestions, err = s.LoadClaimSuggestions(ctx, releaseID); err != nil {
		return ClaimDetail{}, err
	}
	return detail, nil
}

// loadClaimReviews 读取旧版本上的核对：问题类别、核对时间与核对人（经 operations 的公开读取）。
func (s *Store) loadClaimReviews(ctx context.Context, releaseID string) ([]ClaimReviewDetail, error) {
	reviews, err := s.loadIssueReviews(ctx, releaseID)
	if err != nil {
		return nil, err
	}
	result := make([]ClaimReviewDetail, 0, len(reviews))
	for _, issue := range claimReviewOrder {
		if info, ok := reviews[issue]; ok {
			result = append(result, ClaimReviewDetail{Issue: issue, ReviewedAt: info.ReviewedAt, Reviewer: info.Reviewer})
		}
	}
	return result, nil
}

// ClaimHint 是专辑详情里“可能接替了已不可见的《…》”的一个旧版本。
type ClaimHint struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// LoadClaimHints 返回把可见版本 releaseID 列为推荐的待认领旧版本，最多 maxClaimHints 个，最近看到的在前；没有时是空切片。
// 先按“待认领”过滤（这样的旧版本很少），再逐个检查 releaseID 是否命中它的路径集合或同一目录；不从 releaseID 出发反查整个库。
// 命中条件与推荐一致，但不受推荐 5 个的上限限制：排在后面的版本也算“可能接替”。releaseID 不存在或不可见时返回 ErrReleaseNotFound。
func (s *Store) LoadClaimHints(ctx context.Context, releaseID string) ([]ClaimHint, error) {
	var visible bool
	err := s.database.QueryRowContext(ctx, `SELECT `+releaseVisiblePredicate+` FROM releases WHERE releases.id = $1::uuid`, releaseID).Scan(&visible)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !visible) {
		return nil, ErrReleaseNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("%w: claim hints: %w", ErrQuery, err)
	}
	rows, err := s.database.QueryContext(ctx, `
		WITH target AS (
			SELECT releases.id, releases.source_root_id, releases.relative_directory FROM `+releaseSource+` WHERE releases.id = $1::uuid AND `+releaseVisiblePredicate+`
		)
		SELECT releases.id::text, releases.title FROM `+releaseSource+`, target
		WHERE `+releaseClaimablePredicate+`
		  AND (
			(releases.source_root_id = target.source_root_id AND releases.relative_directory = target.relative_directory)
			OR EXISTS (
				SELECT 1 FROM `+claimPathSetFor("releases.id")+` path_set
				JOIN tracks claim_track ON claim_track.source_root_id = path_set.root_id AND claim_track.relative_path = path_set.relative_path AND claim_track.source_identity IS NOT NULL
				JOIN media claim_medium ON claim_medium.id = claim_track.medium_id
				WHERE path_set.relative_path <> '' AND claim_medium.release_id = target.id AND claim_track.source_status = 'present'
			)
		  )
		ORDER BY `+claimLastSeen+` DESC, releases.id LIMIT $2`, releaseID, maxClaimHints)
	if err != nil {
		return nil, fmt.Errorf("%w: claim hints: %w", ErrQuery, err)
	}
	defer rows.Close()
	hints := []ClaimHint{}
	for rows.Next() {
		var hint ClaimHint
		if err := rows.Scan(&hint.ID, &hint.Title); err != nil {
			return nil, err
		}
		hints = append(hints, hint)
	}
	return hints, rows.Err()
}
