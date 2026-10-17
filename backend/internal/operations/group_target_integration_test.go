package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// bumpGroupOf 是测试执行器：对一张专辑（组）做一次带预期版本的比较更新，记录的目标是专辑。
func (fixture *runFixture) bumpGroupOf(groupID string, expected int64) Executor {
	return func(ctx context.Context, tx *sql.Tx, operationID string) (Outcome, error) {
		result, err := tx.ExecContext(ctx, `UPDATE release_groups SET edit_revision = edit_revision + 1 WHERE id = $1::uuid AND edit_revision = $2`, groupID, expected)
		if err != nil {
			return Outcome{}, err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return Outcome{}, Conflict("revision_conflict", "专辑版本已变化")
		}
		return Outcome{
			TargetType: TargetReleaseGroup, TargetID: groupID,
			ExpectedRevision: expected, ResultRevision: expected + 1,
			Before:   map[string]any{"revision": expected},
			After:    map[string]any{"revision": expected + 1},
			Response: map[string]any{"operation_id": operationID, "revision": expected + 1},
		}, nil
	}
}

func (fixture *runFixture) bumpGroup(expected int64) Executor {
	return fixture.bumpGroupOf(fixture.groupID, expected)
}

// fakeGroupKind 是测试用的组目标撤销能力：撤销只是让专辑的版本号再加一，前提是专辑仍停在原记录的结果版本。
type fakeGroupKind struct{}

func (fakeGroupKind) Type() string { return TypeReleaseGroupDefault }

func (fakeGroupKind) Revert(ctx context.Context, tx *sql.Tx, _ string, original Record) (Outcome, error) {
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT edit_revision FROM release_groups WHERE id=$1::uuid FOR UPDATE`, original.TargetID).Scan(&revision); err != nil {
		return Outcome{}, err
	}
	if revision != original.ResultRevision {
		return Outcome{}, Conflict("revert_conflict", "之后又被修改过")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE release_groups SET edit_revision = edit_revision + 1 WHERE id=$1::uuid`, original.TargetID); err != nil {
		return Outcome{}, err
	}
	return Outcome{TargetType: TargetReleaseGroup, TargetID: original.TargetID, ExpectedRevision: revision, ResultRevision: revision + 1,
		Before: map[string]any{"revision": revision}, After: map[string]any{"revision": revision + 1}}, nil
}

func (fakeGroupKind) Describe(context.Context, Queryer, []Record) (map[string]Description, error) {
	return nil, nil
}

func violatedConstraint(err error) string {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		return postgresError.ConstraintName
	}
	return ""
}

