package governance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestArtistMergedSourcesInvalidIDBeforeDatabase(t *testing.T) {
	_, err := ReadArtistMergedSources(context.Background(), nil, "not-an-id")
	assertArtistFailure(t, err, "invalid_id")
}

func TestArtistMergedSourcesHistoryUsesTypedRedirectChain(t *testing.T) {
	a, b, c, keyA, keyB, op1, op2 := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	stamp := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	encode := func(id, target string, before, after artistRecordState) operations.Record {
		t.Helper()
		rawBefore, err := json.Marshal(before)
		if err != nil {
			t.Fatal(err)
		}
		rawAfter, err := json.Marshal(after)
		if err != nil {
			t.Fatal(err)
		}
		return operations.Record{ID: id, Type: operations.TypeArtistMerge, TargetType: operations.TargetArtist, TargetID: target, Before: rawBefore, After: rawAfter, CreatedAt: stamp}
	}
	firstBefore, firstAfter := emptyArtistState(), emptyArtistState()
	firstBefore.Artists = []artistIdentity{{ID: a, Revision: 1}, {ID: b, Revision: 1}}
	firstAfter.Artists = []artistIdentity{{ID: a, Revision: 2}, {ID: b, Revision: 2, RedirectArtistID: &a, RedirectOperationID: &op1}}
	firstBefore.Bindings = []artistBindingState{{KeyID: keyB, ArtistID: b}}
	firstAfter.Changes = map[string]any{"source": "这不是恢复证据"}
	secondBefore, secondAfter := emptyArtistState(), emptyArtistState()
	secondBefore.Artists = []artistIdentity{{ID: c, Revision: 1}, {ID: a, Revision: 2}, firstAfter.Artists[1]}
	secondAfter.Artists = []artistIdentity{{ID: c, Revision: 2}, {ID: a, Revision: 3, RedirectArtistID: &c, RedirectOperationID: &op2}, {ID: b, Revision: 2, RedirectArtistID: &c, RedirectOperationID: &op2}}
	secondBefore.Bindings = []artistBindingState{{KeyID: keyA, ArtistID: a}, {KeyID: keyB, ArtistID: a}}
	records := map[string]operations.Record{op1: encode(op1, a, firstBefore, firstAfter), op2: encode(op2, c, secondBefore, secondAfter)}
	loads := map[string]int{}
	history := mergedSourceHistory{entries: map[string]mergedSourceEvidence{}, load: func(id string) (operations.Record, error) { loads[id]++; return records[id], nil }}
	result, err := history.origin(context.Background(), b, c, op2)
	if err != nil || result.id != op1 || !reflect.DeepEqual(result.keys, []string{keyB}) || !result.createdAt.Equal(stamp) {
		t.Fatalf("原始来源 %+v %v", result, err)
	}
	result, err = history.origin(context.Background(), a, c, op2)
	if err != nil || result.id != op2 || len(result.keys) != 2 {
		t.Fatalf("中间来源 %+v %v", result, err)
	}
	if loads[op1] != 1 || loads[op2] != 1 {
		t.Fatal("重复读取同一持久快照", loads)
	}
	for _, tc := range []struct {
		name   string
		change func(*operations.Record)
	}{
		{"错误操作类型", func(r *operations.Record) { r.Type = operations.TypeArtistSplit }},
		{"未知快照版本", func(r *operations.Record) { r.Before = []byte(`{"schema_version":2}`) }},
		{"损坏前态", func(r *operations.Record) { r.Before = []byte(`[]`) }},
		{"缺失后态身份", func(r *operations.Record) { r.After = []byte(`{"schema_version":1,"artists":[]}`) }},
		{"错误目标", func(r *operations.Record) { r.TargetID = uuid.New() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := records[op2]
			tc.change(&record)
			h := mergedSourceHistory{entries: map[string]mergedSourceEvidence{}, load: func(string) (operations.Record, error) { return record, nil }}
			if _, err := h.origin(context.Background(), b, c, op2); !errors.Is(err, errMergedSourceEvidence) {
				t.Fatal("损坏历史未拒绝", err)
			}
		})
	}
	cause := errors.New("读取记录失败")
	history = mergedSourceHistory{entries: map[string]mergedSourceEvidence{}, load: func(string) (operations.Record, error) { return operations.Record{}, cause }}
	if _, err := history.origin(context.Background(), b, c, op2); !errors.Is(err, cause) {
		t.Fatal("隐藏记录读取失败", err)
	}
}

