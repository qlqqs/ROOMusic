package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/postgres"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// runFixture 是隔离 schema 的已迁移测试库、两个操作者和一个合成发行（只用来承载 edit_revision）；不读任何文件。
type runFixture struct {
	t         *testing.T
	database  *sql.DB
	actorID   string
	otherID   string
	releaseID string
}

func newRunFixture(t *testing.T) *runFixture {
	t.Helper()
	scopedURL := postgrestest.NewSchema(t, "roomusic_test_")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	database, err := postgres.Open(ctx, scopedURL)
	if err != nil {
		t.Fatalf("open isolated operations database: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close isolated operations database: %v", err)
		}
	})
	fixture := &runFixture{t: t, database: database, actorID: uuid.New(), otherID: uuid.New(), releaseID: uuid.New()}
	groupID := uuid.New()
	fixture.exec(`INSERT INTO users(id,username,password_hash) VALUES($1::uuid,'admin','x'),($2::uuid,'other','x')`, fixture.actorID, fixture.otherID)
	fixture.exec(`INSERT INTO release_groups(id) VALUES($1::uuid)`, groupID)
	fixture.exec(`INSERT INTO releases(id,group_id,title,artist) VALUES($1::uuid,$2::uuid,'专辑','艺术家')`, fixture.releaseID, groupID)
	return fixture
}

func (fixture *runFixture) exec(statement string, arguments ...any) {
	fixture.t.Helper()
	if _, err := fixture.database.Exec(statement, arguments...); err != nil {
		fixture.t.Fatalf("exec %q: %v", statement, err)
	}
}

func (fixture *runFixture) revision() int64 {
	fixture.t.Helper()
	var revision int64
	if err := fixture.database.QueryRow(`SELECT edit_revision FROM releases WHERE id = $1::uuid`, fixture.releaseID).Scan(&revision); err != nil {
		fixture.t.Fatalf("read release revision: %v", err)
	}
	return revision
}

func (fixture *runFixture) recordCount() int {
	fixture.t.Helper()
	var count int
	if err := fixture.database.QueryRow(`SELECT COUNT(*) FROM operations`).Scan(&count); err != nil {
		fixture.t.Fatalf("count operation records: %v", err)
	}
	return count
}

// bump 是测试执行器：对发行做一次带预期版本的比较更新，返回合法的结果。calls 统计真正执行的次数。
func (fixture *runFixture) bump(expected int64, calls *atomic.Int32) Executor {
	return func(ctx context.Context, tx *sql.Tx, operationID string) (Outcome, error) {
		if calls != nil {
			calls.Add(1)
		}
		result, err := tx.ExecContext(ctx, `UPDATE releases SET edit_revision = edit_revision + 1 WHERE id = $1::uuid AND edit_revision = $2`, fixture.releaseID, expected)
		if err != nil {
			return Outcome{}, err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return Outcome{}, Conflict("revision_conflict", "发行版本已变化")
		}
		return Outcome{
			TargetType: TargetRelease, TargetID: fixture.releaseID,
			ExpectedRevision: expected, ResultRevision: expected + 1,
			Before:   map[string]any{"revision": expected},
			After:    map[string]any{"revision": expected + 1},
			Response: map[string]any{"operation_id": operationID, "revision": expected + 1},
		}, nil
	}
}

func (fixture *runFixture) spec(actorID, key, operationType string, fingerprintParts map[string]any) Spec {
	return Spec{ActorID: actorID, Type: operationType, Key: key, Fingerprint: Fingerprint(fingerprintParts), RequestID: "request-" + key[:8]}
}

func decodeResponse(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode response %q: %v", raw, err)
	}
	return decoded
}

