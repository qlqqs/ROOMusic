package governance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func rawChange(mode string, value string) RawCorrectionChange {
	change := RawCorrectionChange{Mode: mode}
	if value != "" {
		change.Value = json.RawMessage(value)
	}
	return change
}

func TestParseCorrectionChanges(t *testing.T) {
	parsed, err := ParseCorrectionChanges(map[string]RawCorrectionChange{
		"title":          rawChange("set", `"  新   标题 "`),
		"year":           rawChange("set", `1975`),
		"genre":          rawChange("null", ""),
		"label":          rawChange("clear", ""),
		"catalog_number": rawChange("clear", `null`),
		"barcode":        rawChange("null", `null`),
	})
	if err != nil {
		t.Fatalf("ParseCorrectionChanges: %v", err)
	}
	want := map[string]CorrectionChange{
		"title": {Mode: "set", Text: "新 标题"}, "year": {Mode: "set", Year: 1975}, "genre": {Mode: "null"},
		"label": {Mode: "clear"}, "catalog_number": {Mode: "clear"}, "barcode": {Mode: "null"},
	}
	if !reflect.DeepEqual(parsed, want) {
		t.Fatalf("parsed = %+v, want %+v (text trimmed and folded, year as an integer)", parsed, want)
	}
	if empty, err := ParseCorrectionChanges(nil); err != nil || len(empty) != 0 {
		t.Fatalf("ParseCorrectionChanges(nil) = %v, %v; want no changes and no error", empty, err)
	}

	for name, raw := range map[string]map[string]RawCorrectionChange{
		"unknown field":                  {"source_type": rawChange("set", `"CD"`)},
		"track-level field":              {"track_title": rawChange("set", `"x"`)},
		"unknown mode":                   {"genre": rawChange("replace", `["x"]`)},
		"missing mode":                   {"genre": rawChange("", `["x"]`)},
		"title set to empty":             {"title": rawChange("null", "")},
		"album artist set to empty":      {"album_artist": rawChange("null", "")},
		"set without a value":            {"genre": rawChange("set", "")},
		"set with a null value":          {"genre": rawChange("set", "null")},
		"blank text":                     {"genre": rawChange("set", `["   \t "]`)},
		"control character":              {"genre": rawChange("set", `["a\u0007b"]`)},
		"text over the byte limit":       {"genre": rawChange("set", `["`+strings.Repeat("x", maxCorrectionTextBytes+1)+`"]`)},
		"year zero":                      {"year": rawChange("set", `0`)},
		"year too large":                 {"year": rawChange("set", `10000`)},
		"year with a fraction":           {"year": rawChange("set", `1975.5`)},
		"year as text":                   {"year": rawChange("set", `"1975"`)},
		"text field given a number":      {"genre": rawChange("set", `1975`)},
		"text field given an object":     {"genre": rawChange("set", `{"a":1}`)},
		"clear with a value":             {"genre": rawChange("clear", `["x"]`)},
		"null with a value":              {"genre": rawChange("null", `["x"]`)},
		"one invalid among valid items":  {"genre": rawChange("set", `["古典"]`), "year": rawChange("set", `0`)},
		"a valid item cannot hide a bad": {"label": rawChange("set", `"厂牌"`), "nope": rawChange("clear", "")},
	} {
		_, err := ParseCorrectionChanges(raw)
		var failure *operations.Failure
		if !errors.As(err, &failure) || failure.Code != "invalid_correction" || failure.Class != operations.ClassInvalid || failure.Message == "" {
			t.Errorf("%s: error = %v, want an invalid_correction failure", name, err)
		}
		if failure != nil {
			wantMessage := map[string]string{
				"control character":        "流派含有不允许的字符",
				"text over the byte limit": "流派超过 4096 字节",
			}[name]
			if wantMessage != "" && failure.Message != wantMessage {
				t.Errorf("%s: message = %q, want %q", name, failure.Message, wantMessage)
			}
		}
	}
	// 恰好 4096 字节的文本可以接受。
	if _, err := ParseCorrectionChanges(map[string]RawCorrectionChange{"genre": rawChange("set", `["`+strings.Repeat("x", maxCorrectionTextBytes)+`"]`)}); err != nil {
		t.Errorf("text of exactly %d bytes was rejected: %v", maxCorrectionTextBytes, err)
	}
}

