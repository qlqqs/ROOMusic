package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

const curationBase = "/api/v1/admin/curation"

type curationFixture struct {
	*operationsEnvironment
	writer *curation.Writer
}

func newCurationFixture(t *testing.T) *curationFixture {
	t.Helper()
	e := newOperationsEnvironment(t)
	return &curationFixture{e, curation.NewWriter(e.application.database)}
}

func (f *curationFixture) enable(t *testing.T) {
	t.Helper()
	if _, err := f.writer.SetSettings(t.Context(), f.admin.id, true); err != nil {
		t.Fatal(err)
	}
}

func autoIntent(value any) catalog.CorrectionIntent {
	if value == nil {
		return catalog.CorrectionIntent{Mode: "null"}
	}
	return catalog.CorrectionIntent{Mode: "set", Value: value}
}

func autoRequest(id, provider, batch string, values map[string]any) curation.Request {
	fields := map[string]catalog.CorrectionIntent{}
	for field, value := range values {
		fields[field] = autoIntent(value)
	}
	return curation.Request{ReleaseID: id, Source: curation.Source{Kind: "source", Provider: provider, Basis: "合成标签匹配"}, BatchID: batch, Key: uuid.New(), Fields: fields}
}

func (f *curationFixture) apply(t *testing.T, id, provider, batch string, values map[string]any) string {
	t.Helper()
	r, err := f.writer.Apply(t.Context(), autoRequest(id, provider, batch, values))
	if err != nil || !uuid.Valid(r.OperationID) || len(r.Written) != len(values) || len(r.Skipped) != 0 {
		t.Fatalf("自动写入 %v：%+v, %v", values, r, err)
	}
	return r.OperationID
}

func (f *curationFixture) undo(t *testing.T, id string) map[string]any {
	t.Helper()
	r := f.revert(t, &f.admin, id, uuid.New())
	if r.Code != 200 {
		t.Fatalf("撤销 %s：%d %s", id, r.Code, r.Body.String())
	}
	return decodeJSONMap(t, r)
}

func (f *curationFixture) textOwner(t *testing.T, field, value, owner string) {
	t.Helper()
	var gotValue, gotOwner string
	err := f.application.database.QueryRow(`SELECT text_value,operation_id::text FROM release_auto_values WHERE release_id=$1 AND field=$2`, f.a, field).Scan(&gotValue, &gotOwner)
	if owner == "" && err == sql.ErrNoRows {
		return
	}
	if err != nil || gotValue != value || gotOwner != owner {
		t.Fatalf("%s 的自动值与写入者=(%q,%s,%v)，期望=(%q,%s)", field, gotValue, gotOwner, err, value, owner)
	}
}

func requireCurationProblem(t *testing.T, err error, code, reason string) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望 %s（%s），实际成功", code, reason)
	}
	status, got, message := operations.Problem(err)
	if status != 409 || got != code || !strings.Contains(message, reason) {
		t.Fatalf("实际错误 %d %s %q，期望 409 %s 且含 %q", status, got, message, code, reason)
	}
}