func TestPostgreSQLRunRecordsReplaysAndRejectsConflictingReuse(t *testing.T) {
	fixture := newRunFixture(t)
	ctx := context.Background()
	key := uuid.New()
	parts := map[string]any{"type": TypeReleaseCorrection, "target": fixture.releaseID, "revision": 1, "changes": "a"}
	var calls atomic.Int32

	first, err := Run(ctx, fixture.database, fixture.spec(fixture.actorID, key, TypeReleaseCorrection, parts), fixture.bump(1, &calls))
	if err != nil || first.Replayed || first.OperationID == "" {
		t.Fatalf("first Run = %+v, %v; want a fresh success", first, err)
	}
	if got := decodeResponse(t, first.Response); got["operation_id"] != first.OperationID || got["revision"] != float64(2) {
		t.Fatalf("first response = %v, want operation_id %s and revision 2", got, first.OperationID)
	}
	if fixture.revision() != 2 || fixture.recordCount() != 1 || calls.Load() != 1 {
		t.Fatalf("after first Run: revision %d, records %d, executions %d; want 2, 1, 1", fixture.revision(), fixture.recordCount(), calls.Load())
	}
	var actor, kind, targetType, target, requestID string
	var expected, result int64
	var before, after, response string
	if err := fixture.database.QueryRow(`SELECT actor_id::text, operation_type, target_type, target_id::text, expected_revision, result_revision,
			before_state::text, after_state::text, response::text, request_id FROM operations WHERE id = $1::uuid`, first.OperationID).
		Scan(&actor, &kind, &targetType, &target, &expected, &result, &before, &after, &response, &requestID); err != nil {
		t.Fatalf("read operation record: %v", err)
	}
	if actor != fixture.actorID || kind != TypeReleaseCorrection || targetType != TargetRelease || target != fixture.releaseID ||
		expected != 1 || result != 2 || !strings.Contains(before, `"revision": 1`) || !strings.Contains(after, `"revision": 2`) ||
		!strings.Contains(response, first.OperationID) || requestID != "request-"+key[:8] {
		t.Fatalf("recorded fields are wrong: %s %s %s %s %d %d %s %s %s %s", actor, kind, targetType, target, expected, result, before, after, response, requestID)
	}

	// 同键同义：重放已存响应，不执行、不改版本、不新增记录；键的大小写不影响。
	replayed, err := Run(ctx, fixture.database, fixture.spec(fixture.actorID, strings.ToUpper(key), TypeReleaseCorrection, parts), fixture.bump(1, &calls))
	if err != nil || !replayed.Replayed || replayed.OperationID != first.OperationID {
		t.Fatalf("replay = %+v, %v; want the first operation replayed", replayed, err)
	}
	if got, want := decodeResponse(t, replayed.Response), decodeResponse(t, first.Response); got["operation_id"] != want["operation_id"] || got["revision"] != want["revision"] {
		t.Fatalf("replayed response %v differs from the original %v", got, want)
	}
	if fixture.revision() != 2 || fixture.recordCount() != 1 || calls.Load() != 1 {
		t.Fatalf("after replay: revision %d, records %d, executions %d; want 2, 1, 1", fixture.revision(), fixture.recordCount(), calls.Load())
	}

	// 同键异义：不同内容、不同操作类型、不同目标都是冲突，什么都不改。
	for name, conflicting := range map[string]Spec{
		"another revision": fixture.spec(fixture.actorID, key, TypeReleaseCorrection, map[string]any{"type": TypeReleaseCorrection, "target": fixture.releaseID, "revision": 2, "changes": "a"}),
		"another content":  fixture.spec(fixture.actorID, key, TypeReleaseCorrection, map[string]any{"type": TypeReleaseCorrection, "target": fixture.releaseID, "revision": 1, "changes": "b"}),
		"another type":     fixture.spec(fixture.actorID, key, TypeReleaseGroupChange, map[string]any{"type": TypeReleaseGroupChange, "target": fixture.releaseID, "revision": 1}),
		"another target":   fixture.spec(fixture.actorID, key, TypeReleaseCorrection, map[string]any{"type": TypeReleaseCorrection, "target": uuid.New(), "revision": 1, "changes": "a"}),
	} {
		_, err := Run(ctx, fixture.database, conflicting, fixture.bump(2, &calls))
		if status, code, _ := Problem(err); status != 409 || code != "idempotency_conflict" {
			t.Errorf("%s: Run error = %v (status %d, code %q), want 409 idempotency_conflict", name, err, status, code)
		}
	}
	if fixture.revision() != 2 || fixture.recordCount() != 1 || calls.Load() != 1 {
		t.Fatalf("after conflicting reuse: revision %d, records %d, executions %d; want 2, 1, 1", fixture.revision(), fixture.recordCount(), calls.Load())
	}

	// 键的作用域是操作者：另一个操作者可以用同一个键做自己的操作。
	other, err := Run(ctx, fixture.database, fixture.spec(fixture.otherID, key, TypeReleaseCorrection, parts), fixture.bump(2, &calls))
	if err != nil || other.Replayed || other.OperationID == first.OperationID {
		t.Fatalf("another actor's Run with the same key = %+v, %v; want an independent success", other, err)
	}
	if fixture.revision() != 3 || fixture.recordCount() != 2 || calls.Load() != 2 {
		t.Fatalf("after another actor: revision %d, records %d, executions %d; want 3, 2, 2", fixture.revision(), fixture.recordCount(), calls.Load())
	}
}