func TestCorrectionFingerprintCoversTheCompleteSemantics(t *testing.T) {
	base := CorrectionRequest{ReleaseID: "01234567-89ab-cdef-0123-456789abcdef", ExpectedRevision: 1, Changes: map[string]CorrectionChange{
		"title": {Mode: "set", Text: "标题"}, "year": {Mode: "set", Year: 1990}, "genre": {Mode: "null"}, "label": {Mode: "clear"},
	}}
	if base.Fingerprint() != base.Fingerprint() || len(base.Fingerprint()) != 64 {
		t.Fatal("the fingerprint is not stable SHA-256 hex")
	}
	upper := base
	upper.ReleaseID = strings.ToUpper(base.ReleaseID)
	if upper.Fingerprint() != base.Fingerprint() {
		t.Error("the release ID case changes the fingerprint")
	}
	variant := func(mutate func(request *CorrectionRequest)) string {
		request := CorrectionRequest{ReleaseID: base.ReleaseID, ExpectedRevision: base.ExpectedRevision, Changes: map[string]CorrectionChange{}}
		for name, change := range base.Changes {
			request.Changes[name] = change
		}
		mutate(&request)
		return request.Fingerprint()
	}
	for name, mutate := range map[string]func(*CorrectionRequest){
		"another release":  func(r *CorrectionRequest) { r.ReleaseID = "11111111-89ab-cdef-0123-456789abcdef" },
		"another revision": func(r *CorrectionRequest) { r.ExpectedRevision = 2 },
		"another text":     func(r *CorrectionRequest) { r.Changes["title"] = CorrectionChange{Mode: "set", Text: "别的标题"} },
		"another year":     func(r *CorrectionRequest) { r.Changes["year"] = CorrectionChange{Mode: "set", Year: 1991} },
		"set instead of null": func(r *CorrectionRequest) {
			r.Changes["genre"] = CorrectionChange{Mode: "set", Genres: []string{"古典"}}
		},
		"null instead of set":   func(r *CorrectionRequest) { r.Changes["title"] = CorrectionChange{Mode: "null"} },
		"clear instead of set":  func(r *CorrectionRequest) { r.Changes["year"] = CorrectionChange{Mode: "clear"} },
		"set instead of clear":  func(r *CorrectionRequest) { r.Changes["label"] = CorrectionChange{Mode: "set", Text: "厂牌"} },
		"null instead of clear": func(r *CorrectionRequest) { r.Changes["label"] = CorrectionChange{Mode: "null"} },
		"clear instead of null": func(r *CorrectionRequest) { r.Changes["genre"] = CorrectionChange{Mode: "clear"} },
		"an extra field":        func(r *CorrectionRequest) { r.Changes["barcode"] = CorrectionChange{Mode: "clear"} },
		"a missing field":       func(r *CorrectionRequest) { delete(r.Changes, "label") },
	} {
		if variant(mutate) == base.Fingerprint() {
			t.Errorf("%s produced the same fingerprint", name)
		}
	}
}

// correctionFixture 在合成发行上执行真正的修正流程（operations.Run + CorrectionExecutor）。
type correctionFixture struct {
	*effectiveFixture
}

func newCorrectionFixture(t *testing.T) *correctionFixture {
	t.Helper()
	return &correctionFixture{newEffectiveFixture(t)}
}

func (fixture *correctionFixture) correctRelease(releaseID string, expectedRevision int64, changes map[string]CorrectionChange) (operations.Result, error) {
	fixture.t.Helper()
	request := CorrectionRequest{ReleaseID: releaseID, ExpectedRevision: expectedRevision, Changes: changes}
	return operations.Run(context.Background(), fixture.database, operations.Spec{
		ActorID: fixture.actorID, Type: operations.TypeReleaseCorrection, Key: uuid.New(),
		Fingerprint: request.Fingerprint(), RequestID: "test",
	}, CorrectionExecutor(request))
}