// 历史读取没有单次治理写操作的数量上限，也不能因累计读取超过 64 MiB 拒绝合法记录。
func TestArtistMergedSourcesHistoryHasNoAggregateCap(t *testing.T) {
	target := uuid.New()
	raw := []byte(`{"schema_version":1,"changes":"` + strings.Repeat("x", 7000) + `"}`)
	history := mergedSourceHistory{entries: map[string]mergedSourceEvidence{}, load: func(id string) (operations.Record, error) {
		return operations.Record{ID: id, Type: operations.TypeArtistMerge, TargetType: operations.TargetArtist, TargetID: target, Before: raw, After: []byte(`{"schema_version":1}`)}, nil
	}}
	for i := 0; i < 10001; i++ {
		if _, err := history.evidence(uuid.New()); err != nil {
			t.Fatalf("第 %d 条合法历史受到读取上限限制: %v", i+1, err)
		}
	}
	if len(history.entries) != 10001 {
		t.Fatal("历史被截断", len(history.entries))
	}
}

func TestArtistMergedSourcesCachedHistoryCancellationAndCycle(t *testing.T) {
	source, target, op := uuid.New(), uuid.New(), uuid.New()
	identity := artistIdentity{ID: source, Revision: 2, RedirectArtistID: &target, RedirectOperationID: &op}
	history := mergedSourceHistory{entries: map[string]mergedSourceEvidence{op: {id: op, targetID: target, before: map[string]artistIdentity{source: identity}, after: map[string]artistIdentity{source: identity}}}, load: func(string) (operations.Record, error) {
		t.Fatal("不应重读缓存记录")
		return operations.Record{}, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := history.origin(ctx, source, target, op); !errors.Is(err, context.Canceled) {
		t.Fatal("缓存命中未响应取消", err)
	}
	if _, err := history.origin(context.Background(), source, target, op); !errors.Is(err, errMergedSourceEvidence) {
		t.Fatal("未拒绝循环证据", err)
	}
}

func TestArtistMergedSourcesEvidenceAllowsReadbackWhitespace(t *testing.T) {
	source, target, key, operationID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	before, after := emptyArtistState(), emptyArtistState()
	before.Artists = []artistIdentity{{ID: target, Revision: 1}, {ID: source, Revision: 1}}
	before.Bindings = []artistBindingState{{KeyID: key, ArtistID: source}}
	after.Artists = []artistIdentity{{ID: target, Revision: 2}, {ID: source, Revision: 2, RedirectArtistID: &target, RedirectOperationID: &operationID}}
	after.Bindings = []artistBindingState{{KeyID: key, ArtistID: target, OperationID: &operationID}}
	marshal := func(state artistRecordState) []byte {
		t.Helper()
		if err := boundedArtistState(state); err != nil {
			t.Fatalf("夹具必须满足原紧凑写入预算: %v", err)
		}
		raw, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	record := operations.Record{ID: operationID, Type: operations.TypeArtistMerge, TargetType: operations.TargetArtist, TargetID: target, Before: marshal(before), After: marshal(after)}
	expected, err := decodeMergedSourceEvidence(record)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"before", "after"} {
		t.Run(field, func(t *testing.T) {
			expanded := record
			raw := record.Before
			if field == "after" {
				raw = record.After
			}
			// 只改变 JSON 表示的空白，模拟读回文本变大；不是扩大单次治理的语义载荷。
			readback := append([]byte{'{'}, bytes.Repeat([]byte{' '}, 8<<20)...)
			readback = append(readback, raw[1:]...)
			var compact bytes.Buffer
			if err := json.Compact(&compact, readback); err != nil || !bytes.Equal(compact.Bytes(), raw) || len(readback) <= 8<<20 {
				t.Fatalf("夹具未保持紧凑写入语义: %v", err)
			}
			if field == "before" {
				expanded.Before = readback
			} else {
				expanded.After = readback
			}
			got, err := decodeMergedSourceEvidence(expanded)
			if err != nil || !reflect.DeepEqual(got, expected) {
				t.Fatalf("合法读回空白受到字节上限限制: %v", err)
			}
		})
	}
}