func TestPostgreSQLRunExecutesOnceForConcurrentRequestsWithTheSameKey(t *testing.T) {
	fixture := newRunFixture(t)
	key := uuid.New()
	parts := map[string]any{"type": TypeReleaseCorrection, "target": fixture.releaseID, "revision": 1}
	var calls atomic.Int32
	slow := func(ctx context.Context, tx *sql.Tx, operationID string) (Outcome, error) {
		time.Sleep(80 * time.Millisecond) // 拉宽竞争窗口：其余请求必须在锁上等待，而不是各自执行
		return fixture.bump(1, &calls)(ctx, tx, operationID)
	}

	const requests = 8
	results := make([]Result, requests)
	errs := make([]error, requests)
	var wait sync.WaitGroup
	start := make(chan struct{})
	for index := range requests {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			results[index], errs[index] = Run(context.Background(), fixture.database, fixture.spec(fixture.actorID, key, TypeReleaseCorrection, parts), slow)
		}()
	}
	close(start)
	wait.Wait()

	fresh := 0
	for index := range requests {
		if errs[index] != nil {
			t.Fatalf("request %d failed: %v", index, errs[index])
		}
		if results[index].OperationID != results[0].OperationID {
			t.Errorf("request %d recorded operation %s, want %s", index, results[index].OperationID, results[0].OperationID)
		}
		if !results[index].Replayed {
			fresh++
		}
	}
	if fresh != 1 || calls.Load() != 1 || fixture.revision() != 2 || fixture.recordCount() != 1 {
		t.Fatalf("fresh %d, executions %d, revision %d, records %d; want exactly one execution", fresh, calls.Load(), fixture.revision(), fixture.recordCount())
	}
}

func TestPostgreSQLRunRollsBackWhenExecutionFails(t *testing.T) {
	fixture := newRunFixture(t)
	ctx := context.Background()
	key := uuid.New()
	parts := map[string]any{"type": TypeReleaseCorrection, "target": fixture.releaseID, "revision": 1}
	spec := fixture.spec(fixture.actorID, key, TypeReleaseCorrection, parts)

	// 业务变化已经写下后执行器报告冲突：整体回滚，不写记录。
	_, err := Run(ctx, fixture.database, spec, func(ctx context.Context, tx *sql.Tx, operationID string) (Outcome, error) {
		if _, err := tx.ExecContext(ctx, `UPDATE releases SET edit_revision = edit_revision + 1 WHERE id = $1::uuid`, fixture.releaseID); err != nil {
			return Outcome{}, err
		}
		return Outcome{}, Conflict("revision_conflict", "发行版本已变化")
	})
	if status, code, _ := Problem(err); status != 409 || code != "revision_conflict" {
		t.Fatalf("Run error = %v (status %d, code %q), want 409 revision_conflict", err, status, code)
	}
	if fixture.revision() != 1 || fixture.recordCount() != 0 {
		t.Fatalf("after a failed execution: revision %d, records %d; want 1, 0", fixture.revision(), fixture.recordCount())
	}

	// 执行器返回非分类错误：500，同样回滚，错误里的细节不出现在响应说明里。
	_, err = Run(ctx, fixture.database, spec, func(ctx context.Context, tx *sql.Tx, operationID string) (Outcome, error) {
		if _, err := tx.ExecContext(ctx, `UPDATE releases SET edit_revision = edit_revision + 1 WHERE id = $1::uuid`, fixture.releaseID); err != nil {
			return Outcome{}, err
		}
		return Outcome{}, errors.New("secret internal detail")
	})
	status, code, message := Problem(err)
	if status != 500 || code != "operation_failed" || strings.Contains(message, "secret") || !errors.Is(err, ErrOperationFailed) {
		t.Fatalf("Run error = %v (status %d, code %q, message %q), want an opaque 500 operation_failed", err, status, code, message)
	}
	if fixture.revision() != 1 || fixture.recordCount() != 0 {
		t.Fatalf("after an executor error: revision %d, records %d; want 1, 0", fixture.revision(), fixture.recordCount())
	}

	// 失败不占用幂等键：同一个键之后可以成功执行一次。
	var calls atomic.Int32
	if result, err := Run(ctx, fixture.database, spec, fixture.bump(1, &calls)); err != nil || result.Replayed || fixture.revision() != 2 {
		t.Fatalf("Run after failures = %+v, %v (revision %d); want a fresh success", result, err, fixture.revision())
	}
}

