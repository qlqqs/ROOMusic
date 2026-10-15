package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/operations"
)

// 默认版本（阶段 M4）。管理员可以为一张多版本专辑指定一个默认版本，它成为专辑的代表版本（releaseGroupMemberOrder 把它排在最前）；
// 没有指定时仍按“年份最早”自动选择。默认版本存在 release_groups.default_release_id，不要求它属于这个组：扫描与归组都不改这一列，
// 它只在“这个发行可见、并且现在属于这个组”时生效，否则静默失效、回到自动规则，版本回来时重新生效。
// 专辑有自己的人工修改版本号 release_groups.edit_revision，只有这里的设定、清除与撤销递增它（归组递增的是发行的版本号）。
// 写入只经 operations.Run 的共同流程；加锁顺序与归组一致：先发行、后组。

// GroupDefaultRequest 是一次已校验的请求：ReleaseID 是要设为默认版本的发行，为空表示改回自动选择。
type GroupDefaultRequest struct {
	GroupID          string // 小写 UUID
	ExpectedRevision int64  // 专辑的人工修改版本号
	ReleaseID        string // 小写 UUID；空表示改回自动选择
}

// Fingerprint 返回请求的规范化指纹：含操作类型、小写的专辑 ID、预期版本与目标版本（改回自动选择时为 null）。
func (request GroupDefaultRequest) Fingerprint() string {
	var release any
	if request.ReleaseID != "" {
		release = strings.ToLower(request.ReleaseID)
	}
	return operations.Fingerprint(map[string]any{
		"type": operations.TypeReleaseGroupDefault, "group_id": strings.ToLower(request.GroupID),
		"expected_revision": request.ExpectedRevision, "release_id": release,
	})
}

// GroupDefaultResult 是设定或清除成功的响应：专辑、现在已存的默认版本（null 为自动选择）、专辑新的版本号与这次操作的 ID（撤销用）。
type GroupDefaultResult struct {
	GroupID          string  `json:"group_id"`
	DefaultReleaseID *string `json:"default_release_id"`
	Revision         int64   `json:"revision"`
	OperationID      string  `json:"operation_id"`
}

// defaultVersionSnapshot 是操作记录里默认版本的快照：当时的有效标题、年份与版本说明（版本之后可能改名或变得不可见，记录仍要能读）。
type defaultVersionSnapshot struct {
	ReleaseID string  `json:"release_id"`
	Title     string  `json:"title"`
	Year      *int    `json:"year"`
	Edition   *string `json:"edition"`
}

// groupDefaultState 是操作记录里的类型化状态：这张专辑当时的标题（代表版本的有效标题，组可能之后变空，记录仍要能读），
// 以及已存的默认版本（nil 为自动选择；包括当时已经失效的）。撤销恢复的就是前态的 Default。
type groupDefaultState struct {
	GroupTitle string                  `json:"group_title"`
	Default    *defaultVersionSnapshot `json:"default"`
}

// lockReleaseRow 锁住发行行（不要求可见），返回它所在的组与是否可见；发行不存在时 exists 为 false。
func lockReleaseRow(ctx context.Context, tx *sql.Tx, releaseID string) (groupID string, visible, exists bool, err error) {
	err = tx.QueryRowContext(ctx, `SELECT releases.group_id::text, `+releaseVisiblePredicate+` FROM releases WHERE releases.id=$1::uuid FOR UPDATE`, releaseID).
		Scan(&groupID, &visible)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, false, nil
	}
	if err != nil {
		return "", false, false, err
	}
	return groupID, visible, true, nil
}

// lockedGroup 是加锁后读到的专辑状态：人工修改版本号、已存的默认版本（空表示自动选择，包括已经失效的）。
type lockedGroup struct {
	revision       int64
	defaultRelease string
}

// lockVisibleGroup 锁住组行并返回它的状态；组不存在或没有可见成员时返回 404 分类错误。
func lockVisibleGroup(ctx context.Context, tx *sql.Tx, groupID string) (lockedGroup, error) {
	var locked lockedGroup
	var stored sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT edit_revision, default_release_id::text FROM release_groups WHERE id=$1::uuid FOR UPDATE`, groupID).Scan(&locked.revision, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return lockedGroup{}, operations.NotFound("not_found", "专辑不存在")
	}
	if err != nil {
		return lockedGroup{}, err
	}
	locked.defaultRelease = stored.String
	var visible bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM `+releaseSource+` WHERE releases.group_id=$1::uuid AND `+releaseVisiblePredicate+`)`, groupID).Scan(&visible); err != nil {
		return lockedGroup{}, err
	}
	if !visible {
		return lockedGroup{}, operations.NotFound("not_found", "专辑不存在")
	}
	return locked, nil
}

