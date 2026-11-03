package governance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// 认领的预览与写入（阶段 M4 第三项）。管理员把已不可见的旧版本 S 上的人工修改一次迁给扫描新建的新版本 T：
// 字段修正、所在的专辑、默认版本与问题核对四类，新版本已有的以新版本为准（跳过，预览里列出旧值）。
//
// 预览和写入共用同一个纯函数 buildClaimPlan，按同一份状态快照得出每一项的去向，所以“预览写的就是执行的”是结构保证。
// 令牌是计划的决定性内容的指纹（不含展示用的标题），提交时在加锁之后重新读取状态、重新算计划，令牌不同就拒绝（claim_changed）。
// 加锁顺序：先对旧版本核对行引用的操作记录加 KEY SHARE，再按 UUID 顺序锁两个发行行，最后按 UUID 顺序锁涉及的组行。
// 这与归组、默认版本、核对的顺序一致（先发行、后组），扫描只更新发行行、从不锁组行，所以不会形成环形等待。

// 预览里一项的去向。
const (
	ClaimActionMove  = "move"  // 迁给新版本
	ClaimActionSkip  = "skip"  // 保留新版本已有的，或无法迁移；旧版本上的这一项随认领清除
	ClaimActionReset = "reset" // 默认版本：新版本没有进入那张专辑，那张专辑改回自动选择
)

// 预览里一项的类别。
const (
	ClaimKindCorrection = "correction"
	ClaimKindAlbum      = "album"
	ClaimKindDefault    = "default"
	ClaimKindReview     = "review"
)

// 迁移的核对在新版本上的效果。
const (
	ClaimEffectValid   = "valid"    // 新版本现在有同样的问题，依据相同，核对立即生效
	ClaimEffectStale   = "stale"    // 新版本现在有同类问题，但依据不同：核对不生效，问题会重新提醒
	ClaimEffectNoIssue = "no_issue" // 新版本现在没有这类问题：核对暂时无事可压，将来出现依据相同的问题时才生效
)

// 跳过的原因。
const (
	ClaimSkipTargetHasCorrection = "target_has_correction" // 新版本已有同一字段的修正
	ClaimSkipTargetHasReview     = "target_has_review"     // 新版本已核对过同一类问题
	ClaimSkipTargetInAlbum       = "target_in_album"       // 新版本已经在另一张多版本专辑里
	ClaimSkipAlreadyInAlbum      = "already_in_album"      // 新版本已经在那张专辑里，不用动
	ClaimSkipNotTransferable     = "not_transferable"      // 缺失曲目的核对：针对旧版本自己的曲目，不迁移
)

// claimedFieldOrder 是修正字段的固定顺序，预览与记录里字段按它排列。
var claimedFieldOrder = func() []string {
	names := make([]string, len(catalog.CorrectableFields))
	for index, field := range catalog.CorrectableFields {
		names[index] = field.Name
	}
	return names
}()

// claimReviewOrder 是核对类别的固定顺序。
var claimReviewOrder = []string{IssueSourceType, IssueMediaType, IssueGrouping, IssueMissingTracks}

// ClaimItem 是预览里的一项：类别、字段或问题、去向、跳过的原因，以及旧值与新版本现在的值。
// From 是旧版本上的状态，To 是新版本现在的状态（跳过时说明为什么保留它）；都是展示用的，不进令牌。
type ClaimItem struct {
	Kind   string `json:"kind"`
	Field  string `json:"field,omitempty"`
	Issue  string `json:"issue,omitempty"`
	Group  string `json:"group,omitempty"` // 专辑与默认版本项：那张专辑的 ID
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
	// Effect 只用于迁移的核对：迁过去之后在新版本上的效果。
	Effect string `json:"effect,omitempty"`
	From   any    `json:"from,omitempty"`
	To     any    `json:"to,omitempty"`
}

// claimReviewState 是一个版本上一类核对的快照：依据（JSONB 文本）与设下它的操作。
type claimReviewState struct {
	Basis       json.RawMessage `json:"basis"`
	OperationID string          `json:"operation_id"`
}

