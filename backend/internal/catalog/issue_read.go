package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/qlqq/roomusic/backend/internal/operations"
)

// 问题的读取（阶段 M4 第二项，只有管理员）：问题列表按版本列出各类待处理与已核对的数目，版本的问题逐条给出原因、依据与核对状态。
// 数目都来自 issues.go 的常量，所以列表、详情与“需要关注”一致；可以核对的问题带依据令牌，核对时带回（issue_review.go）。

const (
	// maxIssueSiblings 是归组冲突详情里同一目录其他版本的上限。
	maxIssueSiblings = 20
	// maxIssueMissingTracks 是缺失曲目详情的上限；依据令牌始终覆盖全部缺失曲目。
	maxIssueMissingTracks = 1000
)

// 问题列表的状态与类别筛选。
const (
	IssueStatusOpen     = "open"
	IssueStatusReviewed = "reviewed"

	IssueCategoryFields        = "fields"
	IssueCategoryGrouping      = "grouping"
	IssueCategoryMissingTracks = "missing_tracks"
)

// IssueListParams 是问题列表的已校验参数：Status 是 open 或 reviewed，Category 为空表示全部类别，Page 与 PageSize 都大于 0。
type IssueListParams struct {
	Status, Category string
	Page, PageSize   int
}

// IssueCounts 是一个版本（或全库）三类问题的数目。
type IssueCounts struct {
	Fields        int64 `json:"fields"`
	Grouping      int64 `json:"grouping"`
	MissingTracks int64 `json:"missing_tracks"`
}

// IssueListItem 是问题列表的一行：版本摘要（attention_count 等于 Open 三项之和）、所在的组、版本说明，以及待处理与已核对的数目。
type IssueListItem struct {
	Release  ReleaseSummary `json:"release"`
	GroupID  string         `json:"group_id"`
	Edition  *string        `json:"edition"`
	Open     IssueCounts    `json:"open"`
	Reviewed IssueCounts    `json:"reviewed"`
}

// IssueSummaryCounts 是筛选项上的数字：有某类问题的版本数（All 是有任意一类的版本数）。
type IssueSummaryCounts struct {
	All           int64 `json:"all"`
	Fields        int64 `json:"fields"`
	Grouping      int64 `json:"grouping"`
	MissingTracks int64 `json:"missing_tracks"`
}

// IssueSummary 是两种状态下的筛选项数字；它不随类别筛选与分页变化。
type IssueSummary struct {
	Open     IssueSummaryCounts `json:"open"`
	Reviewed IssueSummaryCounts `json:"reviewed"`
}

// IssuePage 是一页问题列表、满足筛选的版本总数与筛选项数字；Items 永远非 nil。
type IssuePage struct {
	Items   []IssueListItem
	Total   int64
	Summary IssueSummary
}

// issueCountsCTE 为每个可见版本算出六个数目（三类待处理，再三类已核对），列表、总数与筛选项数字都从它读。
const issueCountsCTE = `issue_counts AS (
		SELECT releases.id,
			` + releaseOpenFieldIssues + ` AS open_fields,
			` + releaseOpenGroupingIssue + ` AS open_grouping,
			` + releaseOpenMissingTracks + ` AS open_missing,
			` + releaseReviewedFieldIssues + ` AS reviewed_fields,
			` + releaseReviewedGroupingIssue + ` AS reviewed_grouping,
			` + releaseReviewedMissingTracks + ` AS reviewed_missing
		FROM ` + releaseSource + ` WHERE ` + releaseVisiblePredicate + `
	)`

// issueFilter 返回状态与类别对应的条件（列来自 issue_counts）；参数已经校验过。
func issueFilter(status, category string) string {
	prefix := "open_"
	if status == IssueStatusReviewed {
		prefix = "reviewed_"
	}
	switch category {
	case IssueCategoryFields:
		return prefix + "fields > 0"
	case IssueCategoryGrouping:
		return prefix + "grouping > 0"
	case IssueCategoryMissingTracks:
		return prefix + "missing > 0"
	}
	return "(" + prefix + "fields + " + prefix + "grouping + " + prefix + "missing) > 0"
}