// loadDefaultSnapshot 读取一个版本当前的有效标题、年份与版本说明；releaseID 为空返回 nil（自动选择）。不要求版本可见。
func loadDefaultSnapshot(ctx context.Context, tx *sql.Tx, releaseID string) (*defaultVersionSnapshot, error) {
	if releaseID == "" {
		return nil, nil
	}
	var snapshot defaultVersionSnapshot
	err := tx.QueryRowContext(ctx, `SELECT releases.id::text, releases.title, NULLIF(releases.year, 0), NULLIF(BTRIM(releases.edition), '') FROM `+releaseSource+` WHERE releases.id=$1::uuid`, releaseID).
		Scan(&snapshot.ReleaseID, &snapshot.Title, &snapshot.Year, &snapshot.Edition)
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// writeGroupDefault 更新专辑的默认版本并把人工修改版本号加一；releaseID 为空写入 NULL（自动选择）。
func writeGroupDefault(ctx context.Context, tx *sql.Tx, groupID, releaseID string) error {
	var value any
	if releaseID != "" {
		value = releaseID
	}
	_, err := tx.ExecContext(ctx, `UPDATE release_groups SET default_release_id=$2::uuid, edit_revision = edit_revision + 1 WHERE id=$1::uuid`, groupID, value)
	return err
}

// recordedDefaultID 返回快照里的版本 ID（小写）；快照为 nil 时为空。
func recordedDefaultID(snapshot *defaultVersionSnapshot) string {
	if snapshot == nil {
		return ""
	}
	return strings.ToLower(snapshot.ReleaseID)
}

func optionalDefaultID(releaseID string) *string {
	if releaseID == "" {
		return nil
	}
	return &releaseID
}

// GroupDefaultExecutor 返回设定与清除默认版本的执行器，由 operations.Run 在事务里调用。设定时先锁目标发行行、再锁组行
// （与归组同样的顺序，同时进行不会互相等待成环），清除时只锁组行。然后依次检查：组不存在或没有可见成员是 404 not_found，
// 预期版本不一致是 409 revision_conflict，目标版本不可见或不在这张专辑里是 409 not_in_group，请求的值等于已存的值（包括已经失效的）
// 是 400 no_changes；最后更新默认版本、专辑的人工修改版本号加一。
func GroupDefaultExecutor(request GroupDefaultRequest) operations.Executor {
	return func(ctx context.Context, tx *sql.Tx, operationID string) (operations.Outcome, error) {
		groupID, target := strings.ToLower(request.GroupID), strings.ToLower(request.ReleaseID)
		var targetGroup string
		var targetVisible, targetExists bool
		if target != "" {
			var err error
			if targetGroup, targetVisible, targetExists, err = lockReleaseRow(ctx, tx, target); err != nil {
				return operations.Outcome{}, err
			}
		}
		locked, err := lockVisibleGroup(ctx, tx, groupID)
		if err != nil {
			return operations.Outcome{}, err
		}
		if locked.revision != request.ExpectedRevision {
			return operations.Outcome{}, errRevisionConflict()
		}
		if target != "" && (!targetExists || !targetVisible || targetGroup != groupID) {
			return operations.Outcome{}, operations.Conflict("not_in_group", "这个版本不在这张专辑里，或现在不可见")
		}
		if target == locked.defaultRelease {
			return operations.Outcome{}, operations.Invalid("no_changes", "没有需要修改的内容")
		}

		previous, err := loadDefaultSnapshot(ctx, tx, locked.defaultRelease)
		if err != nil {
			return operations.Outcome{}, err
		}
		next, err := loadDefaultSnapshot(ctx, tx, target)
		if err != nil {
			return operations.Outcome{}, err
		}
		titleBefore, err := representativeTitle(ctx, tx, groupID)
		if err != nil {
			return operations.Outcome{}, err
		}
		if err := writeGroupDefault(ctx, tx, groupID, target); err != nil {
			return operations.Outcome{}, err
		}
		titleAfter, err := representativeTitle(ctx, tx, groupID)
		if err != nil {
			return operations.Outcome{}, err
		}
		return operations.Outcome{
			TargetType: operations.TargetReleaseGroup, TargetID: groupID,
			ExpectedRevision: locked.revision, ResultRevision: locked.revision + 1,
			Before: groupDefaultState{GroupTitle: titleBefore, Default: previous},
			After:  groupDefaultState{GroupTitle: titleAfter, Default: next},
			Response: GroupDefaultResult{
				GroupID: groupID, DefaultReleaseID: optionalDefaultID(target),
				Revision: locked.revision + 1, OperationID: operationID,
			},
		}, nil
	}
}

// GroupDefaultOperations 返回设定默认版本操作的撤销与展示能力。
func GroupDefaultOperations() operations.Kind { return groupDefaultKind{} }

type groupDefaultKind struct{}

func (groupDefaultKind) Type() string { return operations.TypeReleaseGroupDefault }

// Revert 把专辑的默认版本恢复成这条记录之前的状态（自动选择或某个版本）。前态的版本现在失效也照样恢复：恢复的是意图，
// 失效规则照常生效。前态是某个版本时先锁它的发行行、再锁组行，与设定时的加锁顺序一致。
func (groupDefaultKind) Revert(ctx context.Context, tx *sql.Tx, operationID string, original operations.Record) (operations.Outcome, error) {
	var recordedBefore groupDefaultState
	if err := json.Unmarshal(original.Before, &recordedBefore); err != nil {
		return operations.Outcome{}, fmt.Errorf("decode recorded default state: %w", err)
	}
	restored := recordedDefaultID(recordedBefore.Default)
	if restored != "" {
		if _, _, exists, err := lockReleaseRow(ctx, tx, restored); err != nil {
			return operations.Outcome{}, err
		} else if !exists {
			return operations.Outcome{}, errRevertConflict()
		}
	}
	groupID := strings.ToLower(original.TargetID)
	locked, err := lockVisibleGroup(ctx, tx, groupID)
	if err != nil {
		return operations.Outcome{}, err
	}
	if locked.revision != original.ResultRevision {
		return operations.Outcome{}, errRevertConflict()
	}
	current, err := loadDefaultSnapshot(ctx, tx, locked.defaultRelease)
	if err != nil {
		return operations.Outcome{}, err
	}
	next, err := loadDefaultSnapshot(ctx, tx, restored)
	if err != nil {
		return operations.Outcome{}, err
	}
	titleBefore, err := representativeTitle(ctx, tx, groupID)
	if err != nil {
		return operations.Outcome{}, err
	}
	if err := writeGroupDefault(ctx, tx, groupID, restored); err != nil {
		return operations.Outcome{}, err
	}
	titleAfter, err := representativeTitle(ctx, tx, groupID)
	if err != nil {
		return operations.Outcome{}, err
	}
	return operations.Outcome{
		TargetType: operations.TargetReleaseGroup, TargetID: groupID,
		ExpectedRevision: locked.revision, ResultRevision: locked.revision + 1,
		Before: groupDefaultState{GroupTitle: titleBefore, Default: current},
		After:  groupDefaultState{GroupTitle: titleAfter, Default: next},
	}, nil
}

// Describe 给记录配上专辑当前的标题、是否可见与能否撤销，以及改动摘要（前后的默认版本快照）。
func (groupDefaultKind) Describe(ctx context.Context, database operations.Queryer, records []operations.Record) (map[string]operations.Description, error) {
	infos, err := loadAlbumInfos(ctx, database, records)
	if err != nil {
		return nil, err
	}
	descriptions := make(map[string]operations.Description, len(records))
	for _, record := range records {
		description := infos.describe(record)
		var before, after groupDefaultState
		if json.Unmarshal(record.Before, &before) == nil && json.Unmarshal(record.After, &after) == nil {
			description.Changes = map[string]any{"kind": operations.TypeReleaseGroupDefault, "from": before, "to": after}
			// 专辑已经没有任何成员时，用记录里当时的标题，记录仍然有名字。
			if description.Album != nil && description.Album.Title == "" {
				description.Album.Title = after.GroupTitle
			}
		}
		descriptions[record.ID] = description
	}
	return descriptions, nil
}

// albumInfos 是一批记录所指专辑的当前状态，键是小写的组 ID。
type albumInfos map[string]struct {
	info     operations.AlbumInfo
	revision int64
}

// loadAlbumInfos 用一条查询读取这些记录所指专辑的当前标题（代表版本的有效标题）、是否可见（至少有一个可见成员）与人工修改版本号。
// 专辑没有可见成员时标题取自排在最前的成员（可见的优先，其次按同一条规则），这样不可见的专辑在记录里仍然有名字。
func loadAlbumInfos(ctx context.Context, database operations.Queryer, records []operations.Record) (albumInfos, error) {
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.TargetID)
	}
	infos := albumInfos{}
	if len(ids) == 0 {
		return infos, nil
	}
	rows, err := database.QueryContext(ctx, `SELECT album.id::text, album.edit_revision, representative.title, COALESCE(representative.visible, FALSE)
		FROM release_groups album
		LEFT JOIN LATERAL (
			SELECT releases.title, `+releaseVisiblePredicate+` AS visible FROM `+releaseSource+`
			WHERE releases.group_id = album.id
			ORDER BY NOT (`+releaseVisiblePredicate+`), `+releaseGroupMemberOrder+` LIMIT 1
		) representative ON TRUE
		WHERE album.id = ANY($1::text[]::uuid[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var entry struct {
			info     operations.AlbumInfo
			revision int64
		}
		var title sql.NullString
		if err := rows.Scan(&entry.info.ID, &entry.revision, &title, &entry.info.Visible); err != nil {
			return nil, err
		}
		entry.info.Title = title.String
		infos[entry.info.ID] = entry
	}
	return infos, rows.Err()
}

// describe 给一条记录配上专辑当前的展示信息；专辑仍可见且人工修改版本号等于记录的结果版本时，这条记录可以被撤销。
func (infos albumInfos) describe(record operations.Record) operations.Description {
	entry, exists := infos[strings.ToLower(record.TargetID)]
	if !exists {
		return operations.Description{}
	}
	info := entry.info
	return operations.Description{Album: &info, Revertible: info.Visible && entry.revision == record.ResultRevision}
}
