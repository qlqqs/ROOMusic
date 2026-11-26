package upstreammock

import (
	"encoding/json"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
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

func TestLocalAssistScriptBindings(t *testing.T) {
	const fixture = `{"rules":[{"when":{"tool_available":"submit_local_cleanup"},"reply":{"tool_calls":[
		{"name":"submit_local_cleanup","arguments":{"item_id":"$current_item","decision":"apply",
			"texts":[{"target_id":"$track:album/a.flac#1","field":"title","value":"外部补写"},
			         {"target_id":"$release:other/a.flac","field":"title","value":"别处"}],
			"encodings":[{"release_id":"$release:album/a.flac","encoding":"GBK"}],
			"splits":[{"target_id":"$release:album/a.flac","source_ref":"$source:album/a.flac#1#album_artist#ALBUMARTIST/0"}],
			"grouping":{"albums":[{"files":[{"file_id":"$file:album/a.flac"}]}]}}}
	]}}]}`
	var s script
	if err := json.Unmarshal([]byte(fixture), &s); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		item, release, track, file := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		source := track + "/ALBUMARTIST/0"
		evidence, err := json.Marshal([]object{{"item_id": item, "evidence": object{
			"releases": []object{{"release_id": release,
				"tracks":  []object{{"track_id": track, "file_id": file, "path": "album/a.flac", "source_path": "album/a.flac#1"}},
				"credits": []object{{"source_ref": source, "role": "album_artist"}},
			}},
		}}})
		if err != nil {
			t.Fatal(err)
		}
		result := s.reply(turn{tools: []string{"submit_local_cleanup"}, curation: string(evidence)})
		want := object{"item_id": item, "decision": "apply",
			"texts": []any{
				map[string]any{"target_id": track, "field": "title", "value": "外部补写"},
				map[string]any{"target_id": "$release:other/a.flac", "field": "title", "value": "别处"},
			},
			"encodings": []any{map[string]any{"release_id": release, "encoding": "GBK"}},
			"splits":    []any{map[string]any{"target_id": release, "source_ref": source}},
			"grouping":  map[string]any{"albums": []any{map[string]any{"files": []any{map[string]any{"file_id": file}}}}},
		}
		if len(result.Calls) != 1 || !reflect.DeepEqual(result.Calls[0].Arguments, want) {
			t.Fatalf("当前输入 %s 的回包 %+v，期望 %+v", evidence, result, want)
		}
	}
}