// mustCorrect 执行一次应该成功的修正，核对响应与随后读取到的状态逐字相同（同一份投影），返回带类型值的状态。
func (fixture *correctionFixture) mustCorrect(releaseID string, expectedRevision int64, changes map[string]CorrectionChange) CorrectionResult {
	fixture.t.Helper()
	result, err := fixture.correctRelease(releaseID, expectedRevision, changes)
	if err != nil {
		fixture.t.Fatalf("correct release: %v", err)
	}
	var decoded CorrectionResult
	if err := json.Unmarshal(result.Response, &decoded); err != nil {
		fixture.t.Fatalf("decode correction result: %v", err)
	}
	if decoded.OperationID != result.OperationID {
		fixture.t.Fatalf("response operation_id %q differs from the recorded operation %q", decoded.OperationID, result.OperationID)
	}
	state := mustState(fixture.t, fixture, releaseID)
	fromResponse, _ := json.Marshal(decoded.CorrectionState)
	fromRead, _ := json.Marshal(state)
	if !bytes.Equal(fromResponse, fromRead) {
		fixture.t.Fatalf("the correction response and a later read differ:\n%s\n%s", fromResponse, fromRead)
	}
	return CorrectionResult{CorrectionState: state, OperationID: result.OperationID}
}

func fieldOf(t *testing.T, state CorrectionState, name string) CorrectionField {
	t.Helper()
	for _, field := range state.Fields {
		if field.Field == name {
			return field
		}
	}
	t.Fatalf("state has no field %q", name)
	return CorrectionField{}
}

func (fixture *correctionFixture) revisionOf(releaseID string) int64 {
	fixture.t.Helper()
	var revision int64
	if err := fixture.database.QueryRow(`SELECT edit_revision FROM releases WHERE id=$1::uuid`, releaseID).Scan(&revision); err != nil {
		fixture.t.Fatalf("read revision: %v", err)
	}
	return revision
}

func (fixture *correctionFixture) operationCount() int {
	fixture.t.Helper()
	var count int
	if err := fixture.database.QueryRow(`SELECT COUNT(*) FROM operations`).Scan(&count); err != nil {
		fixture.t.Fatalf("count operations: %v", err)
	}
	return count
}