// claimSide 是认领中一个版本的人工状态快照：版本号、所在的组、有效标题、修正意图与核对。
type claimSide struct {
	ReleaseID   string                       `json:"release_id"`
	Revision    int64                        `json:"revision"`
	Title       string                       `json:"title"`
	GroupID     string                       `json:"group_id"`
	Corrections map[string]claimedCorrection `json:"corrections"`
	Reviews     map[string]claimReviewState  `json:"reviews"`
}

// claimedCorrection 是一个字段的修正意图和设下它的操作。
type claimedCorrection struct {
	Intent      catalog.CorrectionIntent `json:"intent"`
	OperationID string                   `json:"operation_id"`
}

// claimGroupState 是认领涉及的一个组的快照：版本号与默认版本（空表示自动选择）。
type claimGroupState struct {
	Revision         int64  `json:"revision"`
	DefaultReleaseID string `json:"default_release_id"`
}

// claimSnapshot 是计算计划所需的全部状态：两个版本、涉及的组，以及几个事实（来源的组里有没有别的发行、新版本所在的专辑有几个别的可见版本）。
type claimSnapshot struct {
	Source, Target claimSide
	Groups         map[string]claimGroupState
	// SourceGroupHasOthers 记录来源的组里有没有来源以外的发行；TargetOthersVisible 是新版本所在的组里新版本以外的可见版本数。
	SourceGroupHasOthers bool
	TargetOthersVisible  int
	// DefaultGroups 是以来源为默认版本的组 ID，升序。
	DefaultGroups []string
}

// claimPlan 是计划：逐项的去向，加上执行器需要的整体动作。
type claimPlan struct {
	Items []ClaimItem
	// MoveAlbum 为真时新版本归入来源原来的组；LeaveAlbum 为真时来源移进一个新建的空组。
	MoveAlbum  bool
	LeaveAlbum bool
	// AlbumGroup 是来源原来的组（有别的发行时）；TargetFinalGroup 是认领之后新版本所在的组。
	AlbumGroup, TargetFinalGroup string
}

// buildClaimPlan 由状态快照得出计划，是预览与执行器共用的唯一一套判断。
func buildClaimPlan(snapshot claimSnapshot) claimPlan {
	plan := claimPlan{Items: []ClaimItem{}, AlbumGroup: snapshot.Source.GroupID, TargetFinalGroup: snapshot.Target.GroupID}

	for _, field := range claimedFieldOrder {
		old, hasOld := snapshot.Source.Corrections[field]
		if !hasOld {
			continue
		}
		item := ClaimItem{Kind: ClaimKindCorrection, Field: field, From: old.Intent}
		if current, hasNew := snapshot.Target.Corrections[field]; hasNew {
			item.Action, item.Reason, item.To = ClaimActionSkip, ClaimSkipTargetHasCorrection, current.Intent
		} else {
			item.Action = ClaimActionMove
		}
		plan.Items = append(plan.Items, item)
	}

	// 专辑：来源的组里有别的发行时才有这一项。
	if snapshot.SourceGroupHasOthers {
		plan.LeaveAlbum = true
		item := ClaimItem{Kind: ClaimKindAlbum, Group: snapshot.Source.GroupID}
		switch {
		case snapshot.Target.GroupID == snapshot.Source.GroupID:
			item.Action, item.Reason = ClaimActionSkip, ClaimSkipAlreadyInAlbum
		case snapshot.TargetOthersVisible > 0:
			item.Action, item.Reason = ClaimActionSkip, ClaimSkipTargetInAlbum
		default:
			item.Action = ClaimActionMove
			plan.MoveAlbum = true
			plan.TargetFinalGroup = snapshot.Source.GroupID
		}
		plan.Items = append(plan.Items, item)
	}

	// 默认版本：每个以来源为默认版本的组；新版本在认领之后属于那个组就接任，否则改回自动选择。
	for _, group := range snapshot.DefaultGroups {
		item := ClaimItem{Kind: ClaimKindDefault, Group: group}
		if plan.TargetFinalGroup == group {
			item.Action = ClaimActionMove
		} else {
			item.Action = ClaimActionReset
		}
		plan.Items = append(plan.Items, item)
	}

	for _, issue := range claimReviewOrder {
		if !hasReview(snapshot.Source.Reviews, issue) {
			continue
		}
		item := ClaimItem{Kind: ClaimKindReview, Issue: issue}
		switch {
		case issue == IssueMissingTracks:
			item.Action, item.Reason = ClaimActionSkip, ClaimSkipNotTransferable
		case hasReview(snapshot.Target.Reviews, issue):
			item.Action, item.Reason = ClaimActionSkip, ClaimSkipTargetHasReview
		default:
			item.Action = ClaimActionMove
		}
		plan.Items = append(plan.Items, item)
	}
	return plan
}