// 迁移 0018：新增的两列有默认值与约束；operations 接受专辑目标与新的操作类型（0019 再加核对问题），仍然拒绝其他取值。
func TestPostgreSQLReleaseGroupColumnsAndOperationConstraints(t *testing.T) {
	fixture := newRunFixture(t)

	var defaultRelease sql.NullString
	var revision int64
	if err := fixture.database.QueryRow(`SELECT default_release_id::text, edit_revision FROM release_groups WHERE id = $1::uuid`, fixture.groupID).Scan(&defaultRelease, &revision); err != nil {
		t.Fatalf("read the group columns: %v", err)
	}
	if defaultRelease.Valid || revision != 1 {
		t.Fatalf("a new group has default %v and revision %d; want no default and revision 1", defaultRelease, revision)
	}

	if _, err := fixture.database.Exec(`UPDATE release_groups SET edit_revision = 0 WHERE id = $1::uuid`, fixture.groupID); violatedConstraint(err) != "release_groups_edit_revision_ck" {
		t.Errorf("revision 0: error = %v, want release_groups_edit_revision_ck", err)
	}
	if _, err := fixture.database.Exec(`UPDATE release_groups SET default_release_id = $2::uuid WHERE id = $1::uuid`, fixture.groupID, uuid.New()); violatedConstraint(err) != "release_groups_default_release_fk" {
		t.Errorf("an unknown default release: error = %v, want release_groups_default_release_fk", err)
	}

	// 默认版本不约束属于这个组：归组会移动版本，失效由读取时的排序保证。发行被删除时清空（保险，发行实际从不删除）。
	otherGroup, otherRelease := uuid.New(), uuid.New()
	fixture.exec(`INSERT INTO release_groups(id) VALUES($1::uuid)`, otherGroup)
	fixture.exec(`INSERT INTO releases(id,group_id,title,artist) VALUES($1::uuid,$2::uuid,'另一张专辑','艺术家')`, otherRelease, otherGroup)
	fixture.exec(`UPDATE release_groups SET default_release_id = $2::uuid WHERE id = $1::uuid`, fixture.groupID, otherRelease)
	fixture.exec(`DELETE FROM releases WHERE id = $1::uuid`, otherRelease)
	if err := fixture.database.QueryRow(`SELECT default_release_id::text FROM release_groups WHERE id = $1::uuid`, fixture.groupID).Scan(&defaultRelease); err != nil || defaultRelease.Valid {
		t.Errorf("after the default release was deleted: default %v (%v); want NULL", defaultRelease, err)
	}

	insertRecord := func(operationType, targetType string) error {
		_, err := fixture.database.Exec(`INSERT INTO operations(id, actor_id, operation_type, target_type, target_id, idempotency_key, fingerprint,
				expected_revision, result_revision, before_state, after_state, response, request_id)
			VALUES($1::uuid, $2::uuid, $3, $4, $5::uuid, $6::uuid, 'f', 1, 2, '{}', '{}', '{}', 'test')`,
			uuid.New(), fixture.actorID, operationType, targetType, fixture.groupID, uuid.New())
		return err
	}
	if err := insertRecord(TypeReleaseGroupDefault, TargetReleaseGroup); err != nil {
		t.Fatalf("a default-version record targeting a group: %v", err)
	}
	// 迁移 0019 加入核对问题，目标是发行（这里只检查类型约束，目标 ID 不设外键）。
	if err := insertRecord(TypeReleaseIssueReview, TargetRelease); err != nil {
		t.Fatalf("an issue-review record targeting a release: %v", err)
	}
	for name, testCase := range map[string]struct{ operationType, targetType, constraint string }{
		"unknown type":        {"release_merge", TargetReleaseGroup, "operations_type_ck"},
		"unknown target type": {TypeReleaseGroupDefault, "track", "operations_target_type_ck"},
	} {
		if err := insertRecord(testCase.operationType, testCase.targetType); violatedConstraint(err) != testCase.constraint {
			t.Errorf("%s: error = %v, want %s", name, err, testCase.constraint)
		}
	}
}

// 专辑详情的“修改记录”同时列出一个版本的记录和这张专辑的记录：并集按时间倒序，分页与总数针对并集。
func TestPostgreSQLListUnionsReleaseAndGroupTargets(t *testing.T) {
	fixture := newRunFixture(t)
	ctx := context.Background()
	otherGroup := uuid.New()
	fixture.exec(`INSERT INTO release_groups(id) VALUES($1::uuid)`, otherGroup)

	run := func(operationType string, execute Executor) string {
		t.Helper()
		result, err := Run(ctx, fixture.database, fixture.spec(fixture.actorID, uuid.New(), operationType, map[string]any{"n": uuid.New()}), execute)
		if err != nil {
			t.Fatalf("operation: %v", err)
		}
		return result.OperationID
	}
	// 创建顺序：版本、专辑、版本、专辑、版本；另一张专辑的记录不在筛选范围内。
	created := []string{
		run(TypeReleaseCorrection, fixture.bump(1, nil)),
		run(TypeReleaseGroupDefault, fixture.bumpGroup(1)),
		run(TypeReleaseCorrection, fixture.bump(2, nil)),
		run(TypeReleaseGroupDefault, fixture.bumpGroup(2)),
		run(TypeReleaseCorrection, fixture.bump(3, nil)),
	}
	run(TypeReleaseGroupDefault, fixture.bumpGroupOf(otherGroup, 1))
	newestFirst := slices.Clone(created)
	slices.Reverse(newestFirst)
	idsOf := func(page Page) []string {
		ids := make([]string, len(page.Items))
		for index, item := range page.Items {
			ids[index] = item.ID
		}
		return ids
	}
	both := []Target{{TargetRelease, fixture.releaseID}, {TargetReleaseGroup, fixture.groupID}}

	union, err := List(ctx, fixture.database, ListParams{Targets: both, Page: 1, PageSize: 100})
	if err != nil || union.Total != 5 || !slices.Equal(idsOf(union), newestFirst) {
		t.Fatalf("List(union) = total %d, %v, %v; want the five records newest first %v", union.Total, idsOf(union), err, newestFirst)
	}
	if group := union.Items[1]; group.Type != TypeReleaseGroupDefault || group.TargetType != TargetReleaseGroup || group.TargetID != fixture.groupID {
		t.Errorf("the second newest record = %+v, want the group's default-version record", group)
	}

	for page, want := range map[int][]string{1: newestFirst[0:2], 2: newestFirst[2:4], 3: newestFirst[4:5]} {
		listed, err := List(ctx, fixture.database, ListParams{Targets: both, Page: page, PageSize: 2})
		if err != nil || listed.Total != 5 || !slices.Equal(idsOf(listed), want) {
			t.Errorf("List(union, page %d of size 2) = total %d, %v, %v; want %v of 5", page, listed.Total, idsOf(listed), err, want)
		}
	}

	groupOnly, err := List(ctx, fixture.database, ListParams{Targets: []Target{{TargetReleaseGroup, fixture.groupID}}, Page: 1, PageSize: 100})
	if err != nil || groupOnly.Total != 2 || !slices.Equal(idsOf(groupOnly), []string{created[3], created[1]}) {
		t.Errorf("List(group only) = total %d, %v, %v; want the group's two records", groupOnly.Total, idsOf(groupOnly), err)
	}
	releaseOnly, err := List(ctx, fixture.database, ListParams{Targets: []Target{{TargetRelease, fixture.releaseID}}, Page: 1, PageSize: 100})
	if err != nil || releaseOnly.Total != 3 {
		t.Errorf("List(release only) = total %d, %v; want the release's three records", releaseOnly.Total, err)
	}
	// 目标类型参与匹配：把专辑的 ID 当作发行筛选，或反过来，都不匹配。
	crossed, err := List(ctx, fixture.database, ListParams{Targets: []Target{{TargetRelease, fixture.groupID}, {TargetReleaseGroup, fixture.releaseID}}, Page: 1, PageSize: 100})
	if err != nil || crossed.Total != 0 || crossed.Items == nil || len(crossed.Items) != 0 {
		t.Errorf("List(crossed target types) = %+v, %v; want an empty page", crossed, err)
	}
	// 并集里的一个目标没有记录时，只列出另一个目标的记录。
	partial, err := List(ctx, fixture.database, ListParams{Targets: []Target{{TargetRelease, fixture.releaseID}, {TargetReleaseGroup, uuid.New()}}, Page: 1, PageSize: 100})
	if err != nil || partial.Total != 3 {
		t.Errorf("List(release plus an unknown group) = total %d, %v; want the release's three records", partial.Total, err)
	}
}

