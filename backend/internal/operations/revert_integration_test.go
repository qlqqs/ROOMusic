package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// fakeKind 是测试用的撤销能力：撤销只是让发行的版本号再加一，前提是发行仍停在原记录的结果版本。
type fakeKind struct {
	fixture *runFixture
	kind    string
}

func (kind fakeKind) Type() string { return kind.kind }

func (kind fakeKind) Revert(ctx context.Context, tx *sql.Tx, _ string, original Record) (Outcome, error) {
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT edit_revision FROM releases WHERE id=$1::uuid FOR UPDATE`, original.TargetID).Scan(&revision); err != nil {
		return Outcome{}, err
	}
	if revision != original.ResultRevision {
		return Outcome{}, Conflict("revert_conflict", "之后又被修改过")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE releases SET edit_revision = edit_revision + 1 WHERE id=$1::uuid`, original.TargetID); err != nil {
		return Outcome{}, err
	}
	return Outcome{TargetType: TargetRelease, TargetID: original.TargetID, ExpectedRevision: revision, ResultRevision: revision + 1,
		Before: map[string]any{"revision": revision}, After: map[string]any{"revision": revision + 1}}, nil
}

func (kind fakeKind) Describe(context.Context, Queryer, []Record) (map[string]Description, error) {
	return nil, nil
}

func (fixture *runFixture) revert(kind Kind, originalID string) (Result, error) {
	fixture.t.Helper()
	return Run(context.Background(), fixture.database, Spec{
		ActorID: fixture.actorID, Type: TypeRevert, Key: uuid.New(),
		Fingerprint: Fingerprint(map[string]any{"type": TypeRevert, "operation_id": originalID}), RequestID: "test",
	}, RevertExecutor(kind, originalID))
}

func problemOf(err error) (int, string) {
	status, code, _ := Problem(err)
	return status, code
}

func TestPostgreSQLRevertExecutorRecordsAndGuardsTheOriginal(t *testing.T) {
	fixture := newRunFixture(t)
	ctx := context.Background()
	kind := fakeKind{fixture: fixture, kind: TypeReleaseCorrection}
	original, err := Run(ctx, fixture.database, fixture.spec(fixture.actorID, uuid.New(), TypeReleaseCorrection, map[string]any{"n": 1}), fixture.bump(1, nil))
	if err != nil {
		t.Fatalf("original operation: %v", err)
	}

	reverted, err := fixture.revert(kind, original.OperationID)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	var response RevertResult
	if err := json.Unmarshal(reverted.Response, &response); err != nil {
		t.Fatalf("decode the revert response: %v", err)
	}
	if response != (RevertResult{OperationID: reverted.OperationID, RevertsOperationID: original.OperationID, TargetType: TargetRelease, TargetID: fixture.releaseID, Revision: 3}) {
		t.Fatalf("revert response = %+v", response)
	}
	record, err := Load(ctx, fixture.database, reverted.OperationID)
	if err != nil || record.Type != TypeRevert || record.RevertsID != original.OperationID || record.OriginalType != TypeReleaseCorrection ||
		record.ChangeType() != TypeReleaseCorrection || record.ExpectedRevision != 2 || record.ResultRevision != 3 {
		t.Fatalf("revert record = %+v, %v", record, err)
	}
	if loaded, _ := Load(ctx, fixture.database, original.OperationID); loaded.RevertedByID != reverted.OperationID || loaded.ChangeType() != TypeReleaseCorrection {
		t.Fatalf("the original reports reverted_by %q, want %s", loaded.RevertedByID, reverted.OperationID)
	}

	// 已经撤销过：再撤销是 already_reverted；撤销记录本身不能被撤销；不存在的记录是 404；类型不符的记录不被撤销。
	records := fixture.recordCount()
	for name, testCase := range map[string]struct {
		kind       Kind
		original   string
		status     int
		code       string
		revisionAt int64
	}{
		"already reverted":       {kind, original.OperationID, 409, "already_reverted", 3},
		"revert of a revert":     {kind, reverted.OperationID, 409, "not_revertible", 3},
		"unknown record":         {kind, uuid.New(), 404, "not_found", 3},
		"record of another kind": {fakeKind{fixture: fixture, kind: TypeReleaseGroupChange}, original.OperationID, 409, "not_revertible", 3},
	} {
		_, err := fixture.revert(testCase.kind, testCase.original)
		if status, code := problemOf(err); status != testCase.status || code != testCase.code {
			t.Errorf("%s: error = %v (status %d, code %q), want %d %s", name, err, status, code, testCase.status, testCase.code)
		}
	}
	if fixture.recordCount() != records || fixture.revision() != 3 {
		t.Fatalf("rejected reverts changed the records (%d→%d) or the revision (%d)", records, fixture.recordCount(), fixture.revision())
	}

	// 之后又有修改：撤销被拒绝，且不覆盖新状态。
	later, err := Run(ctx, fixture.database, fixture.spec(fixture.actorID, uuid.New(), TypeReleaseCorrection, map[string]any{"n": 2}), fixture.bump(3, nil))
	if err != nil {
		t.Fatalf("later operation: %v", err)
	}
	secondOriginal, err := Run(ctx, fixture.database, fixture.spec(fixture.actorID, uuid.New(), TypeReleaseCorrection, map[string]any{"n": 3}), fixture.bump(4, nil))
	if err != nil {
		t.Fatalf("operation after the later one: %v", err)
	}
	if _, err := fixture.revert(kind, later.OperationID); err == nil {
		t.Fatal("an operation that is no longer the latest edit was reverted")
	} else if status, code := problemOf(err); status != 409 || code != "revert_conflict" {
		t.Fatalf("revert of a superseded operation: error = %v (status %d, code %q), want 409 revert_conflict", err, status, code)
	}
	if fixture.revision() != 5 {
		t.Fatalf("a refused revert changed the revision to %d", fixture.revision())
	}
	if _, err := fixture.revert(kind, secondOriginal.OperationID); err != nil {
		t.Fatalf("revert of the latest operation: %v", err)
	}
}

