package app

import (
	"encoding/json"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/curation"
)

func Test整理由模型选择时严格限定已发送的候选(t *testing.T) {
	items := []curation.PendingItem{
		{ID: "甲项", Candidates: json.RawMessage(`[{"external_id":"甲候选"}]`)},
		{ID: "乙项", Candidates: json.RawMessage(`[{"external_id":"乙候选"}]`)},
	}
	valid := `{"items":[{"item_id":"甲项","decision":"judge","selected_id":"甲候选"},{"item_id":"乙项","decision":"undetermined","reason":"定不了"}]}`
	actions, err := selectionActions(valid, items)
	requireCurationOK(t, err)
	if len(actions) != 2 || actions[0].ID != "甲项" || actions[0].Name != "submit_judge" ||
		actions[1].ID != "乙项" || actions[1].Name != "shelve_pending_item" {
		t.Fatalf("后台执行动作 %+v", actions)
	}

	fenced, err := selectionActions("```json\n"+valid+"\n```", items)
	requireCurationOK(t, err)
	if len(fenced) != 2 || fenced[0].ID != actions[0].ID || fenced[1].ID != actions[1].ID {
		t.Fatalf("代码块中的决定 %+v", fenced)
	}

	for _, tc := range []struct{ name, reply, wantID string }{
		{"候选属于别项", `{"items":[{"item_id":"甲项","decision":"judge","selected_id":"乙候选"},{"item_id":"乙项","decision":"undetermined","reason":"定不了"}]}`, "乙项"},
		{"缺少一个决定", `{"items":[{"item_id":"甲项","decision":"undetermined","reason":"定不了"}]}`, "甲项"},
		{"重复决定", `{"items":[{"item_id":"甲项","decision":"undetermined","reason":"定不了"},{"item_id":"乙项","decision":"undetermined","reason":"定不了"},{"item_id":"甲项","decision":"undetermined","reason":"定不了"}]}`, "乙项"},
		{"定不了附带编号", `{"items":[{"item_id":"甲项","decision":"undetermined","reason":"定不了","selected_id":"甲候选"},{"item_id":"乙项","decision":"undetermined","reason":"定不了"}]}`, "乙项"},
		{"JSON 重复字段", `{"items":[{"item_id":"甲项","decision":"select","decision":"undetermined","reason":"定不了"},{"item_id":"乙项","decision":"undetermined","reason":"定不了"}]}`, "乙项"},
		{"JSON 外的文字", valid + "已完成", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actions, err := selectionActions(tc.reply, items)
			switch tc.wantID {
			case "":
				if err == nil || len(actions) != 0 {
					t.Fatalf("无效回复产生了动作 %+v，错误 %v", actions, err)
				}
			default:
				if err != nil || len(actions) != 1 || actions[0].ID != tc.wantID {
					t.Fatalf("期望仅执行 %s，实际动作 %+v，错误 %v", tc.wantID, actions, err)
				}
			}
		})
	}
}

func TestCurationModelRulesUndeterminedRequiresReason(t *testing.T) {
	item := curation.PendingItem{ID: "synthetic-item", Candidates: json.RawMessage(`[]`)}
	for _, tc := range []struct{ name, reply, reason string }{
		{"明确理由", `{"items":[{"item_id":"synthetic-item","decision":"undetermined","reason":"曲序证据不足"}]}`, "曲序证据不足"},
		{"缺理由", `{"items":[{"item_id":"synthetic-item","decision":"undetermined"}]}`, ""},
		{"空白理由", `{"items":[{"item_id":"synthetic-item","decision":"undetermined","reason":"  "}]}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actions, err := selectionActions(tc.reply, []curation.PendingItem{item})
			if tc.reason == "" {
				if err == nil || len(actions) != 0 {
					t.Fatalf("缺少明确理由不能产生结案动作，实际 %+v，错误 %v", actions, err)
				}
				return
			}
			requireCurationOK(t, err)
			if len(actions) != 1 || actions[0].ID != item.ID || actions[0].Name != "shelve_pending_item" {
				t.Fatalf("明确的定不了应产生一个结案动作：%+v", actions)
			}
			var payload map[string]string
			requireCurationOK(t, json.Unmarshal(actions[0].Arguments, &payload))
			if payload["reason"] != tc.reason {
				t.Fatalf("结案理由=%q，期望 %q", payload["reason"], tc.reason)
			}
		})
	}
}