func TestPostgreSQLCorrectionStateTransitions(t *testing.T) {
	fixture := newCorrectionFixture(t)
	ctx := context.Background()
	release := fixture.visibleRelease("原标题")
	fixture.exec(`UPDATE releases SET artist='扫描艺人', album_artist=NULL, year=0, genres=ARRAY['扫描流派'], label=NULL WHERE id=$1::uuid`, release.releaseID)

	// 初始状态：没有修正；没有专辑艺人时扫描值是展示艺人；年份 0 与缺失的文本是 null。
	state, err := fixture.governance.LoadCorrectionState(ctx, release.releaseID)
	if err != nil || state.Revision != 1 || state.ReleaseID != release.releaseID || len(state.Fields) != len(catalog.CorrectableFieldNames()) {
		t.Fatalf("initial state = %+v, %v", state, err)
	}
	for index, name := range catalog.CorrectableFieldNames() {
		if state.Fields[index].Field != name || state.Fields[index].Correction != nil {
			t.Fatalf("field %d = %+v, want %q without a correction", index, state.Fields[index], name)
		}
	}
	if got := fieldOf(t, state, "album_artist"); got.ScanValue != "扫描艺人" || got.EffectiveValue != "扫描艺人" {
		t.Errorf("album artist without a scanned value = %+v, want the display artist", got)
	}
	if got := fieldOf(t, state, "year"); got.ScanValue != nil || got.EffectiveValue != nil {
		t.Errorf("year 0 = %+v, want null", got)
	}
	if got := fieldOf(t, state, "genre"); !reflect.DeepEqual(got.ScanValue, []string{"扫描流派"}) || !reflect.DeepEqual(got.EffectiveValue, []string{"扫描流派"}) {
		t.Errorf("genre = %+v", got)
	}

	// 设值：版本加一，修正与有效值更新，其余字段不变。
	state = fixture.mustCorrect(release.releaseID, 1, map[string]CorrectionChange{
		"title": {Mode: "set", Text: "新标题"}, "year": {Mode: "set", Year: 1975}, "genre": {Mode: "null"}, "label": {Mode: "set", Text: "新厂牌"},
	}).CorrectionState
	if state.Revision != 2 || fixture.revisionOf(release.releaseID) != 2 {
		t.Fatalf("revision after the first correction = %d (table %d), want 2", state.Revision, fixture.revisionOf(release.releaseID))
	}
	if got := fieldOf(t, state, "title"); got.ScanValue != "原标题" || got.EffectiveValue != "新标题" || got.Correction == nil || got.Correction.Mode != "set" || got.Correction.Value != "新标题" {
		t.Errorf("title = %+v", got)
	}
	if got := fieldOf(t, state, "year"); got.ScanValue != nil || got.EffectiveValue != 1975 || got.Correction == nil || got.Correction.Value != 1975 {
		t.Errorf("year = %+v", got)
	}
	if got := fieldOf(t, state, "genre"); !reflect.DeepEqual(got.ScanValue, []string{"扫描流派"}) || got.EffectiveValue != nil || got.Correction == nil || got.Correction.Mode != "null" || got.Correction.Value != nil {
		t.Errorf("genre set to empty = %+v", got)
	}
	if got := fieldOf(t, state, "label"); got.ScanValue != nil || got.EffectiveValue != "新厂牌" {
		t.Errorf("label = %+v", got)
	}
	if got := fieldOf(t, state, "barcode"); got.Correction != nil {
		t.Errorf("barcode was not requested but has %+v", got)
	}

	// 不改变状态的条目被忽略（相同的 set、相同的 null、没有修正时 clear）；全部被忽略是 no_changes，版本与记录都不变。
	records := fixture.operationCount()
	_, err = fixture.correctRelease(release.releaseID, 2, map[string]CorrectionChange{
		"title": {Mode: "set", Text: "新标题"}, "genre": {Mode: "null"}, "barcode": {Mode: "clear"},
	})
	if status, code, _ := operations.Problem(err); status != 400 || code != "no_changes" {
		t.Fatalf("a request that changes nothing: error = %v (status %d, code %q), want 400 no_changes", err, status, code)
	}
	if _, err := fixture.correctRelease(release.releaseID, 2, nil); err == nil {
		t.Fatal("an empty request was accepted")
	}
	if fixture.revisionOf(release.releaseID) != 2 || fixture.operationCount() != records {
		t.Fatalf("a rejected request changed the revision to %d or recorded an operation", fixture.revisionOf(release.releaseID))
	}

	// 混合：被忽略的条目不影响其余条目；只有真正改变的字段记入记录。
	result, err := fixture.correctRelease(release.releaseID, 2, map[string]CorrectionChange{
		"title": {Mode: "set", Text: "新标题"}, "barcode": {Mode: "set", Text: "4988000000000"},
	})
	if err != nil {
		t.Fatalf("mixed correction: %v", err)
	}
	record, err := operations.Load(ctx, fixture.database, result.OperationID)
	if err != nil {
		t.Fatalf("load operation: %v", err)
	}
	var before, after correctionRecordState
	if err := json.Unmarshal(record.Before, &before); err != nil {
		t.Fatalf("decode before: %v", err)
	}
	if err := json.Unmarshal(record.After, &after); err != nil {
		t.Fatalf("decode after: %v", err)
	}
	if len(before.Fields) != 1 || before.Fields["barcode"] != nil || before.Effective["barcode"] != nil ||
		len(after.Fields) != 1 || after.Fields["barcode"] == nil || after.Fields["barcode"].Value != "4988000000000" || after.Effective["barcode"] != "4988000000000" {
		t.Fatalf("recorded states = before %+v, after %+v; want only the barcode, from no correction to the value", before, after)
	}
	if record.Type != operations.TypeReleaseCorrection || record.TargetType != operations.TargetRelease || record.TargetID != release.releaseID ||
		record.ExpectedRevision != 2 || record.ResultRevision != 3 || record.ActorID != fixture.actorID {
		t.Fatalf("operation record = %+v", record)
	}

	// 改值、再设空、再清除：记录前后意图与有效值；清除回到当前的扫描值（重扫之后的值）。
	replaced := fixture.mustCorrect(release.releaseID, 3, map[string]CorrectionChange{"title": {Mode: "set", Text: "再改的标题"}})
	state = replaced.CorrectionState
	if got := fieldOf(t, state, "title"); got.EffectiveValue != "再改的标题" || state.Revision != 4 {
		t.Errorf("replacing a value: %+v at revision %d", got, state.Revision)
	}
	var titleOperation string
	if err := fixture.database.QueryRow(`SELECT operation_id::text FROM release_corrections WHERE release_id=$1::uuid AND field='title'`, release.releaseID).Scan(&titleOperation); err != nil || titleOperation != replaced.OperationID {
		t.Errorf("the replaced title correction references %q (%v), want the operation that replaced it, %s", titleOperation, err, replaced.OperationID)
	}
	fixture.exec(`UPDATE releases SET title='重扫后的标题', genres=ARRAY['重扫流派'], year=2001 WHERE id=$1::uuid`, release.releaseID) // 模拟重扫
	state = fixture.mustCorrect(release.releaseID, 4, map[string]CorrectionChange{
		"title": {Mode: "clear"}, "genre": {Mode: "clear"}, "year": {Mode: "clear"},
	}).CorrectionState
	if got := fieldOf(t, state, "title"); got.EffectiveValue != "重扫后的标题" || got.Correction != nil {
		t.Errorf("title after clearing = %+v, want the rescanned value", got)
	}
	if got := fieldOf(t, state, "genre"); !reflect.DeepEqual(got.EffectiveValue, []string{"重扫流派"}) || got.Correction != nil {
		t.Errorf("genre after clearing = %+v, want the rescanned value", got)
	}
	if got := fieldOf(t, state, "year"); got.EffectiveValue != 2001 || got.Correction != nil {
		t.Errorf("year after clearing = %+v, want the rescanned value", got)
	}
	if got := fieldOf(t, state, "label"); got.Correction == nil || got.EffectiveValue != "新厂牌" {
		t.Errorf("label must keep its correction, got %+v", got)
	}
	// 每次成功的修正恰好一条记录，修正行引用设下它的操作。
	if fixture.operationCount() != 4 {
		t.Errorf("operation records = %d, want 4 (one per successful correction; the rejected requests left none)", fixture.operationCount())
	}
	var referencing string
	if err := fixture.database.QueryRow(`SELECT operation_id::text FROM release_corrections WHERE release_id=$1::uuid AND field='barcode'`, release.releaseID).Scan(&referencing); err != nil || referencing != result.OperationID {
		t.Errorf("the barcode correction references %q (%v), want the operation that set it, %s", referencing, err, result.OperationID)
	}

	records = fixture.operationCount()
	// 流派设两项，再留空，撤销留空后恢复完整列表及原顺序。
	genres := []string{"国语流行乐", "台语流行乐"}
	state = fixture.mustCorrect(release.releaseID, state.Revision, map[string]CorrectionChange{
		"genre": {Mode: catalog.CorrectionSet, Genres: genres},
	}).CorrectionState
	if got := fieldOf(t, state, "genre"); !reflect.DeepEqual(got.EffectiveValue, genres) || got.Correction == nil ||
		got.Correction.Mode != catalog.CorrectionSet || !reflect.DeepEqual(got.Correction.Value, genres) || got.Uncertain {
		t.Fatalf("流派设两项后的状态 = %+v", got)
	}
	emptied := fixture.mustCorrect(release.releaseID, state.Revision, map[string]CorrectionChange{
		"genre": {Mode: catalog.CorrectionNull},
	})
	if got := fieldOf(t, emptied.CorrectionState, "genre"); got.EffectiveValue != nil || got.Correction == nil ||
		got.Correction.Mode != catalog.CorrectionNull || got.Correction.Value != nil {
		t.Fatalf("流派留空后的状态 = %+v", got)
	}
	fixture.mustRevert(CorrectionOperations(), emptied.OperationID)
	state = mustState(t, fixture, release.releaseID)
	if got := fieldOf(t, state, "genre"); !reflect.DeepEqual(got.ScanValue, []string{"重扫流派"}) || !reflect.DeepEqual(got.EffectiveValue, genres) ||
		got.Correction == nil || got.Correction.Mode != catalog.CorrectionSet || !reflect.DeepEqual(got.Correction.Value, genres) ||
		state.Revision != emptied.Revision+1 || fixture.operationCount() != records+3 {
		t.Fatalf("撤销流派留空后的状态 = %+v，版本 %d，记录数 %d", got, state.Revision, fixture.operationCount())
	}
}