// 撤销组目标的记录：响应里的目标类型与 ID 是专辑；发行类型的能力不能撤销它。
func TestPostgreSQLRevertExecutorReportsTheGroupTarget(t *testing.T) {
	fixture := newRunFixture(t)
	ctx := context.Background()
	original, err := Run(ctx, fixture.database, fixture.spec(fixture.actorID, uuid.New(), TypeReleaseGroupDefault, map[string]any{"n": 1}), fixture.bumpGroup(1))
	if err != nil {
		t.Fatalf("original group operation: %v", err)
	}

	if _, err := fixture.revert(fakeKind{fixture: fixture, kind: TypeReleaseCorrection}, original.OperationID); err == nil {
		t.Fatal("a release kind reverted a group record")
	} else if status, code := problemOf(err); status != 409 || code != "not_revertible" {
		t.Fatalf("release kind on a group record: status %d, code %q; want 409 not_revertible", status, code)
	}

	reverted, err := fixture.revert(fakeGroupKind{}, original.OperationID)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	var response RevertResult
	if err := json.Unmarshal(reverted.Response, &response); err != nil {
		t.Fatalf("decode the revert response: %v", err)
	}
	if response != (RevertResult{OperationID: reverted.OperationID, RevertsOperationID: original.OperationID, TargetType: TargetReleaseGroup, TargetID: fixture.groupID, Revision: 3}) {
		t.Fatalf("revert response = %+v", response)
	}
	record, err := Load(ctx, fixture.database, reverted.OperationID)
	if err != nil || record.Type != TypeRevert || record.TargetType != TargetReleaseGroup || record.TargetID != fixture.groupID ||
		record.OriginalType != TypeReleaseGroupDefault || record.ChangeType() != TypeReleaseGroupDefault || record.ExpectedRevision != 2 || record.ResultRevision != 3 {
		t.Fatalf("revert record = %+v, %v", record, err)
	}
	if _, err := fixture.revert(fakeGroupKind{}, reverted.OperationID); err == nil {
		t.Fatal("a revert of a group revert succeeded")
	} else if status, code := problemOf(err); status != 409 || code != "not_revertible" {
		t.Fatalf("revert of a revert: status %d, code %q; want 409 not_revertible", status, code)
	}
}