// ListIssues 返回有问题的可见版本的一页，按有效标题、艺人与 ID 排序（与专辑列表一致）。查询执行失败时错误包含 ErrQuery。
func (s *Store) ListIssues(ctx context.Context, params IssueListParams) (IssuePage, error) {
	page := IssuePage{Items: []IssueListItem{}}
	summaryQuery := `WITH ` + issueCountsCTE + ` SELECT
		COUNT(*) FILTER (WHERE ` + issueFilter(IssueStatusOpen, "") + `),
		COUNT(*) FILTER (WHERE ` + issueFilter(IssueStatusOpen, IssueCategoryFields) + `),
		COUNT(*) FILTER (WHERE ` + issueFilter(IssueStatusOpen, IssueCategoryGrouping) + `),
		COUNT(*) FILTER (WHERE ` + issueFilter(IssueStatusOpen, IssueCategoryMissingTracks) + `),
		COUNT(*) FILTER (WHERE ` + issueFilter(IssueStatusReviewed, "") + `),
		COUNT(*) FILTER (WHERE ` + issueFilter(IssueStatusReviewed, IssueCategoryFields) + `),
		COUNT(*) FILTER (WHERE ` + issueFilter(IssueStatusReviewed, IssueCategoryGrouping) + `),
		COUNT(*) FILTER (WHERE ` + issueFilter(IssueStatusReviewed, IssueCategoryMissingTracks) + `)
		FROM issue_counts`
	open, reviewed := &page.Summary.Open, &page.Summary.Reviewed
	if err := s.database.QueryRowContext(ctx, summaryQuery).Scan(&open.All, &open.Fields, &open.Grouping, &open.MissingTracks,
		&reviewed.All, &reviewed.Fields, &reviewed.Grouping, &reviewed.MissingTracks); err != nil {
		return IssuePage{}, fmt.Errorf("%w: count issues: %w", ErrQuery, err)
	}
	counts := page.Summary.Open
	if params.Status == IssueStatusReviewed {
		counts = page.Summary.Reviewed
	}
	switch params.Category {
	case IssueCategoryFields:
		page.Total = counts.Fields
	case IssueCategoryGrouping:
		page.Total = counts.Grouping
	case IssueCategoryMissingTracks:
		page.Total = counts.MissingTracks
	default:
		page.Total = counts.All
	}

	rows, err := s.database.QueryContext(ctx, `WITH `+issueCountsCTE+` SELECT `+releaseSummaryProjection+`,
			releases.group_id::text, NULLIF(BTRIM(releases.edition), ''),
			issue_counts.open_fields, issue_counts.open_grouping, issue_counts.open_missing,
			issue_counts.reviewed_fields, issue_counts.reviewed_grouping, issue_counts.reviewed_missing
		FROM issue_counts JOIN `+releaseSource+` ON releases.id = issue_counts.id`+releaseSummaryArtworkJoin+`
		WHERE `+issueFilter(params.Status, params.Category)+`
		ORDER BY LOWER(releases.title), LOWER(releases.artist), releases.id
		LIMIT $1 OFFSET $2`, params.PageSize, (params.Page-1)*params.PageSize)
	if err != nil {
		return IssuePage{}, fmt.Errorf("%w: list issues: %w", ErrQuery, err)
	}
	defer rows.Close()
	for rows.Next() {
		var columns releaseSummaryColumns
		var item IssueListItem
		var edition sql.NullString
		if err := rows.Scan(append(columns.targets(), &item.GroupID, &edition,
			&item.Open.Fields, &item.Open.Grouping, &item.Open.MissingTracks,
			&item.Reviewed.Fields, &item.Reviewed.Grouping, &item.Reviewed.MissingTracks)...); err != nil {
			return IssuePage{}, err
		}
		if item.Release, err = columns.summary(); err != nil {
			return IssuePage{}, err
		}
		item.Edition = stringPointerFromNull(edition)
		page.Items = append(page.Items, item)
	}
	return page, rows.Err()
}