func hasReview(reviews map[string]claimReviewState, issue string) bool {
	_, ok := reviews[issue]
	return ok
}

// token 是计划的指纹：来源与目标的 ID，以及每一项的去向（类别、字段或问题、专辑、去向、原因）。不含标题等展示字段，改名不会让令牌失效。
// 两个发行的修正与核对只能被受治理写入改变，每次改变都让发行的人工修改版本号加一，版本号另行核对（revision_conflict），所以不必进令牌。
// 令牌要拦住的是不改变这两个版本号的变化：扫描改变了成员关系（来源的组里是否还有别的发行、新版本所在的专辑有几个别的可见版本），
// 以及别人改了以来源为默认版本的专辑（只让专辑的版本号加一）；这些都体现在计划各项的去向里。
// 扫描对已有发行不改 group_id，人工归组会改发行的版本号，所以双方所在的组不需要另外进令牌。
func (snapshot claimSnapshot) token(plan claimPlan) string {
	type tokenItem struct{ Kind, Field, Issue, Group, Action, Reason string }
	items := make([]tokenItem, len(plan.Items))
	for index, item := range plan.Items {
		items[index] = tokenItem{item.Kind, item.Field, item.Issue, item.Group, item.Action, item.Reason}
	}
	return operations.Fingerprint(map[string]any{
		"source": snapshot.Source.ReleaseID, "target": snapshot.Target.ReleaseID, "items": items,
	})
}

// loadClaimSide 读取一个版本的人工状态快照（修正与核对）；forUpdate 时对核对行加 FOR UPDATE（调用方已锁住发行行，只是保险）。
func loadClaimSide(ctx context.Context, database operations.Queryer, releaseID string, forUpdate bool) (claimSide, error) {
	side := claimSide{ReleaseID: releaseID, Corrections: map[string]claimedCorrection{}, Reviews: map[string]claimReviewState{}}
	if err := database.QueryRowContext(ctx, `SELECT releases.title, releases.group_id::text, releases.edit_revision FROM `+releasesql.ReleaseSource+` WHERE releases.id=$1::uuid`, releaseID).
		Scan(&side.Title, &side.GroupID, &side.Revision); err != nil {
		return claimSide{}, err
	}
	rows, err := database.QueryContext(ctx, `SELECT field, mode, text_value, year_value, operation_id::text FROM release_corrections WHERE release_id=$1::uuid`, releaseID)
	if err != nil {
		return claimSide{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, mode, operationID string
		var text sql.NullString
		var year sql.NullInt64
		if err := rows.Scan(&name, &mode, &text, &year, &operationID); err != nil {
			return claimSide{}, err
		}
		intent := catalog.CorrectionIntent{Mode: mode}
		switch {
		case mode != catalog.CorrectionSet:
		case year.Valid:
			intent.Value = int(year.Int64)
		default:
			intent.Value = text.String
		}
		side.Corrections[name] = claimedCorrection{Intent: intent, OperationID: operationID}
	}
	if err := rows.Err(); err != nil {
		return claimSide{}, err
	}
	rows.Close()
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE"
	}
	reviewRows, err := database.QueryContext(ctx, `SELECT issue, basis::text, operation_id::text FROM release_issue_reviews WHERE release_id=$1::uuid`+lock, releaseID)
	if err != nil {
		return claimSide{}, err
	}
	defer reviewRows.Close()
	for reviewRows.Next() {
		var issue, basis, operationID string
		if err := reviewRows.Scan(&issue, &basis, &operationID); err != nil {
			return claimSide{}, err
		}
		side.Reviews[issue] = claimReviewState{Basis: json.RawMessage(basis), OperationID: operationID}
	}
	return side, reviewRows.Err()
}

