package zones

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/zones/queue"
)

func TestMoveEntryCommandPlan(t *testing.T) {
	s := NewService(nil, Options{})
	defer s.Close()
	entries := []queue.Entry{{EntryID: uuid.New()}, {EntryID: uuid.New()}, {EntryID: uuid.New()}, {EntryID: uuid.New()}}
	for _, tc := range []struct {
		name    string
		entryID string
		toIndex int
		invalid bool
		noop    bool
		order   []int
	}{
		{"移到首项", entries[3].EntryID, 0, false, false, []int{3, 0, 1, 2}},
		{"移到尾项", entries[0].EntryID, 3, false, false, []int{1, 2, 3, 0}},
		{"同位置", entries[1].EntryID, 1, false, true, nil},
		{"不存在条目", uuid.New(), 1, false, true, nil},
		{"负下标", entries[0].EntryID, -1, true, false, nil},
		{"下标等于长度", entries[0].EntryID, 4, true, false, nil},
		{"下标超过长度", entries[0].EntryID, 10, true, false, nil},
		{"不存在条目也先拒绝越界", uuid.New(), 4, true, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			z := &zoneRuntime{persisted: PersistedZone{
				Revision: 8, QueueRevision: 5,
				Queue: queue.State{Entries: slices.Clone(entries), CurrentEntryID: entries[1].EntryID,
					Mode: queue.ModeShuffle, History: []string{entries[0].EntryID}, Bag: []string{entries[3].EntryID, entries[2].EntryID}},
			}}
			before := z.persisted
			before.Queue.Entries = slices.Clone(z.persisted.Queue.Entries)
			before.Queue.History = slices.Clone(z.persisted.Queue.History)
			before.Queue.Bag = slices.Clone(z.persisted.Queue.Bag)
			command := Command{Command: "move_entry", EntryID: tc.entryID, ToIndex: tc.toIndex}
			err := validateCommand(command)
			var plan commandPlan
			if err == nil {
				plan, err = s.plan(context.Background(), "user", z, command, nil)
			}
			if tc.invalid {
				if !errors.Is(err, ErrInvalidInput) {
					t.Fatalf("越界错误 = %v，期望 ErrInvalidInput", err)
				}
			} else if err != nil {
				t.Fatal(err)
			} else if tc.noop {
				if !plan.noop || plan.change != nil || plan.writeEntries != nil || plan.notify {
					t.Fatalf("空操作仍计划写入或通知：%+v", plan)
				}
			} else {
				want := before.Queue
				want.Entries = make([]queue.Entry, len(tc.order))
				for i, index := range tc.order {
					want.Entries[i] = entries[index]
				}
				if !reflect.DeepEqual(plan.persisted.Queue, want) || plan.noop || !plan.notify || plan.writeEntries == nil {
					t.Fatalf("移动规划不正确：%+v", plan)
				}
				encoded, err := json.Marshal(plan.change)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]any
				if err := json.Unmarshal(encoded, &fields); err != nil {
					t.Fatal(err)
				}
				wantFields := map[string]any{"kind": "move", "entry_id": tc.entryID, "to_index": float64(tc.toIndex), "queue_revision": float64(0)}
				if !reflect.DeepEqual(fields, wantFields) {
					t.Fatalf("移动增量 JSON = %s，期望 %+v", encoded, wantFields)
				}
			}
			if !reflect.DeepEqual(z.persisted, before) {
				t.Fatal("规划阶段修改了原播放区")
			}
		})
	}
}