// IssueReviewInfo 是一条核对行的展示信息：设下它的操作、时间与核对人（用户名由传输层补上）。
type IssueReviewInfo struct {
	OperationID string    `json:"operation_id"`
	ReviewedAt  time.Time `json:"reviewed_at"`
	Reviewer    Reviewer  `json:"reviewer"`
}

// Reviewer 是核对人；Username 由传输层按 ID 批量补上（用户不存在时为空）。
type Reviewer struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// FieldIssue 是一个拿不准的字段：有效值、原因、候选、能否核对（只有来源类型、介质类型），以及核对状态。
// 已修正的字段不再是问题，不列出；Basis 只在可以核对时给出。
type FieldIssue struct {
	Field      string           `json:"field"`
	Value      *string          `json:"value"`
	ReasonCode *string          `json:"reason_code"`
	Candidates []string         `json:"candidates"`
	Reviewable bool             `json:"reviewable"`
	Basis      *string          `json:"basis"`
	Status     string           `json:"status"`
	Review     *IssueReviewInfo `json:"review"`
}

// IssueSibling 是同一扫描目录里的另一个可见版本。
type IssueSibling struct {
	ReleaseID string `json:"release_id"`
	GroupID   string `json:"group_id"`
	Title     string `json:"title"`
}

// GroupingIssue 是同一目录的归组冲突：原因码、涉及的来源相对路径、同一目录的其他版本，以及依据令牌与核对状态。
type GroupingIssue struct {
	ReasonCodes []string         `json:"reason_codes"`
	SourceRefs  []string         `json:"source_refs"`
	Truncated   bool             `json:"truncated"`
	Siblings    []IssueSibling   `json:"siblings"`
	Basis       string           `json:"basis"`
	Status      string           `json:"status"`
	Review      *IssueReviewInfo `json:"review"`
}

// MissingTrack 是一首缺失的曲目：碟与曲序、标题、来源相对路径、最后一次被看到的时间，以及它是否被仍然有效的核对覆盖。
type MissingTrack struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	MediumPosition int       `json:"medium_position"`
	Position       int       `json:"position"`
	Source         string    `json:"source"`
	LastSeenAt     time.Time `json:"last_seen_at"`
	Reviewed       bool      `json:"reviewed"`
}

// MissingTracksIssue 是缺失的曲目：总数、最多 maxIssueMissingTracks 首的明细，以及依据令牌与核对状态。
type MissingTracksIssue struct {
	Count     int64            `json:"count"`
	Tracks    []MissingTrack   `json:"tracks"`
	Truncated bool             `json:"truncated"`
	Basis     string           `json:"basis"`
	Status    string           `json:"status"`
	Review    *IssueReviewInfo `json:"review"`
}

// ReleaseIssues 是一个可见版本的全部问题。Release 是版本摘要（页面的标题与“需要关注”），Edition 是版本说明；
// Fields 永远非 nil；没有归组冲突或缺失曲目时对应一项为 null。
// 状态 open 表示还有待处理的部分；有核对行（Review 非空）而状态是 open，就是“核对之后情况变了”。
type ReleaseIssues struct {
	ReleaseID     string              `json:"release_id"`
	GroupID       string              `json:"group_id"`
	Revision      int64               `json:"revision"`
	Release       ReleaseSummary      `json:"release"`
	Edition       *string             `json:"edition"`
	Fields        []FieldIssue        `json:"fields"`
	Grouping      *GroupingIssue      `json:"grouping"`
	MissingTracks *MissingTracksIssue `json:"missing_tracks"`
}

// ReviewerIDs 返回这些问题里全部核对人的 ID（可能重复），传输层用它批量补上用户名。
func (issues *ReleaseIssues) ReviewerIDs() []string {
	ids := []string{}
	for _, field := range issues.Fields {
		if field.Review != nil {
			ids = append(ids, field.Review.Reviewer.ID)
		}
	}
	if issues.Grouping != nil && issues.Grouping.Review != nil {
		ids = append(ids, issues.Grouping.Review.Reviewer.ID)
	}
	if issues.MissingTracks != nil && issues.MissingTracks.Review != nil {
		ids = append(ids, issues.MissingTracks.Review.Reviewer.ID)
	}
	return ids
}

