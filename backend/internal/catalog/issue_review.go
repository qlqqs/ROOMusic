package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/operations"
)

// 核对问题（阶段 M4 第二项）。管理员把来源类型、介质类型、归组冲突或缺失曲目标记为“已核对”，或取消核对；
// 核对行记住当时的依据（issues.go），依据之后变了问题就重新计入“需要关注”。写入只经 operations.Run 的共同流程，
// 与修正、归组共用发行的人工修改版本号，只锁发行行。

// 可以核对的四类问题，也是 release_issue_reviews.issue 允许的取值。
const (
	IssueGrouping      = "grouping"
	IssueMissingTracks = "missing_tracks"
	IssueSourceType    = "source_type"
	IssueMediaType     = "media_type"
)

// 核对请求的两个动作。
const (
	IssueReview   = "review"
	IssueUnreview = "unreview"
)

// ReviewableIssue 报告 issue 是否是可以核对的问题。
func ReviewableIssue(issue string) bool {
	switch issue {
	case IssueGrouping, IssueMissingTracks, IssueSourceType, IssueMediaType:
		return true
	}
	return false
}

// currentIssueBasisQuery 返回一类问题现在的依据的 JSONB 文本输出（参数 $1 是发行 ID）；问题现在不存在时没有行。
// 缺失曲目的依据覆盖全部缺失曲目。
func currentIssueBasisQuery(issue string) string {
	switch issue {
	case IssueGrouping:
		return `SELECT (` + groupingIssueBasis + `)::text FROM release_grouping_evidence grouping
			WHERE grouping.release_id = $1::uuid AND NULLIF(BTRIM(grouping.reason), '') IS NOT NULL`
	case IssueMissingTracks:
		return `SELECT jsonb_build_object('tracks', jsonb_object_agg(missing_track.id::text, ` + missingTrackSeenAt + `))::text
			FROM tracks missing_track JOIN media missing_medium ON missing_medium.id = missing_track.medium_id
			WHERE missing_medium.release_id = $1::uuid AND missing_track.source_status = 'missing'
			HAVING COUNT(*) > 0`
	default:
		return `SELECT (` + fieldIssueBasis + `)::text FROM release_field_decisions decision
			WHERE decision.release_id = $1::uuid AND decision.field_key = $2 AND decision.action = 'uncertain_apply'`
	}
}

// loadIssueBasis 读取一类问题现在的依据（JSONB 的文本输出）；问题现在不存在时 exists 为 false。依据令牌由它算出（issueBasisToken）。
func loadIssueBasis(ctx context.Context, database operations.Queryer, releaseID, issue string) (basis string, exists bool, err error) {
	arguments := []any{releaseID}
	if issue == IssueSourceType || issue == IssueMediaType {
		arguments = append(arguments, issue)
	}
	err = database.QueryRowContext(ctx, currentIssueBasisQuery(issue), arguments...).Scan(&basis)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return basis, err == nil, err
}

// LoadIssueBasisToken 返回一类问题现在的依据令牌；问题现在不存在时为空。
func (s *Store) LoadIssueBasisToken(ctx context.Context, releaseID, issue string) (string, error) {
	basis, exists, err := loadIssueBasis(ctx, s.database, releaseID, issue)
	if err != nil || !exists {
		return "", err
	}
	return issueBasisToken(basis), nil
}

// issueBasisToken 是依据的令牌：JSONB 文本输出的 SHA-256。PostgreSQL 对同一个 JSONB 值的文本输出唯一（键排序、去重），
// 所以依据相等当且仅当令牌相等。读取时把它交给页面，核对时带回，执行器锁住发行之后重新计算。
func issueBasisToken(basis string) string {
	digest := sha256.Sum256([]byte(basis))
	return hex.EncodeToString(digest[:])
}

