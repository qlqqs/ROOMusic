package governance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func mergedSourcesRead(t *testing.T, f *artistFixture, id string) []ArtistMergedSource {
	t.Helper()
	before, count := f.intentSnapshot(), f.count()
	result, err := ReadArtistMergedSources(context.Background(), f.database, id)
	if err != nil {
		t.Fatal(err)
	}
	if result.Items == nil || before != f.intentSnapshot() || count != f.count() {
		t.Fatal("读取改变治理状态或返回 null")
	}
	return result.Items
}
func mergedSourcesAssert(t *testing.T, items []ArtistMergedSource, id, operationID string, revision int64, keys ...string) {
	t.Helper()
	sort.Strings(keys)
	for _, item := range items {
		if item.ArtistID == id {
			if item.OperationID != operationID || item.Revision != revision || item.Name == "" || item.MergedAt.IsZero() || len(item.NameKeyIDs) != len(keys) || (len(keys) > 0 && !reflect.DeepEqual(item.NameKeyIDs, keys)) {
				t.Fatalf("旧身份恢复提示错误: %+v, operation=%s keys=%v", item, operationID, keys)
			}
			if item.NameKeyIDs == nil {
				t.Fatal("空键必须 []")
			}
			return
		}
	}
	t.Fatalf("未找到旧身份 %s: %+v", id, items)
}
func mergedSourcesMerge(t *testing.T, f *artistFixture, target, source string) operations.Result {
	t.Helper()
	r := ArtistMergeRequest{ArtistID: target, ExpectedRevision: f.revision(target), SourceArtistID: source, SourceExpectedRevision: f.revision(source)}
	result, err := f.run(operations.TypeArtistMerge, uuid.New(), r.Fingerprint(), f.governor.MergeExecutor(r))
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func mergedSourcesSplit(t *testing.T, f *artistFixture, source string, scope ArtistSplitScope, target ArtistSplitTarget) string {
	t.Helper()
	preview, err := PreviewArtistSplit(context.Background(), f.database, source, scope)
	if err != nil {
		t.Fatal(err)
	}
	r := ArtistSplitRequest{ArtistID: source, ExpectedRevision: f.revision(source), ArtistSplitScope: scope, Basis: preview.Basis, Target: target}
	result, err := f.run(operations.TypeArtistSplit, uuid.New(), r.Fingerprint(), f.governor.SplitExecutor(r))
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Target struct {
			ID string `json:"id"`
		} `json:"target"`
	}
	if err := json.Unmarshal(result.Response, &response); err != nil {
		t.Fatal(err)
	}
	return response.Target.ID
}

func TestPostgreSQLArtistMergedSourcesChainRestoreAndRemerge(t *testing.T) {
	f := newArtistFixture(t)
	if items := mergedSourcesRead(t, f, f.alpha); len(items) != 0 {
		t.Fatal(items)
	}
	first := f.merge()
	mergedSourcesAssert(t, mergedSourcesRead(t, f, strings.ToUpper(f.alpha)), f.beta, first.OperationID, 2, f.keyBeta)
	third := uuid.New()
	f.exec(`INSERT INTO artists(id,initial_name) VALUES($1::uuid,'第三位')`, third)
	second := mergedSourcesMerge(t, f, third, f.alpha)
	items := mergedSourcesRead(t, f, third)
	if len(items) != 2 {
		t.Fatal(items)
	}
	mergedSourcesAssert(t, items, f.beta, first.OperationID, 2, f.keyBeta)
	mergedSourcesAssert(t, items, f.alpha, second.OperationID, 3, f.keyAlpha, f.keyBeta)
	if !reflect.DeepEqual(items, mergedSourcesRead(t, f, f.beta)) {
		t.Fatal("旧链接未解析到 canonical")
	}
	// 展示摘要不承担恢复证据，改坏 changes 不改变类型化前后态的结论。
	f.exec(`UPDATE operations SET after_state=jsonb_set(after_state,'{changes}','{"source":{"id":"wrong","name":"误导"}}'::jsonb) WHERE id=$1::uuid`, first.OperationID)
	mergedSourcesAssert(t, mergedSourcesRead(t, f, third), f.beta, first.OperationID, 2, f.keyBeta)
	scope := ArtistSplitScope{Mode: "aliases", NameKeyIDs: []string{f.keyBeta}}
	restored := mergedSourcesSplit(t, f, third, scope, ArtistSplitTarget{Kind: "restore", ArtistID: f.beta, ExpectedRevision: f.revision(f.beta)})
	if restored != f.beta {
		t.Fatal("没有恢复旧身份")
	}
	items = mergedSourcesRead(t, f, third)
	if len(items) != 1 {
		t.Fatal("已恢复的旧身份仍在列表", items)
	}
	mergedSourcesAssert(t, items, f.alpha, second.OperationID, 3, f.keyAlpha)
	// 再次合并同一身份必须返回新事件，不沿已经清除的旧 redirect 追到上一次。
	again := mergedSourcesMerge(t, f, third, f.beta)
	items = mergedSourcesRead(t, f, third)
	mergedSourcesAssert(t, items, f.beta, again.OperationID, 4, f.keyBeta)
	mergedSourcesAssert(t, items, f.alpha, second.OperationID, 3, f.keyAlpha, f.keyBeta)
}