func TestPostgreSQLCorrectionRejectsStaleInvisibleAndUnknownReleases(t *testing.T) {
	fixture := newCorrectionFixture(t)
	release := fixture.visibleRelease("专辑")
	hidden := fixture.release("只有缺失曲目")
	fixture.track(hidden, "缺失", "missing", 10.0)
	changes := map[string]CorrectionChange{"genre": {Mode: "set", Genres: []string{"古典"}}}

	fixture.mustCorrect(release.releaseID, 1, changes)
	// 过期版本：没有任何变化，不写记录。
	records := fixture.operationCount()
	_, err := fixture.correctRelease(release.releaseID, 1, map[string]CorrectionChange{"genre": {Mode: "set", Genres: []string{"爵士"}}})
	if status, code, _ := operations.Problem(err); status != 409 || code != "revision_conflict" {
		t.Fatalf("stale revision: error = %v (status %d, code %q), want 409 revision_conflict", err, status, code)
	}
	if got := fieldOf(t, mustState(t, fixture, release.releaseID), "genre"); !reflect.DeepEqual(got.EffectiveValue, []string{"古典"}) || fixture.operationCount() != records {
		t.Fatalf("a stale request changed the value to %v or recorded an operation", got.EffectiveValue)
	}
	// 版本号超前同样冲突。
	if _, err := fixture.correctRelease(release.releaseID, 99, changes); err == nil {
		t.Fatal("a revision from the future was accepted")
	}
	// 不可见与不存在的发行：404，不创建修正。
	for name, releaseID := range map[string]string{"hidden": hidden.releaseID, "unknown": uuid.New()} {
		_, err := fixture.correctRelease(releaseID, 1, changes)
		if status, code, _ := operations.Problem(err); status != 404 || code != "not_found" {
			t.Errorf("%s release: error = %v (status %d, code %q), want 404 not_found", name, err, status, code)
		}
		if _, err := fixture.governance.LoadCorrectionState(context.Background(), releaseID); !errors.Is(err, catalog.ErrReleaseNotFound) {
			t.Errorf("%s release: LoadCorrectionState error = %v, want ErrReleaseNotFound", name, err)
		}
	}
	var corrections int
	_ = fixture.database.QueryRow(`SELECT COUNT(*) FROM release_corrections WHERE release_id <> $1::uuid`, release.releaseID).Scan(&corrections)
	if corrections != 0 {
		t.Fatalf("rejected requests created %d correction rows", corrections)
	}
}

