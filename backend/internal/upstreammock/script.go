package upstreammock

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"maps"
	"os"
	"slices"
	"strings"
)

type script struct {
	Models         []string `json:"models"`
	ArtistScenario string   `json:"artist_scenario"`
	Rules          []rule   `json:"rules"`
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
	switch s.ArtistScenario {
	case "", "identity", "media", "candidates":
	default:
		return s, fmt.Errorf("未知艺人脚本 %q", s.ArtistScenario)
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
	model, user, result    string
	pendingResult, baseURL string
	curation               string
	tools                  []string
	nativeSearch           bool
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
			} else {
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
		if _, body, found := strings.Cut(t.user, `<roomusic-source type="curation_pending" trust="untrusted">`); found {
			t.curation, _, _ = strings.Cut(body, "</roomusic-source>")
		}
		if strings.HasPrefix(strings.TrimSpace(t.result), `<roomusic-source kind="tool" name="read_pending_items">`) {
			t.pendingResult = t.result
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
			return bindScriptItem(r.Reply, t.curation)
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
				decision, risk, reason := "approved", "low", "合成审查已批准。"
				if s.ArtistScenario == "candidates" && strings.Contains(t.user, `"tool":"resolve_artist_identity"`) {
					decision, risk, reason = "needs_human_confirmation", "medium", "合成署名对应两个同名艺人，请确认。"
				}
				items = append(items, object{"item_id": item.ID, "decision": decision, "risk": risk, "reasons": []string{reason}})
			}
			return reply{Calls: []call{{"submit_review", object{"items": items}}}}
		}
	}
	if t.curation != "" {
		if slices.Contains(t.tools, "submit_patrol") || slices.Contains(t.tools, "submit_local_cleanup") {
			return patrolLocalReply(t)
		}
		if response := s.curationReply(t); response.Text != "" || len(response.Calls) > 0 {
			return response
		}
	}

	if slices.Contains(t.tools, "read_pending_items") && strings.Contains(t.user, "read_pending_items") && t.result == "" {
		return reply{Calls: []call{{"read_pending_items", object{}}}}
	}
	if slices.Contains(t.tools, "resolve_release_match") && t.result != "" {
		if calls := s.pendingCalls(t); len(calls) > 0 {
			return reply{Calls: calls}
		}
	}
	return reply{Text: "这是 ROOMusic 外部接口 mock 的合成回复。"}
}

// 单项整理脚本只能引用本次领取的编号，固定 JSON 不需要保存数据库 UUID。
func bindScriptItem(rep reply, current string) reply {
	var items []pendingItem
	if current == "" || json.Unmarshal([]byte(current), &items) != nil || len(items) != 1 {
		return rep
	}
	rep.Calls = slices.Clone(rep.Calls)
	for i, call := range rep.Calls {
		if call.Arguments["item_id"] == "$current_item" {
			args := maps.Clone(call.Arguments)
			args["item_id"] = items[0].ID
			rep.Calls[i].Arguments = args
		}
	}
	return rep
}

func patrolLocalReply(t turn) reply {
	var items []struct {
		ID       string `json:"item_id"`
		Kind     string
		Evidence struct {
			Kind  string
			Names []struct{ ID, Name string }
		}
	}
	// 默认 mock 不猜测身份和本地片段，可以用显式脚本指定结果。
	if t.result != "" || json.Unmarshal([]byte(t.curation), &items) != nil || len(items) != 1 {
		return reply{Text: "合成整理已结束。"}
	}
	item := items[0]
	if item.Kind == "name_patrol" && slices.Contains(t.tools, "submit_patrol") {
		results := []object{}
		if item.Evidence.Kind == "artist" {
			for _, name := range item.Evidence.Names {
				results = append(results, object{"id": name.ID, "canonical": name.Name + " | ", "confidence": "low"})
			}
		}
		return reply{Calls: []call{{"submit_patrol", object{"item_id": item.ID, "results": results, "issues": []object{}}}}}
	}
	if item.Kind == "local_cleanup" && slices.Contains(t.tools, "submit_local_cleanup") {
		return reply{Calls: []call{{"submit_local_cleanup", object{"item_id": item.ID}}}}
	}
	return reply{Text: "合成整理已结束。"}
}

type pendingItem struct {
	ID       string `json:"item_id"`
	Kind     string `json:"kind"`
	Evidence struct {
		Names         []string
		Title, Artist string
		Gaps          struct{ Image, Bio bool }
		Tracks        json.RawMessage
	}
	Candidates []struct {
		ID string `json:"external_id"`
	}
}