// loadClaimSnapshot 读取计划所需的全部状态。目标不存在或不可见返回 ErrReleaseNotFound；来源不存在返回 ErrReleaseNotFound，
// 来源不满足待认领的判定返回 errNotClaimable。调用方负责加锁（执行器）或不加锁（预览）。
func loadClaimSnapshot(ctx context.Context, database operations.Queryer, sourceID, targetID string, forUpdate bool) (claimSnapshot, error) {
	snapshot := claimSnapshot{Groups: map[string]claimGroupState{}}
	var targetVisible bool
	err := database.QueryRowContext(ctx, `SELECT `+releasesql.ReleaseVisiblePredicate+` FROM releases WHERE releases.id=$1::uuid`, targetID).Scan(&targetVisible)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !targetVisible) {
		return claimSnapshot{}, catalog.ErrReleaseNotFound
	}
	if err != nil {
		return claimSnapshot{}, err
	}
	var claimable bool
	err = database.QueryRowContext(ctx, `SELECT `+releaseClaimablePredicate+` FROM `+releasesql.ReleaseSource+` WHERE releases.id=$1::uuid`, sourceID).Scan(&claimable)
	if errors.Is(err, sql.ErrNoRows) {
		return claimSnapshot{}, catalog.ErrReleaseNotFound
	}
	if err != nil {
		return claimSnapshot{}, err
	}
	if !claimable {
		return claimSnapshot{}, errNotClaimable
	}
	if snapshot.Source, err = loadClaimSide(ctx, database, sourceID, forUpdate); err != nil {
		return claimSnapshot{}, err
	}
	if snapshot.Target, err = loadClaimSide(ctx, database, targetID, forUpdate); err != nil {
		return claimSnapshot{}, err
	}

	if err := database.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM releases WHERE group_id=$1::uuid AND id<>$2::uuid)`, snapshot.Source.GroupID, sourceID).
		Scan(&snapshot.SourceGroupHasOthers); err != nil {
		return claimSnapshot{}, err
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+releasesql.ReleaseSource+` WHERE releases.group_id=$1::uuid AND releases.id<>$2::uuid AND `+releasesql.ReleaseVisiblePredicate,
		snapshot.Target.GroupID, targetID).Scan(&snapshot.TargetOthersVisible); err != nil {
		return claimSnapshot{}, err
	}

	groupIDs := map[string]bool{snapshot.Source.GroupID: true, snapshot.Target.GroupID: true}
	rows, err := database.QueryContext(ctx, `SELECT id::text FROM release_groups WHERE default_release_id=$1::uuid ORDER BY id`, sourceID)
	if err != nil {
		return claimSnapshot{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return claimSnapshot{}, err
		}
		snapshot.DefaultGroups = append(snapshot.DefaultGroups, id)
		groupIDs[id] = true
	}
	if err := rows.Err(); err != nil {
		return claimSnapshot{}, err
	}
	rows.Close()
	for id := range groupIDs {
		var state claimGroupState
		var stored sql.NullString
		if err := database.QueryRowContext(ctx, `SELECT edit_revision, default_release_id::text FROM release_groups WHERE id=$1::uuid`, id).Scan(&state.Revision, &stored); err != nil {
			return claimSnapshot{}, err
		}
		state.DefaultReleaseID = stored.String
		snapshot.Groups[id] = state
	}
	sort.Strings(snapshot.DefaultGroups)
	return snapshot, nil
}

var errNotClaimable = operations.Conflict("not_claimable", "这个版本现在不需要认领：它可见，或已经没有人工修改")

// errClaimChanged 表示提交时的状态与页面预览的不同（页面打开之后又扫描或修改过）。
func errClaimChanged() error {
	return operations.Conflict("claim_changed", "两个版本的状态在预览之后变了，请重新查看预览")
}

// ClaimPreview 是预览：新版本、旧版本、两个版本号、逐项去向与令牌。
type ClaimPreview struct {
	Release        ClaimPreviewRelease `json:"release"`
	Source         ClaimPreviewRelease `json:"source"`
	Revision       int64               `json:"revision"`
	SourceRevision int64               `json:"source_revision"`
	Items          []ClaimItem         `json:"items"`
	Token          string              `json:"token"`
}

// ClaimPreviewRelease 是预览里一个版本的信息：ID、有效标题、所在的组与是否可见。
type ClaimPreviewRelease struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	GroupID string `json:"group_id"`
	Visible bool   `json:"visible"`
}

// PreviewClaim 不加锁地读取并计算预览；页面打开到提交之间状态变了，由令牌与版本号拦下。
// 新版本不可见或来源不存在返回 ErrReleaseNotFound；来源不满足待认领的判定返回 not_claimable；新旧相同由调用方在入口拒绝。
func (s *Store) PreviewClaim(ctx context.Context, sourceID, targetID string) (ClaimPreview, error) {
	snapshot, err := loadClaimSnapshot(ctx, s.database, sourceID, targetID, false)
	if err != nil {
		var failure *operations.Failure
		if errors.Is(err, catalog.ErrReleaseNotFound) || errors.As(err, &failure) {
			return ClaimPreview{}, err
		}
		return ClaimPreview{}, fmt.Errorf("%w: preview claim: %w", catalog.ErrQuery, err)
	}
	plan := buildClaimPlan(snapshot)
	annotateClaimItems(ctx, s.database, snapshot, &plan)
	return ClaimPreview{
		Release:  ClaimPreviewRelease{ID: targetID, Title: snapshot.Target.Title, GroupID: snapshot.Target.GroupID, Visible: true},
		Source:   ClaimPreviewRelease{ID: sourceID, Title: snapshot.Source.Title, GroupID: snapshot.Source.GroupID, Visible: false},
		Revision: snapshot.Target.Revision, SourceRevision: snapshot.Source.Revision,
		Items: plan.Items, Token: snapshot.token(plan),
	}, nil
}

// annotateClaimItems 给迁移的核对项补上迁移之后在新版本上的效果：新版本现在没有这类问题是 no_issue；有，依据与旧依据相同是 valid，
// 不同是 stale（问题会重新提醒）。这是展示信息，不进令牌。
func annotateClaimItems(ctx context.Context, database operations.Queryer, snapshot claimSnapshot, plan *claimPlan) {
	for index := range plan.Items {
		item := &plan.Items[index]
		if item.Kind != ClaimKindReview || item.Action != ClaimActionMove {
			continue
		}
		old := snapshot.Source.Reviews[item.Issue]
		current, exists, err := loadIssueBasis(ctx, database, snapshot.Target.ReleaseID, item.Issue)
		switch {
		case err != nil || !exists:
			item.Effect = ClaimEffectNoIssue
		case issueBasisToken(current) == issueBasisToken(string(old.Basis)):
			item.Effect = ClaimEffectValid
		default:
			item.Effect = ClaimEffectStale
		}
	}
}

// ClaimRequest 是一次已校验的认领请求：ReleaseID 是新版本，SourceID 是旧版本（都是小写 UUID），两个版本号与预览令牌。
type ClaimRequest struct {
	ReleaseID        string
	ExpectedRevision int64
	SourceID         string
	SourceRevision   int64
	Preview          string
}

// Fingerprint 返回请求的规范化指纹：含操作类型、两个版本的 ID 与版本号和预览令牌。
func (request ClaimRequest) Fingerprint() string {
	return operations.Fingerprint(map[string]any{
		"type": operations.TypeReleaseClaim, "release_id": strings.ToLower(request.ReleaseID),
		"expected_revision": request.ExpectedRevision, "source_release_id": strings.ToLower(request.SourceID),
		"source_revision": request.SourceRevision, "preview": request.Preview,
	})
}

// ClaimResult 是认领成功的响应。GroupID 与 PreviousGroupID 是新版本认领之后与认领之前所在的组；Moved 与 Skipped 列出各类的去向。
type ClaimResult struct {
	ReleaseID       string      `json:"release_id"`
	SourceReleaseID string      `json:"source_release_id"`
	GroupID         string      `json:"group_id"`
	PreviousGroupID string      `json:"previous_group_id"`
	Revision        int64       `json:"revision"`
	SourceRevision  int64       `json:"source_revision"`
	OperationID     string      `json:"operation_id"`
	Moved           ClaimMoved  `json:"moved"`
	Skipped         ClaimMoved  `json:"skipped"`
	Defaults        ClaimGroups `json:"defaults"`
}

// ClaimMoved 是某一类去向（迁移或跳过）的项：修正的字段、是否涉及专辑、核对的问题。
type ClaimMoved struct {
	Corrections []string `json:"corrections"`
	Album       bool     `json:"album"`
	Reviews     []string `json:"reviews"`
}

// ClaimGroups 是默认版本的去向：新版本接任的专辑，以及改回自动选择的专辑。
type ClaimGroups struct {
	Moved []string `json:"moved"`
	Reset []string `json:"reset"`
}

// claimRecordState 是操作记录里的类型化状态（前态与后态同形状）：来源、目标与涉及的组。撤销以前态写回，以后态核对。
type claimRecordState struct {
	Source claimSide                  `json:"source"`
	Target claimSide                  `json:"target"`
	Groups map[string]claimGroupState `json:"groups"`
}

// ClaimExecutor 返回认领的执行器，由 operations.Run 在事务里调用。检查顺序：先对来源核对行引用的操作记录加 KEY SHARE，
// 再按 UUID 顺序锁两个发行行（不存在或新版本不可见是 404），然后锁涉及的组行；来源可见或已无人工修改是 409 not_claimable，
// 任一版本号不等是 409 revision_conflict，重新算出的令牌与请求不同是 409 claim_changed；最后按计划写入，两个发行的版本号各加一。
func ClaimExecutor(request ClaimRequest) operations.Executor {
	return func(ctx context.Context, tx *sql.Tx, operationID string) (operations.Outcome, error) {
		sourceID, targetID := strings.ToLower(request.SourceID), strings.ToLower(request.ReleaseID)
		if err := lockReviewOperations(ctx, tx, sourceID); err != nil {
			return operations.Outcome{}, err
		}
		firstID, secondID := sourceID, targetID
		if secondID < firstID {
			firstID, secondID = secondID, firstID
		}
		_, firstVisible, firstExists, err := lockReleaseRow(ctx, tx, firstID)
		if err != nil {
			return operations.Outcome{}, err
		}
		_, secondVisible, secondExists, err := lockReleaseRow(ctx, tx, secondID)
		if err != nil {
			return operations.Outcome{}, err
		}
		exists := map[string]bool{firstID: firstExists, secondID: secondExists}
		visible := map[string]bool{firstID: firstVisible, secondID: secondVisible}
		if !exists[sourceID] || !exists[targetID] || !visible[targetID] {
			return operations.Outcome{}, operations.NotFound("not_found", "发行版本不存在")
		}
		if err := lockClaimGroups(ctx, tx, sourceID, targetID); err != nil {
			return operations.Outcome{}, err
		}

		snapshot, err := loadClaimSnapshot(ctx, tx, sourceID, targetID, true)
		if errors.Is(err, catalog.ErrReleaseNotFound) {
			return operations.Outcome{}, operations.NotFound("not_found", "发行版本不存在")
		}
		if err != nil {
			return operations.Outcome{}, err
		}
		if snapshot.Target.Revision != request.ExpectedRevision || snapshot.Source.Revision != request.SourceRevision {
			return operations.Outcome{}, errRevisionConflict()
		}
		plan := buildClaimPlan(snapshot)
		if snapshot.token(plan) != request.Preview {
			return operations.Outcome{}, errClaimChanged()
		}
		return executeClaimPlan(ctx, tx, operationID, snapshot, plan)
	}
}

// lockReviewOperations 对来源核对行引用的操作记录加 FOR KEY SHARE。核对行迁到新版本时保留原来的 operation_id（核对人与时间不变），
// 提交时的外键检查会加同一种锁；而撤销那条更早的记录会先 FOR UPDATE 锁住它、再锁发行行。不先加这把锁就会死锁（核对合同第 5 节）。
func lockReviewOperations(ctx context.Context, tx *sql.Tx, sourceID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT operations.id::text FROM operations
		WHERE operations.id IN (SELECT operation_id FROM release_issue_reviews WHERE release_id=$1::uuid)
		ORDER BY operations.id FOR KEY SHARE`, sourceID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
	}
	return rows.Err()
}