// A-V1：八个字段逐个核对三层。每个可空字段都分别在自动层和人工层显式设空。
func TestPostgreSQLCurationFieldPriority(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	mustExec(t, f.application, `UPDATE releases SET album_artist='扫描艺人',genres=ARRAY['扫描流派'],label='扫描厂牌',catalog_number='S-1',edition='扫描版本',barcode='111' WHERE id=$1`, f.a)
	scan := map[string]any{"title": "Album A", "album_artist": "扫描艺人", "year": float64(1998), "genre": []any{"扫描流派"}, "label": "扫描厂牌", "catalog_number": "S-1", "edition": "扫描版本", "barcode": "111"}
	read := func(want map[string]any) {
		t.Helper()
		var state struct {
			Fields []struct {
				Field     string
				Scan      any `json:"scan_value"`
				Effective any `json:"effective_value"`
			}
		}
		getJSON(t, f.application, &f.admin, "/api/v1/releases/"+f.a+"/corrections", &state)
		got := map[string]any{}
		for _, x := range state.Fields {
			got[x.Field] = x.Effective
			if !reflect.DeepEqual(x.Scan, scan[x.Field]) {
				t.Errorf("%s 扫描值=%v，期望 %v", x.Field, x.Scan, scan[x.Field])
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("有效值=%v，期望 %v", got, want)
		}
	}
	revision := 1
	read(scan)
	for i := 0; i < 2; i++ {
		auto := map[string]any{"title": fmt.Sprintf("自动标题%d", i), "album_artist": fmt.Sprintf("自动艺人%d", i), "year": float64(2010), "genre": []any{"自动流派"}, "label": "自动厂牌", "catalog_number": "A-1", "edition": "自动版", "barcode": "222"}
		manual := map[string]any{"title": "人工标题", "album_artist": "人工艺人", "year": float64(2020), "genre": []any{"人工流派"}, "label": "人工厂牌", "catalog_number": "M-1", "edition": "人工版", "barcode": "333"}
		changes, clear := map[string]any{}, map[string]any{}
		for name, v := range manual {
			changes[name] = setChange(v)
			clear[name] = clearChange
			if name == "title" || name == "album_artist" {
				continue
			}
			if i == 0 {
				manual[name] = nil
				changes[name] = nullChange
			} else {
				auto[name] = nil
			}
		}
		id := f.apply(t, f.a, "test-source", "", auto)
		_, revision = f.correct(t, f.a, revision, changes)
		read(manual)
		_, revision = f.correct(t, f.a, revision, clear)
		read(auto)
		f.undo(t, id)
		read(scan)
	}
}

// A-V4、A-V5：两个方向都核对操作归属及人工版本号。
func TestPostgreSQLCurationIndependentRevisions(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	snapshot := func() string {
		t.Helper()
		var v string
		if err := f.application.database.QueryRow(`SELECT jsonb_build_array(r.edit_revision,(SELECT jsonb_agg(to_jsonb(c) ORDER BY c.field) FROM release_corrections c WHERE c.release_id=r.id))::text FROM releases r WHERE id=$1`, f.a).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	manual, _ := f.correct(t, f.a, 1, map[string]any{"title": setChange("人工标题"), "label": setChange("人工厂牌")})
	before := snapshot()
	first := f.apply(t, f.a, "fixture", "", map[string]any{"title": "第一条"})
	if got := snapshot(); got != before {
		t.Fatalf("自动写入改变了人工行或版本号：%s -> %s", before, got)
	}
	f.undo(t, manual)
	f.textOwner(t, "title", "第一条", first)
	second := f.apply(t, f.a, "fixture", "", map[string]any{"title": "第二条"})
	f.correct(t, f.a, 3, map[string]any{"title": setChange("后写的人工标题"), "edition": setChange("后写的版本")})
	before = snapshot()
	response := f.undo(t, second)
	if got := snapshot(); got != before || response["revision"] != float64(4) {
		t.Fatalf("自动撤销后人工状态或响应版本错误：%s -> %s, %v", before, got, response)
	}
	f.textOwner(t, "title", "第一条", first)
}

// A-V6：任一字段被改写时整条拒绝，按倒序恢复前任的值和所有权。
func TestPostgreSQLCurationRevertOrder(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	first := f.apply(t, f.a, "fixture", "", map[string]any{"title": "第一条", "label": "保留"})
	second := f.apply(t, f.a, "fixture", "", map[string]any{"title": "第二条"})
	r := f.revert(t, &f.admin, first, uuid.New())
	requireAPIError(t, r, 409, "revert_conflict")
	if !strings.Contains(r.Body.String(), second) {
		t.Fatalf("冲突没有指出改写操作 %s：%s", second, r.Body.String())
	}
	f.textOwner(t, "label", "保留", first)
	f.undo(t, second)
	f.textOwner(t, "title", "第一条", first)
	f.undo(t, first)
	f.textOwner(t, "title", "", "")
	f.textOwner(t, "label", "", "")
}

func (f *curationFixture) batch(t *testing.T, tier string) string {
	t.Helper()
	b, err := f.writer.OpenBatch(t.Context(), tier)
	if err != nil || b.OpenedAt.IsZero() || b.Tier != tier || b.ClosedAt != nil {
		t.Fatalf("开批次：%+v %v", b, err)
	}
	return b.ID
}

func (f *curationFixture) scope(t *testing.T, scope curation.Scope, cursor, key, requestID string) curation.RevertResult {
	t.Helper()
	headers := idempotencyHeader(key)
	headers["X-Request-ID"] = requestID
	r := f.application.request(t, "POST", curationBase+"/reverts", map[string]any{"scope": scope, "cursor": cursor}, &f.admin, headers)
	var result curation.RevertResult
	if r.Code != 200 {
		t.Fatalf("范围撤销：%d %s", r.Code, r.Body.String())
	}
	if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// A-V8、A-V9：同批同格按倒序撤销，跨批改写只跳过冲突项。同键重试时只统计本次新撤销的操作。
func TestPostgreSQLCurationBatchRevert(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial_%t", partial), func(t *testing.T) {
			f := newCurationFixture(t)
			f.enable(t)
			b := f.batch(t, "program")
			other := f.batch(t, "model")
			first := f.apply(t, f.a, "fixture", b, map[string]any{"title": "旧值"})
			second := f.apply(t, f.a, "fixture", b, map[string]any{"title": "新值"})
			third := f.apply(t, f.b, "fixture", b, map[string]any{"label": "同批"})
			control := map[string]any{"edition": "其他批次"}
			if partial {
				control["label"] = "覆盖同批"
			}
			f.apply(t, f.b, "fixture", other, control)
			if err := f.writer.CloseBatch(t.Context(), b); err != nil {
				t.Fatal(err)
			}
			_, err := f.writer.Apply(t.Context(), autoRequest(f.a, "fixture", b, map[string]any{"label": "结束后"}))
			requireCurationProblem(t, err, "batch_closed", "结束")
			list, _ := f.list(t, "?batch_id="+b)
			ids := []string{}
			for _, x := range list.Items {
				ids = append(ids, x.ID)
			}
			if !slices.Equal(ids, []string{third, second, first}) || list.Pagination.Total != 3 {
				t.Fatalf("按批筛选=%v，总数 %d", ids, list.Pagination.Total)
			}
			scope := curation.Scope{Kind: "batch", ID: b}
			key := uuid.New()
			requestID := uuid.New()
			result := f.scope(t, scope, "", key, requestID)
			want := curation.RevertResult{Reverted: 3, Skipped: []curation.SkippedRevert{}}
			if partial {
				want.Reverted = 2
				want.Skipped = []curation.SkippedRevert{{OperationID: third, Code: "revert_conflict"}}
			}
			if !reflect.DeepEqual(result, want) {
				t.Fatalf("范围撤销=%+v，期望 %+v", result, want)
			}
			const revertCount = "SELECT count(*) FROM operations WHERE reverts_operation_id IS NOT NULL"
			beforeRetry := countRows(t, f.application, revertCount)
			if beforeRetry != want.Reverted {
				t.Fatalf("首次撤销记录数=%d，期望 %d", beforeRetry, want.Reverted)
			}
			if n := countRows(t, f.application, revertCount+" AND request_id=$1", requestID); n != beforeRetry {
				t.Fatalf("带本次 X-Request-ID 的撤销记录数=%d，期望 %d", n, beforeRetry)
			}
			wantRetry := curation.RevertResult{Reverted: 0, Skipped: want.Skipped}
			if retry := f.scope(t, scope, "", key, uuid.New()); !reflect.DeepEqual(retry, wantRetry) {
				t.Fatalf("重试=%+v，期望 %+v", retry, wantRetry)
			}
			if n := countRows(t, f.application, revertCount); n != beforeRetry {
				t.Fatalf("重试前有 %d 条撤销记录，重试后有 %d 条", beforeRetry, n)
			}
			var detail curation.Batch
			getJSON(t, f.application, &f.admin, curationBase+"/batches/"+b, &detail)
			if detail.ID != b || detail.Operations != 3 || detail.Reverted != int64(want.Reverted) || detail.ClosedAt == nil || detail.ClosedAt.Before(detail.OpenedAt) {
				t.Fatalf("批次详情=%+v", detail)
			}
			f.textOwner(t, "title", "", "")
		})
	}
}