// 对同一条记录的并发撤销串行：恰好一个成功，其余得到 already_reverted（不是 revert_conflict）。
func TestPostgreSQLConcurrentRevertsOfTheSameOperation(t *testing.T) {
	fixture := newRunFixture(t)
	kind := fakeKind{fixture: fixture, kind: TypeReleaseCorrection}
	original, err := Run(context.Background(), fixture.database, fixture.spec(fixture.actorID, uuid.New(), TypeReleaseCorrection, map[string]any{"n": 1}), fixture.bump(1, nil))
	if err != nil {
		t.Fatalf("original operation: %v", err)
	}

	holder, err := fixture.database.Begin()
	if err != nil {
		t.Fatalf("begin lock holder: %v", err)
	}
	t.Cleanup(func() { _ = holder.Rollback() })
	// 测试事务先锁住原记录：所有撤销请求都卡在“锁住原记录”这一步，释放之后才依次进入，因此一定真正重叠。
	if _, err := holder.Exec(`SELECT id FROM operations WHERE id=$1::uuid FOR UPDATE`, original.OperationID); err != nil {
		t.Fatalf("lock the original record: %v", err)
	}
	const requests = 6
	type outcome struct {
		result Result
		err    error
	}
	outcomes := make(chan outcome, requests)
	for range requests {
		go func() {
			result, err := fixture.revert(kind, original.OperationID)
			outcomes <- outcome{result, err}
		}()
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := fixture.database.QueryRow(`SELECT COUNT(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND pid<>pg_backend_pid()`).Scan(&waiting); err != nil {
			t.Fatalf("count lock waiters: %v", err)
		}
		if waiting >= requests {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d reverts are waiting on the original record", waiting)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := holder.Rollback(); err != nil {
		t.Fatalf("release the original record: %v", err)
	}

	succeeded, already := 0, 0
	for range requests {
		select {
		case result := <-outcomes:
			status, code := problemOf(result.err)
			switch {
			case result.err == nil:
				succeeded++
			case status == 409 && code == "already_reverted":
				already++
			default:
				t.Fatalf("a concurrent revert failed with %v (status %d, code %q)", result.err, status, code)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("the concurrent reverts did not finish")
		}
	}
	if succeeded != 1 || already != requests-1 || fixture.revision() != 3 || fixture.recordCount() != 2 {
		t.Fatalf("%d succeeded, %d already reverted, revision %d, %d records; want one revert and %d refusals", succeeded, already, fixture.revision(), fixture.recordCount(), requests-1)
	}
}

func TestPostgreSQLLoadForUpdateReportsMissingRecords(t *testing.T) {
	fixture := newRunFixture(t)
	tx, err := fixture.database.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	for _, id := range []string{uuid.New(), "not-a-uuid", ""} {
		if _, err := LoadForUpdate(context.Background(), tx, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("LoadForUpdate(%q) error = %v, want ErrNotFound", id, err)
		}
	}
}