func mustState(t *testing.T, fixture *correctionFixture, releaseID string) CorrectionState {
	t.Helper()
	state, err := fixture.governance.LoadCorrectionState(context.Background(), releaseID)
	if err != nil {
		t.Fatalf("LoadCorrectionState: %v", err)
	}
	return state
}

func TestPostgreSQLConfirmingTheScannedValueClearsAttention(t *testing.T) {
	fixture := newCorrectionFixture(t)
	release := fixture.visibleRelease("待确认的标题")
	fixture.decision(release.releaseID, "title", "uncertain_apply")
	fixture.decision(release.releaseID, "artist", "uncertain_apply")
	fixture.decision(release.releaseID, "year", "uncertain_apply")
	attention := func() int64 {
		t.Helper()
		summary, err := fixture.store.LoadReleaseSummary(context.Background(), release.releaseID)
		if err != nil {
			t.Fatalf("LoadReleaseSummary: %v", err)
		}
		return summary.AttentionCount
	}
	state := mustState(t, fixture, release.releaseID)
	if !fieldOf(t, state, "title").Uncertain || !fieldOf(t, state, "album_artist").Uncertain || !fieldOf(t, state, "year").Uncertain || fieldOf(t, state, "genre").Uncertain {
		t.Fatalf("uncertain flags = %+v; want title, album_artist (through the display artist) and year", state.Fields)
	}
	if attention() != 3 {
		t.Fatalf("attention = %d, want 3", attention())
	}

	// “确认扫描值”：把当前扫描值存为修正，字段变成有修正的字段，不再计入需要关注；有效值不变。
	scanned := fieldOf(t, state, "title").ScanValue.(string)
	confirmed := fixture.mustCorrect(release.releaseID, state.Revision, map[string]CorrectionChange{"title": {Mode: "set", Text: scanned}}).CorrectionState
	if got := fieldOf(t, confirmed, "title"); got.EffectiveValue != scanned || got.Correction == nil || !got.Uncertain {
		t.Errorf("confirmed title = %+v; the value stays, the correction exists and the scan still reports uncertainty", got)
	}
	if attention() != 2 {
		t.Fatalf("attention after confirming the title = %d, want 2", attention())
	}
	// 专辑艺人的修正（这里确认扫描的展示艺人）覆盖“艺人”字段的不确定。
	albumArtist := fieldOf(t, confirmed, "album_artist").ScanValue.(string)
	confirmed = fixture.mustCorrect(release.releaseID, confirmed.Revision, map[string]CorrectionChange{"album_artist": {Mode: "set", Text: albumArtist}}).CorrectionState
	if attention() != 1 {
		t.Fatalf("attention after confirming the album artist = %d, want 1", attention())
	}
	// 清除修正后，扫描的不确定重新计入。
	fixture.mustCorrect(release.releaseID, confirmed.Revision, map[string]CorrectionChange{"title": {Mode: "clear"}, "album_artist": {Mode: "clear"}})
	if attention() != 3 {
		t.Fatalf("attention after clearing = %d, want 3 again", attention())
	}
}