// issueReviewOpenQuery 判断一类问题现在是否还有没被核对压下的部分（参数 $1 是发行 ID）：issues.go 的待处理数目大于 0。
func issueReviewOpenQuery(issue string) string {
	open := releaseOpenGroupingIssue
	switch issue {
	case IssueMissingTracks:
		open = releaseOpenMissingTracks
	case IssueSourceType, IssueMediaType:
		open = `(SELECT COUNT(*) ` + fieldIssueRows + ` AND decision.field_key = $2 AND NOT ` + fieldReviewValid + `)`
	}
	return `SELECT ` + open + ` > 0 FROM releases WHERE releases.id = $1::uuid`
}

// IssueReviewRequest 是一次已校验的核对请求：Action 是 review 或 unreview，review 时 Basis 是页面读到的依据令牌。
type IssueReviewRequest struct {
	ReleaseID        string // 小写 UUID
	ExpectedRevision int64
	Issue            string
	Action           string
	Basis            string // 只有 review 有
}

// Fingerprint 返回请求的规范化指纹：含操作类型、小写的发行 ID、预期版本、问题、动作与依据令牌。
func (request IssueReviewRequest) Fingerprint() string {
	return operations.Fingerprint(map[string]any{
		"type": operations.TypeReleaseIssueReview, "release_id": strings.ToLower(request.ReleaseID),
		"expected_revision": request.ExpectedRevision, "issue": request.Issue, "action": request.Action, "basis": request.Basis,
	})
}

// IssueReviewResult 是核对或取消核对成功的响应：发行、问题、现在是否有核对、新的版本号与这次操作的 ID（撤销用）。
type IssueReviewResult struct {
	ReleaseID   string `json:"release_id"`
	Issue       string `json:"issue"`
	Reviewed    bool   `json:"reviewed"`
	Revision    int64  `json:"revision"`
	OperationID string `json:"operation_id"`
}

// issueReviewSnapshot 是核对行的快照：依据与设下它的操作（核对人与时间由那条记录给出）。
type issueReviewSnapshot struct {
	Basis       json.RawMessage `json:"basis"`
	OperationID string          `json:"operation_id"`
}

// issueReviewState 是操作记录里的类型化状态：问题与它的核对行（nil 表示没有核对）。撤销把核对行恢复成前态的 Review。
type issueReviewState struct {
	Issue  string               `json:"issue"`
	Review *issueReviewSnapshot `json:"review"`
}

