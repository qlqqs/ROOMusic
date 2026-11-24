package upstreammock

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"slices"
	"strings"
)

type script struct {
	Models []string `json:"models"`
	Rules  []rule   `json:"rules"`
}
type rule struct {
	When struct {
		User   string `json:"last_user_contains"`
		Result string `json:"tool_result_contains"`
		Tool   string `json:"tool_available"`
		Model  string `json:"model"`
	} `json:"when"`
	Reply reply `json:"reply"`
}
type reply struct {
	Text  string `json:"text"`
	Calls []call `json:"tool_calls"`
}
type call struct {
	Name      string `json:"name"`
	Arguments object `json:"arguments"`
}

func loadScript(path string) (script, error) {
	s := script{Models: []string{"mock-model", "mock-reviewer"}}
	if path == "" {
		return s, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	raw = bytes.TrimSpace(raw)
	if bytes.HasPrefix(raw, []byte("[")) {
		err = json.Unmarshal(raw, &s.Rules)
	} else {
		err = json.Unmarshal(raw, &s)
	}
	if err != nil {
		return s, fmt.Errorf("模型脚本: %w", err)
	}
	for i, rule := range s.Rules {
		if (rule.Reply.Text == "") == (len(rule.Reply.Calls) == 0) {
			return s, fmt.Errorf("脚本规则 %d 需要一种回复", i+1)
		}
		for _, c := range rule.Reply.Calls {
			if c.Name == "" || c.Arguments == nil {
				return s, fmt.Errorf("脚本规则 %d 缺少工具名称或参数对象", i+1)
			}
		}
	}
	return s, nil
}

type modelInput struct {
	Model           string
	Input, Messages []wireMessage
	Tools           []struct {
		Type, Name string
		Function   struct{ Name string }
	}
}
type wireMessage struct {
	Type, Role, Output string
	Content            json.RawMessage
}
type turn struct {
	model, user, result string
	tools               []string
	nativeSearch        bool
}

func (input modelInput) turn() turn {
	t := turn{model: input.Model}
	for _, tool := range input.Tools {
		name := tool.Name
		if name == "" {
			name = tool.Function.Name
		}
		t.tools = append(t.tools, name)
		if tool.Type == "web_search" || strings.HasPrefix(tool.Type, "web_search_") {
			t.nativeSearch = true
		}
	}
	messages := input.Messages
	if input.Input != nil {
		messages = input.Input
	}
	for _, m := range messages {
		switch {
		case m.Type == "function_call_output":
			t.result, t.user = m.Output, ""
		case m.Role == "tool":
			t.user = ""
			_ = json.Unmarshal(m.Content, &t.result)
		case m.Role == "user":
			var plain string
			if json.Unmarshal(m.Content, &plain) == nil {
				t.user, t.result = plain, ""
				continue
			}
			var blocks []struct{ Type, Text, Content string }
			if json.Unmarshal(m.Content, &blocks) != nil {
				continue
			}
			t.user, t.result = "", ""
			for _, b := range blocks {
				switch b.Type {
				case "text", "input_text":
					t.user += b.Text
				case "tool_result":
					t.result = b.Content
				}
			}
		}
	}
	return t
}
func (s script) reply(t turn) reply {
	if slices.Contains(t.tools, "roomusic_probe") {
		return reply{Calls: []call{{"roomusic_probe", object{}}}}
	}
	for _, r := range s.Rules {
		w := r.When
		if (w.Model == "" || w.Model == t.model) && (w.Tool == "" || slices.Contains(t.tools, w.Tool)) && strings.Contains(t.user, w.User) && strings.Contains(t.result, w.Result) {
			return r.Reply
		}
	}
	if slices.Contains(t.tools, "submit_review") {
		var request struct {
			Items []struct {
				ID string `json:"item_id"`
			}
		}
		if json.Unmarshal([]byte(t.user), &request) == nil && len(request.Items) > 0 {
			items := []object{}
			for _, item := range request.Items {
				items = append(items, object{"item_id": item.ID, "decision": "approved", "risk": "low", "reasons": []string{"合成审查已批准。"}})
			}
			return reply{Calls: []call{{"submit_review", object{"items": items}}}}
		}
	}
	if slices.Contains(t.tools, "read_pending_items") && strings.Contains(t.user, "read_pending_items") && t.result == "" {
		return reply{Calls: []call{{"read_pending_items", object{}}}}
	}
	if slices.Contains(t.tools, "resolve_release_match") && t.result != "" {
		if calls := pendingCalls(t); len(calls) > 0 {
			return reply{Calls: calls}
		}
	}
	return reply{Text: "这是 ROOMusic 外部接口 mock 的合成回复。"}
}

func pendingCalls(t turn) []call {
	result := strings.TrimSpace(t.result)
	// 管家把不可信的工具结果包在来源标签内，并转义标签中的文本。
	if strings.HasPrefix(result, `<roomusic-source kind="tool" name="read_pending_items">`) {
		_, body, _ := strings.Cut(result, ">")
		result = html.UnescapeString(strings.TrimSuffix(body, "</roomusic-source>"))
	}
	var items []struct {
		ID         string `json:"item_id"`
		Candidates []struct {
			ID string `json:"external_id"`
		}
	}
	if json.Unmarshal([]byte(result), &items) != nil {
		return nil
	}
	var calls []call
	for _, item := range items {
		if item.ID == "" {
			continue
		}
		if len(item.Candidates) > 0 && item.Candidates[0].ID != "" {
			calls = append(calls, call{"resolve_release_match", object{"item_id": item.ID, "external_id": item.Candidates[0].ID, "basis": object{"note": "使用合成候选核对发行。"}}})
		} else if slices.Contains(t.tools, "shelve_pending_item") {
			calls = append(calls, call{"shelve_pending_item", object{"item_id": item.ID, "reason": "合成样例没有发行候选。"}})
		}
	}
	return calls
}