// SetReviewerNames 按 ID 补上核对人的用户名（键是小写 UUID）。
func (issues *ReleaseIssues) SetReviewerNames(names map[string]string) {
	set := func(review *IssueReviewInfo) {
		if review != nil {
			review.Reviewer.Username = names[review.Reviewer.ID]
		}
	}
	for index := range issues.Fields {
		set(issues.Fields[index].Review)
	}
	if issues.Grouping != nil {
		set(issues.Grouping.Review)
	}
	if issues.MissingTracks != nil {
		set(issues.MissingTracks.Review)
	}
}

func issueStatus(open bool) string {
	if open {
		return IssueStatusOpen
	}
	return IssueStatusReviewed
}

// LoadReleaseIssues 读取一个可见版本的全部问题；不存在或不可见时返回 ErrReleaseNotFound。
func (s *Store) LoadReleaseIssues(ctx context.Context, releaseID string) (ReleaseIssues, error) {
	result := ReleaseIssues{ReleaseID: releaseID, Fields: []FieldIssue{}}
	var sourceRoot, directory, edition sql.NullString
	var columns releaseSummaryColumns
	err := s.database.QueryRowContext(ctx, `SELECT `+releaseSummaryProjection+`, releases.group_id::text, releases.edit_revision,
			releases.source_root_id::text, releases.relative_directory, NULLIF(BTRIM(releases.edition), '')
		FROM `+releaseSource+releaseSummaryArtworkJoin+` WHERE releases.id=$1::uuid AND `+releaseVisiblePredicate, releaseID).
		Scan(append(columns.targets(), &result.GroupID, &result.Revision, &sourceRoot, &directory, &edition)...)
	if errors.Is(err, sql.ErrNoRows) {
		return ReleaseIssues{}, ErrReleaseNotFound
	}
	if err != nil {
		return ReleaseIssues{}, fmt.Errorf("%w: load release: %w", ErrQuery, err)
	}
	if result.Release, err = columns.summary(); err != nil {
		return ReleaseIssues{}, err
	}
	result.Edition = stringPointerFromNull(edition)
	reviews, err := s.loadIssueReviews(ctx, releaseID)
	if err != nil {
		return ReleaseIssues{}, err
	}
	if err := s.loadFieldIssues(ctx, &result, reviews); err != nil {
		return ReleaseIssues{}, err
	}
	if err := s.loadGroupingIssue(ctx, &result, reviews, sourceRoot, directory); err != nil {
		return ReleaseIssues{}, err
	}
	if err := s.loadMissingTracksIssue(ctx, &result, reviews); err != nil {
		return ReleaseIssues{}, err
	}
	return result, nil
}