// A-V10：同一格内连续 101 次改写必须跨页倒序完成，另一个来源保持原状。
func TestPostgreSQLCurationProviderCursor(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	control := f.apply(t, f.a, "other", "", map[string]any{"label": "其他来源"})
	var first string
	for i := 0; i < 101; i++ {
		id := f.apply(t, f.a, "paged", "", map[string]any{"title": fmt.Sprintf("标题 %d", i)})
		if i == 0 {
			first = id
		}
	}
	scope := curation.Scope{Kind: "provider", Provider: "paged"}
	key := uuid.New()
	one := f.scope(t, scope, "", key, uuid.New())
	if one.Reverted != 100 || len(one.Skipped) != 0 || one.NextCursor == nil {
		t.Fatalf("第一页=%+v", one)
	}
	f.textOwner(t, "title", "标题 0", first)
	two := f.scope(t, scope, *one.NextCursor, key, uuid.New())
	if two.Reverted != 1 || len(two.Skipped) != 0 || two.NextCursor != nil {
		t.Fatalf("续页=%+v", two)
	}
	f.textOwner(t, "title", "", "")
	f.textOwner(t, "label", "其他来源", control)
}

// A-V11：标记只阻止同一发行、字段和值，清除后恢复写入。
func TestPostgreSQLCurationSuppression(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	values := map[string]any{"title": "不再提出"}
	op := f.apply(t, f.a, "fixture", "", values)
	undo := f.undo(t, op)
	rejected, err := f.writer.Apply(t.Context(), autoRequest(f.a, "fixture", "", values))
	requireCurationProblem(t, err, "nothing_to_apply", "不再提出")
	if rejected.Skipped["title"] != "suppressed" {
		t.Fatalf("跳过原因=%v", rejected.Skipped)
	}
	f.textOwner(t, "title", "", "")
	f.apply(t, f.b, "fixture", "", values)
	f.apply(t, f.a, "fixture", "", map[string]any{"title": "其他值", "label": "不再提出"})
	var marks struct{ Items []curation.Suppression }
	getJSON(t, f.application, &f.admin, curationBase+"/suppressions?release_id="+f.a, &marks)
	if len(marks.Items) != 1 || marks.Items[0].TargetID != f.a || marks.Items[0].Subject != "title" || marks.Items[0].OperationID == nil || *marks.Items[0].OperationID != undo["operation_id"] {
		t.Fatalf("标记=%+v", marks)
	}
	r := f.application.request(t, "DELETE", curationBase+"/suppressions/"+marks.Items[0].ID, nil, &f.admin, nil)
	if r.Code != 204 {
		t.Fatalf("清除标记：%d %s", r.Code, r.Body.String())
	}
	f.apply(t, f.a, "fixture", "", values)
}