func (s script) curationReply(t turn) reply {
	var items []pendingItem
	if json.Unmarshal([]byte(t.curation), &items) != nil || len(items) == 0 {
		return reply{}
	}
	if len(t.tools) == 0 {
		decisions := make([]object, 0, len(items))
		for _, item := range items {
			d := object{"item_id": item.ID, "decision": "upgrade"}
			if len(item.Candidates) > 0 && item.Candidates[0].ID != "" {
				d["decision"], d["external_id"] = "select", item.Candidates[0].ID
			}
			decisions = append(decisions, d)
		}
		raw, _ := json.Marshal(object{"items": decisions})
		return reply{Text: string(raw)}
	}
	item := items[0]
	abandon := reply{Calls: []call{{"shelve_pending_item", object{"item_id": item.ID, "reason": "合成样例没有可核实的资料。"}}}}
	if slices.Contains(t.tools, "web_search") && item.Kind == "artist_identity" && len(item.Evidence.Names) > 0 {
		switch {
		case t.result == "":
			return reply{Calls: []call{{"web_search", object{"q": strings.Join(item.Evidence.Names, " ")}}}}
		case strings.Contains(t.result, `name="web_search"`):
			return reply{Calls: []call{{"fetch_page", object{"url": t.baseURL + "/searxng/artists/coast"}}}}
		case strings.Contains(t.result, `name="fetch_page"`):
			return reply{Calls: s.artistCalls(t.baseURL, item.ID, item.Evidence.Gaps.Image, item.Evidence.Gaps.Bio)}
		}
	}
	if slices.Contains(t.tools, "search_music_metadata") {
		entity, title := "release", item.Evidence.Title
		if item.Kind == "recording_match" {
			entity = "recording"
			var tracks []struct{ Title string }
			if json.Unmarshal(item.Evidence.Tracks, &tracks) == nil && len(tracks) > 0 {
				title = tracks[0].Title
			}
		}
		switch {
		case t.result == "" && title != "":
			return reply{Calls: []call{{"search_music_metadata", object{"entity": entity, "query": title}}}}
		case strings.Contains(t.result, `name="search_music_metadata"`):
			_, body, _ := strings.Cut(t.result, ">")
			var data struct{ Results []struct{ MBID string } }
			if json.Unmarshal([]byte(html.UnescapeString(strings.TrimSuffix(body, "</roomusic-source>"))), &data) == nil && len(data.Results) > 0 {
				return reply{Calls: []call{{"get_music_metadata", object{"entity": entity, "mbid": data.Results[0].MBID}}}}
			}
		case strings.Contains(t.result, `name="get_music_metadata"`):
			_, body, _ := strings.Cut(t.result, ">")
			var data struct{ Metadata struct{ MBID string } }
			if json.Unmarshal([]byte(html.UnescapeString(strings.TrimSuffix(body, "</roomusic-source>"))), &data) == nil && data.Metadata.MBID != "" {
				return reply{Calls: []call{{"resolve_release_match", object{"item_id": item.ID, "external_id": data.Metadata.MBID, "basis": object{"note": "已读取合成外部资料。"}}}}}
			}
		}
	}
	if t.result != "" {
		return reply{Text: "处理完成。"}
	}
	if slices.Contains(t.tools, "submit_verification") && strings.HasPrefix(item.Kind, "verify_") {
		return reply{Calls: []call{{"submit_verification", object{"item_id": item.ID, "verdict": "uncertain", "reason": "合成样例没有指定核对结论。"}}}}
	}
	return abandon
}

func (s script) pendingCalls(t turn) []call {
	result := strings.TrimSpace(t.result)
	searched := strings.HasPrefix(result, `<roomusic-source kind="tool" name="web_search">`)
	if searched {
		result = strings.TrimSpace(t.pendingResult)
	}
	// 管家把不可信的工具结果包在来源标签内，并转义标签中的文本。
	if strings.HasPrefix(result, `<roomusic-source kind="tool" name="read_pending_items">`) {
		_, body, _ := strings.Cut(result, ">")
		result = html.UnescapeString(strings.TrimSuffix(body, "</roomusic-source>"))
	}
	var items []pendingItem
	if json.Unmarshal([]byte(result), &items) != nil {
		return nil
	}
	var calls []call
	for _, item := range items {
		if item.ID == "" {
			continue
		}
		if item.Kind == "artist_identity" {
			if !searched && slices.Contains(t.tools, "web_search") && len(item.Evidence.Names) > 0 {
				return []call{{"web_search", object{"q": strings.Join(item.Evidence.Names, " ")}}}
			}
			if slices.Contains(t.tools, "resolve_artist_identity") {
				calls = append(calls, s.artistCalls(t.baseURL, item.ID, item.Evidence.Gaps.Image, item.Evidence.Gaps.Bio)...)
			}
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