// lockClaimGroups 按 UUID 顺序锁住认领涉及的组行：新版本现在的组、来源现在的组，以及以来源为默认版本的组。
func lockClaimGroups(ctx context.Context, tx *sql.Tx, sourceID, targetID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id::text FROM release_groups WHERE id IN (
			SELECT group_id FROM releases WHERE id IN ($1::uuid, $2::uuid)
			UNION SELECT id FROM release_groups WHERE default_release_id = $1::uuid
		) ORDER BY id FOR UPDATE`, sourceID, targetID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
	}
	return rows.Err()
}

// executeClaimPlan 按计划写入，并返回操作记录与响应。
func executeClaimPlan(ctx context.Context, tx *sql.Tx, operationID string, snapshot claimSnapshot, plan claimPlan) (operations.Outcome, error) {
	sourceID, targetID := snapshot.Source.ReleaseID, snapshot.Target.ReleaseID
	result := ClaimResult{
		ReleaseID: targetID, SourceReleaseID: sourceID, PreviousGroupID: snapshot.Target.GroupID,
		Moved:    ClaimMoved{Corrections: []string{}, Reviews: []string{}},
		Skipped:  ClaimMoved{Corrections: []string{}, Reviews: []string{}},
		Defaults: ClaimGroups{Moved: []string{}, Reset: []string{}},
	}

	// 修正：新版本没有的复制过去（operation_id 写本次操作），然后清除旧版本的全部修正。
	for _, item := range plan.Items {
		switch item.Kind {
		case ClaimKindCorrection:
			if item.Action == ClaimActionMove {
				field, _ := correctableFieldByName(item.Field)
				intent := snapshot.Source.Corrections[item.Field].Intent
				if err := writeIntent(ctx, tx, targetID, field, &intent, operationID); err != nil {
					return operations.Outcome{}, err
				}
				result.Moved.Corrections = append(result.Moved.Corrections, item.Field)
			} else {
				result.Skipped.Corrections = append(result.Skipped.Corrections, item.Field)
			}
		case ClaimKindReview:
			if item.Action == ClaimActionMove {
				review := snapshot.Source.Reviews[item.Issue]
				if _, err := tx.ExecContext(ctx, `INSERT INTO release_issue_reviews(release_id,issue,basis,operation_id) VALUES($1::uuid,$2,$3::jsonb,$4::uuid)`,
					targetID, item.Issue, string(review.Basis), review.OperationID); err != nil {
					return operations.Outcome{}, err
				}
				result.Moved.Reviews = append(result.Moved.Reviews, item.Issue)
			} else {
				result.Skipped.Reviews = append(result.Skipped.Reviews, item.Issue)
			}
		case ClaimKindAlbum:
			if item.Action == ClaimActionMove {
				result.Moved.Album = true
			} else {
				result.Skipped.Album = true
			}
		case ClaimKindDefault:
			if item.Action == ClaimActionMove {
				result.Defaults.Moved = append(result.Defaults.Moved, item.Group)
			} else {
				result.Defaults.Reset = append(result.Defaults.Reset, item.Group)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM release_corrections WHERE release_id=$1::uuid`, sourceID); err != nil {
		return operations.Outcome{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM release_issue_reviews WHERE release_id=$1::uuid`, sourceID); err != nil {
		return operations.Outcome{}, err
	}

	// 专辑：新版本归入来源原来的组；来源离开原来的组，移进一个新建的空组（这样它才不再满足待认领的判定）。
	if plan.MoveAlbum {
		if _, err := tx.ExecContext(ctx, `UPDATE releases SET group_id=$2::uuid WHERE id=$1::uuid`, targetID, plan.AlbumGroup); err != nil {
			return operations.Outcome{}, err
		}
	}
	if plan.LeaveAlbum {
		parked := uuid.New()
		if _, err := tx.ExecContext(ctx, `INSERT INTO release_groups(id) VALUES($1::uuid)`, parked); err != nil {
			return operations.Outcome{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE releases SET group_id=$2::uuid WHERE id=$1::uuid`, sourceID, parked); err != nil {
			return operations.Outcome{}, err
		}
	}

	// 默认版本：新版本接任，或改回自动选择；组的版本号各加一。
	for _, item := range plan.Items {
		if item.Kind != ClaimKindDefault {
			continue
		}
		next := ""
		if item.Action == ClaimActionMove {
			next = targetID
		}
		if err := writeGroupDefault(ctx, tx, item.Group, next); err != nil {
			return operations.Outcome{}, err
		}
	}
	for _, id := range []string{sourceID, targetID} {
		if _, err := tx.ExecContext(ctx, `UPDATE releases SET edit_revision = edit_revision + 1 WHERE id=$1::uuid`, id); err != nil {
			return operations.Outcome{}, err
		}
	}

	after, err := loadClaimRecordState(ctx, tx, snapshot)
	if err != nil {
		return operations.Outcome{}, err
	}
	result.GroupID = after.Target.GroupID
	result.Revision, result.SourceRevision = snapshot.Target.Revision+1, snapshot.Source.Revision+1
	result.OperationID = operationID
	return operations.Outcome{
		TargetType: operations.TargetRelease, TargetID: targetID,
		ExpectedRevision: snapshot.Target.Revision, ResultRevision: snapshot.Target.Revision + 1,
		Before: snapshotRecordState(snapshot), After: after,
		Response: result,
	}, nil
}