// A-V12：默认关、曾经开启后关闭、开启者停用和降级均独立验证。
func TestPostgreSQLCurationEnabler(t *testing.T) {
	f := newCurationFixture(t)
	write := func() { t.Helper(); f.apply(t, f.a, "fixture", "", map[string]any{"label": "自动厂牌"}) }
	reject := func(reason string) {
		t.Helper()
		_, err := f.writer.Apply(t.Context(), autoRequest(f.a, "fixture", "", map[string]any{"title": "禁止"}))
		requireCurationProblem(t, err, "curation_disabled", reason)
	}
	reject("开关")
	f.enable(t)
	write()
	list, _ := f.list(t, "?source=curation&provider=fixture")
	if len(list.Items) != 1 || list.Items[0].Actor.ID != f.admin.id {
		t.Fatalf("开启者记录=%+v", list)
	}
	op := list.Items[0].ID
	if _, err := f.writer.SetSettings(t.Context(), f.admin.id, false); err != nil {
		t.Fatal(err)
	}
	reject("开关")
	f.textOwner(t, "label", "自动厂牌", op)
	f.enable(t)
	for _, q := range []string{"UPDATE users SET disabled_at=now() WHERE id=$1", "UPDATE users SET disabled_at=NULL,role='user' WHERE id=$1"} {
		mustExec(t, f.application, q, f.admin.id)
		reject("开启者")
		s, err := f.writer.Settings(t.Context())
		if err != nil || !s.Enabled || s.Active || s.InactiveReason == nil || *s.InactiveReason != "enabler_unavailable" {
			t.Fatalf("开启者失效时的设置=%+v, %v", s, err)
		}
	}
	if n := countRows(t, f.application, "SELECT count(*) FROM operations"); n != 1 {
		t.Fatalf("被拒绝后操作记录数=%d", n)
	}
}

// A-V13：使用同源的有效成员会话，覆盖所有读取与写入路由。
func TestPostgreSQLCurationMemberDenied(t *testing.T) {
	f := newCurationFixture(t)
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"GET", curationBase + "/settings", nil}, {"PUT", curationBase + "/settings", map[string]bool{"enabled": true}},
		{"GET", curationBase + "/batches", nil}, {"GET", curationBase + "/batches/" + uuid.New(), nil},
		{"POST", curationBase + "/reverts", map[string]any{"scope": curation.Scope{Kind: "batch", ID: uuid.New()}}},
		{"POST", curationBase + "/reverts", map[string]any{"scope": curation.Scope{Kind: "provider", Provider: "test"}}},
		{"GET", curationBase + "/suppressions", nil}, {"DELETE", curationBase + "/suppressions/" + uuid.New(), nil},
		{"GET", "/api/v1/releases/" + f.a + "/corrections", nil},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			r := f.application.request(t, tc.method, tc.path, tc.body, &f.ordinary, idempotencyHeader(uuid.New()))
			requireAPIError(t, r, 403, "forbidden")
		})
	}
}

// A-V14：自动确认扫描值也能消除拿不准的字段，撤销后恢复。
func TestPostgreSQLCurationAttention(t *testing.T) {
	f := newCurationFixture(t)
	f.enable(t)
	mustExec(t, f.application, `UPDATE release_field_decisions SET field_key='artist' WHERE release_id=$1`, f.a)
	check := func(want int64) {
		t.Helper()
		var s catalog.ReleaseSummary
		getJSON(t, f.application, &f.admin, "/api/v1/releases/"+f.a, &s)
		if s.AttentionCount != want {
			t.Fatalf("需要关注=%d，期望 %d", s.AttentionCount, want)
		}
	}
	check(1)
	op := f.apply(t, f.a, "fixture", "", map[string]any{"album_artist": "Artist A"})
	check(0)
	f.undo(t, op)
	check(1)
}
