package governance

import (
	"context"
	"database/sql"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/writegate"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// 人工归组（阶段 M3，D6：Core 1 只做人工确认的归组）。一次只动一个版本：把它归入另一张专辑（join），
// 或把它移出为独立专辑（detach，得到一个新的 ReleaseGroup）。扫描对已有发行不改 group_id（persist.go 的 UPDATE 不含它），
// 新发行仍各自新建一个组，所以重扫不会冲掉归组。ReleaseGroup 没有自己的字段，组从不删除：空了（没有可见版本）就从列表里消失，
// 撤销时才能回到原来的组 ID，原地址随之恢复。写入只经 operations.Run 的共同流程。

// 归组的两个动作。
const (
	GroupJoin   = "join"
	GroupDetach = "detach"
)

// GroupChangeRequest 是一次已校验的归组请求：Action 是 join 或 detach，join 时 GroupID 是目标组（小写 UUID）。
type GroupChangeRequest struct {
	ReleaseID        string // 小写 UUID
	ExpectedRevision int64
	Action           string
	GroupID          string
}

// Fingerprint 返回请求的规范化指纹：含操作类型、小写的目标 ID、预期版本、动作与目标组。
func (request GroupChangeRequest) Fingerprint() string {
	return operations.Fingerprint(map[string]any{
		"type": operations.TypeReleaseGroupChange, "release_id": strings.ToLower(request.ReleaseID),
		"expected_revision": request.ExpectedRevision, "action": request.Action, "group_id": strings.ToLower(request.GroupID),
	})
}

// GroupChangeResult 是归组成功的响应：发行现在所在的组、原来的组、新的版本号与这次操作的 ID（撤销用）。
type GroupChangeResult struct {
	ReleaseID       string `json:"release_id"`
	GroupID         string `json:"group_id"`
	PreviousGroupID string `json:"previous_group_id"`
	Revision        int64  `json:"revision"`
	OperationID     string `json:"operation_id"`
}

// groupRecordState 是操作记录里的类型化状态：组 ID，以及当时用户看到的这张专辑的标题快照（组可能之后变空，记录仍要能读）。
// 前态的标题是原组在移动之前的代表版本标题；后态的标题是目标组在移动之前的代表版本标题（join，即用户选中的那张专辑），
// 或这个版本自己的标题（detach，新组里只有它）。
type groupRecordState struct {
	GroupID    string `json:"group_id"`
	GroupTitle string `json:"group_title"`
}

// lockGroups 按 UUID 顺序锁住这些组行（不存在的组不会出现在结果里），返回存在的组。所有会锁多个组的操作都用同一个顺序，
// 所以两个涉及同一对组的归组互相等待而不会死锁。
func lockGroups(ctx context.Context, tx *sql.Tx, groupIDs ...string) (map[string]bool, error) {
	if err := writegate.Lock(ctx, tx); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id::text FROM release_groups WHERE id = ANY($1::text[]::uuid[]) ORDER BY id FOR UPDATE`, groupIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	existing := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		existing[id] = true
	}
	return existing, rows.Err()
}

// visibleMembersExcept 返回组里除 releaseID 之外的可见版本数。
func visibleMembersExcept(ctx context.Context, tx *sql.Tx, groupID, releaseID string) (int, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+releasesql.ReleaseSource+` WHERE releases.group_id=$1::uuid AND releases.id<>$2::uuid AND `+releasesql.ReleaseVisiblePredicate,
		groupID, releaseID).Scan(&count)
	return count, err
}

// representativeTitle 返回组的代表版本的有效标题（代表版本规则见 releasesql.ReleaseGroupMemberOrder）。
func representativeTitle(ctx context.Context, tx *sql.Tx, groupID string) (string, error) {
	var title string
	err := tx.QueryRowContext(ctx, `SELECT releases.title FROM `+releasesql.ReleaseSource+` WHERE releases.group_id=$1::uuid AND `+releasesql.ReleaseVisiblePredicate+
		` ORDER BY `+releasesql.ReleaseGroupMemberOrder+` LIMIT 1`, groupID).Scan(&title)
	return title, err
}

// GroupChangeExecutor 返回归组的执行器，由 operations.Run 在事务里调用：先锁发行行并核对预期版本，再按 UUID 顺序锁涉及的组行，
// 然后检查动作的前提（join：目标就是当前组是 same_group，目标组不存在或没有可见版本是 group_not_found；
// detach：组里没有别的可见版本是 already_alone），更新 group_id，人工修改版本号加一。
func GroupChangeExecutor(request GroupChangeRequest) operations.Executor {
	return func(ctx context.Context, tx *sql.Tx, operationID string) (operations.Outcome, error) {
		locked, err := lockVisibleRelease(ctx, tx, request.ReleaseID)
		if err != nil {
			return operations.Outcome{}, err
		}
		if locked.revision != request.ExpectedRevision {
			return operations.Outcome{}, errRevisionConflict()
		}
		current := locked.groupID

		var target, targetTitle string
		switch request.Action {
		case GroupJoin:
			target = strings.ToLower(request.GroupID)
			if target == current {
				return operations.Outcome{}, operations.Conflict("same_group", "这个版本已经在这张专辑里")
			}
			existing, err := lockGroups(ctx, tx, current, target)
			if err != nil {
				return operations.Outcome{}, err
			}
			if !existing[target] {
				return operations.Outcome{}, operations.NotFound("group_not_found", "目标专辑不存在")
			}
			members, err := visibleMembersExcept(ctx, tx, target, request.ReleaseID)
			if err != nil {
				return operations.Outcome{}, err
			}
			if members == 0 {
				return operations.Outcome{}, operations.NotFound("group_not_found", "目标专辑不存在")
			}
			if targetTitle, err = representativeTitle(ctx, tx, target); err != nil {
				return operations.Outcome{}, err
			}
		case GroupDetach:
			if _, err := lockGroups(ctx, tx, current); err != nil {
				return operations.Outcome{}, err
			}
			members, err := visibleMembersExcept(ctx, tx, current, request.ReleaseID)
			if err != nil {
				return operations.Outcome{}, err
			}
			if members == 0 {
				return operations.Outcome{}, operations.Conflict("already_alone", "这个版本已经是独立专辑")
			}
			target = uuid.New()
			if _, err := tx.ExecContext(ctx, `INSERT INTO release_groups(id) VALUES($1::uuid)`, target); err != nil {
				return operations.Outcome{}, err
			}
			if err := tx.QueryRowContext(ctx, `SELECT title FROM `+releasesql.ReleaseSource+` WHERE releases.id=$1::uuid`, request.ReleaseID).Scan(&targetTitle); err != nil {
				return operations.Outcome{}, err
			}
		default:
			return operations.Outcome{}, operations.Invalid("invalid_input", "归组动作无效")
		}

		currentTitle, err := representativeTitle(ctx, tx, current)
		if err != nil {
			return operations.Outcome{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE releases SET group_id=$2::uuid, edit_revision = edit_revision + 1 WHERE id=$1::uuid`, request.ReleaseID, target); err != nil {
			return operations.Outcome{}, err
		}
		if err := refreshGovernedRelease(ctx, tx, request.ReleaseID); err != nil {
			return operations.Outcome{}, err
		}
		return operations.Outcome{
			TargetType: operations.TargetRelease, TargetID: request.ReleaseID,
			ExpectedRevision: locked.revision, ResultRevision: locked.revision + 1,
			Before: groupRecordState{GroupID: current, GroupTitle: currentTitle},
			After:  groupRecordState{GroupID: target, GroupTitle: targetTitle},
			Response: GroupChangeResult{
				ReleaseID: strings.ToLower(request.ReleaseID), GroupID: target, PreviousGroupID: current,
				Revision: locked.revision + 1, OperationID: operationID,
			},
		}, nil
	}
}