func TestPostgreSQLRunRollsBackWhenTheRecordCannotBeWritten(t *testing.T) {
	fixture := newRunFixture(t)
	ctx := context.Background()
	parts := map[string]any{"type": TypeReleaseCorrection, "target": fixture.releaseID, "revision": 1}

	// 触发器让记录写入失败：业务变化与记录一起回滚，响应是不含内部细节的 500。
	fixture.exec(`CREATE FUNCTION reject_operation_record() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'record write refused'; END $$`)
	fixture.exec(`CREATE TRIGGER reject_operation_record BEFORE INSERT ON operations FOR EACH ROW EXECUTE FUNCTION reject_operation_record()`)
	key := uuid.New()
	_, err := Run(ctx, fixture.database, fixture.spec(fixture.actorID, key, TypeReleaseCorrection, parts), fixture.bump(1, nil))
	status, code, message := Problem(err)
	if status != 500 || code != "operation_failed" || strings.Contains(message, "refused") {
		t.Fatalf("Run error = %v (status %d, code %q, message %q), want an opaque 500 operation_failed", err, status, code, message)
	}
	if fixture.revision() != 1 || fixture.recordCount() != 0 {
		t.Fatalf("after a refused record: revision %d, records %d; want 1, 0", fixture.revision(), fixture.recordCount())
	}

	// 拿掉触发器后同一个键可以正常执行：失败的尝试没有留下任何痕迹。
	fixture.exec(`DROP TRIGGER reject_operation_record ON operations`)
	if result, err := Run(ctx, fixture.database, fixture.spec(fixture.actorID, key, TypeReleaseCorrection, parts), fixture.bump(1, nil)); err != nil || result.Replayed {
		t.Fatalf("Run after the trigger was removed = %+v, %v; want a fresh success", result, err)
	}
	if fixture.revision() != 2 || fixture.recordCount() != 1 {
		t.Fatalf("after the retry: revision %d, records %d; want 2, 1", fixture.revision(), fixture.recordCount())
	}
}

func TestPostgreSQLRunRefusesRecordsThatBreakTheRevisionContract(t *testing.T) {
	fixture := newRunFixture(t)
	spec := fixture.spec(fixture.actorID, uuid.New(), TypeReleaseCorrection, map[string]any{"target": fixture.releaseID})
	_, err := Run(context.Background(), fixture.database, spec, func(ctx context.Context, tx *sql.Tx, operationID string) (Outcome, error) {
		if _, err := tx.ExecContext(ctx, `UPDATE releases SET edit_revision = edit_revision + 1 WHERE id = $1::uuid`, fixture.releaseID); err != nil {
			return Outcome{}, err
		}
		return Outcome{TargetType: TargetRelease, TargetID: fixture.releaseID, ExpectedRevision: 1, ResultRevision: 5,
			Before: map[string]any{}, After: map[string]any{}, Response: map[string]any{}}, nil
	})
	if !errors.Is(err, ErrOperationFailed) {
		t.Fatalf("Run error = %v, want ErrOperationFailed for a result revision that is not expected + 1", err)
	}
	if fixture.revision() != 1 || fixture.recordCount() != 0 {
		t.Fatalf("revision %d, records %d; want 1, 0 after the refused record", fixture.revision(), fixture.recordCount())
	}
}