// loadIssueReview 读取并锁住发行一类问题的核对行；没有时返回 nil。调用方已锁住发行行，同一发行的核对互相串行，这里的锁只是保险。
func loadIssueReview(ctx context.Context, tx *sql.Tx, releaseID, issue string) (*issueReviewSnapshot, error) {
	var snapshot issueReviewSnapshot
	var basis string
	err := tx.QueryRowContext(ctx, `SELECT basis::text, operation_id::text FROM release_issue_reviews WHERE release_id=$1::uuid AND issue=$2 FOR UPDATE`, releaseID, issue).
		Scan(&basis, &snapshot.OperationID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	snapshot.Basis = json.RawMessage(basis)
	return &snapshot, nil
}

// writeIssueReview 把发行一类问题的核对行写成 snapshot（nil 表示删除），并把发行的人工修改版本号加一。
func writeIssueReview(ctx context.Context, tx *sql.Tx, releaseID, issue string, snapshot *issueReviewSnapshot) error {
	if snapshot == nil {
		if _, err := tx.ExecContext(ctx, `DELETE FROM release_issue_reviews WHERE release_id=$1::uuid AND issue=$2`, releaseID, issue); err != nil {
			return err
		}
	} else if _, err := tx.ExecContext(ctx, `INSERT INTO release_issue_reviews(release_id,issue,basis,operation_id) VALUES($1::uuid,$2,$3::jsonb,$4::uuid)
		ON CONFLICT(release_id,issue) DO UPDATE SET basis=EXCLUDED.basis, operation_id=EXCLUDED.operation_id`, releaseID, issue, string(snapshot.Basis), snapshot.OperationID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE releases SET edit_revision = edit_revision + 1 WHERE id=$1::uuid`, releaseID)
	return err
}

// errIssueChanged 表示问题在页面读到之后变了（扫描改了依据，或问题已经不存在），核对的不是用户看到的那份情况。
func errIssueChanged() error {
	return operations.Conflict("issue_changed", "扫描刚刚改变了这个问题，请重新读取后再核对")
}

// IssueReviewExecutor 返回核对与取消核对的执行器，由 operations.Run 在事务里调用：锁发行行（不存在或不可见 404），
// 预期版本不一致 409 revision_conflict；review 时问题现在不存在或依据令牌与请求不同是 409 issue_changed，问题现在没有待处理的部分
// （已经核对过且仍然有效）是 400 no_changes，否则写入现在的依据（覆盖过期的行）；unreview 时没有核对行是 400 no_changes
// （过期的行也可以取消），否则删除。最后发行的人工修改版本号加一。
func IssueReviewExecutor(request IssueReviewRequest) operations.Executor {
	return func(ctx context.Context, tx *sql.Tx, operationID string) (operations.Outcome, error) {
		releaseID := strings.ToLower(request.ReleaseID)
		locked, err := lockVisibleRelease(ctx, tx, releaseID)
		if err != nil {
			return operations.Outcome{}, err
		}
		if locked.revision != request.ExpectedRevision {
			return operations.Outcome{}, errRevisionConflict()
		}
		previous, err := loadIssueReview(ctx, tx, releaseID, request.Issue)
		if err != nil {
			return operations.Outcome{}, err
		}
		var next *issueReviewSnapshot
		if request.Action == IssueReview {
			basis, exists, err := loadIssueBasis(ctx, tx, releaseID, request.Issue)
			if err != nil {
				return operations.Outcome{}, err
			}
			if !exists || issueBasisToken(basis) != request.Basis {
				return operations.Outcome{}, errIssueChanged()
			}
			arguments := []any{releaseID}
			if request.Issue == IssueSourceType || request.Issue == IssueMediaType {
				arguments = append(arguments, request.Issue)
			}
			var open bool
			if err := tx.QueryRowContext(ctx, issueReviewOpenQuery(request.Issue), arguments...).Scan(&open); err != nil {
				return operations.Outcome{}, err
			}
			if !open {
				return operations.Outcome{}, operations.Invalid("no_changes", "这个问题已经核对过了")
			}
			next = &issueReviewSnapshot{Basis: json.RawMessage(basis), OperationID: operationID}
		} else if previous == nil {
			return operations.Outcome{}, operations.Invalid("no_changes", "这个问题没有核对过")
		}
		if err := writeIssueReview(ctx, tx, releaseID, request.Issue, next); err != nil {
			return operations.Outcome{}, err
		}
		return operations.Outcome{
			TargetType: operations.TargetRelease, TargetID: releaseID,
			ExpectedRevision: locked.revision, ResultRevision: locked.revision + 1,
			Before: issueReviewState{Issue: request.Issue, Review: previous},
			After:  issueReviewState{Issue: request.Issue, Review: next},
			Response: IssueReviewResult{
				ReleaseID: releaseID, Issue: request.Issue, Reviewed: next != nil,
				Revision: locked.revision + 1, OperationID: operationID,
			},
		}, nil
	}
}

// IssueReviewOperations 返回核对问题操作的撤销与展示能力。
func IssueReviewOperations() operations.Kind { return issueReviewKind{} }

type issueReviewKind struct{}

func (issueReviewKind) Type() string { return operations.TypeReleaseIssueReview }

// Revert 把核对行恢复成这条记录之前的状态：有前态就写回当时的依据与操作（核对人仍是原来的人），没有就删除。
// 恢复的核对现在过期也照样恢复：恢复的是意图，是否仍然有效在读取时比较依据。
//
// 写回的行引用一条更早的操作记录，提交时的外键检查要给那条记录加 KEY SHARE 锁；而撤销那条更早的记录会先 FOR UPDATE 锁住它、
// 再锁发行行。所以这里先锁它、后锁发行行，与那边的顺序一致，两个撤销同时进行时不会互相等待成环（操作记录从不更新或删除）。
func (issueReviewKind) Revert(ctx context.Context, tx *sql.Tx, operationID string, original operations.Record) (operations.Outcome, error) {
	var recorded issueReviewState
	if err := json.Unmarshal(original.Before, &recorded); err != nil {
		return operations.Outcome{}, fmt.Errorf("decode recorded issue review: %w", err)
	}
	if !ReviewableIssue(recorded.Issue) || (recorded.Review != nil && (len(recorded.Review.Basis) == 0 || recorded.Review.OperationID == "")) {
		return operations.Outcome{}, fmt.Errorf("recorded issue review is malformed")
	}
	if recorded.Review != nil {
		var referenced string
		if err := tx.QueryRowContext(ctx, `SELECT id::text FROM operations WHERE id=$1::uuid FOR KEY SHARE`, recorded.Review.OperationID).Scan(&referenced); err != nil {
			return operations.Outcome{}, fmt.Errorf("lock the reviewing operation: %w", err)
		}
	}
	releaseID := strings.ToLower(original.TargetID)
	locked, err := lockVisibleRelease(ctx, tx, releaseID)
	if err != nil {
		return operations.Outcome{}, err
	}
	if locked.revision != original.ResultRevision {
		return operations.Outcome{}, errRevertConflict()
	}
	current, err := loadIssueReview(ctx, tx, releaseID, recorded.Issue)
	if err != nil {
		return operations.Outcome{}, err
	}
	if err := writeIssueReview(ctx, tx, releaseID, recorded.Issue, recorded.Review); err != nil {
		return operations.Outcome{}, err
	}
	return operations.Outcome{
		TargetType: operations.TargetRelease, TargetID: releaseID,
		ExpectedRevision: locked.revision, ResultRevision: locked.revision + 1,
		Before: issueReviewState{Issue: recorded.Issue, Review: current},
		After:  issueReviewState{Issue: recorded.Issue, Review: recorded.Review},
	}, nil
}

// issueReviewChange 是改动摘要里一侧的状态：是否有核对，缺失曲目的核对另给依据里的曲目数。
type issueReviewChange struct {
	Reviewed   bool `json:"reviewed"`
	TrackCount *int `json:"track_count"`
}

func issueReviewChangeOf(state issueReviewState) issueReviewChange {
	change := issueReviewChange{Reviewed: state.Review != nil}
	if state.Review != nil && state.Issue == IssueMissingTracks {
		var basis struct {
			Tracks map[string]json.RawMessage `json:"tracks"`
		}
		if json.Unmarshal(state.Review.Basis, &basis) == nil {
			count := len(basis.Tracks)
			change.TrackCount = &count
		}
	}
	return change
}

// Describe 给记录配上发行当前的标题、所在的组、是否可见与能否撤销，以及改动摘要（问题与前后是否有核对）。
func (issueReviewKind) Describe(ctx context.Context, database operations.Queryer, records []operations.Record) (map[string]operations.Description, error) {
	infos, err := loadReleaseInfos(ctx, database, records)
	if err != nil {
		return nil, err
	}
	descriptions := make(map[string]operations.Description, len(records))
	for _, record := range records {
		description := infos.describe(record)
		var before, after issueReviewState
		if json.Unmarshal(record.Before, &before) == nil && json.Unmarshal(record.After, &after) == nil && ReviewableIssue(after.Issue) {
			description.Changes = map[string]any{
				"kind": operations.TypeReleaseIssueReview, "issue": after.Issue,
				"from": issueReviewChangeOf(before), "to": issueReviewChangeOf(after),
			}
		}
		descriptions[record.ID] = description
	}
	return descriptions, nil
}