// loadIssueReviews 读取版本的核对行，经 operations 的公开读取补上核对时间与核对人；每个版本至多四行。
func (s *Store) loadIssueReviews(ctx context.Context, releaseID string) (map[string]*IssueReviewInfo, error) {
	rows, err := s.database.QueryContext(ctx, `SELECT issue, operation_id::text FROM release_issue_reviews WHERE release_id=$1::uuid`, releaseID)
	if err != nil {
		return nil, fmt.Errorf("%w: load reviews: %w", ErrQuery, err)
	}
	operationIDs := map[string]string{}
	for rows.Next() {
		var issue, operationID string
		if err := rows.Scan(&issue, &operationID); err != nil {
			rows.Close()
			return nil, err
		}
		operationIDs[issue] = operationID
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	reviews := make(map[string]*IssueReviewInfo, len(operationIDs))
	for issue, operationID := range operationIDs {
		record, err := operations.Load(ctx, s.database, operationID)
		if err != nil {
			return nil, err
		}
		reviews[issue] = &IssueReviewInfo{OperationID: record.ID, ReviewedAt: record.CreatedAt, Reviewer: Reviewer{ID: record.ActorID}}
	}
	return reviews, nil
}

// loadFieldIssues、loadGroupingIssue 与 loadMissingTracksIssue 在 issues.go 定义问题的同一批行上读取明细（LATERAL 子查询里
// releases 是这个版本），所以详情里待处理的条目与“需要关注”的计数一致。
func (s *Store) loadFieldIssues(ctx context.Context, result *ReleaseIssues, reviews map[string]*IssueReviewInfo) error {
	rows, err := s.database.QueryContext(ctx, `SELECT issue.field_key, issue.value, issue.reason, issue.candidates, issue.basis, issue.reviewed
		FROM releases JOIN LATERAL (
			SELECT decision.field_key, COALESCE(decision.selected_value #>> '{}', '') AS value, COALESCE(decision.reason, '') AS reason,
				COALESCE(decision.candidates::text, '[]') AS candidates, (`+fieldIssueBasis+`)::text AS basis, `+fieldReviewValid+` AS reviewed
			`+fieldIssueRows+`
		) issue ON TRUE
		WHERE releases.id = $1::uuid
		ORDER BY issue.field_key
		LIMIT $2`, result.ReleaseID, maxReleaseEvidence+1)
	if err != nil {
		return fmt.Errorf("%w: load field issues: %w", ErrQuery, err)
	}
	defer rows.Close()
	for rows.Next() {
		if len(result.Fields) == maxReleaseEvidence {
			return errors.New("release field issues exceed bound")
		}
		var issue FieldIssue
		var value, reason, candidates, basis string
		var reviewed bool
		if err := rows.Scan(&issue.Field, &value, &reason, &candidates, &basis, &reviewed); err != nil {
			return err
		}
		if bounded := boundedDisplayString(value); bounded != "" {
			issue.Value = &bounded
		}
		if bounded := boundedDisplayString(reason); bounded != "" {
			issue.ReasonCode = &bounded
		}
		if issue.Candidates, _, err = decodeBoundedStringArray(candidates, maxEvidenceCandidates, false); err != nil {
			return err
		}
		issue.Reviewable = issue.Field == IssueSourceType || issue.Field == IssueMediaType
		issue.Status = issueStatus(!reviewed)
		if issue.Reviewable {
			token := issueBasisToken(basis)
			issue.Basis = &token
			issue.Review = reviews[issue.Field]
		}
		result.Fields = append(result.Fields, issue)
	}
	return rows.Err()
}

func (s *Store) loadGroupingIssue(ctx context.Context, result *ReleaseIssues, reviews map[string]*IssueReviewInfo, sourceRoot, directory sql.NullString) error {
	var reason, sourceRefs, basis string
	var reviewed bool
	err := s.database.QueryRowContext(ctx, `SELECT issue.reason, issue.source_refs, issue.basis, issue.reviewed
		FROM releases JOIN LATERAL (
			SELECT grouping.reason, grouping.source_refs::text AS source_refs, (`+groupingIssueBasis+`)::text AS basis, `+groupingReviewValid+` AS reviewed
			`+groupingIssueRows+`
		) issue ON TRUE
		WHERE releases.id = $1::uuid`, result.ReleaseID).Scan(&reason, &sourceRefs, &basis, &reviewed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: load grouping issue: %w", ErrQuery, err)
	}
	issue := GroupingIssue{Siblings: []IssueSibling{}, Basis: issueBasisToken(basis), Status: issueStatus(!reviewed), Review: reviews[IssueGrouping]}
	var truncated bool
	if issue.SourceRefs, truncated, err = decodeBoundedStringArray(sourceRefs, maxEvidenceSourceRefs, true); err != nil {
		return err
	}
	issue.Truncated = truncated
	issue.ReasonCodes, truncated = splitBoundedReasonCodes(reason)
	issue.Truncated = issue.Truncated || truncated
	if sourceRoot.Valid && directory.Valid {
		rows, err := s.database.QueryContext(ctx, `SELECT releases.id::text, releases.group_id::text, releases.title FROM `+releaseSource+`
			WHERE releases.source_root_id = $1::uuid AND releases.relative_directory = $2 AND releases.id <> $3::uuid AND `+releaseVisiblePredicate+`
			ORDER BY LOWER(releases.title), LOWER(releases.artist), releases.id LIMIT $4`, sourceRoot.String, directory.String, result.ReleaseID, maxIssueSiblings+1)
		if err != nil {
			return fmt.Errorf("%w: load grouping siblings: %w", ErrQuery, err)
		}
		defer rows.Close()
		for rows.Next() {
			if len(issue.Siblings) == maxIssueSiblings {
				issue.Truncated = true
				break
			}
			var sibling IssueSibling
			if err := rows.Scan(&sibling.ReleaseID, &sibling.GroupID, &sibling.Title); err != nil {
				return err
			}
			issue.Siblings = append(issue.Siblings, sibling)
		}
		if err := rows.Err(); err != nil {
			return err
		}
	}
	result.Grouping = &issue
	return nil
}

func (s *Store) loadMissingTracksIssue(ctx context.Context, result *ReleaseIssues, reviews map[string]*IssueReviewInfo) error {
	basis, exists, err := loadIssueBasis(ctx, s.database, result.ReleaseID, IssueMissingTracks)
	if err != nil {
		return fmt.Errorf("%w: load missing tracks basis: %w", ErrQuery, err)
	}
	if !exists {
		return nil
	}
	issue := MissingTracksIssue{Tracks: []MissingTrack{}, Basis: issueBasisToken(basis), Review: reviews[IssueMissingTracks]}
	var open int64
	if err := s.database.QueryRowContext(ctx, `SELECT `+releaseOpenMissingTracks+` FROM releases WHERE releases.id = $1::uuid`, result.ReleaseID).Scan(&open); err != nil {
		return fmt.Errorf("%w: count open missing tracks: %w", ErrQuery, err)
	}
	issue.Status = issueStatus(open > 0)
	rows, err := s.database.QueryContext(ctx, `SELECT issue.id, issue.title, issue.medium_position, issue.position, issue.relative_path, issue.observed_at,
			issue.reviewed, COUNT(*) OVER ()
		FROM releases JOIN LATERAL (
			SELECT missing_track.id::text AS id, missing_track.title, missing_medium.position AS medium_position, missing_track.position,
				missing_track.relative_path, missing_track.observed_at, `+missingTrackReviewValid+` AS reviewed
			`+missingTrackRows+`
		) issue ON TRUE
		WHERE releases.id = $1::uuid
		ORDER BY issue.medium_position, issue.position, issue.id
		LIMIT $2`, result.ReleaseID, maxIssueMissingTracks)
	if err != nil {
		return fmt.Errorf("%w: load missing tracks: %w", ErrQuery, err)
	}
	defer rows.Close()
	for rows.Next() {
		var track MissingTrack
		var path string
		if err := rows.Scan(&track.ID, &track.Title, &track.MediumPosition, &track.Position, &path, &track.LastSeenAt, &track.Reviewed, &issue.Count); err != nil {
			return err
		}
		if safe, ok := SafeRelativeSourceRef(path); ok {
			track.Source = safe
		} else {
			track.Source = safeSourceLabel(path)
		}
		track.Title = boundedDisplayString(track.Title)
		issue.Tracks = append(issue.Tracks, track)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	issue.Truncated = issue.Count > int64(len(issue.Tracks))
	result.MissingTracks = &issue
	return nil
}

// ParseIssueListStatus 与 ParseIssueListCategory 校验问题列表的筛选：状态缺省为 open，类别缺省为全部；非法值返回 false。
func ParseIssueListStatus(values []string) (string, bool) {
	switch {
	case len(values) == 0:
		return IssueStatusOpen, true
	case len(values) == 1 && (values[0] == IssueStatusOpen || values[0] == IssueStatusReviewed):
		return values[0], true
	}
	return "", false
}

func ParseIssueListCategory(values []string) (string, bool) {
	switch {
	case len(values) == 0:
		return "", true
	case len(values) == 1 && (values[0] == IssueCategoryFields || values[0] == IssueCategoryGrouping || values[0] == IssueCategoryMissingTracks):
		return values[0], true
	}
	return "", false
}