// 修正行在执行器里写入，引用的操作记录在执行器之后才插入：外键延迟到提交时检查，提交时记录已经存在。
func TestPostgreSQLRunLetsRowsReferenceTheOperationBeingRecorded(t *testing.T) {
	fixture := newRunFixture(t)
	ctx := context.Background()
	correct := func(operationID string) Executor {
		return func(ctx context.Context, tx *sql.Tx, _ string) (Outcome, error) {
			if _, err := tx.ExecContext(ctx, `INSERT INTO release_corrections(release_id, field, mode, text_value, operation_id)
				VALUES($1::uuid, 'title', 'set', '修正后的标题', $2::uuid)`, fixture.releaseID, operationID); err != nil {
				return Outcome{}, err
			}
			return fixture.bump(1, nil)(ctx, tx, operationID)
		}
	}
	// 引用了不存在的操作：提交时外键失败，业务变化回滚。
	_, err := Run(ctx, fixture.database, fixture.spec(fixture.actorID, uuid.New(), TypeReleaseCorrection, map[string]any{"a": 1}), correct(uuid.New()))
	if !errors.Is(err, ErrOperationFailed) {
		t.Fatalf("Run error = %v, want ErrOperationFailed when a row references an operation that was never recorded", err)
	}
	var corrections int
	_ = fixture.database.QueryRow(`SELECT COUNT(*) FROM release_corrections`).Scan(&corrections)
	if corrections != 0 || fixture.revision() != 1 || fixture.recordCount() != 0 {
		t.Fatalf("after the refused commit: corrections %d, revision %d, records %d; want 0, 1, 0", corrections, fixture.revision(), fixture.recordCount())
	}

	// 引用本次操作的 ID（执行器拿到的 operationID）：提交成功。
	spec := fixture.spec(fixture.actorID, uuid.New(), TypeReleaseCorrection, map[string]any{"a": 2})
	result, err := Run(ctx, fixture.database, spec, func(ctx context.Context, tx *sql.Tx, operationID string) (Outcome, error) {
		return correct(operationID)(ctx, tx, operationID)
	})
	if err != nil {
		t.Fatalf("Run with a row referencing the operation being recorded: %v", err)
	}
	var referenced string
	if err := fixture.database.QueryRow(`SELECT operation_id::text FROM release_corrections WHERE release_id = $1::uuid AND field = 'title'`, fixture.releaseID).Scan(&referenced); err != nil || referenced != result.OperationID {
		t.Fatalf("correction references operation %q (%v), want %s", referenced, err, result.OperationID)
	}
}

func TestPostgreSQLRevertRecordsLinkToTheirOriginal(t *testing.T) {
	fixture := newRunFixture(t)
	ctx := context.Background()
	original, err := Run(ctx, fixture.database, fixture.spec(fixture.actorID, uuid.New(), TypeReleaseCorrection, map[string]any{"n": 1}), fixture.bump(1, nil))
	if err != nil {
		t.Fatalf("original operation: %v", err)
	}
	revertExecutor := func(revertsID string, expected int64) Executor {
		return func(ctx context.Context, tx *sql.Tx, operationID string) (Outcome, error) {
			outcome, err := fixture.bump(expected, nil)(ctx, tx, operationID)
			outcome.RevertsID = revertsID
			return outcome, err
		}
	}
	// 一个类型为 revert 的记录必须指向原操作；一般记录不能带 reverts。
	if _, err := Run(ctx, fixture.database, fixture.spec(fixture.actorID, uuid.New(), TypeRevert, map[string]any{"n": 2}), fixture.bump(2, nil)); !errors.Is(err, ErrOperationFailed) {
		t.Fatalf("a revert record without an original: error = %v, want ErrOperationFailed", err)
	}
	if _, err := Run(ctx, fixture.database, fixture.spec(fixture.actorID, uuid.New(), TypeReleaseCorrection, map[string]any{"n": 3}), revertExecutor(original.OperationID, 2)); !errors.Is(err, ErrOperationFailed) {
		t.Fatalf("a non-revert record pointing at an original: error = %v, want ErrOperationFailed", err)
	}
	reverted, err := Run(ctx, fixture.database, fixture.spec(fixture.actorID, uuid.New(), TypeRevert, map[string]any{"n": 4}), revertExecutor(original.OperationID, 2))
	if err != nil {
		t.Fatalf("revert record: %v", err)
	}
	// 一条操作最多被撤销一次（唯一约束是最后一道防线，业务上由执行器先判断）。
	if _, err := Run(ctx, fixture.database, fixture.spec(fixture.actorID, uuid.New(), TypeRevert, map[string]any{"n": 5}), revertExecutor(original.OperationID, 3)); !errors.Is(err, ErrOperationFailed) {
		t.Fatalf("a second revert of the same operation: error = %v, want ErrOperationFailed", err)
	}

	loaded, err := Load(ctx, fixture.database, original.OperationID)
	if err != nil || loaded.RevertedByID != reverted.OperationID || loaded.RevertsID != "" || loaded.Type != TypeReleaseCorrection {
		t.Fatalf("Load(original) = %+v, %v; want it reverted by %s", loaded, err, reverted.OperationID)
	}
	revert, err := Load(ctx, fixture.database, reverted.OperationID)
	if err != nil || revert.RevertsID != original.OperationID || revert.RevertedByID != "" || revert.Type != TypeRevert ||
		revert.ExpectedRevision != 2 || revert.ResultRevision != 3 || revert.ActorID != fixture.actorID || revert.CreatedAt.IsZero() {
		t.Fatalf("Load(revert) = %+v, %v", revert, err)
	}
	if _, err := Load(ctx, fixture.database, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load(unknown) error = %v, want ErrNotFound", err)
	}
	if _, err := Load(ctx, fixture.database, "not-a-uuid"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load(malformed) error = %v, want ErrNotFound", err)
	}
}