// snapshotRecordState 把执行前的快照写成记录里的前态。
func snapshotRecordState(snapshot claimSnapshot) claimRecordState {
	return claimRecordState{Source: snapshot.Source, Target: snapshot.Target, Groups: snapshot.Groups}
}

// loadClaimRecordState 读取执行之后两个版本与涉及的组的状态（记录里的后态）。组的集合与前态相同，再加上来源新建的空组。
func loadClaimRecordState(ctx context.Context, tx *sql.Tx, snapshot claimSnapshot) (claimRecordState, error) {
	source, err := loadClaimSide(ctx, tx, snapshot.Source.ReleaseID, false)
	if err != nil {
		return claimRecordState{}, err
	}
	target, err := loadClaimSide(ctx, tx, snapshot.Target.ReleaseID, false)
	if err != nil {
		return claimRecordState{}, err
	}
	groups := map[string]claimGroupState{}
	ids := map[string]bool{source.GroupID: true, target.GroupID: true}
	for id := range snapshot.Groups {
		ids[id] = true
	}
	for id := range ids {
		var state claimGroupState
		var stored sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT edit_revision, default_release_id::text FROM release_groups WHERE id=$1::uuid`, id).Scan(&state.Revision, &stored); err != nil {
			return claimRecordState{}, err
		}
		state.DefaultReleaseID = stored.String
		groups[id] = state
	}
	return claimRecordState{Source: source, Target: target, Groups: groups}, nil
}

// correctableFieldByName 按名字找可修正字段。
func correctableFieldByName(name string) (catalog.CorrectableField, bool) {
	for _, field := range catalog.CorrectableFields {
		if field.Name == name {
			return field, true
		}
	}
	return catalog.CorrectableField{}, false
}
