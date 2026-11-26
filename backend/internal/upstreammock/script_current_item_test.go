package upstreammock

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestScriptCurrentItem(t *testing.T) {
	var s script
	err := json.Unmarshal([]byte(`{"rules":[{"when":{"tool_available":"submit_verification"},"reply":{"tool_calls":[
		{"name":"submit_verification","arguments":{"item_id":"$current_item","verdict":"typo"}},
		{"name":"shelve_pending_item","arguments":{"item_id":"explicit"}}
	]}}]}`), &s)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, items, want string }{
		{"first", `[{"item_id":"item-a"}]`, "item-a"},
		{"second", `[{"item_id":"item-b"}]`, "item-b"},
		{"multiple", `[{"item_id":"item-a"},{"item_id":"item-b"}]`, "$current_item"},
		{"empty", `[]`, "$current_item"},
		{"absent", "", "$current_item"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := modelInput{Model: "mock-model"}
			if tc.items != "" {
				body, err := json.Marshal(`<roomusic-source type="curation_pending" trust="untrusted">` + tc.items + "</roomusic-source>")
				if err != nil {
					t.Fatal(err)
				}
				input.Messages = []wireMessage{{Role: "user", Content: body}}
			}
			turn := input.turn()
			turn.tools = []string{"submit_verification"}
			got := s.reply(turn)
			want := reply{Calls: []call{
				{"submit_verification", object{"item_id": tc.want, "verdict": "typo"}},
				{"shelve_pending_item", object{"item_id": "explicit"}},
			}}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("同一规则当前项 %s，回包 %+v，期望 %+v", tc.items, got, want)
			}
		})
	}
}