func TestPostgreSQLListOrdersPagesAndFiltersByTarget(t *testing.T) {
	fixture := newRunFixture(t)
	ctx := context.Background()
	secondRelease, secondGroup := uuid.New(), uuid.New()
	fixture.exec(`INSERT INTO release_groups(id) VALUES($1::uuid)`, secondGroup)
	fixture.exec(`INSERT INTO releases(id,group_id,title,artist) VALUES($1::uuid,$2::uuid,'另一张专辑','艺术家')`, secondRelease, secondGroup)

	var ids []string
	for index := range 5 {
		result, err := Run(ctx, fixture.database, fixture.spec(fixture.actorID, uuid.New(), TypeReleaseCorrection, map[string]any{"n": index}), fixture.bump(int64(index+1), nil))
		if err != nil {
			t.Fatalf("operation %d: %v", index, err)
		}
		ids = append(ids, result.OperationID)
	}
	_, err := Run(ctx, fixture.database, fixture.spec(fixture.actorID, uuid.New(), TypeReleaseCorrection, map[string]any{"other": true}),
		func(ctx context.Context, tx *sql.Tx, operationID string) (Outcome, error) {
			if _, err := tx.ExecContext(ctx, `UPDATE releases SET edit_revision = 2 WHERE id = $1::uuid`, secondRelease); err != nil {
				return Outcome{}, err
			}
			return Outcome{TargetType: TargetRelease, TargetID: secondRelease, ExpectedRevision: 1, ResultRevision: 2,
				Before: map[string]any{}, After: map[string]any{}, Response: map[string]any{"operation_id": operationID}}, nil
		})
	if err != nil {
		t.Fatalf("operation on the second release: %v", err)
	}

	all, err := List(ctx, fixture.database, ListParams{Page: 1, PageSize: 100})
	if err != nil || all.Total != 6 || len(all.Items) != 6 {
		t.Fatalf("List(all) = total %d, %d items, %v; want 6", all.Total, len(all.Items), err)
	}
	for index := 1; index < len(all.Items); index++ {
		if all.Items[index].CreatedAt.After(all.Items[index-1].CreatedAt) {
			t.Fatalf("items are not newest first at %d: %v then %v", index, all.Items[index-1].CreatedAt, all.Items[index].CreatedAt)
		}
	}
	if all.Items[0].TargetID != secondRelease {
		t.Fatalf("the most recent operation targets %s, want the second release %s", all.Items[0].TargetID, secondRelease)
	}

	filtered, err := List(ctx, fixture.database, ListParams{TargetType: TargetRelease, TargetID: fixture.releaseID, Page: 1, PageSize: 100})
	if err != nil || filtered.Total != 5 || len(filtered.Items) != 5 {
		t.Fatalf("List(filtered) = total %d, %d items, %v; want 5", filtered.Total, len(filtered.Items), err)
	}
	for position, item := range filtered.Items {
		if want := ids[len(ids)-1-position]; item.ID != want {
			t.Errorf("filtered item %d = %s, want %s (newest first)", position, item.ID, want)
		}
	}

	pageTwo, err := List(ctx, fixture.database, ListParams{TargetType: TargetRelease, TargetID: fixture.releaseID, Page: 2, PageSize: 2})
	if err != nil || pageTwo.Total != 5 || len(pageTwo.Items) != 2 || pageTwo.Items[0].ID != ids[2] || pageTwo.Items[1].ID != ids[1] {
		t.Fatalf("List(page 2) = %+v, %v; want the third and second newest of five", pageTwo, err)
	}
	beyond, err := List(ctx, fixture.database, ListParams{Page: 9, PageSize: 10})
	if err != nil || beyond.Total != 6 || beyond.Items == nil || len(beyond.Items) != 0 {
		t.Fatalf("List(beyond the end) = %+v, %v; want total 6 and a non-nil empty page", beyond, err)
	}
	none, err := List(ctx, fixture.database, ListParams{TargetType: TargetRelease, TargetID: uuid.New(), Page: 1, PageSize: 10})
	if err != nil || none.Total != 0 || none.Items == nil || len(none.Items) != 0 {
		t.Fatalf("List(unknown target) = %+v, %v; want an empty page", none, err)
	}
}