// 修正后所有读取显示同一个有效值：列表、详情头部、搜索、排序、按组读取与按曲目读取。
func TestPostgreSQLCorrectedValuesAreConsistentAcrossReads(t *testing.T) {
	fixture := newCorrectionFixture(t)
	ctx := context.Background()
	zulu, beta := fixture.visibleRelease("Zulu"), fixture.visibleRelease("Beta")
	fixture.exec(`UPDATE releases SET artist='Artist Z', album_artist='Album Artist Z', year=2000 WHERE id=$1::uuid`, zulu.releaseID)
	fixture.exec(`UPDATE releases SET artist='Artist B', album_artist='Album Artist B', year=2001 WHERE id=$1::uuid`, beta.releaseID)
	fixture.mustCorrect(zulu.releaseID, 1, map[string]CorrectionChange{
		"title": {Mode: "set", Text: "Alpha (corrected)"}, "album_artist": {Mode: "set", Text: "Corrected Artist"}, "year": {Mode: "null"},
	})

	list, err := fixture.store.ListReleases(ctx, catalog.ListParams{Page: 1, PageSize: 10})
	if err != nil || list.Total != 2 || list.Items[0].Title != "Alpha (corrected)" || list.Items[0].Artist != "Corrected Artist" || list.Items[0].Year != nil {
		t.Fatalf("list = %+v, %v; want the corrected release first with its corrected artist and no year", list, err)
	}
	groups, err := fixture.store.ListReleaseGroups(ctx, catalog.ListParams{Search: "Corrected", Page: 1, PageSize: 10})
	if err != nil || groups.Total != 1 || groups.Items[0].Representative.ID != zulu.releaseID || groups.Items[0].Representative.AlbumArtist == nil || *groups.Items[0].Representative.AlbumArtist != "Corrected Artist" {
		t.Fatalf("group search = %+v, %v", groups, err)
	}
	header, err := fixture.store.LoadReleaseHeader(ctx, zulu.releaseID)
	if err != nil || header.Revision != 2 || !reflect.DeepEqual(header.CorrectedFields, []string{"title", "album_artist", "year"}) {
		t.Fatalf("header = %+v, %v; want revision 2 and the corrected fields in the fixed order", header, err)
	}
	if untouched, err := fixture.store.LoadReleaseHeader(ctx, beta.releaseID); err != nil || untouched.Revision != 1 || untouched.CorrectedFields == nil || len(untouched.CorrectedFields) != 0 {
		t.Fatalf("an uncorrected release header = %+v, %v; want revision 1 and an empty, non-nil list", untouched, err)
	}
}
