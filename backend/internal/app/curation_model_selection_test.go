package app

import (
	"encoding/json"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/curation"
)

func Test整理第一档选择严格限定已发送的候选(t *testing.T) {
	items := []curation.PendingItem{
		{ID: "甲项", Candidates: json.RawMessage(`[{"external_id":"甲候选"}]`)},
		{ID: "乙项", Candidates: json.RawMessage(`[{"external_id":"乙候选"}]`)},
	}
	valid := `{"items":[{"item_id":"甲项","decision":"select","external_id":"甲候选"},{"item_id":"乙项","decision":"upgrade"}]}`
	actions, err := selectionActions(valid, items)
	requireCurationOK(t, err)
	if len(actions) != 2 || actions[0].ID != "甲项" || actions[0].Name != "resolve_release_match" ||
		actions[1].ID != "乙项" || actions[1].Name != "shelve_pending_item" {
		t.Fatalf("后台执行动作 %+v", actions)
	}

	for _, tc := range []struct{ name, reply string }{
		{"候选属于别项", `{"items":[{"item_id":"甲项","decision":"select","external_id":"乙候选"},{"item_id":"乙项","decision":"upgrade"}]}`},
		{"缺少一个决定", `{"items":[{"item_id":"甲项","decision":"upgrade"}]}`},
		{"重复决定", `{"items":[{"item_id":"甲项","decision":"upgrade"},{"item_id":"甲项","decision":"upgrade"}]}`},
		{"升级附带编号", `{"items":[{"item_id":"甲项","decision":"upgrade","external_id":"甲候选"},{"item_id":"乙项","decision":"upgrade"}]}`},
		{"JSON 重复字段", `{"items":[{"item_id":"甲项","decision":"select","decision":"upgrade"},{"item_id":"乙项","decision":"upgrade"}]}`},
		{"JSON 外的文字", valid + "已完成"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if actions, err := selectionActions(tc.reply, items); err == nil || len(actions) != 0 {
				t.Fatalf("无效回复产生了动作 %+v，错误 %v", actions, err)
			}
		})
	}
}