func TestPostgreSQLArtistMergedSourcesBindingChangesAndEmptyKeys(t *testing.T) {
	f := newArtistFixture(t)
	merged := f.merge()
	aliases := ArtistSplitScope{Mode: "aliases", NameKeyIDs: []string{f.keyBeta}}
	newID := mergedSourcesSplit(t, f, f.alpha, aliases, ArtistSplitTarget{Kind: "new"})
	items := mergedSourcesRead(t, f, f.alpha)
	mergedSourcesAssert(t, items, f.beta, merged.OperationID, 2)
	raw, err := json.Marshal(items)
	if err != nil || !strings.Contains(string(raw), `"name_key_ids":[]`) {
		t.Fatalf("空键 JSON %s %v", raw, err)
	}
	// initial_artist 仍是 beta，不得据此把已拆给第三人的 key 放回默认选项。
	if f.trackArtist(f.trackB) != newID {
		t.Fatal("未真正移动名字路由")
	}
	// 恢复旧 beta 时允许选择明确的其他键；再次合并只提示这次移入的新键。
	scope := ArtistSplitScope{Mode: "aliases", NameKeyIDs: []string{f.keyAlpha}}
	mergedSourcesSplit(t, f, f.alpha, scope, ArtistSplitTarget{Kind: "restore", ArtistID: f.beta, ExpectedRevision: 2})
	if len(mergedSourcesRead(t, f, f.alpha)) != 0 {
		t.Fatal("恢复后的身份仍被列出")
	}
	again := mergedSourcesMerge(t, f, f.alpha, f.beta)
	mergedSourcesAssert(t, mergedSourcesRead(t, f, f.alpha), f.beta, again.OperationID, 4, f.keyAlpha)
}

func TestPostgreSQLArtistMergedSourcesScopedMerge(t *testing.T) {
	f := newArtistFixture(t)
	scoped := mergedSourcesSplit(t, f, f.alpha, ArtistSplitScope{Mode: "albums", NameKeyIDs: []string{f.keyAlpha}, GroupIDs: []string{f.first.groupID}}, ArtistSplitTarget{Kind: "new"})
	merged := mergedSourcesMerge(t, f, f.alpha, scoped)
	// 只有作用域覆盖、没有全局绑定的合并也必须留下名字键提示。
	mergedSourcesAssert(t, mergedSourcesRead(t, f, f.alpha), scoped, merged.OperationID, 2, f.keyAlpha)
}

func TestPostgreSQLArtistMergedSourcesReadinessAndUnknown(t *testing.T) {
	f := newArtistFixture(t)
	if _, err := ReadArtistMergedSources(context.Background(), f.database, uuid.New()); !errors.Is(err, catalog.ErrArtistNotFound) {
		t.Fatal(err)
	}
	f.ready(false)
	if _, err := ReadArtistMergedSources(context.Background(), f.database, uuid.New()); !errors.Is(err, catalog.ErrArtistCatalogPreparing) {
		t.Fatal(err)
	}
	if _, err := ReadArtistMergedSources(context.Background(), f.database, "bad"); err == nil {
		t.Fatal("非法 ID 未拒绝")
	}
}

type mergedSourcesAbortedBeginner struct{ conn *sql.Conn }

func (b mergedSourcesAbortedBeginner) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	tx, err := b.conn.BeginTx(ctx, opts)
	if err == nil {
		_, _ = tx.ExecContext(ctx, "SELECT 1/0")
	}
	return tx, err
}
func TestPostgreSQLArtistMergedSourcesLocalJIT(t *testing.T) {
	f := newArtistFixture(t)
	ctx := context.Background()
	conn, err := f.database.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var before string
	var pid int
	if err := conn.QueryRowContext(ctx, `SELECT current_setting('jit'),pg_backend_pid()`).Scan(&before, &pid); err != nil {
		t.Fatal(err)
	}
	if before != "on" {
		t.Fatalf("隔离库应默认 JIT on，实际 %s", before)
	}
	for _, fail := range []bool{false, true} {
		expected := errors.New("受控读取故障")
		_, err := catalog.ReadWithLocalJITOff(ctx, conn, func(tx *sql.Tx) (ArtistMergedSources, error) {
			var jit, readOnly, isolation string
			var innerPID int
			if err := tx.QueryRowContext(ctx, `SELECT current_setting('jit'),current_setting('transaction_read_only'),current_setting('transaction_isolation'),pg_backend_pid()`).Scan(&jit, &readOnly, &isolation, &innerPID); err != nil {
				return ArtistMergedSources{}, err
			}
			if jit != "off" || readOnly != "on" || isolation != "repeatable read" || innerPID != pid {
				t.Fatalf("错误事务配置 %s/%s/%s/%d", jit, readOnly, isolation, innerPID)
			}
			if fail {
				return ArtistMergedSources{}, expected
			}
			return readArtistMergedSources(ctx, tx, f.alpha)
		})
		if (fail && !errors.Is(err, expected)) || (!fail && err != nil) {
			t.Fatal(err)
		}
		assertMergedSourcesJITRestored(t, conn, before, pid)
	}
	called := false
	if _, err := catalog.ReadWithLocalJITOff(ctx, mergedSourcesAbortedBeginner{conn}, func(*sql.Tx) (bool, error) { called = true; return true, nil }); err == nil || called {
		t.Fatalf("配置失败未回滚短路: called=%v err=%v", called, err)
	}
	assertMergedSourcesJITRestored(t, conn, before, pid)
	if _, err := ReadArtistMergedSources(ctx, conn, f.alpha); err != nil {
		t.Fatal(err)
	}
	assertMergedSourcesJITRestored(t, conn, before, pid)
}
func assertMergedSourcesJITRestored(t *testing.T, conn *sql.Conn, before string, pid int) {
	t.Helper()
	var after string
	var afterPID int
	if err := conn.QueryRowContext(context.Background(), `SELECT current_setting('jit'),pg_backend_pid()`).Scan(&after, &afterPID); err != nil || after != before || afterPID != pid {
		t.Fatalf("事务后配置泄漏或换连接: %s/%d %v", after, afterPID, err)
	}
}
